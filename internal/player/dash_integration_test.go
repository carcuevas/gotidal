package player

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeDASHFixture renders a short hi-res FLAC and fragments it into real
// MPEG-DASH segments, returning the directory and the segment file names in
// play order (initialization first).
//
// The fixture is generated rather than committed so the test exercises a real
// fragmented-MP4 stream — the same shape Tidal serves hi-res as — without
// carrying binary blobs in the repository. It skips when ffmpeg is absent.
// bits is fixed at 24: hi-res is the case under test, and 16-bit sources are
// already covered by the non-segmented path.
const fixtureBits = 24

func makeDASHFixture(t *testing.T, rate, seconds int) (dir string, segments []string) {
	const bits = fixtureBits
	t.Helper()

	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available; skipping the DASH integration test")
	}

	dir = t.TempDir()
	src := filepath.Join(dir, "src.flac")
	ctx := t.Context()

	sampleFmt := "s16"
	if bits > 16 {
		sampleFmt = "s32"
	}
	//nolint:gosec // G204: every argument is a literal or a test-controlled int
	gen := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error",
		"-f", "lavfi",
		"-i", sine(rate, seconds),
		"-c:a", "flac",
		"-sample_fmt", sampleFmt,
		"-bits_per_raw_sample", itoa(bits),
		src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("could not render the FLAC source (%v): %s", err, out)
	}

	//nolint:gosec // G204: every argument is a literal or a test-controlled path
	frag := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-loglevel", "error",
		"-i", src,
		"-c:a", "copy",
		"-f", "dash",
		"-seg_duration", "2",
		"-use_template", "1",
		"-use_timeline", "1",
		filepath.Join(dir, "manifest.mpd"))
	if out, err := frag.CombinedOutput(); err != nil {
		t.Skipf("could not fragment into DASH (%v): %s", err, out)
	}

	segments = append(segments, "init-stream0.m4s")
	for i := 1; ; i++ {
		name := "chunk-stream0-" + pad5(i) + ".m4s"
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			break
		}
		segments = append(segments, name)
	}
	if len(segments) < 3 {
		t.Fatalf("expected an init segment and at least two media segments, got %v", segments)
	}
	return dir, segments
}

// The whole point of the DASH work: a hi-res track arrives as an
// initialization segment plus a numbered series, and has to decode as one
// continuous 192 kHz / 24-bit stream.
func TestDASHHiResDecodesEndToEnd(t *testing.T) {
	const (
		rate    = 192000
		bits    = 24
		seconds = 6
	)
	dir, segments := makeDASHFixture(t, rate, seconds)

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	urls := make([]string, len(segments))
	for i, name := range segments {
		urls[i] = srv.URL + "/" + name
	}

	stream, err := openStream(context.Background(), StreamSource{
		URLs:       urls,
		BitDepth:   bits,
		SampleRate: rate,
	})
	if err != nil {
		t.Fatalf("openStream over %d segments: %v", len(urls), err)
	}
	defer stream.Close()

	if stream.Info.SampleRate != rate {
		t.Errorf("SampleRate = %d, want %d", stream.Info.SampleRate, rate)
	}
	// This is what the ALSA format ladder is chosen from, so a wrong value
	// here means a 24-bit source played out as something else.
	if stream.Info.BitsPerSample != bits {
		t.Errorf("BitsPerSample = %d, want %d", stream.Info.BitsPerSample, bits)
	}

	total := 0
	for {
		s, err := stream.ReadSamples()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("ReadSamples after %d samples: %v", total, err)
		}
		total += len(s)
	}

	// Every segment must be stitched: a boundary dropped or a segment skipped
	// shows up here as a short count. Allow a little slack for codec padding.
	want := rate * seconds * int(stream.Info.NChannels)
	if lo := want - want/100; total < lo {
		t.Errorf("decoded %d samples, want ~%d — segments were lost at the boundaries", total, want)
	}
	if total > want+want/100 {
		t.Errorf("decoded %d samples, want ~%d — segments were duplicated", total, want)
	}
}

// 96/24 is the fallback target when a track has no 192 kHz master, and it
// takes a different ALSA format path, so it is worth its own pass.
func TestDASH96k24DecodesEndToEnd(t *testing.T) {
	const (
		rate    = 96000
		bits    = 24
		seconds = 4
	)
	dir, segments := makeDASHFixture(t, rate, seconds)

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	urls := make([]string, len(segments))
	for i, name := range segments {
		urls[i] = srv.URL + "/" + name
	}

	stream, err := openStream(context.Background(), StreamSource{URLs: urls, BitDepth: bits, SampleRate: rate})
	if err != nil {
		t.Fatalf("openStream: %v", err)
	}
	defer stream.Close()

	if stream.Info.SampleRate != rate || stream.Info.BitsPerSample != bits {
		t.Errorf("format = %d/%d, want %d/%d",
			stream.Info.SampleRate, stream.Info.BitsPerSample, rate, bits)
	}
	if _, err := stream.ReadSamples(); err != nil {
		t.Errorf("first ReadSamples: %v", err)
	}
}

// A missing segment mid-stream must fail the decode rather than truncate the
// track silently, which would look to the queue like a short song.
func TestDASHMissingSegmentIsAnError(t *testing.T) {
	dir, segments := makeDASHFixture(t, 96000, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve everything except the last media segment.
		if filepath.Base(r.URL.Path) == segments[len(segments)-1] {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		http.ServeFile(w, r, filepath.Join(dir, filepath.Base(r.URL.Path)))
	}))
	defer srv.Close()

	urls := make([]string, len(segments))
	for i, name := range segments {
		urls[i] = srv.URL + "/" + name
	}

	stream, err := openStream(context.Background(), StreamSource{URLs: urls})
	if err != nil {
		// Failing at open is an acceptable outcome too — either way it is an
		// error rather than a silently short stream.
		return
	}
	defer stream.Close()

	var readErr error
	for {
		if _, readErr = stream.ReadSamples(); readErr != nil {
			break
		}
	}
	if errors.Is(readErr, io.EOF) {
		t.Error("a missing segment ended the stream as a clean EOF; it must be an error")
	}
}

func sine(rate, seconds int) string {
	return "sine=frequency=440:sample_rate=" + itoa(rate) + ":duration=" + itoa(seconds)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func pad5(i int) string {
	s := itoa(i)
	for len(s) < 5 {
		s = "0" + s
	}
	return s
}

// Segment-aligned seek rests on each media segment being independently
// decodable given the initialization segment. If that were not true, seeking
// would produce a stream FFmpeg cannot open — so it is worth proving against
// real fragmented MP4 rather than assuming.
func TestDASHSeekFromMidStreamSegmentDecodes(t *testing.T) {
	const (
		rate    = 96000
		bits    = 24
		seconds = 8
	)
	dir, segments := makeDASHFixture(t, rate, seconds)

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	urls := make([]string, len(segments))
	for i, name := range segments {
		urls[i] = srv.URL + "/" + name
	}
	media := len(urls) - 1
	if media < 3 {
		t.Skipf("fixture produced only %d media segments", media)
	}

	full := StreamSource{
		URLs:           urls,
		InitURLs:       1,
		SegmentSeconds: float64(seconds) / float64(media),
		BitDepth:       bits,
		SampleRate:     rate,
	}

	// Seek to the start of the last segment.
	target := full.SegmentSeconds * float64(media-1)
	trimmed, offset := full.SeekTo(target)

	if len(trimmed.URLs) != 2 {
		t.Fatalf("expected init + the last segment, got %d URLs", len(trimmed.URLs))
	}
	if offset > full.SegmentSeconds {
		t.Errorf("offset %v exceeds one segment (%v)", offset, full.SegmentSeconds)
	}

	stream, err := openStream(context.Background(), trimmed)
	if err != nil {
		t.Fatalf("a mid-stream segment plus the init segment must be decodable: %v", err)
	}
	defer stream.Close()

	if stream.Info.SampleRate != rate {
		t.Errorf("SampleRate = %d, want %d", stream.Info.SampleRate, rate)
	}
	if _, err := stream.ReadSamples(); err != nil {
		t.Errorf("decoding from a mid-stream segment: %v", err)
	}
}

// Dropping the initialization segment must fail, which is what makes keeping
// it in every trimmed source load-bearing rather than incidental.
func TestDASHMediaSegmentAloneIsNotDecodable(t *testing.T) {
	dir, segments := makeDASHFixture(t, 96000, 6)

	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	defer srv.Close()

	// Only a media segment, no init segment: no moov box, so nothing to
	// demux with.
	stream, err := openStream(context.Background(), StreamSource{
		URLs: []string{srv.URL + "/" + segments[len(segments)-1]},
	})
	if err != nil {
		return // failing to open is the expected outcome
	}
	defer stream.Close()
	if _, err := stream.ReadSamples(); err == nil {
		t.Error("a media segment without the initialization segment should not decode")
	}
}
