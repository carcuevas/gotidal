package player

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/carcuevas/gotidal/internal/logger"
)

// Read-ahead sizing: chunkSize is one producer read, readAheadChunks how many
// may sit buffered ahead of the decoder.
//
// 96 × 64 KiB is 6 MiB, which at 192 kHz/24-bit stereo FLAC (roughly 700 KB/s
// compressed) is around eight seconds of audio — comfortably more than one
// segment, so the next segment is always already in hand.
const (
	chunkSize       = 64 * 1024
	readAheadChunks = 96
)

// segmentReader presents a list of HTTP URLs as one continuous byte stream,
// fetching ahead of the reader so a segment boundary never stalls playback.
//
// This is what makes hi-res playable. Hi-res arrives as an MPEG-DASH
// presentation — an initialization segment carrying the moov box, followed by
// a few hundred numbered media segments — and concatenating them yields the
// fragmented-MP4 stream FFmpeg expects. The decoder reads through a custom
// AVIO callback backed by an io.Reader, so stitching at this level means
// neither the decoder nor the playback loop has to know whether it is playing
// one URL or three hundred.
//
// The read-ahead is not an optimisation. Fetching each segment on demand from
// inside the decode path produced an ALSA underrun at every boundary: a
// 192 kHz stream drains the 4096-frame device buffer in about 21 ms, while a
// fresh HTTPS request takes considerably longer, so playback stuttered once
// per segment — measured as one "Broken pipe" recovery every 4.15 s against
// 4.15 s segments. Running a producer ahead of the decoder takes the boundary
// out of the audio path altogether.
type segmentReader struct {
	ctx    context.Context //nolint:containedctx // the producer outlives the call that creates the reader, and each segment needs its own request
	cancel context.CancelFunc

	urls   []string
	client *http.Client

	chunks  chan []byte
	cur     []byte // unserved remainder of the current chunk
	started bool
	closed  bool

	// producerDone is set once produce() returns, for any reason (stream
	// exhausted, a fetch failed, Close was called). prime polls this rather
	// than len(chunks) alone so a short stream — fewer total chunks than the
	// prime target — doesn't spin until its timeout for buffering that will
	// never arrive.
	producerDone atomic.Bool

	mu      sync.Mutex
	lastErr error
}

// newSegmentReader returns a reader over urls, which must not be empty.
// Fetching begins on the first Read, so a caller that never reads costs
// nothing.
func newSegmentReader(ctx context.Context, client *http.Client, urls []string) (*segmentReader, error) {
	if len(urls) == 0 {
		return nil, errors.New("no stream URLs")
	}
	if client == nil {
		client = http.DefaultClient
	}
	// cancel is stored on the reader and called by Close, which is the only
	// thing that can stop the producer goroutine.
	cctx, cancel := context.WithCancel(ctx) //nolint:gosec // G118: retained as r.cancel and invoked by Close
	return &segmentReader{
		ctx:    cctx,
		cancel: cancel,
		urls:   urls,
		client: client,
		chunks: make(chan []byte, readAheadChunks),
	}, nil
}

func (r *segmentReader) setErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.lastErr == nil {
		r.lastErr = err
	}
}

func (r *segmentReader) err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastErr
}

// produce fetches every segment in order and feeds the chunk channel, running
// as far ahead of the decoder as the channel's capacity allows.
func (r *segmentReader) produce() {
	defer close(r.chunks)
	defer r.producerDone.Store(true)

	for i, u := range r.urls {
		body, err := r.fetch(u, i)
		if err != nil {
			r.setErr(err)
			return
		}
		if !r.drain(body, i) {
			_ = body.Close()
			return
		}
		_ = body.Close()
	}
}

// drain copies one segment body into the chunk channel, reporting false when
// the reader has been closed or the segment failed part-way through.
func (r *segmentReader) drain(body io.Reader, index int) bool {
	for {
		buf := make([]byte, chunkSize)
		n, err := body.Read(buf)
		if n > 0 {
			select {
			case r.chunks <- buf[:n]:
			case <-r.ctx.Done():
				return false
			}
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return true
		}
		// A truncated segment is a real failure, not the end of the track.
		r.setErr(fmt.Errorf("read stream segment %d: %w", index, err))
		return false
	}
}

func (r *segmentReader) fetch(u string, index int) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch stream segment %d: %w", index, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("fetch stream segment %d: HTTP %d", index, resp.StatusCode)
	}
	return resp.Body, nil
}

// ensureStarted launches the producer on first use. Safe to call from both
// Read and prime — whichever runs first wins, and the other is a no-op.
func (r *segmentReader) ensureStarted() {
	if r.started {
		return
	}
	r.started = true
	if n := len(r.urls); n > 1 {
		logger.L.Debug("starting segment read-ahead", "segments", n, "buffer", chunkSize*readAheadChunks)
	}
	go r.produce()
}

// primeChunks and primeTimeout bound how long prime waits for the read-ahead
// buffer to build a cushion before the caller starts consuming it in earnest.
// primeChunks is comfortably more than one ALSA period even at 192kHz/24-bit
// (period sizes there run a few tens of milliseconds; this is on the order of
// a second of audio), and primeTimeout caps the wait so a slow connection
// delays playback start a little rather than indefinitely.
const (
	primeChunks  = 24
	primeTimeout = 300 * time.Millisecond
)

// prime blocks until either primeChunks have queued, the producer has already
// finished (nothing more will ever arrive, e.g. a short stream), or
// primeTimeout elapses — whichever comes first. It never errors: whatever
// state the buffer ends up in, Read reports it accurately.
//
// This exists because avformat's format probe (avformat_find_stream_info)
// reads real data out of this same buffer while opening the stream, which can
// leave it nearly empty right as real playback is about to begin — and a
// 192kHz/24-bit stream drains ALSA's comparatively small buffer fast enough
// that gap surfaces as an audible underrun. It fires on every fresh stream
// open, including the reopen after a seek, which is why "Broken pipe"
// recovery could recur immediately after seeking on a hi-res track — the
// producer has to build its lead back up from zero every time. Priming here,
// after the probe but before real samples start flowing to ALSA, gives it
// that head start.
func (r *segmentReader) prime() {
	r.ensureStarted()
	deadline := time.Now().Add(primeTimeout)
	for len(r.chunks) < primeChunks {
		if r.producerDone.Load() || r.err() != nil || time.Now().After(deadline) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Read serves bytes from the read-ahead buffer, returning io.EOF only once
// every segment has been consumed, and the producer's error otherwise.
func (r *segmentReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.ErrClosedPipe
	}
	r.ensureStarted()

	for len(r.cur) == 0 {
		chunk, ok := <-r.chunks
		if !ok {
			// The producer has finished: either cleanly, or with an error it
			// recorded before closing the channel.
			if err := r.err(); err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		r.cur = chunk
	}

	n := copy(p, r.cur)
	r.cur = r.cur[n:]
	return n, nil
}

// Close stops the producer and abandons anything still buffered.
func (r *segmentReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	r.cancel()
	// Drain what is buffered so the producer is never left blocked on a send
	// while it holds an open response body.
	go func() {
		for range r.chunks {
		}
	}()
	return nil
}

// segmentCount reports how many URLs make up this stream, for logging.
func (r *segmentReader) segmentCount() int { return len(r.urls) }

// StreamSource is everything the player needs to play one track: the ordered
// URLs, plus whatever the provider already knows about the source.
//
// The hints are not a convenience. ALSA is configured — and its format ladder
// walked — before the first frame is decoded, so a depth discovered during
// decoding arrives too late to choose the output format with. Tidal's
// playbackinfo endpoint states the depth and rate up front, and for
// FLAC-in-MP4 (how hi-res is delivered) that is more dependable than hoping
// the container reports bits_per_raw_sample.
type StreamSource struct {
	URLs []string

	// BitDepth and SampleRate are hints; 0 means "unknown, infer from the
	// container". A non-zero hint is only used where the container is silent,
	// never to override something the decoder actually reported.
	BitDepth   uint8
	SampleRate uint32

	// TotalSamples is the whole presentation's length in samples per channel,
	// 0 when unknown.
	//
	// A fragmented stream cannot be measured from its own container: only the
	// initialization segment is available when the decoder opens, so
	// libavformat reports the duration of the first fragment — 3.99 s of a
	// four-minute track — and the progress bar was scaled to that. The DASH
	// manifest states the real length, so it is passed in.
	TotalSamples uint64

	// InitURLs is how many leading entries of URLs are initialization
	// segments (1 for DASH, 0 for a plain stream). They carry the moov box and
	// so must precede any media segment, including after a seek.
	InitURLs int

	// SegmentSeconds is the playing time of one media segment, 0 when the
	// stream is not segmented. It is what makes seeking cheap: without it a
	// seek has to re-fetch and discard everything before the target.
	SegmentSeconds float64
}

// SeekTo returns the source to reopen in order to play from seconds onwards,
// together with how far into that source the target actually lies.
//
// Seeking used to reopen at segment zero and decode-and-discard forward to the
// target. On a segmented hi-res stream that means re-downloading every segment
// before the target — tens of megabytes for a seek into the middle of a track
// — while producing no audio at all, which also starved the visualiser of PCM
// and left the meters frozen. Each media segment is independently decodable
// given the initialization segment, so the reopen can start at the segment
// containing the target and discard at most one segment's worth.
//
// A non-segmented stream is returned unchanged: there is nothing to trim.
func (s StreamSource) SeekTo(seconds float64) (src StreamSource, offsetSeconds float64) {
	if seconds < 0 {
		seconds = 0
	}
	media := len(s.URLs) - s.InitURLs
	if s.SegmentSeconds <= 0 || media <= 1 {
		return s, seconds
	}

	idx := min(max(int(seconds/s.SegmentSeconds), 0), media-1)

	trimmed := make([]string, 0, s.InitURLs+media-idx)
	trimmed = append(trimmed, s.URLs[:s.InitURLs]...)
	trimmed = append(trimmed, s.URLs[s.InitURLs+idx:]...)

	src = s
	src.URLs = trimmed
	return src, seconds - float64(idx)*s.SegmentSeconds
}

// Single returns a StreamSource for one plain URL and no hints.
func Single(url string) StreamSource { return StreamSource{URLs: []string{url}} }

// URL returns the first URL, for logs and comparisons that only need to
// identify the stream rather than read all of it.
func (s StreamSource) URL() string {
	if len(s.URLs) == 0 {
		return ""
	}
	return s.URLs[0]
}
