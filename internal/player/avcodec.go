package player

/*
#include "avcodec.h"
*/
import "C" //nolint:gocritic // dupImport false positive: cgo "C" pseudo-package aliases unsafe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"unsafe" //nolint:gocritic // dupImport false positive: cgo "C" pseudo-package aliases unsafe

	"github.com/carcuevas/gotidal/internal/logger"
)

// streamInfo holds the audio parameters of an opened stream.
type streamInfo struct {
	SampleRate uint32
	NChannels  uint8
	// BitsPerSample is the source container/codec's bit depth (16 or 24 for
	// FLAC/ALAC) — not the S32LE format av_read_samples always outputs — so
	// the ALSA format-preference ladder in alsa.c and the UI's quality badge
	// both see the real source depth. Falls back to 32 for codecs with no
	// fixed depth (lossy AAC), which alsa.c treats the same as an explicit
	// 32: try S32_LE only, no format-preference ladder to walk.
	BitsPerSample uint8
	NSamples      uint64
}

// audioStream wraps an avcodec decoder and the HTTP body feeding it. It is
// the type returned by openStream and consumed by playbackLoop.
//
// The stream owns its body so that Close releases both halves together. The
// playback loop used to track the *http.Response separately, which meant
// every reopen path had to remember to close the one it was replacing — and a
// failed reopen left a nil response for the deferred close to panic on.
type audioStream struct {
	Info    streamInfo
	decoder *avDecoder
	body    *segmentReader
}

// ReadSamples returns the next block of interleaved S32LE samples.
// Returns nil, io.EOF at end of stream.
func (s *audioStream) ReadSamples() ([]int32, error) {
	return s.decoder.readSamples()
}

// primeBuffer waits briefly for the read-ahead buffer to build a cushion
// before real playback resumes — see segmentReader.prime. A no-op if body is
// nil, which only happens after Close.
func (s *audioStream) primeBuffer() {
	if s.body != nil {
		s.body.prime()
	}
}

// Close frees the decoder and the underlying HTTP body. Safe to call twice.
func (s *audioStream) Close() {
	s.decoder.close()
	if s.body != nil {
		_ = s.body.Close()
		s.body = nil
	}
}

// --- reader registry ---------------------------------------------------------
// AVIO callbacks are C functions; they cannot capture Go values. We pass a
// numeric ID as the opaque pointer and look up the Go reader from a map.

// streamReader is the registry entry for one decoder's input: the reader plus
// the last non-EOF error it produced.
//
// The error has to be remembered on the Go side because the C layer can only
// answer with an FFmpeg error code, which loses the detail that makes a
// failure diagnosable — "fetch stream segment 37: HTTP 404" rather than
// "Input/output error".
type streamReader struct {
	r io.Reader

	mu      sync.Mutex
	lastErr error
}

func (sr *streamReader) setErr(err error) {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	if sr.lastErr == nil {
		sr.lastErr = err
	}
}

func (sr *streamReader) err() error {
	sr.mu.Lock()
	defer sr.mu.Unlock()
	return sr.lastErr
}

var (
	readerMu  sync.Mutex
	readerMap = map[uintptr]*streamReader{}
)

// registerReader records r and returns the opaque pointer to hand FFmpeg.
//
// The handle is a real one-byte C allocation rather than a counter cast to a
// pointer. avio_alloc_context takes a void*, and manufacturing one from a
// small integer is pointer arithmetic on a non-pointer: it happens to work,
// but checkptr — which -race enables — rightly rejects it, so the package
// could not be tested under the race detector at all. Allocating gives a
// genuine address, and the only conversion left is pointer-to-uintptr, which
// is always safe.
func registerReader(r io.Reader) unsafe.Pointer {
	key := C.malloc(1)
	readerMu.Lock()
	defer readerMu.Unlock()
	readerMap[uintptr(key)] = &streamReader{r: r}
	return key
}

func lookupReader(key unsafe.Pointer) *streamReader {
	readerMu.Lock()
	defer readerMu.Unlock()
	return readerMap[uintptr(key)]
}

// unregisterReader drops the registration and frees the handle. The handle
// must outlive every read, so this runs only once the decoder is closed.
func unregisterReader(key unsafe.Pointer) {
	readerMu.Lock()
	delete(readerMap, uintptr(key))
	readerMu.Unlock()
	C.free(key)
}

//export avio_read_cb
func avio_read_cb(opaque unsafe.Pointer, buf *C.uint8_t, bufSize C.int) C.int {
	sr := lookupReader(opaque)
	if sr == nil {
		return C.int(C.AVERROR_EOF)
	}
	dst := unsafe.Slice((*byte)(unsafe.Pointer(buf)), int(bufSize))
	n, err := sr.r.Read(dst)
	if n > 0 {
		return C.int(n)
	}
	if err == nil || errors.Is(err, io.EOF) {
		return C.int(C.AVERROR_EOF)
	}
	// A real read failure must not be reported as end-of-stream. Doing so
	// made a dropped connection or a dead segment indistinguishable from the
	// track finishing: the decoder returned EOF, the UI treated it as a
	// completed track and advanced the queue, and the rest of the song was
	// silently dropped. Segmented hi-res makes that far more reachable —
	// hundreds of requests per track instead of one.
	sr.setErr(err)
	return C.int(C.GOTIDAL_AVIO_ERROR)
}

// --- avDecoder ---------------------------------------------------------------

type avDecoder struct {
	d C.av_decoder_t
	// readerKey is the C-allocated opaque pointer FFmpeg passes back to
	// avio_read_cb; it identifies this decoder's reader in readerMap.
	readerKey unsafe.Pointer
	closed    uint32
}

func newAvDecoder(r io.Reader) (*avDecoder, error) {
	dec := &avDecoder{}
	dec.readerKey = registerReader(r)

	rc := C.av_open(&dec.d, dec.readerKey)
	if rc < 0 {
		unregisterReader(dec.readerKey)
		return nil, avErr("avcodec open", rc)
	}
	return dec, nil
}

func (d *avDecoder) readSamples() ([]int32, error) {
	var outBuf *C.int32_t
	var outCount C.int

	rc := C.av_read_samples(&d.d, &outBuf, &outCount)
	if rc < 0 {
		if rc == C.int(C.AVERROR_EOF) {
			return nil, io.EOF
		}
		// Prefer the reader's own error: FFmpeg can only hand back an error
		// code, which reduces "fetch stream segment 37: HTTP 404" to
		// "Input/output error".
		if sr := lookupReader(d.readerKey); sr != nil {
			//nolint:gocritic // uncheckedInlineErr false positive: err is checked on this line
			if err := sr.err(); err != nil {
				return nil, err
			}
		}
		return nil, avErr("av_read_samples", rc)
	}

	n := int(outCount) * int(d.d.channels)
	samples := make([]int32, n)
	src := unsafe.Slice((*int32)(unsafe.Pointer(outBuf)), n)
	copy(samples, src)
	C.av_free(unsafe.Pointer(outBuf))
	return samples, nil
}

func (d *avDecoder) close() {
	if !atomic.CompareAndSwapUint32(&d.closed, 0, 1) {
		return
	}
	C.av_close(&d.d)
	unregisterReader(d.readerKey)
}

func avErr(op string, rc C.int) error {
	buf := make([]byte, 256)
	C.av_strerr(rc, (*C.char)(unsafe.Pointer(&buf[0])), C.int(len(buf)))
	return fmt.Errorf("%s: %s", op, buf)
}

// --- openStream --------------------------------------------------------------

// openStream fetches the audio stream described by urls and opens an avcodec
// decoder over it. A single URL is read directly; several are stitched into
// one byte stream by segmentReader, which is how a hi-res DASH presentation
// is played. The caller must call stream.Close(), which releases both the
// decoder and the HTTP body.
func openStream(ctx context.Context, src StreamSource) (*audioStream, error) {
	body, err := newSegmentReader(ctx, http.DefaultClient, src.URLs)
	if err != nil {
		return nil, err
	}
	if n := body.segmentCount(); n > 1 {
		logger.L.Debug("opening segmented stream", "segments", n)
	}

	dec, err := newAvDecoder(body)
	if err != nil {
		_ = body.Close()
		return nil, err
	}

	// Prefer what the decoder found; fall back to the provider's hint, and
	// only then to 32 (which alsa.c reads as "no ladder to walk, try S32_LE
	// alone"). FLAC-in-MP4 carries its depth in a dfLa box the demuxer does
	// not always surface, so without the hint a 24-bit hi-res stream could
	// be configured as if it were 32-bit.
	bits := uint8(dec.d.bits_per_raw_sample)
	if bits == 0 {
		bits = src.BitDepth
	}
	if bits == 0 {
		bits = 32
	}
	rate := uint32(dec.d.sample_rate)
	if rate == 0 {
		rate = src.SampleRate
	}
	if uint8(dec.d.bits_per_raw_sample) == 0 && src.BitDepth != 0 {
		logger.L.Debug("container reported no bit depth, using the provider hint",
			"bits", src.BitDepth, "rate", rate)
	}

	// The container's own count is only trustworthy when it holds the whole
	// stream. For a fragmented presentation the decoder has seen just the
	// initialization segment, so n_samples describes the first fragment —
	// 3.99 s of a four-minute track, which scaled the progress bar to a few
	// seconds and made it useless. The manifest knows the real length.
	nSamples := uint64(dec.d.n_samples)
	if src.TotalSamples > 0 {
		if nSamples != src.TotalSamples {
			logger.L.Debug("using the manifest sample count over the container's",
				"manifest", src.TotalSamples, "container", nSamples)
		}
		nSamples = src.TotalSamples
	}

	info := streamInfo{
		SampleRate:    rate,
		NChannels:     uint8(dec.d.channels),
		BitsPerSample: bits,
		NSamples:      nSamples,
	}
	return &audioStream{Info: info, decoder: dec, body: body}, nil
}
