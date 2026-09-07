// Package lyrics fetches synced (LRC-format) lyrics for a track from LRCLIB
// (lrclib.net), the same free, unauthenticated service most third-party music
// clients use. Tidal's own API has no public lyrics endpoint.
package lyrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Line is one timestamped line of synced lyrics.
type Line struct {
	TimeSec float64
	Text    string
}

// Lyrics is the result of a lookup: either synced Lines, a Plain fallback (no
// timestamps), or neither (Found is false).
type Lyrics struct {
	Found bool
	Lines []Line // synced lyrics, empty if only plain text was available
	Plain string // unsynced fallback text, empty if synced lines are present
}

const lrclibBaseURL = "https://lrclib.net/api/get"

var httpClient = &http.Client{Timeout: 8 * time.Second}

// lrclibResponse mirrors the subset of LRCLIB's /api/get response we use.
type lrclibResponse struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
	Instrumental bool   `json:"instrumental"`
}

// FetchSynced looks up lyrics for a track by exact metadata match (LRCLIB's
// /api/get is an exact-match lookup — artist, title, album, and duration in
// seconds must match the release LRCLIB indexed). A 404 (no match) is not an
// error: it is reported as Lyrics{Found: false}.
func FetchSynced(ctx context.Context, artist, title, album string, durationSec int) (*Lyrics, error) {
	q := url.Values{}
	q.Set("track_name", title)
	q.Set("artist_name", artist)
	if album != "" {
		q.Set("album_name", album)
	}
	if durationSec > 0 {
		q.Set("duration", strconv.Itoa(durationSec))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lrclibBaseURL+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("lrclib request: %w", err)
	}
	req.Header.Set("User-Agent", "gotidal (https://github.com/carcuevas/gotidal)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("lrclib fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return &Lyrics{Found: false}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("lrclib fetch: status %d", resp.StatusCode)
	}

	var body lrclibResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("lrclib decode: %w", err)
	}
	if body.Instrumental || (body.SyncedLyrics == "" && body.PlainLyrics == "") {
		return &Lyrics{Found: false}, nil
	}
	if body.SyncedLyrics != "" {
		return &Lyrics{Found: true, Lines: ParseLRC(body.SyncedLyrics)}, nil
	}
	return &Lyrics{Found: true, Plain: body.PlainLyrics}, nil
}

// lrcLineRE matches one or more leading "[mm:ss.xx]" timestamp tags on an LRC
// line (a line can carry more than one, for repeated lyrics at different times).
var lrcLineRE = regexp.MustCompile(`^(\[\d{1,2}:\d{2}(?:\.\d{1,3})?\])+`)

var lrcTagRE = regexp.MustCompile(`\[(\d{1,2}):(\d{2})(?:\.(\d{1,3}))?\]`)

// ParseLRC parses standard LRC-format synced lyrics ("[mm:ss.xx]text" per
// line) into timestamp-ordered Lines. Metadata tags ([ar:], [ti:], [al:], …)
// and blank lines are skipped; a line with multiple timestamp tags expands
// into one Line per tag.
func ParseLRC(raw string) []Line {
	var lines []Line
	for ln := range strings.SplitSeq(raw, "\n") {
		ln = strings.TrimRight(ln, "\r")
		tags := lrcLineRE.FindString(ln)
		if tags == "" {
			continue
		}
		text := strings.TrimSpace(ln[len(tags):])
		for _, m := range lrcTagRE.FindAllStringSubmatch(tags, -1) {
			minutes, _ := strconv.Atoi(m[1])
			seconds, _ := strconv.Atoi(m[2])
			frac := 0.0
			if m[3] != "" {
				millis, _ := strconv.ParseFloat(m[3], 64)
				divisor := 1.0
				for range len(m[3]) {
					divisor *= 10
				}
				frac = millis / divisor
			}
			lines = append(lines, Line{
				TimeSec: float64(minutes*60+seconds) + frac,
				Text:    text,
			})
		}
	}
	return lines
}

// ActiveIndex returns the index of the line that should be highlighted for
// playback position posSec, or -1 if lines is empty or playback is before the
// first line.
func ActiveIndex(lines []Line, posSec float64) int {
	active := -1
	for i, ln := range lines {
		if ln.TimeSec > posSec {
			break
		}
		active = i
	}
	return active
}
