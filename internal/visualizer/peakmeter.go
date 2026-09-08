package visualizer

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// PeakMeter computes a decaying RMS level (0..1) per audio channel directly
// from the same decoded, post-volume PCM tap Cava reads (player.Player.TapPCM,
// fanned out via Broadcast) — no subprocess, no FFT, just signal amplitude.
// RMS (root-mean-square over each buffer), not the single loudest sample in
// it: a true-peak reading spikes to near-0dBFS on every transient in any
// well-mastered/limited track and, combined with a decay slow enough to stay
// visible, reads as permanently pinned near the top even though the track
// itself is nowhere near clipping. RMS is what a real VU meter measures and
// what actually tracks perceived loudness. Unlike Cava it has no external
// dependency, so it's available even when the cava binary isn't installed.
type PeakMeter struct {
	channels atomic.Uint32 // 0 until the first Configure call

	mu      sync.Mutex
	cancel  context.CancelFunc
	stopped chan struct{}

	levelMu sync.RWMutex
	level   []float64 // decaying peak per channel, 0..1
	lastAt  time.Time
}

// NewPeakMeter creates an idle meter; Configure starts it.
func NewPeakMeter() *PeakMeter { return &PeakMeter{} }

// Configure sets the channel count to de-interleave PCM by and starts the
// goroutine reading pcmTap, if not already running. Safe to call on every
// track change — most tracks share the same channel count and this is then a
// cheap atomic store, not a restart: unlike Cava's subprocess, there is
// nothing here that needs restarting when the format changes, since pcmTap is
// a stable per-model subscriber channel (see Broadcast).
func (m *PeakMeter) Configure(channels uint8, pcmTap <-chan []byte) {
	if channels == 0 {
		return
	}
	m.channels.Store(uint32(channels))

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	m.cancel = cancel
	m.stopped = stopped
	go m.run(ctx, stopped, pcmTap)
}

// Stop halts the consuming goroutine and clears the displayed level.
func (m *PeakMeter) Stop() {
	m.mu.Lock()
	cancel := m.cancel
	stopped := m.stopped
	m.cancel, m.stopped = nil, nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if stopped != nil {
		<-stopped
	}
	m.levelMu.Lock()
	for i := range m.level {
		m.level[i] = 0
	}
	m.levelMu.Unlock()
}

func (m *PeakMeter) run(ctx context.Context, stopped chan struct{}, pcmTap <-chan []byte) {
	defer close(stopped)
	for {
		select {
		case <-ctx.Done():
			return
		case buf, ok := <-pcmTap:
			if !ok {
				return
			}
			m.update(buf)
		}
	}
}

// peakDecayPerSecond is the release ballistics: how fast the meter falls back
// toward zero once the signal drops, expressed as full-scale-per-second — a
// real peak meter's needle falls rather than snapping, so a loud transient
// stays visible for a moment after it passes.
const peakDecayPerSecond = 1.6 // ~625ms from full scale to zero

// update decodes S32LE interleaved samples (the format player.Player always
// writes — see mpv.go) and folds each channel's RMS level over this buffer
// into a wall-clock-decayed level.
func (m *PeakMeter) update(buf []byte) {
	channels := int(m.channels.Load())
	const bytesPerSample = 4
	frameBytes := bytesPerSample * channels
	if channels == 0 || len(buf) < frameBytes {
		return
	}

	sumSq := make([]float64, channels)
	frames := 0
	for off := 0; off+frameBytes <= len(buf); off += frameBytes {
		for c := range channels {
			i := off + c*bytesPerSample
			s := int32(uint32(buf[i]) | uint32(buf[i+1])<<8 | uint32(buf[i+2])<<16 | uint32(buf[i+3])<<24)
			v := float64(s) / math.MaxInt32
			sumSq[c] += v * v
		}
		frames++
	}
	rms := make([]float64, channels)
	if frames > 0 {
		for c := range channels {
			rms[c] = math.Sqrt(sumSq[c] / float64(frames))
		}
	}

	now := time.Now()
	m.levelMu.Lock()
	if len(m.level) != channels {
		m.level = make([]float64, channels)
		m.lastAt = now
	}
	decay := now.Sub(m.lastAt).Seconds() * peakDecayPerSecond
	m.lastAt = now
	for c := range m.level {
		m.level[c] = max(m.level[c]-decay, 0)
		if rms[c] > m.level[c] {
			m.level[c] = rms[c]
		}
	}
	m.levelMu.Unlock()
}

// peakMeterFloorDB is the bottom of the meter's displayed dB range. Linear
// amplitude packs nearly all of a typical mastered track's peaks into the
// last few percent below full scale — a track peaking at -3dBFS already
// reads as ~0.7 linear, i.e. 70% up the bar — so a meter scaled linearly
// looks pinned near max almost constantly. Real peak/VU meters read in dB for
// exactly this reason; Levels converts to dB against this floor so normal
// program material actually shows movement instead of sitting maxed out.
const peakMeterFloorDB = -48.0

// Levels returns each channel's current peak level scaled to [0, maxHeight]
// on a dB scale (see peakMeterFloorDB), mirroring Cava.Bars' contract so
// render code can treat either source the same way. Returns nil until the
// first buffer has been processed.
func (m *PeakMeter) Levels(maxHeight int) []int {
	m.levelMu.RLock()
	defer m.levelMu.RUnlock()
	if len(m.level) == 0 {
		return nil
	}
	out := make([]int, len(m.level))
	for i, v := range m.level {
		db := peakMeterFloorDB
		if v > 0 {
			db = 20 * math.Log10(v)
		}
		frac := max((db-peakMeterFloorDB)/-peakMeterFloorDB, 0)
		h := int(frac * float64(maxHeight))
		out[i] = max(min(h, maxHeight), 0)
	}
	return out
}
