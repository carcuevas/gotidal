package tidal

import (
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Manifest MIME types returned by /playbackinfopostpaywall.
//
// BTS ("bit transport stream", Tidal's own name) is a small JSON document
// holding one or more ready-to-fetch URLs, and is what the lossy and plain
// lossless tiers return. Hi-res comes back as MPEG-DASH instead: a manifest
// describing an initialization segment plus a numbered series of media
// segments, which is why the old urlpostpaywall endpoint could never serve
// hi-res — it has no way to express more than one URL.
const (
	manifestBTS  = "application/vnd.tidal.bts"
	manifestDASH = "application/dash+xml"
)

// btsManifest is the decoded BTS JSON document.
type btsManifest struct {
	MimeType       string   `json:"mimeType"`
	Codecs         string   `json:"codecs"`
	EncryptionType string   `json:"encryptionType"`
	URLs           []string `json:"urls"`
}

// mpd is the subset of an MPEG-DASH manifest Tidal actually uses: one period,
// one adaptation set, one representation, addressed by SegmentTemplate.
//
// Deliberately narrow. DASH is a large specification and this is not a general
// DASH client; anything outside this shape is reported as unsupported rather
// than half-parsed, so a manifest change surfaces as a clear error instead of
// silent truncation.
type mpd struct {
	MediaPresentationDuration string `xml:"mediaPresentationDuration,attr"`
	Period                    struct {
		AdaptationSet struct {
			Representation struct {
				ID                string `xml:"id,attr"`
				Codecs            string `xml:"codecs,attr"`
				AudioSamplingRate string `xml:"audioSamplingRate,attr"`
				SegmentTemplate   struct {
					Initialization  string `xml:"initialization,attr"`
					Media           string `xml:"media,attr"`
					StartNumber     *int   `xml:"startNumber,attr"`
					Timescale       int    `xml:"timescale,attr"`
					SegmentTimeline struct {
						S []struct {
							D int `xml:"d,attr"`
							// R is the *additional* repeat count: a segment
							// with r="4" means five segments in total. -1
							// means "repeat to the end of the period", which
							// has to be resolved against the duration.
							R *int `xml:"r,attr"`
						} `xml:"S"`
					} `xml:"SegmentTimeline"`
				} `xml:"SegmentTemplate"`
			} `xml:"Representation"`
		} `xml:"AdaptationSet"`
	} `xml:"Period"`
}

// parseManifest decodes the base64 manifest from a playbackinfo response into
// the ordered list of URLs that make up the stream. A BTS manifest yields the
// URLs it carries; a DASH manifest yields the initialization segment followed
// by every media segment, which concatenate into a single fragmented-MP4 byte
// stream FFmpeg can demux.
func parseManifest(mimeType, encoded string) (urls []string, durationSec float64, initURLs int, err error) {
	raw, decErr := base64.StdEncoding.DecodeString(encoded)
	if decErr != nil {
		return nil, 0, 0, fmt.Errorf("manifest is not valid base64: %w", decErr)
	}

	// The MIME type can carry parameters (e.g. "...+xml; charset=utf-8").
	switch base, _, _ := strings.Cut(mimeType, ";"); strings.TrimSpace(base) {
	case manifestBTS:
		// A single-file stream measures itself: the container carries the
		// whole thing, so there is no duration to pass along.
		btsURLs, btsErr := parseBTSManifest(raw)
		return btsURLs, 0, 0, btsErr
	case manifestDASH:
		return parseDASHManifest(raw)
	default:
		return nil, 0, 0, fmt.Errorf("unsupported manifest type %q", mimeType)
	}
}

func parseBTSManifest(raw []byte) ([]string, error) {
	var m btsManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decode BTS manifest: %w", err)
	}
	if len(m.URLs) == 0 {
		return nil, errors.New("BTS manifest contained no URLs")
	}
	// Encrypted streams need a key we have no way to obtain, so say so plainly
	// rather than handing FFmpeg bytes it will fail to decode.
	if t := strings.ToUpper(m.EncryptionType); t != "" && t != "NONE" {
		return nil, fmt.Errorf("stream is encrypted (%s), which is not supported", m.EncryptionType)
	}
	return m.URLs, nil
}

// parseDASHManifest returns the segment URLs and the presentation's duration
// in seconds. The duration matters because a fragmented stream cannot be
// measured from its own container: at open time only the initialization
// segment has been read, so libavformat reports the first fragment's length
// as the whole track's.
func parseDASHManifest(raw []byte) (urls []string, durationSec float64, initURLs int, err error) {
	var doc mpd
	if xmlErr := xml.Unmarshal(raw, &doc); xmlErr != nil {
		return nil, 0, 0, fmt.Errorf("decode DASH manifest: %w", xmlErr)
	}

	tmpl := doc.Period.AdaptationSet.Representation.SegmentTemplate
	if tmpl.Media == "" {
		return nil, 0, 0, errors.New("DASH manifest has no SegmentTemplate media URL")
	}

	count, countErr := segmentCount(&doc)
	if countErr != nil {
		return nil, 0, 0, countErr
	}

	// Prefer the timeline: summing the segment durations is exact, where
	// mediaPresentationDuration is rounded to the manifest's precision. Fall
	// back to the declared duration when the timescale is missing.
	durationSec = timelineSeconds(&doc)
	if durationSec == 0 {
		durationSec, _ = parseISODuration(doc.MediaPresentationDuration)
	}

	start := 1
	if tmpl.StartNumber != nil {
		start = *tmpl.StartNumber
	}

	repID := doc.Period.AdaptationSet.Representation.ID
	urls = make([]string, 0, count+1)
	// The initialization segment carries the moov box: without it the media
	// segments are undemuxable. It is optional in DASH but always present in
	// Tidal's manifests.
	if tmpl.Initialization != "" {
		urls = append(urls, expandSegmentURL(tmpl.Initialization, repID, 0))
		initURLs = 1
	}
	for i := range count {
		urls = append(urls, expandSegmentURL(tmpl.Media, repID, start+i))
	}
	return urls, durationSec, initURLs, nil
}

// timelineSeconds totals the SegmentTimeline's durations, in seconds. Returns
// 0 when the timeline cannot be measured (no timescale, or an open-ended
// repeat that segmentCount had to resolve against the declared duration).
func timelineSeconds(doc *mpd) float64 {
	tmpl := doc.Period.AdaptationSet.Representation.SegmentTemplate
	if tmpl.Timescale <= 0 {
		return 0
	}
	ticks := 0
	for _, e := range tmpl.SegmentTimeline.S {
		r := 0
		if e.R != nil {
			if *e.R < 0 {
				// Open-ended: the declared duration is the only source.
				return 0
			}
			r = *e.R
		}
		ticks += e.D * (1 + r)
	}
	if ticks <= 0 {
		return 0
	}
	return float64(ticks) / float64(tmpl.Timescale)
}

// segmentCount totals the segments described by the SegmentTimeline, resolving
// an open-ended repeat (r="-1") against the presentation duration.
func segmentCount(doc *mpd) (int, error) {
	tmpl := doc.Period.AdaptationSet.Representation.SegmentTemplate
	entries := tmpl.SegmentTimeline.S
	if len(entries) == 0 {
		return 0, errors.New("DASH manifest has no SegmentTimeline")
	}

	total := 0
	for i, e := range entries {
		if e.D <= 0 {
			return 0, fmt.Errorf("DASH segment %d has a non-positive duration", i)
		}
		r := 0
		if e.R != nil {
			r = *e.R
		}
		if r >= 0 {
			total += 1 + r
			continue
		}

		// r < 0 repeats to the end of the period. Only the final entry can do
		// that meaningfully, and resolving it needs both a timescale and a
		// presentation duration.
		if i != len(entries)-1 {
			return 0, fmt.Errorf("DASH segment %d repeats indefinitely but is not the last entry", i)
		}
		if tmpl.Timescale <= 0 {
			return 0, errors.New("DASH manifest repeats indefinitely without a timescale")
		}
		secs, err := parseISODuration(doc.MediaPresentationDuration)
		if err != nil {
			return 0, fmt.Errorf("DASH manifest repeats indefinitely: %w", err)
		}
		// Everything before this entry is already counted; the remainder of
		// the timeline is filled by segments of duration e.D.
		elapsed := 0
		for _, prev := range entries[:i] {
			pr := 0
			if prev.R != nil && *prev.R > 0 {
				pr = *prev.R
			}
			elapsed += prev.D * (1 + pr)
		}
		remaining := int(secs*float64(tmpl.Timescale)) - elapsed
		if remaining <= 0 {
			return 0, errors.New("DASH manifest duration leaves no room for the repeating segment")
		}
		// Round up: a partial final segment is still a segment to fetch.
		total += (remaining + e.D - 1) / e.D
	}

	if total <= 0 {
		return 0, errors.New("DASH manifest describes no segments")
	}
	return total, nil
}

// segmentIdentifierRE matches a DASH URL identifier with its optional format
// specifier: $Number$, $Number%05d$, $RepresentationID$ and so on. The
// padded form is not decoration — FFmpeg's own DASH muxer emits
// "chunk-stream$RepresentationID$-$Number%05d$.m4s", and substituting only
// the bare form would leave the placeholder in the URL and 404 every segment.
var segmentIdentifierRE = regexp.MustCompile(`\$(Number|RepresentationID|Bandwidth|Time)(%0\d+[du])?\$`)

// expandSegmentURL substitutes the DASH identifiers in a segment template.
//
// $$ is an escaped literal dollar; it is handled first so its dollars cannot
// be mistaken for the delimiters of an identifier, and the placeholder used
// while doing so is one that cannot occur in a URL.
func expandSegmentURL(tmpl, repID string, number int) string {
	const escaped = "\x00escaped-dollar\x00"
	u := strings.ReplaceAll(tmpl, "$$", escaped)

	u = segmentIdentifierRE.ReplaceAllStringFunc(u, func(m string) string {
		groups := segmentIdentifierRE.FindStringSubmatch(m)
		name, format := groups[1], groups[2]

		var value string
		switch name {
		case "Number":
			if format != "" {
				// %05d as written in the manifest is already a Go verb.
				return fmt.Sprintf(format, number)
			}
			value = strconv.Itoa(number)
		case "RepresentationID":
			value = repID
		default:
			// $Bandwidth$ and $Time$ appear in the grammar but not in the
			// manifests this parses (which address by number). Leaving the
			// identifier in place would produce a URL that 404s, so drop it
			// and let the resulting error name the segment.
			return ""
		}
		if format != "" {
			return fmt.Sprintf(format, value)
		}
		return value
	})

	return strings.ReplaceAll(u, escaped, "$")
}

// parseISODuration parses the restricted ISO-8601 form DASH uses for
// durations, e.g. "PT3M20.5S" or "PT0H3M20.000S", into seconds.
func parseISODuration(s string) (float64, error) {
	if !strings.HasPrefix(s, "PT") {
		return 0, fmt.Errorf("unsupported duration %q", s)
	}
	rest := s[2:]
	if rest == "" {
		return 0, errors.New("empty duration")
	}

	var total float64
	var num strings.Builder
	for _, r := range rest {
		if (r >= '0' && r <= '9') || r == '.' {
			num.WriteRune(r)
			continue
		}
		v, err := strconv.ParseFloat(num.String(), 64)
		if err != nil {
			return 0, fmt.Errorf("unsupported duration %q", s)
		}
		num.Reset()
		switch r {
		case 'H':
			total += v * 3600
		case 'M':
			total += v * 60
		case 'S':
			total += v
		default:
			return 0, fmt.Errorf("unsupported duration unit %q in %q", r, s)
		}
	}
	if num.Len() != 0 {
		return 0, fmt.Errorf("duration %q ends without a unit", s)
	}
	return total, nil
}
