package tidal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// playbackInfoResponse is the subset of /playbackinfopostpaywall we use.
//
// BitDepth and SampleRate are the reason this endpoint matters beyond hi-res:
// they state the source format authoritatively, rather than leaving it to be
// inferred from the container after FFmpeg has opened the stream — which is
// too late, because ALSA is configured before the first frame is decoded.
// maxSampleRate bounds the rate accepted from the server. Well above any real
// PCM rate (768 kHz is the highest in circulation) and far below the point
// where the conversion to uint32 could misbehave.
const maxSampleRate = 1 << 24

type playbackInfoResponse struct {
	TrackID          int    `json:"trackId"`
	AudioQuality     string `json:"audioQuality"`
	ManifestMimeType string `json:"manifestMimeType"`
	Manifest         string `json:"manifest"`
	BitDepth         int    `json:"bitDepth"`
	SampleRate       int    `json:"sampleRate"`
}

// playbackInfo resolves a single quality tier through
// /tracks/{id}/playbackinfopostpaywall.
//
// This is the endpoint that can serve hi-res. urlpostpaywall returns a bare
// list of URLs, which cannot express a DASH presentation, so HI_RES_LOSSLESS
// requested there either fails or is silently downgraded — the reason hi-res
// never played however the ladder was configured.
func (c *Client) playbackInfo(ctx context.Context, trackID int, q Quality) (StreamInfo, error) {
	params := url.Values{}
	params.Set("audioquality", string(q))
	params.Set("playbackmode", "STREAM")
	params.Set("assetpresentation", "FULL")

	u := fmt.Sprintf("%s/tracks/%d/playbackinfopostpaywall?%s", BaseURL, trackID, params.Encode())
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return StreamInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return StreamInfo{}, qualityError{quality: q, reason: apiReason(resp.StatusCode, body)}
	}

	var pi playbackInfoResponse
	if err := decodeJSON(resp.Body, &pi); err != nil {
		return StreamInfo{}, err
	}
	if pi.Manifest == "" {
		return StreamInfo{}, qualityError{quality: q, reason: "response carried no manifest"}
	}

	urls, durationSec, initURLs, err := parseManifest(pi.ManifestMimeType, pi.Manifest)
	if err != nil {
		return StreamInfo{}, qualityError{quality: q, reason: err.Error()}
	}

	// Uniform media segments, which is what Tidal's manifests describe, so one
	// segment's length is the presentation divided by the media count. Left at
	// 0 for a single-file stream, where there is nothing to seek across.
	var segmentSeconds float64
	if media := len(urls) - initURLs; media > 1 && durationSec > 0 {
		segmentSeconds = durationSec / float64(media)
	}

	// Trust the tier the server says it granted over the one we asked for:
	// requesting HI_RES_LOSSLESS on a track that has no hi-res master is
	// answered with a lower tier rather than an error, and the quality badge
	// must not claim otherwise.
	granted := q
	if pi.AudioQuality != "" {
		granted = Quality(pi.AudioQuality)
	}

	// ...but do not let a lossy answer end the ladder. Asking for hi-res on an
	// ordinary 44.1/16 track is answered with HIGH — lossy AAC — and accepting
	// that at the top rung skipped LOSSLESS entirely, so every normal track
	// started playing lossy. Reporting it as this tier's failure sends the
	// ladder on to LOSSLESS, which is what the track actually has. Once the
	// ladder asks for HIGH itself (Data Saver), a HIGH grant is correct and
	// this does not fire.
	if granted.Lossy() && !q.Lossy() {
		return StreamInfo{}, qualityError{
			quality: q,
			reason:  fmt.Sprintf("server offered lossy %s instead", granted),
		}
	}

	return StreamInfo{
		URLs:    urls,
		Ext:     streamExt(pi.ManifestMimeType, urls[0]),
		Quality: granted,
		// Clamped rather than trusted: these come straight off the wire and
		// are used to configure the ALSA device.
		BitDepth:       uint8(min(max(pi.BitDepth, 0), 32)),
		SampleRate:     uint32(max(min(pi.SampleRate, maxSampleRate), 0)),
		DurationSec:    durationSec,
		InitURLs:       initURLs,
		SegmentSeconds: segmentSeconds,
	}, nil
}

// streamExt reports the container for logging. A DASH presentation is always
// fragmented MP4; a BTS manifest is named by its URL.
func streamExt(mimeType, firstURL string) string {
	if base, _, _ := strings.Cut(mimeType, ";"); strings.TrimSpace(base) == manifestDASH {
		return "mp4"
	}
	base, _, _ := strings.Cut(firstURL, "?")
	if i := strings.LastIndex(base, "."); i >= 0 {
		return strings.ToLower(base[i+1:])
	}
	return ""
}
