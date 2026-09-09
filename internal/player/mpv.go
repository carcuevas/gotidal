package player

/*
#cgo LDFLAGS: -lasound
#include "alsa.h"
*/
import "C" //nolint:gocritic // dupImport false positive: cgo "C" pseudo-package aliases unsafe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe" //nolint:gocritic // dupImport false positive: cgo "C" pseudo-package aliases unsafe

	"github.com/carcuevas/gotidal/internal/logger"
	"github.com/carcuevas/gotidal/internal/sanitize"

	"github.com/godbus/dbus/v5"
)

// knownDACs lists substrings to search for in /proc/asound/cards output.
// First match wins, so order determines priority.
var knownDACs = []string{"hidizs", "s9pro", "focusrite", "scarlett"}

// DeviceInfo describes an ALSA playback device.
type DeviceInfo struct {
	HWName   string // ALSA device string, e.g. "hw:1,0"
	CardName string // short name from brackets, e.g. "S9Pro"
	LongName string // description after " - ", e.g. "HiDizs S9 Pro"
}

// ListDevices returns all ALSA cards that have at least one playback PCM.
func ListDevices() ([]DeviceInfo, error) {
	cardData, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		return nil, fmt.Errorf("cannot read /proc/asound/cards: %w", err)
	}
	pcmData, err := os.ReadFile("/proc/asound/pcm")
	if err != nil {
		return nil, fmt.Errorf("cannot read /proc/asound/pcm: %w", err)
	}

	// Collect card numbers that have at least one playback PCM.
	playback := make(map[int]bool)
	for line := range strings.SplitSeq(string(pcmData), "\n") {
		if !strings.Contains(line, "playback") {
			continue
		}
		var card, dev int
		if _, err := fmt.Sscanf(line, "%d-%d:", &card, &dev); err == nil {
			playback[card] = true
		}
	}

	var devices []DeviceInfo
	for line := range strings.SplitSeq(string(cardData), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		var cardNum int
		//nolint:gocritic // uncheckedInlineErr false positive: err is checked on the next line
		if _, err := fmt.Sscanf(trimmed, "%d", &cardNum); err != nil {
			continue // continuation line, not a card header
		}
		if !playback[cardNum] {
			continue
		}
		cardName := ""
		if s := strings.Index(line, "["); s != -1 {
			if e := strings.Index(line, "]"); e > s {
				cardName = strings.TrimSpace(line[s+1 : e])
			}
		}
		longName := ""
		if _, after, found := strings.Cut(line, " - "); found {
			longName = strings.TrimSpace(after)
		}
		if longName == "" {
			longName = cardName
		}
		// The card and long names in /proc/asound/cards come from the device's
		// own USB string descriptors — remote text by any reasonable
		// definition — and are rendered into the device picker, so they get
		// the same escape-stripping as anything off the network.
		devices = append(devices, DeviceInfo{
			HWName:   fmt.Sprintf("hw:%d,0", cardNum),
			CardName: sanitize.Text(cardName),
			LongName: sanitize.Text(longName),
		})
	}
	return devices, nil
}

type Player struct {
	mu             sync.Mutex
	cancel         context.CancelFunc
	doneCh         chan struct{}
	deviceOverride string // set via SetDevice; empty = auto-detect
	currentURL     string // stored so Seek can signal the playback loop

	// dacMode selects the output path. true (the default) opens the ALSA
	// hw: device directly for bit-perfect output, reserved exclusively via
	// D-Bus so PipeWire releases it first. false instead opens the ALSA
	// "default" PCM — normally PipeWire's own plugin — cooperating with
	// whatever PipeWire currently routes to (laptop speakers, HDMI,
	// Bluetooth, ...) rather than stealing a device from it, so playback
	// works without a recognized DAC connected. Trades away bit-perfectness:
	// PipeWire may resample or mix. See SetDACMode.
	dacMode bool

	// seekCh carries seek targets (in samples) to the running playback loop.
	// Buffered 1 so Seek never blocks; the loop drains it before checking again.
	seekCh chan uint64

	// nextURLCh carries the next track's stream URL into the running
	// playbackLoop so it can transition without closing the ALSA device.
	// Buffered 1 so PlayNext never blocks.
	nextURLCh chan string
	// transitionDoneCh is set by PlayNext() before sending on nextURLCh.
	// The playbackLoop installs it as the new doneCh once the new stream starts.
	transitionDoneCh chan struct{}
	// skipCh is closed by PlayNext to interrupt the current streamLoop
	// immediately, so the outer loop can pick up the next URL without
	// waiting for the current track to finish.
	skipCh chan struct{}
	// loopDone is closed when the playbackLoop goroutine returns.
	// Used by stop() to wait for the goroutine independently of doneCh.
	loopDone chan struct{}

	// pausedCh carries an out-of-band notification that the player forced
	// itself back into the paused state — currently only when reacquiring the
	// ALSA device on resume failed. The UI must learn about this: it drives
	// play/pause optimistically (flip the atomic, flip the label), so a state
	// change the player makes on its own would otherwise invert the meaning of
	// every subsequent play/pause press. Buffered 1 and sent non-blocking, so
	// the playback loop never stalls when no UI is listening.
	pausedCh chan error

	// Track info — written by playbackLoop, read by UI tick
	muInfo        sync.RWMutex
	sampleRate    uint32
	channels      uint8
	bitsPerSample uint8
	totalSamples  uint64
	// hintDuration is set by SetDuration from the Tidal API track.Duration field
	// and used as a fallback when totalSamples is 0 (e.g. streaming mp4).
	hintDuration float64
	// activeDevice is the ALSA device string actually opened, which differs
	// from the requested one when the plughw: fallback engaged. bitPerfect
	// reports whether that path preserves samples untouched.
	activeDevice string
	bitPerfect   bool

	// plugFallback memoises devices whose hw: endpoint refused the requested
	// format, so pause/resume and gapless transitions skip the known-failing
	// hw: open (and its reservation stall) instead of re-paying it every time.
	muPlug       sync.Mutex
	plugFallback map[string]string

	// Atomics: safe for concurrent access without a mutex
	samplesPlayed uint64
	paused        uint32 // 0 = playing, 1 = paused
	volumeBits    uint64 // float64 stored via math.Float64bits; range 0.0–1.0

	// pcmTapCh is a best-effort tee of the same packed PCM buffer written to
	// ALSA, consumed by internal/visualizer to drive the CAVA spectrum
	// visualizer. Allocated unconditionally in NewPlayer (not lazily) so the
	// decode goroutine never needs to lock to read it; sends are always
	// non-blocking (select-with-default) so a slow or absent consumer can only
	// ever drop tap frames, never the bit-perfect write to snd_pcm_writei.
	pcmTapCh chan []byte

	// interTrackSilenceMs is the length of digital silence (in milliseconds)
	// written between consecutive tracks. 0 (the default) preserves gapless
	// playback exactly as before. A non-zero value trades that gap away in
	// exchange for a real silent gap a downstream recorder's own
	// silence-based auto-track-detection can key off — it never touches
	// either track's own samples, so it doesn't affect bit-perfectness.
	interTrackSilenceMs atomic.Uint32
}

// SetInterTrackSilenceMs sets how much digital silence (in milliseconds) to
// write between consecutive tracks. 0 disables it (gapless, the default).
func (p *Player) SetInterTrackSilenceMs(ms uint32) { p.interTrackSilenceMs.Store(ms) }

// TapPCM returns a channel that receives a copy of every packed PCM buffer
// this player writes to ALSA (post-volume, pre-write — same bytes, same
// format). Consumers must drain it promptly: sends are non-blocking, so a slow
// reader simply misses frames rather than backing up the audio path.
func (p *Player) TapPCM() <-chan []byte { return p.pcmTapCh }

// Format reports the current stream's sample rate and channel count (0/0
// before any track has started).
func (p *Player) Format() (rate uint32, channels uint8) {
	p.muInfo.RLock()
	defer p.muInfo.RUnlock()
	return p.sampleRate, p.channels
}

// SetDevice sets the ALSA hw device to use for playback. Pass "" to revert to
// auto-detection from the known-DAC list.
func (p *Player) SetDevice(hwName string) {
	p.mu.Lock()
	p.deviceOverride = hwName
	p.mu.Unlock()
}

// pipewireDevice is the ALSA PCM name used in PipeWire mode (SetDACMode
// false) — the generic "default" alias rather than a hardcoded "pipewire",
// so this also works unmodified on a system where ALSA's default is routed
// some other way (plain ALSA, PulseAudio's own ALSA plugin, etc.).
const pipewireDevice = "default"

// SetDACMode selects the output path: true (the default) resolves and opens
// the ALSA hw: device exactly as before (SetDevice / auto-detect, D-Bus
// reservation, bit-perfect format negotiation); false instead always opens
// pipewireDevice, skipping the reservation entirely since nothing is being
// taken from PipeWire. Takes effect on the next Play() — a track already
// playing keeps running under whichever mode it started with.
func (p *Player) SetDACMode(on bool) {
	p.mu.Lock()
	p.dacMode = on
	p.mu.Unlock()
}

// DACMode reports the currently configured output path (see SetDACMode).
func (p *Player) DACMode() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dacMode
}

// getDevice returns the configured device override or falls back to auto-detection.
func (p *Player) getDevice() (string, error) {
	p.mu.Lock()
	override := p.deviceOverride
	p.mu.Unlock()
	if override != "" {
		return override, nil
	}
	return detectDevice()
}

// effectiveDevice returns the device to actually open for the given requested
// device, substituting the memoised plughw: equivalent when a previous open
// established that this hw: endpoint refuses our format.
func (p *Player) effectiveDevice(device string) string {
	p.muPlug.Lock()
	defer p.muPlug.Unlock()
	if plug, ok := p.plugFallback[device]; ok {
		return plug
	}
	return device
}

// rememberPlugFallback memoises that requested must be opened via plug so
// subsequent reopens skip the known-failing hw: attempt.
func (p *Player) rememberPlugFallback(requested, plug string) {
	if requested == plug {
		return
	}
	p.muPlug.Lock()
	defer p.muPlug.Unlock()
	if p.plugFallback == nil {
		p.plugFallback = make(map[string]string)
	}
	p.plugFallback[requested] = plug
}

// openDevice opens the ALSA device for a playback loop, applying the memoised
// plughw: fallback and recording the resulting path (device + bit-perfect
// status) on the Player so the UI can surface it.
func (p *Player) openDevice(ctx context.Context, requested string, channels uint8, rate uint32, bits uint8) (*alsaHandle, error) {
	ah, err := openALSA(ctx, p.effectiveDevice(requested), channels, rate, bits)
	if err != nil {
		return nil, err
	}
	p.rememberPlugFallback(requested, ah.device)
	p.mu.Lock()
	dacMode := p.dacMode
	p.mu.Unlock()
	p.muInfo.Lock()
	p.activeDevice = ah.device
	// In PipeWire mode the negotiated format never reflects genuine
	// bit-perfectness — PipeWire's own graph may still resample or mix
	// downstream — regardless of whether our own hw:->plughw: fallback
	// engaged, so force the badge false rather than let ah.bitPerfect (which
	// only tracks that one narrow condition) claim otherwise.
	p.bitPerfect = ah.bitPerfect && dacMode
	p.muInfo.Unlock()
	return ah, nil
}

// AudioPath reports the ALSA device actually in use and whether that path is
// bit-perfect. Returns ("", true) before any device has been opened.
func (p *Player) AudioPath() (device string, bitPerfect bool) {
	p.muInfo.RLock()
	defer p.muInfo.RUnlock()
	if p.activeDevice == "" {
		return "", true
	}
	return p.activeDevice, p.bitPerfect
}

func NewPlayer() *Player {
	p := &Player{
		seekCh:    make(chan uint64, 1),
		nextURLCh: make(chan string, 1),
		pausedCh:  make(chan error, 1),
		skipCh:    make(chan struct{}),
		pcmTapCh:  make(chan []byte, 4),
		dacMode:   true,
	}
	atomic.StoreUint64(&p.volumeBits, math.Float64bits(1.0))
	return p
}

// Start is a no-op; the ALSA handle is opened per-track in Play.
func (p *Player) Start(_ context.Context) error { return nil }

// detectDevice scans /proc/asound/cards for a known DAC and returns the
// hw device string, e.g. "hw:1,0".
func detectDevice() (string, error) {
	data, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		return "", fmt.Errorf("cannot read ALSA cards: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, name := range knownDACs {
			if !strings.Contains(lower, name) {
				continue
			}
			// The card number is the leading integer on the card's first line.
			// Search current line and the one above it.
			for j := i; j >= 0 && j >= i-1; j-- {
				var num int
				if _, err := fmt.Sscanf(strings.TrimSpace(lines[j]), "%d", &num); err == nil {
					return fmt.Sprintf("hw:%d,0", num), nil
				}
			}
		}
	}
	return "", errors.New("no supported DAC found — connect a Hidizs S9 Pro or Focusrite Scarlett Solo")
}

// parseCardNum extracts the card number from an ALSA hw device string like "hw:1,0".
func parseCardNum(hwDevice string) (int, error) {
	var card, dev int
	// Accept "hw:N,M", "plughw:N,M", and "front:N".
	for _, prefix := range []string{"plughw:%d,%d", "hw:%d,%d"} {
		if _, err := fmt.Sscanf(hwDevice, prefix, &card, &dev); err == nil {
			return card, nil
		}
	}
	if _, err := fmt.Sscanf(hwDevice, "front:%d", &card); err == nil {
		return card, nil
	}
	return 0, fmt.Errorf("cannot parse card number from %q", hwDevice)
}

// Timing budgets for the reserve→open sequence. These stack on the resume
// path (reacquireALSA calls reserveALSADevice then openALSA), and the total
// must stay comfortably under shutdownTimeout: stop() waits that long for the
// loop to exit, and if it gives up, Play() refuses to start the next track
// with "previous playback is still shutting down". A user who hits resume and
// immediately picks a different track walks straight into that sum.
//
//	releaseCallTimeout + releaseSettleDelay + openBusyRetryBudget
//	     500ms         +       200ms        +        800ms        = 1.5s  < 3s
//
// Every wait below also observes ctx, so a cancelled loop leaves early rather
// than spending its full budget.
const (
	// releaseCallTimeout bounds the ReserveDevice1.RequestRelease D-Bus call.
	// An owner that does not implement the interface never replies.
	releaseCallTimeout = 500 * time.Millisecond
	// releaseSettleDelay gives the previous owner a moment to actually close
	// its ALSA handle after it agrees to release the name.
	releaseSettleDelay = 200 * time.Millisecond
	// openBusyRetryBudget bounds how long openALSA retries EBUSY from
	// snd_pcm_open while the previous owner finishes closing its handle.
	openBusyRetryBudget = 800 * time.Millisecond
	// openBusyRetryInterval is the delay between those EBUSY retries.
	openBusyRetryInterval = 100 * time.Millisecond
	// shutdownTimeout bounds how long stop() waits for the playback loop.
	shutdownTimeout = 3 * time.Second
)

// reserveALSADevice acquires the org.freedesktop.ReserveDevice1.Audio{N} D-Bus
// name so that PipeWire/PulseAudio releases the hw: device before we open it.
// If D-Bus is unavailable the function returns a no-op release func and nil error
// so callers can proceed unconditionally.
// The context bounds the whole exchange: reservation happens on the playback
// hot path (initial open and pause→resume reacquire), and a cancelled loop
// must not linger here past stop()'s shutdown window.
func reserveALSADevice(ctx context.Context, cardNum int) (release func(), err error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		// No session bus — skip reservation and try to open ALSA directly.
		return func() {}, nil //nolint:nilerr // no session bus is not an error; callers proceed without reservation
	}

	name := fmt.Sprintf("org.freedesktop.ReserveDevice1.Audio%d", cardNum)
	objPath := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/ReserveDevice1/Audio%d", cardNum))

	releaseFunc := func() {
		_, _ = conn.ReleaseName(name)
		_ = conn.Close()
	}

	// Ask the current owner (WirePlumber) to release the device, then claim
	// the name ourselves with ReplaceExisting so WirePlumber cannot reopen it.
	// If the call errors it means nobody currently holds the name (no owner to
	// dispatch to), so the device is already free — skip straight to RequestName.
	// Only treat an explicit released==false as a hard refusal.
	// The call gets its own short deadline: the owner may be an instance that
	// does not implement RequestRelease and never replies, and a bare Call
	// would block forever.
	//
	// The three outcomes are distinct and must stay distinct:
	//   - reply released==false  → an explicit refusal; honour it and fail.
	//   - deadline exceeded      → an owner exists but is slow (heavy load, a
	//                              JACK client mid-callback). Stealing the name
	//                              with ReplaceExisting would cut its stream
	//                              out from under it, which is exactly what the
	//                              ReserveDevice1 protocol exists to prevent —
	//                              so back off and let the caller retry.
	//   - any other call error   → nobody owns the name (nothing to dispatch
	//                              to), so the device is already free; proceed.
	obj := conn.Object(name, objPath)
	var released bool
	callCtx, callCancel := context.WithTimeout(ctx, releaseCallTimeout)
	callErr := obj.CallWithContext(callCtx,
		"org.freedesktop.ReserveDevice1.RequestRelease", 0, int32(math.MaxInt32)).Store(&released)
	callTimedOut := callCtx.Err() != nil
	callCancel()
	if callErr == nil && !released {
		_ = conn.Close()
		return nil, fmt.Errorf("audio device Audio%d is held by another process and refused to release", cardNum)
	}
	if ctx.Err() != nil {
		_ = conn.Close()
		return nil, ctx.Err()
	}
	if callTimedOut {
		_ = conn.Close()
		return nil, fmt.Errorf("audio device Audio%d: owner did not answer RequestRelease within %s", cardNum, releaseCallTimeout)
	}

	// Give WirePlumber a moment to close its ALSA handle before we claim the
	// name and open the device.
	select {
	case <-time.After(releaseSettleDelay):
	case <-ctx.Done():
		_ = conn.Close()
		return nil, ctx.Err()
	}

	// Claim the name with ReplaceExisting so we take it even if WirePlumber
	// still holds it, and AllowReplacement so it can be returned on release.
	reply, err := conn.RequestName(name,
		dbus.NameFlagReplaceExisting|dbus.NameFlagAllowReplacement)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to claim Audio%d reservation", cardNum)
	}

	return releaseFunc, nil
}

// reserveDevice claims the D-Bus device reservation ahead of a bit-perfect
// hw: open, or is a no-op in PipeWire mode (dacMode false) — there's nothing
// to reserve exclusively when cooperating with PipeWire's own graph instead
// of stealing the device from it.
func (p *Player) reserveDevice(ctx context.Context, dacMode bool, device string) (func(), error) {
	if !dacMode {
		return func() {}, nil
	}
	cardNum, err := parseCardNum(device)
	if err != nil {
		return nil, err
	}
	return reserveALSADevice(ctx, cardNum)
}

// packPCM writes avcodec's always-S32LE samples into dst in the format ALSA
// actually negotiated: bps bytes per sample, little-endian, each sample first
// shifted right by shift (see sampleShift) so only the bits the device carries
// remain. vol scales the sample; 1.0 skips the multiply entirely.
//
// The per-sample byte count must follow bps rather than always being 4: on
// S16_LE (bps 2) or S24_3LE (bps 3) — the formats alsa.c prefers for 16- and
// 24-bit sources — writing a fixed 4 bytes at a stride of bps overlaps every
// earlier sample and runs past the end of the last one's slot, which is an
// out-of-range panic on the very first buffer.
//
// dst must be at least len(samples)*bps long.
func packPCM(dst []byte, samples []int32, bps, shift int, vol float64) {
	for i, s := range samples {
		if vol != 1.0 {
			s = int32(float64(s) * vol)
		}
		// The two's-complement bit pattern is exactly what the device wants.
		v := uint32(s >> shift)
		off := i * bps
		for b := range bps {
			dst[off+b] = byte(v >> (8 * b))
		}
	}
}

// sampleShift returns how far right to shift avcodec's always-S32LE samples
// so that only the bits the negotiated ALSA format actually carries remain in
// the low bytes: 0 for S32_LE, 8 for S24_3LE/S24_LE, 16 for S16_LE. The shift
// is arithmetic, so the sign is preserved — which is also exactly what
// S24_LE's sign-extended 4-byte slot expects. Falls back to the container
// width if the driver didn't report significant bits.
func sampleShift(ah *alsaHandle) int {
	sbits := ah.significantBits
	if sbits <= 0 || sbits > 32 {
		sbits = min(ah.bytesPerSample*8, 32)
	}
	return 32 - sbits
}

type alsaHandle struct {
	pcm             *C.snd_pcm_t
	device          string // ALSA device string actually opened (may differ from the requested one on plughw: fallback)
	format          C.snd_pcm_format_t
	bytesPerSample  int
	significantBits int // actual DAC bit depth
	rate            uint32
	periodSize      uint64
	bufferSize      uint64
	availMin        uint64
	startThreshold  uint64
	stopThreshold   uint64
	// bitPerfect reports whether samples reach the DAC untouched. False when
	// the plughw: fallback engaged, meaning ALSA's plug layer is resampling
	// and/or remixing to the hardware's fixed native shape.
	bitPerfect bool
}

// openALSA opens an ALSA hw device, negotiating the best available format for
// the source bit depth without enabling soft resampling (bit-perfect).
//
// Only the open is retried on EBUSY, and only for openBusyRetryBudget: after
// we reclaim the D-Bus reservation on resume, WirePlumber may take a moment to
// close its own handle, so the first opens can fail with EBUSY. Format
// negotiation is deliberately outside the retry — an EBUSY surfaced from
// snd_pcm_hw_params means the parameters clash rather than the device being
// momentarily taken, and reopening a fresh PCM for each attempt would burn the
// whole budget on a failure that cannot resolve itself. The retry loop
// observes ctx so a cancelled playback loop exits immediately instead of
// finishing the retry window.
//
// If format negotiation itself is refused on a hw: device, the open is retried
// once through ALSA's plug layer. Some USB interfaces (e.g. Focusrite's
// Vocaster line) expose a fixed native channel-count/rate/format and reject
// anything else; plughw: resamples and remixes to that shape. That forfeits
// bit-perfect output, so the returned handle reports bitPerfect=false and
// callers surface the downgrade. Only the negotiation step is eligible for
// this fallback — a device that is merely busy is retried above as hw: and
// never downgraded.
func openALSA(ctx context.Context, device string, channels uint8, rate uint32, bits uint8) (*alsaHandle, error) {
	handle, result, err := openALSARaw(ctx, device, channels, rate, bits)
	bitPerfect := true
	if err != nil && errors.Is(err, errFormatRefused) && strings.HasPrefix(device, "hw:") {
		plugDevice := "plughw:" + strings.TrimPrefix(device, "hw:")
		logger.L.Warn("openALSA: hw: refused the requested format, retrying via plughw: (output will no longer be bit-perfect)",
			"device", device, "plugDevice", plugDevice, "err", err)
		handle, result, err = openALSARaw(ctx, plugDevice, channels, rate, bits)
		if err == nil {
			device = plugDevice
			bitPerfect = false
		}
	}
	if err != nil {
		return nil, err
	}

	return &alsaHandle{
		pcm:             handle,
		device:          device,
		format:          result.format,
		bytesPerSample:  int(result.bytes_per_sample),
		significantBits: int(result.significant_bits),
		rate:            uint32(result.rate),
		periodSize:      uint64(result.period_size),
		bufferSize:      uint64(result.buffer_size),
		availMin:        uint64(result.avail_min),
		startThreshold:  uint64(result.start_threshold),
		stopThreshold:   uint64(result.stop_threshold),
		bitPerfect:      bitPerfect,
	}, nil
}

// errFormatRefused marks a failure of the format-negotiation step, i.e. the
// device answered the open but rejected the requested channel count, rate, or
// sample format. It is the only condition that justifies falling back to
// plughw:; a busy device is retried as hw: instead.
var errFormatRefused = errors.New("device refused the requested PCM format")

// openALSARaw opens and configures a single device, without any plughw:
// fallback. The EBUSY retry covers only snd_pcm_open — see openALSA.
func openALSARaw(ctx context.Context, device string, channels uint8, rate uint32, bits uint8) (handle *C.snd_pcm_t, result C.alsa_open_result_t, err error) {
	cdev := C.CString(device)
	defer C.free(unsafe.Pointer(cdev))

	deadline := time.Now().Add(openBusyRetryBudget)
	for {
		rc := C.open_hw_device(cdev, &handle)
		if rc >= 0 {
			break
		}
		if rc == -C.EBUSY && time.Now().Before(deadline) {
			select {
			case <-time.After(openBusyRetryInterval):
				continue
			case <-ctx.Done():
				return nil, result, ctx.Err()
			}
		}
		return nil, result, fmt.Errorf("snd_pcm_open(%s): %s", device, C.GoString(C.snd_strerror(rc)))
	}

	// configure_hw_pcm closes the handle itself on failure.
	if rc := C.configure_hw_pcm(
		C.uint(channels), C.uint(rate), C.int(bits),
		&handle, &result,
	); rc < 0 {
		return nil, result, fmt.Errorf("configure_hw_pcm(%s, ch=%d, rate=%d, bits=%d): %s: %w",
			device, channels, rate, bits, C.GoString(C.snd_strerror(rc)), errFormatRefused)
	}

	return handle, result, nil
}

// Play starts playback of the given URL and returns the done channel for this
// track. The channel is closed when playback finishes naturally. Callers should
// use the returned channel directly rather than calling Done() separately to
// avoid a race between stop() clearing doneCh and the new one being set.
func (p *Player) Play(url string) (<-chan struct{}, error) {
	// If the previous loop does not shut down within stop()'s window, refuse
	// to start a second one: two loops would fight over the ALSA device and
	// the D-Bus reservation, with the survivor playing the wrong track.
	if !p.stop() {
		return nil, errors.New("previous playback is still shutting down, try again")
	}

	p.mu.Lock()
	dacMode := p.dacMode
	p.mu.Unlock()

	// Resolve device and acquire D-Bus reservation synchronously so we can
	// return an error to the caller if the device cannot be claimed. In
	// PipeWire mode there's no hw: device to resolve or reserve — see
	// SetDACMode.
	device := pipewireDevice
	var err error
	if dacMode {
		device, err = p.getDevice()
		if err != nil {
			return nil, err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	releaseReservation, err := p.reserveDevice(ctx, dacMode, device)
	if err != nil {
		cancel()
		return nil, err
	}
	doneCh := make(chan struct{})
	loopDone := make(chan struct{})

	p.mu.Lock()
	p.cancel = cancel
	p.doneCh = doneCh
	p.loopDone = loopDone
	p.currentURL = url
	p.skipCh = make(chan struct{})
	p.mu.Unlock()

	atomic.StoreUint64(&p.samplesPlayed, 0)
	atomic.StoreUint32(&p.paused, 0)
	p.muInfo.Lock()
	p.totalSamples = 0
	p.muInfo.Unlock()
	// Drain any pending seek/next-URL so the new track starts cleanly.
	select {
	case <-p.seekCh:
	default:
	}
	select {
	case <-p.nextURLCh:
	default:
	}

	// releaseReservation is passed into playbackLoop which manages it for the
	// lifetime of playback (releasing on pause, reacquiring on resume, and
	// releasing again on final exit via defer).
	go func() {
		defer close(loopDone)
		natural := p.playbackLoop(ctx, url, device, releaseReservation)
		// Only close doneCh when the loop ended naturally (track finished or
		// transitioned). An aborted loop (openALSA failed, stream error, etc.)
		// must not close doneCh, otherwise the UI treats it as a completed
		// track and auto-advances into the same broken state.
		if !natural {
			p.mu.Lock()
			p.doneCh = nil
			p.mu.Unlock()
			return
		}
		p.mu.Lock()
		ch := p.doneCh
		p.doneCh = nil
		p.mu.Unlock()
		if ch != nil {
			close(ch)
		}
	}()
	return doneCh, nil
}

// stop cancels the running playback loop and waits for it to exit. Returns
// false if the loop is still alive when the wait times out — callers must not
// start a new loop in that case.
//
// p.cancel/p.loopDone are cleared only once the loop has actually exited. On
// the timeout path they stay in place so a later stop() can re-cancel and keep
// waiting on the same loop: clearing them eagerly would make the retry see a
// nil cancel, return true immediately, and let Play() start a second loop while
// the first still holds the ALSA device and the D-Bus reservation.
func (p *Player) stop() bool {
	p.mu.Lock()
	cancel := p.cancel
	loopDone := p.loopDone
	p.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if loopDone != nil {
		select {
		case <-loopDone:
		case <-time.After(shutdownTimeout):
			return false
		}
	}

	// The loop is gone. Clear the handles, but only if they are still the ones
	// we waited on — a concurrent Play() may already have installed a new loop.
	p.mu.Lock()
	if p.loopDone == loopDone {
		p.cancel = nil
		p.loopDone = nil
	}
	p.mu.Unlock()
	return true
}

// PlayNext signals the running playbackLoop to transition to a new track URL
// without closing the ALSA device. If the new track has a different format
// (sample rate, channels, bits), the loop will close and reopen the device
// internally. If no playback loop is running, it falls back to Play().
func (p *Player) PlayNext(url string) (<-chan struct{}, error) {
	p.mu.Lock()
	loopDone := p.loopDone
	p.mu.Unlock()

	if loopDone == nil {
		return p.Play(url)
	}
	// Check if the loop is actually still alive.
	select {
	case <-loopDone:
		return p.Play(url)
	default:
	}

	newDone := make(chan struct{})

	p.mu.Lock()
	p.transitionDoneCh = newDone
	// Close the current skipCh to interrupt the running streamLoop, then
	// create a fresh one for the next track.
	close(p.skipCh)
	p.skipCh = make(chan struct{})
	p.mu.Unlock()

	// Drain any stale next-URL, then send the new one.
	select {
	case <-p.nextURLCh:
	default:
	}
	p.nextURLCh <- url

	atomic.StoreUint32(&p.paused, 0)

	// The loop may have hit its handoff timeout in the window between the
	// liveness check above and this send. awaitNextURL re-reads the channel
	// once on timeout to catch that, but it cannot catch a send that lands
	// after it has already given up — so confirm the loop is still there. If
	// it has gone, take the URL back and start a fresh loop, which gives the
	// caller a done channel that will actually close instead of a track that
	// silently never plays.
	select {
	case <-loopDone:
		_, _ = p.takeQueuedURL()
		p.mu.Lock()
		p.transitionDoneCh = nil
		p.mu.Unlock()
		return p.Play(url)
	default:
	}

	return newDone, nil
}

// closeALSA drains and closes an ALSA handle. It is idempotent: the pcm
// pointer is cleared after closing so a second call (e.g. the playbackLoop
// cleanup defer running after a pause already released the device) is a no-op
// instead of a use-after-free inside libasound.
func closeALSA(ah *alsaHandle) {
	if ah == nil || ah.pcm == nil {
		return
	}
	C.snd_pcm_drain(ah.pcm)
	C.snd_pcm_close(ah.pcm)
	ah.pcm = nil
}

// writeSilence writes ms milliseconds of zero-valued PCM to ah at the given
// rate/channels/bytesPerSample — pure digital silence, not a fade or a
// resample of either adjacent track, so it never touches their samples.
// Recovers from a transient xrun the same way the main write loop does;
// gives up (logging, not erroring — a missed silence gap is cosmetic) if
// recovery itself fails.
func writeSilence(ah *alsaHandle, ms uint32, channels uint8, bytesPerSample int) {
	if ms == 0 || ah == nil || ah.pcm == nil {
		return
	}
	frames := int(uint64(ms) * uint64(ah.rate) / 1000)
	if frames <= 0 {
		return
	}
	buf := make([]byte, frames*int(channels)*bytesPerSample)
	written := 0
	for written < frames {
		rc := C.snd_pcm_writei(ah.pcm, unsafe.Pointer(&buf[written*int(channels)*bytesPerSample]), C.snd_pcm_uframes_t(frames-written))
		if rc < 0 {
			if rec := C.snd_pcm_recover(ah.pcm, C.int(rc), C.int(1)); rec < 0 {
				logger.L.Warn("writeSilence: recover failed, skipping remaining silence",
					"err", C.GoString(C.snd_strerror(rec)))
				return
			}
			continue
		}
		written += int(rc)
	}
}

// playbackLoop runs the full playback lifecycle for a track (and subsequent
// gapless transitions). Returns true if playback ended naturally (track
// finished or transitioned), false if it aborted due to an error before any
// audio was produced (e.g. openALSA failed, stream could not be opened).
func (p *Player) playbackLoop(ctx context.Context, url, device string, releaseReservation func()) bool {
	logger.L.Debug("playbackLoop start")

	p.mu.Lock()
	dacMode := p.dacMode
	p.mu.Unlock()

	// cardNum is only meaningful in DAC mode — pipewireDevice ("default")
	// doesn't parse as an hw:/plughw: string, and reacquireALSA below skips
	// reserveALSADevice entirely when !dacMode, so it's never dereferenced.
	var cardNum int
	if dacMode {
		var err error
		cardNum, err = parseCardNum(device)
		if err != nil {
			logger.L.Error("playbackLoop: cannot parse card number", "device", device, "err", err)
			releaseReservation()
			return false
		}
	}

	resp, stream, err := openStream(ctx, url)
	if err != nil {
		logger.L.Error("failed to open stream", "err", err)
		releaseReservation()
		return false
	}
	// resp is reassigned on every seek / next-track reopen below; the inner
	// transitions close the body they are replacing. This defer is a backstop
	// that closes whichever response is current when the function returns, so
	// no path leaks the body. Closing an already-closed http body is a no-op.
	//
	// The nil check is load-bearing: openStream returns (nil, nil, err) on
	// failure, so a reopen that fails mid-playback — a dropped connection on
	// a seek or a track transition — leaves resp nil, and an unguarded
	// resp.Body here would panic the whole player instead of stopping it.
	defer func() {
		if resp != nil {
			_ = resp.Body.Close()
		}
	}()

	logger.L.Debug("HTTP response",
		"status", resp.StatusCode,
		"content-type", resp.Header.Get("Content-Type"),
		"content-length", resp.Header.Get("Content-Length"),
	)

	info := stream.Info
	sampleRate := info.SampleRate
	channels := info.NChannels
	bits := info.BitsPerSample

	logger.L.Debug("audio stream",
		"rate", sampleRate,
		"channels", channels,
		"bits", bits,
		"samples", info.NSamples,
	)

	p.muInfo.Lock()
	p.sampleRate = sampleRate
	p.channels = channels
	p.bitsPerSample = bits
	p.totalSamples = info.NSamples
	if info.NSamples > 0 {
		p.hintDuration = 0 // stream has real sample count; clear API hint
	}
	p.muInfo.Unlock()

	// reacquireALSA re-claims the D-Bus reservation (DAC mode only — see
	// reserveDevice) and reopens the ALSA device. Used after releasing on
	// pause.
	reacquireALSA := func() (*alsaHandle, func(), error) {
		rel := func() {}
		if dacMode {
			r, rerr := reserveALSADevice(ctx, cardNum)
			if rerr != nil {
				return nil, nil, rerr
			}
			rel = r
		}
		a, aerr := p.openDevice(ctx, device, channels, sampleRate, bits)
		if aerr != nil {
			rel()
			return nil, nil, aerr
		}
		return a, rel, nil
	}

	ah, err := p.openDevice(ctx, device, channels, sampleRate, bits)
	if err != nil {
		logger.L.Error("openALSA failed", "device", device, "err", err)
		releaseReservation()
		return false
	}
	logger.L.Debug("ALSA opened",
		"device", ah.device,
		"format", ah.format,
		"bps", ah.bytesPerSample,
		"significantBits", ah.significantBits,
		"srcBits", bits,
		"rate_requested", sampleRate,
		"rate_negotiated", ah.rate,
		"period_size", ah.periodSize,
		"buffer_size", ah.bufferSize,
	)
	// ah and releaseReservation are both reassigned on pause/resume and on
	// format-change transitions. The defer captures them by pointer so it
	// always closes the current handle.
	defer func() {
		closeALSA(ah)
		releaseReservation()
	}()

	bps := ah.bytesPerSample
	shift := sampleShift(ah)

	// streamLoop runs the decode→ALSA pipeline for the current HTTP stream.
	// Returns (seekTarget, true, false) if a seek was requested,
	// (0, false, false) when the stream ends naturally or the context is
	// cancelled, or (0, false, true) when an unrecoverable error occurred
	// mid-stream (e.g. ALSA reacquire failed) so the outer loop can exit
	// without signalling a natural track completion.
	type pcmBuf struct {
		data    []byte
		nFrames int
	}

	streamLoop := func(skipSamples uint64) (seekTarget uint64, doSeek, aborted bool) {
		// Capture the current skipCh so we can detect when PlayNext()
		// interrupts this stream.
		p.mu.Lock()
		skipCh := p.skipCh //nolint:gocritic
		p.mu.Unlock()

		stopDecode := make(chan struct{})
		pcmCh := make(chan pcmBuf, 2)

		go func() {
			defer close(pcmCh)
			var skipped uint64
			for skipped < skipSamples {
				select {
				case <-ctx.Done():
					return
				case <-stopDecode:
					return
				default:
				}
				samples, ferr := stream.ReadSamples()
				if ferr != nil {
					return
				}
				n := len(samples) / int(channels)
				skipped += uint64(n)
			}
			atomic.StoreUint64(&p.samplesPlayed, skipped)

			for {
				select {
				case <-ctx.Done():
					return
				case <-stopDecode:
					return
				default:
				}
				samples, ferr := stream.ReadSamples()
				if ferr != nil {
					logger.L.Debug("audio decode done", "err", ferr)
					return
				}
				n := len(samples) / int(channels)
				buf := make([]byte, len(samples)*bps)
				vol := math.Float64frombits(atomic.LoadUint64(&p.volumeBits))
				packPCM(buf, samples, bps, shift, vol)
				// Best-effort tee for the CAVA visualizer — copies the buffer
				// so the visualizer goroutine can hold onto it after this one
				// reuses/writes buf; never blocks the audio path.
				select {
				case p.pcmTapCh <- append([]byte(nil), buf...):
				default:
				}
				select {
				case pcmCh <- pcmBuf{data: buf, nFrames: n}:
				case <-ctx.Done():
					return
				case <-stopDecode:
					return
				}
			}
		}()

		returnSeek := func(target uint64) (uint64, bool, bool) {
			close(stopDecode)
			// Drain so the decode goroutine can unblock and exit.
			for range pcmCh {
			}
			C.snd_pcm_drop(ah.pcm)
			C.snd_pcm_prepare(ah.pcm)
			return target, true, false
		}

		for pcm := range pcmCh {
			framesDone := 0
			for framesDone < pcm.nFrames {
				// Check for a seek request.
				select {
				case target := <-p.seekCh:
					return returnSeek(target)
				default:
				}

				// Check for a skip (next-track) request from PlayNext().
				select {
				case <-skipCh:
					C.snd_pcm_drop(ah.pcm)
					C.snd_pcm_prepare(ah.pcm)
					close(stopDecode)
					for range pcmCh {
					}
					return 0, false, false
				default:
				}

				// Check pause: release the ALSA device so PipeWire / other
				// apps can use it while we are idle, then reacquire on resume.
				if atomic.LoadUint32(&p.paused) == 1 {
					C.snd_pcm_drop(ah.pcm)
					closeALSA(ah)
					releaseReservation()
					// Neutralize the release func so the cleanup defer (or a
					// failed-reacquire exit) cannot release the reservation a
					// second time; a successful reacquire installs a new one.
					releaseReservation = func() {}
					logger.L.Debug("paused: ALSA device released")

					// Wait for resume; on a failed reacquire fall back to the
					// paused state instead of aborting, so the loop stays
					// controllable (skip, seek, stop all keep working) and the
					// next resume attempt can succeed once the device frees up.
					// pendingSeek survives a failed reacquire. A seek target is
					// consumed off p.seekCh destructively, so if the reacquire
					// it triggered fails we must hold onto it rather than drop
					// it: otherwise the user scrubs while paused, resumes, and
					// playback restarts from the old position with only a log
					// line to explain it.
					var pendingSeek uint64
					var havePendingSeek bool

					reacquired := false
					for !reacquired {
						for atomic.LoadUint32(&p.paused) == 1 && !havePendingSeek {
							select {
							case target := <-p.seekCh:
								pendingSeek, havePendingSeek = target, true
							case <-skipCh:
								close(stopDecode)
								for range pcmCh {
								}
								return 0, false, false
							case <-ctx.Done():
								close(stopDecode)
								for range pcmCh {
								}
								return 0, false, false
							case <-time.After(20 * time.Millisecond):
							}
						}

						// Reacquire the device, either to resume or to serve a
						// seek requested while paused.
						newAH, newRel, raErr := reacquireALSA()
						if raErr != nil {
							if ctx.Err() != nil {
								close(stopDecode)
								for range pcmCh {
								}
								return 0, false, false
							}
							logger.L.Error("reacquire ALSA failed, staying paused", "err", raErr)
							// Re-arm the paused state and tell the UI, so it
							// stops rendering this as playing. Without this the
							// UI keeps a ticking progress bar over silence and
							// its play/pause label inverts for the rest of the
							// track: the next space press reads p.paused==1 and
							// actually resumes while the label flips to Paused.
							atomic.StoreUint32(&p.paused, 1)
							p.notifyPaused(raErr)
							continue
						}
						ah = newAH
						releaseReservation = newRel
						reacquired = true

						if havePendingSeek {
							return returnSeek(pendingSeek)
						}
					}
					logger.L.Debug("resumed: ALSA device reacquired")
					break
				}

				select {
				case <-ctx.Done():
					C.snd_pcm_drop(ah.pcm)
					close(stopDecode)
					for range pcmCh {
					}
					return 0, false, false
				default:
				}

				off := framesDone * int(channels) * bps
				written := C.snd_pcm_writei(ah.pcm, unsafe.Pointer(&pcm.data[off]), C.snd_pcm_uframes_t(pcm.nFrames-framesDone))
				if written < 0 {
					errStr := C.GoString(C.snd_strerror(C.int(written)))
					logger.L.Warn("snd_pcm_writei error, recovering", "err", errStr)
					if rc := C.snd_pcm_recover(ah.pcm, C.int(written), C.int(1)); rc < 0 {
						logger.L.Error("snd_pcm_recover failed, stopping playback",
							"err", C.GoString(C.snd_strerror(rc)))
						close(stopDecode)
						for range pcmCh {
						}
						return 0, false, true
					}
					continue
				}
				framesDone += int(written)
			}
			atomic.AddUint64(&p.samplesPlayed, uint64(pcm.nFrames))
		}
		return 0, false, false
	}

	// Outer loop: play the current stream, then wait for a next-track URL
	// or exit. This keeps the ALSA device open between consecutive tracks.
	for {
		seekTarget, doSeek, aborted := streamLoop(0)
		for doSeek {
			// Re-open the HTTP stream and skip to the seek target.
			// samplesPlayed is NOT reset here — streamLoop sets it after skipping,
			// so GetPosition() never briefly returns 0 between seeks.
			stream.Close()
			_ = resp.Body.Close()

			resp, stream, err = openStream(ctx, url)
			if err != nil {
				logger.L.Error("failed to reopen stream for seek", "err", err)
				return false
			}

			seekTarget, doSeek, aborted = streamLoop(seekTarget)
		}

		stream.Close()
		_ = resp.Body.Close()

		// If the stream loop aborted due to an unrecoverable error (e.g. ALSA
		// reacquire failed), exit without signalling a natural track completion
		// so the UI does not auto-advance into the same broken state.
		if aborted {
			return false
		}

		// Stream ended naturally — signal the UI so it can advance the queue.
		p.mu.Lock()
		oldDone := p.doneCh
		p.doneCh = nil
		p.mu.Unlock()
		if oldDone != nil {
			close(oldDone)
		}

		// Wait for the UI to provide the next track URL, or exit if the
		// playlist is over / playback is cancelled.
		nextURL, ok := p.awaitNextURL(ctx)
		if !ok {
			return true
		}
		logger.L.Debug("transitioning to next track")
		url = nextURL

		stream.Close()
		_ = resp.Body.Close()
		resp, stream, err = openStream(ctx, nextURL)
		if err != nil {
			logger.L.Error("failed to open next stream", "err", err)
			return false
		}

		newInfo := stream.Info

		// Reopen the ALSA device if the audio format changed, or if the
		// device is not currently open at all.
		//
		// ah.pcm is nil whenever streamLoop returned from the paused
		// state: pausing calls closeALSA (which nils the pointer) and
		// releases the reservation, and skipping or cancelling while
		// paused returns without ever reacquiring. Reusing the handle in
		// that case dereferences a NULL pcm inside libasound on the first
		// snd_pcm_drop/writei — a SIGSEGV that takes the whole process
		// down. Reaching this branch on a nil handle is the normal
		// pause→skip path, not an error.
		formatChanged := newInfo.SampleRate != sampleRate ||
			newInfo.NChannels != channels ||
			newInfo.BitsPerSample != bits
		deviceClosed := ah == nil || ah.pcm == nil

		sampleRate = newInfo.SampleRate
		channels = newInfo.NChannels
		bits = newInfo.BitsPerSample

		if formatChanged || deviceClosed {
			logger.L.Debug("reopening ALSA for next track",
				"formatChanged", formatChanged,
				"deviceClosed", deviceClosed,
				"rate", sampleRate, "ch", channels, "bits", bits)
			closeALSA(ah)
			// reacquireALSA closes over device/channels/sampleRate/bits,
			// which were just reassigned above, so it already reserves and
			// opens with the new format — no second closure needed. It also
			// reclaims the D-Bus reservation, which the pause released.
			newAH, newRel, raErr := reacquireALSA()
			if raErr != nil {
				logger.L.Error("reopen ALSA failed for next track", "err", raErr)
				_ = resp.Body.Close()
				return false
			}
			releaseReservation()
			ah = newAH
			releaseReservation = newRel
			bps = ah.bytesPerSample
			shift = sampleShift(ah)
		}

		writeSilence(ah, p.interTrackSilenceMs.Load(), channels, bps)

		p.muInfo.Lock()
		p.sampleRate = sampleRate
		p.channels = channels
		p.bitsPerSample = bits
		p.totalSamples = newInfo.NSamples
		if newInfo.NSamples > 0 {
			p.hintDuration = 0
		}
		p.muInfo.Unlock()

		atomic.StoreUint64(&p.samplesPlayed, 0)
		// Drain any pending seek so the new track starts from the beginning.
		select {
		case <-p.seekCh:
		default:
		}

		// Install the new doneCh created by PlayNext().
		p.mu.Lock()
		p.doneCh = p.transitionDoneCh
		p.transitionDoneCh = nil
		p.currentURL = nextURL
		p.mu.Unlock()

		logger.L.Debug("audio stream (next track)",
			"rate", sampleRate,
			"channels", channels,
			"bits", bits,
			"samples", newInfo.NSamples,
		)
		// Round the loop to play the next stream.
	}
}

// nextURLTimeout bounds how long the playback loop holds the ALSA device (and
// its D-Bus reservation) open waiting for the UI to hand over the next
// track's stream URL after the current one ends.
const nextURLTimeout = 5 * time.Second

// awaitNextURL waits for the UI to supply the next track's stream URL. It
// reports false when playback is cancelled, or when the handoff window closes
// with nothing arriving — in which case the caller should shut the device down.
//
// The second, non-blocking read after the timeout is what makes the handoff
// race-free. PlayNext checks this loop is still alive and only then sends on
// nextURLCh, so a send landing in the same instant the timer fires would
// otherwise be picked up by neither: Go would choose the timeout case, the
// loop would exit, and the URL would sit unread in the buffered channel. The
// track would never start and the done channel PlayNext handed its caller
// would never close, leaving the UI showing a track playing with no audio and
// no completion — a wedge that only quitting clears.
func (p *Player) awaitNextURL(ctx context.Context) (string, bool) {
	select {
	case u := <-p.nextURLCh:
		return u, true
	case <-ctx.Done():
		return "", false
	case <-time.After(nextURLTimeout):
		if u, ok := p.takeQueuedURL(); ok {
			logger.L.Debug("next-track URL arrived as the handoff window closed")
			return u, true
		}
		logger.L.Debug("no next track within timeout, closing ALSA")
		return "", false
	}
}

// takeQueuedURL takes a next-track URL if one is already sitting in the
// channel, without blocking. Used both to rescue a send that landed as the
// handoff window closed and to reclaim one PlayNext queued for a loop that
// turned out to have already exited.
func (p *Player) takeQueuedURL() (string, bool) {
	select {
	case u := <-p.nextURLCh:
		return u, true
	default:
		return "", false
	}
}

// notifyPaused reports that the player put itself back into the paused state
// without being asked. The send is non-blocking: the playback loop must never
// stall because nothing is draining the channel, and a queued notification
// already conveys the same "you are paused" fact as a second one.
func (p *Player) notifyPaused(err error) {
	select {
	case p.pausedCh <- err:
	default:
	}
}

// PausedEvents returns the channel on which the player reports that it forced
// itself back into the paused state (e.g. reacquiring the ALSA device on
// resume failed). Consumers should treat a receive as "playback is paused
// now" and resync their own play/pause state from it.
func (p *Player) PausedEvents() <-chan error { return p.pausedCh }

// IsPaused reports the player's actual paused state, which can diverge from
// what a UI last requested when a resume fails.
func (p *Player) IsPaused() bool { return atomic.LoadUint32(&p.paused) == 1 }

func (p *Player) Pause() error {
	if atomic.LoadUint32(&p.paused) == 0 {
		atomic.StoreUint32(&p.paused, 1)
	} else {
		atomic.StoreUint32(&p.paused, 0)
	}
	return nil
}

func (p *Player) SetVolume(vol float64) error {
	atomic.StoreUint64(&p.volumeBits, math.Float64bits(vol/100.0))
	return nil
}

func (p *Player) GetVolume() (float64, error) {
	return math.Float64frombits(atomic.LoadUint64(&p.volumeBits)) * 100.0, nil
}

func (p *Player) GetPosition() (float64, error) {
	p.muInfo.RLock()
	sr := p.sampleRate
	p.muInfo.RUnlock()
	sp := atomic.LoadUint64(&p.samplesPlayed)
	if sr == 0 {
		logger.L.Debug("GetPosition: sampleRate=0, returning 0", "samplesPlayed", sp)
		return 0, nil
	}
	pos := float64(sp) / float64(sr)
	logger.L.Debug("GetPosition", "samplesPlayed", sp, "sampleRate", sr, "pos", pos)
	return pos, nil
}

func (p *Player) GetDuration() (float64, error) {
	p.muInfo.RLock()
	sr := p.sampleRate
	ts := p.totalSamples
	hint := p.hintDuration
	p.muInfo.RUnlock()
	if ts > 0 && sr > 0 {
		dur := float64(ts) / float64(sr)
		logger.L.Debug("GetDuration: from totalSamples", "totalSamples", ts, "sampleRate", sr, "dur", dur)
		return dur, nil
	}
	if hint > 0 {
		logger.L.Debug("GetDuration: from hintDuration", "hint", hint)
		return hint, nil
	}
	logger.L.Debug("GetDuration: no data", "totalSamples", ts, "sampleRate", sr, "hint", hint)
	return 0, nil
}

// SetDuration seeds the known track duration (in seconds) from the Tidal API
// so that GetDuration works even when the stream has no embedded duration
// metadata (e.g. mp4 HTTP streams).
func (p *Player) SetDuration(seconds float64) {
	p.muInfo.Lock()
	p.hintDuration = seconds
	p.muInfo.Unlock()
	logger.L.Debug("SetDuration", "hint", seconds)
}

// Seek jumps to the given absolute position in seconds without interrupting
// the ALSA device or D-Bus reservation. The playback loop re-fetches the HTTP
// stream and skips to the target in-place.
func (p *Player) Seek(seconds float64) error {
	p.muInfo.RLock()
	sr := p.sampleRate
	ts := p.totalSamples
	p.muInfo.RUnlock()
	if sr == 0 {
		return nil
	}

	if seconds < 0 {
		seconds = 0
	}
	maxSeconds := float64(ts) / float64(sr)
	if seconds > maxSeconds {
		seconds = maxSeconds
	}

	target := uint64(seconds * float64(sr))

	// Non-blocking send: drop a stale pending seek if the loop hasn't consumed
	// it yet, then send the new target.
	select {
	case <-p.seekCh:
	default:
	}
	p.seekCh <- target
	return nil
}

// Done returns a channel that is closed when the current track finishes
// playing naturally (not when stopped or cancelled). Returns a nil channel
// if no track is playing, which blocks forever in a select — safe to use
// as a sentinel.
func (p *Player) Done() <-chan struct{} {
	p.mu.Lock()
	ch := p.doneCh
	p.mu.Unlock()
	return ch
}

func (p *Player) Close() {
	p.stop()
}
