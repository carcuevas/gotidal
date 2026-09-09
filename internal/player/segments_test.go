package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// segmentServer serves /seg-N with the body bodies[N].
func segmentServer(t *testing.T, bodies []string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		var n int
		if _, err := fmt.Sscanf(r.URL.Path, "/seg-%d", &n); err != nil || n < 0 || n >= len(bodies) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, bodies[n])
	}))
	t.Cleanup(srv.Close)
	return srv, &fetches
}

func segURLs(base string, n int) []string {
	urls := make([]string, n)
	for i := range n {
		urls[i] = fmt.Sprintf("%s/seg-%d", base, i)
	}
	return urls
}

// The whole point: several URLs have to read back as one continuous stream,
// because that is what turns a DASH presentation into something FFmpeg can
// demux through a single io.Reader.
func TestSegmentReaderConcatenates(t *testing.T) {
	bodies := []string{"AAAA", "BBBBBB", "CC", "DDDDDDDD"}
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if want := strings.Join(bodies, ""); string(got) != want {
		t.Errorf("read %q, want %q", got, want)
	}
}

// Reading through a buffer far smaller than a segment must still stitch
// correctly — FFmpeg reads in fixed 32 KB chunks that do not line up with
// segment boundaries.
func TestSegmentReaderSmallReads(t *testing.T) {
	bodies := []string{"AAAA", "BBBBBB", "CC"}
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	var out strings.Builder
	buf := make([]byte, 3)
	for {
		n, err := r.Read(buf)
		out.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if want := strings.Join(bodies, ""); out.String() != want {
		t.Errorf("read %q, want %q", out.String(), want)
	}
}

// Nothing is fetched until the stream is actually read, so opening a stream
// that is then abandoned costs no bandwidth.
func TestSegmentReaderFetchesNothingBeforeFirstRead(t *testing.T) {
	bodies := []string{"AAAA", "BBBB", "CCCC", "DDDD"}
	srv, fetches := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	if n := fetches.Load(); n != 0 {
		t.Errorf("constructing the reader fetched %d segments, want 0", n)
	}
}

// Once reading starts, the producer must run *ahead* of the consumer. Fetching
// each segment only when the decoder reaches it underran the ALSA buffer at
// every boundary — one audible stutter per segment, measured as a "Broken
// pipe" recovery every 4.15s against 4.15s segments.
func TestSegmentReaderReadsAhead(t *testing.T) {
	bodies := make([]string, 8)
	for i := range bodies {
		bodies[i] = strings.Repeat("x", 1024)
	}
	srv, fetches := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	// Read only the first few bytes, then let the producer work.
	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}

	// The buffer is far larger than this whole fixture, so the producer should
	// race ahead of the single 4-byte read. Poll rather than sleep a fixed
	// time so the test is not timing-fragile.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fetches.Load() > 1 {
			return // read-ahead confirmed
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("after reading 4 bytes only %d segment(s) were fetched; the producer is not reading ahead",
		fetches.Load())
}

// Everything still has to arrive in order, and exactly once, however far ahead
// the producer runs.
func TestSegmentReaderReadAheadPreservesOrder(t *testing.T) {
	bodies := []string{"1111", "2222", "3333", "4444", "5555", "6666"}
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if want := strings.Join(bodies, ""); string(got) != want {
		t.Errorf("read %q, want %q", got, want)
	}
}

// A dead segment mid-stream must surface as an error, not as a short stream
// that would be mistaken for the end of the track.
func TestSegmentReaderReportsAMissingSegment(t *testing.T) {
	srv, _ := segmentServer(t, []string{"AAAA"})

	urls := []string{srv.URL + "/seg-0", srv.URL + "/seg-99"}
	r, err := newSegmentReader(context.Background(), srv.Client(), urls)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	_, err = io.ReadAll(r)
	if err == nil {
		t.Fatal("a 404 on the second segment should be an error, not a clean EOF")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should name the HTTP status, got: %v", err)
	}
}

func TestSegmentReaderRejectsAnEmptyList(t *testing.T) {
	if _, err := newSegmentReader(context.Background(), nil, nil); err == nil {
		t.Error("an empty URL list should be rejected")
	}
}

// Close must stop further reads rather than silently resuming mid-stream.
func TestSegmentReaderCloseStopsReading(t *testing.T) {
	bodies := []string{"AAAA", "BBBB"}
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	if _, err := r.Read(buf); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := r.Read(buf); err == nil {
		t.Error("reading after Close should fail")
	}
}

// A cancelled context must stop the fetch of the *next* segment, so stopping
// playback does not keep pulling a long track down in the background.
func TestSegmentReaderHonoursContextCancellation(t *testing.T) {
	bodies := []string{"AAAA", "BBBB", "CCCC"}
	srv, _ := segmentServer(t, bodies)

	ctx, cancel := context.WithCancel(context.Background())
	r, err := newSegmentReader(ctx, srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	buf := make([]byte, 4)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	cancel()

	if _, err := io.ReadAll(r); err == nil {
		t.Error("reads should fail once the context is cancelled")
	}
}

func TestStreamSourceHelpers(t *testing.T) {
	s := Single("https://example.com/a.flac")
	if len(s.URLs) != 1 || s.URL() != "https://example.com/a.flac" {
		t.Errorf("Single built %+v", s)
	}
	if s.BitDepth != 0 || s.SampleRate != 0 {
		t.Error("Single should carry no format hints")
	}
	if (StreamSource{}).URL() != "" {
		t.Error("URL() on an empty source should be empty, not panic")
	}
}

// prime must build a real cushion before returning, not just start the
// producer and immediately give up — otherwise it does nothing to prevent
// the underrun it exists to avoid.
func TestPrimeWaitsForACushion(t *testing.T) {
	bodies := make([]string, primeChunks+10)
	for i := range bodies {
		bodies[i] = strings.Repeat("x", chunkSize)
	}
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	r.prime()

	if n := len(r.chunks); n < primeChunks {
		t.Errorf("buffered %d chunks after prime, want at least %d", n, primeChunks)
	}
}

// A stream shorter than the prime target must not be waited on past its own
// end — prime has to notice the producer already finished and return.
func TestPrimeReturnsEarlyOnAShortStream(t *testing.T) {
	bodies := []string{"AAAA", "BBBB", "CCCC"} // far fewer than primeChunks
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	start := time.Now()
	r.prime()
	if elapsed := time.Since(start); elapsed >= primeTimeout {
		t.Errorf("prime took %v on a short, already-finished stream — it should notice"+
			" the producer is done rather than waiting out the full timeout", elapsed)
	}
	if !r.producerDone.Load() {
		t.Error("producer should have finished on a 3-chunk stream well within the timeout")
	}
}

// A stream that never delivers enough must not hang prime forever — it has
// to give up at primeTimeout and let the caller proceed with whatever
// buffered.
func TestPrimeRespectsTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/seg-0") {
			_, _ = io.WriteString(w, "AAAA")
			return
		}
		<-block // every other segment hangs until this test unblocks it below
	}))
	// httptest.Server.Close waits for every in-flight handler to return, so
	// the channel must be closed — unblocking the stalled handler above —
	// before Close runs, not after. t.Cleanup runs LIFO, so registering Close
	// first and the unblock second is what gives the correct order (unblock,
	// then close), even though it reads back to front here.
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, primeChunks+5))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	start := time.Now()
	r.prime()
	elapsed := time.Since(start)
	if elapsed < primeTimeout {
		t.Errorf("prime returned after %v, before its %v timeout, with the stream still stalled", elapsed, primeTimeout)
	}
	if elapsed > primeTimeout+200*time.Millisecond {
		t.Errorf("prime took %v — far longer than its %v timeout", elapsed, primeTimeout)
	}
}

// Priming must not consume the buffer it is trying to build: calling prime
// and then reading normally afterwards should still see everything, in order.
func TestPrimeDoesNotConsumeBufferedData(t *testing.T) {
	bodies := []string{"1111", "2222", "3333"}
	srv, _ := segmentServer(t, bodies)

	r, err := newSegmentReader(context.Background(), srv.Client(), segURLs(srv.URL, len(bodies)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	r.prime()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if want := strings.Join(bodies, ""); string(got) != want {
		t.Errorf("read %q after priming, want %q", got, want)
	}
}

// audioStream.primeBuffer must not panic once the stream has been closed
// (body nilled out) — a defensive no-op, not a nil-pointer crash.
func TestAudioStreamPrimeBufferNilAfterClose(t *testing.T) {
	s := &audioStream{}
	s.primeBuffer() // must not panic with a nil body
}
