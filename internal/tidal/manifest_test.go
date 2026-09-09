package tidal

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

// The shape Tidal returns for hi-res: init segment plus a numbered series,
// with the repeat count expressed as SegmentTimeline/@r.
const hiResMPD = `<?xml version="1.0" encoding="utf-8"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT0H3M20.0S">
  <Period id="0">
    <AdaptationSet id="0" contentType="audio" mimeType="audio/mp4" segmentAlignment="true">
      <Representation id="1" bandwidth="4608000" codecs="flac" audioSamplingRate="192000">
        <SegmentTemplate timescale="192000" startNumber="1"
            initialization="https://cdn.tidal.com/init.mp4"
            media="https://cdn.tidal.com/$RepresentationID$/seg-$Number$.mp4">
          <SegmentTimeline>
            <S d="768000" r="3"/>
          </SegmentTimeline>
        </SegmentTemplate>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`

func TestParseManifestDASH(t *testing.T) {
	urls, _, _, err := parseManifest(manifestDASH, b64(hiResMPD))
	if err != nil {
		t.Fatalf("parseManifest: %v", err)
	}
	want := []string{
		"https://cdn.tidal.com/init.mp4",
		"https://cdn.tidal.com/1/seg-1.mp4",
		"https://cdn.tidal.com/1/seg-2.mp4",
		"https://cdn.tidal.com/1/seg-3.mp4",
		"https://cdn.tidal.com/1/seg-4.mp4",
	}
	if !slices.Equal(urls, want) {
		t.Errorf("urls =\n%v\nwant\n%v", urls, want)
	}
}

// r is the *additional* repeat count, so r="3" is four segments. Getting this
// off by one would silently truncate or over-fetch every hi-res track.
func TestParseManifestDASHRepeatCountIsInclusive(t *testing.T) {
	cases := []struct{ r, want int }{
		{0, 1},
		{1, 2},
		{9, 10},
	}
	for _, c := range cases {
		mpd := strings.Replace(hiResMPD, `r="3"`, `r="`+itoa(c.r)+`"`, 1)
		urls, _, _, err := parseManifest(manifestDASH, b64(mpd))
		if err != nil {
			t.Fatalf("r=%d: %v", c.r, err)
		}
		// +1 for the initialization segment.
		if got := len(urls) - 1; got != c.want {
			t.Errorf("r=%d gave %d media segments, want %d", c.r, got, c.want)
		}
	}
}

// A timeline with several entries sums them all.
func TestParseManifestDASHMultipleTimelineEntries(t *testing.T) {
	mpd := strings.Replace(hiResMPD,
		`<S d="768000" r="3"/>`,
		`<S d="768000" r="1"/><S d="384000"/><S d="192000" r="2"/>`, 1)
	urls, _, _, err := parseManifest(manifestDASH, b64(mpd))
	if err != nil {
		t.Fatal(err)
	}
	// 2 + 1 + 3 media segments, plus the init segment.
	if len(urls) != 1+6 {
		t.Errorf("got %d urls, want 7: %v", len(urls), urls)
	}
}

// r="-1" repeats to the end of the period, which has to be resolved against
// the presentation duration rather than guessed.
func TestParseManifestDASHOpenEndedRepeat(t *testing.T) {
	// 200s at timescale 192000 = 38,400,000 ticks; segments of 768000 ticks
	// (4s) give exactly 50.
	mpd := strings.Replace(hiResMPD, `r="3"`, `r="-1"`, 1)
	urls, _, _, err := parseManifest(manifestDASH, b64(mpd))
	if err != nil {
		t.Fatalf("open-ended repeat: %v", err)
	}
	if got := len(urls) - 1; got != 50 {
		t.Errorf("got %d media segments, want 50", got)
	}
}

func TestParseManifestBTS(t *testing.T) {
	m := `{"mimeType":"audio/flac","codecs":"flac","encryptionType":"NONE",` +
		`"urls":["https://cdn.tidal.com/a.flac"]}`
	urls, _, _, err := parseManifest(manifestBTS, b64(m))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(urls, []string{"https://cdn.tidal.com/a.flac"}) {
		t.Errorf("urls = %v", urls)
	}
}

// A MIME type may carry parameters; the manifest still has to be recognised.
func TestParseManifestIgnoresMIMEParameters(t *testing.T) {
	m := `{"urls":["https://cdn.tidal.com/a.flac"],"encryptionType":"NONE"}`
	if _, _, _, err := parseManifest(manifestBTS+"; charset=utf-8", b64(m)); err != nil {
		t.Errorf("MIME parameters should not defeat the type match: %v", err)
	}
}

// An encrypted stream needs a key we cannot obtain, so it must be reported
// rather than handed to FFmpeg as undecodable bytes.
func TestParseManifestRejectsEncryptedBTS(t *testing.T) {
	m := `{"urls":["https://x/a.flac"],"encryptionType":"AES-128"}`
	urls, _, _, err := parseManifest(manifestBTS, b64(m))
	if err == nil {
		t.Fatalf("expected an error for an encrypted stream, got %v", urls)
	}
	if !strings.Contains(err.Error(), "encrypted") {
		t.Errorf("error should say the stream is encrypted, got: %v", err)
	}
}

func TestParseManifestErrors(t *testing.T) {
	cases := []struct {
		name     string
		mimeType string
		manifest string
		wantSub  string
	}{
		{"not base64", manifestBTS, "!!!not base64!!!", "base64"},
		{"unknown mime type", "application/octet-stream", b64("{}"), "unsupported manifest type"},
		{"bts with no urls", manifestBTS, b64(`{"urls":[]}`), "no URLs"},
		{"bts not json", manifestBTS, b64("not json"), "decode BTS manifest"},
		{"dash not xml", manifestDASH, b64("not xml"), "decode DASH manifest"},
		{
			"dash with no segment template",
			manifestDASH,
			b64(`<MPD><Period><AdaptationSet><Representation id="1"/></AdaptationSet></Period></MPD>`),
			"no SegmentTemplate",
		},
		{
			"dash with no timeline",
			manifestDASH,
			b64(`<MPD><Period><AdaptationSet><Representation id="1">` +
				`<SegmentTemplate media="https://x/$Number$.mp4"/>` +
				`</Representation></AdaptationSet></Period></MPD>`),
			"no SegmentTimeline",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, _, _, err := parseManifest(c.mimeType, c.manifest)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("error %q does not mention %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestExpandSegmentURL(t *testing.T) {
	cases := []struct {
		tmpl, repID string
		number      int
		want        string
	}{
		{"https://x/$Number$.mp4", "", 7, "https://x/7.mp4"},
		{"https://x/$RepresentationID$/$Number$.mp4", "1", 2, "https://x/1/2.mp4"},
		{"https://x/no-vars.mp4", "1", 3, "https://x/no-vars.mp4"},
		// $$ is an escaped literal dollar and must not be read as the start of
		// an identifier.
		{"https://x/a$$b/$Number$.mp4", "", 1, "https://x/a$b/1.mp4"},
		// The padded form is what FFmpeg's own DASH muxer emits, and what a
		// real Tidal manifest can carry. Substituting only the bare form
		// would leave the placeholder in the URL and 404 every segment.
		{"https://x/chunk-$Number%05d$.m4s", "", 7, "https://x/chunk-00007.m4s"},
		{"https://x/chunk-stream$RepresentationID$-$Number%05d$.m4s", "0", 12, "https://x/chunk-stream0-00012.m4s"},
		{"https://x/$Number%03d$.mp4", "", 1234, "https://x/1234.mp4"},
	}
	for _, c := range cases {
		if got := expandSegmentURL(c.tmpl, c.repID, c.number); got != c.want {
			t.Errorf("expandSegmentURL(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

func TestParseISODuration(t *testing.T) {
	ok := []struct {
		in   string
		want float64
	}{
		{"PT20S", 20},
		{"PT3M20S", 200},
		{"PT0H3M20.0S", 200},
		{"PT1H", 3600},
		{"PT1.5S", 1.5},
	}
	for _, c := range ok {
		got, err := parseISODuration(c.in)
		if err != nil {
			t.Errorf("parseISODuration(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseISODuration(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "20S", "P1D", "PT", "PT20X", "PT20"} {
		if _, err := parseISODuration(bad); err == nil {
			t.Errorf("parseISODuration(%q) should have failed", bad)
		}
	}
}

func itoa(i int) string {
	if i < 0 {
		return "-" + itoa(-i)
	}
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

// A fragmented stream cannot measure itself: at open time only the
// initialization segment has been read, so libavformat reports the first
// fragment's duration as the whole track's — 3.99s of a four-minute song,
// which scaled the progress bar to a few seconds. The manifest is the only
// source for the real length.
func TestParseManifestDASHReportsPresentationDuration(t *testing.T) {
	// 4 segments of 768000 ticks at timescale 192000 = 4 × 4s = 16s.
	_, duration, _, err := parseManifest(manifestDASH, b64(hiResMPD))
	if err != nil {
		t.Fatal(err)
	}
	if duration != 16 {
		t.Errorf("duration = %v, want 16 (summed from the timeline)", duration)
	}
}

// With an open-ended repeat the timeline cannot be summed, so the declared
// mediaPresentationDuration has to be used instead of reporting nothing.
func TestParseManifestDASHFallsBackToDeclaredDuration(t *testing.T) {
	mpd := strings.Replace(hiResMPD, `r="3"`, `r="-1"`, 1)
	_, duration, _, err := parseManifest(manifestDASH, b64(mpd))
	if err != nil {
		t.Fatal(err)
	}
	if duration != 200 { // PT0H3M20.0S
		t.Errorf("duration = %v, want 200 from mediaPresentationDuration", duration)
	}
}

// A single-file stream measures itself, so there is no duration to pass along
// and the container's own count must be left alone.
func TestParseManifestBTSReportsNoDuration(t *testing.T) {
	m := `{"urls":["https://cdn.tidal.com/a.flac"],"encryptionType":"NONE"}`
	_, duration, _, err := parseManifest(manifestBTS, b64(m))
	if err != nil {
		t.Fatal(err)
	}
	if duration != 0 {
		t.Errorf("duration = %v, want 0 so the container's own count is kept", duration)
	}
}
