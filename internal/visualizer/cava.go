// Package visualizer drives a real `cava` subprocess to render an rmpc-style
// spectrum visualizer. It never taps the audio device or the bit-perfect
// write path in internal/player — it is fed a best-effort tee of already
// decoded PCM (see player.Player.TapPCM) over a FIFO it owns, exactly the
// architecture rmpc itself uses (input.method=fifo) rather than a
// reimplemented FFT.
package visualizer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/carcuevas/gotidal/internal/logger"
)

// Available reports whether the cava binary is installed. Callers should gate
// all visualizer use on this — gotidal runs identically without it, just with
// the Cava pane showing a static placeholder.
func Available() bool {
	_, err := exec.LookPath("cava")
	return err == nil
}

// Cava owns one running cava subprocess and its FIFO, restarting only when
// the audio format (rate/channels) actually changes.
type Cava struct {
	bars int

	mu       sync.Mutex
	dir      string
	fifoPath string
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	stopped  chan struct{}
	rate     uint32
	channels uint8

	barsMu sync.RWMutex
	latest []int
}

// New creates a Cava driver that renders the given number of bars. It does
// not start a subprocess until Configure is called with a known audio format.
func New(bars int) *Cava {
	if bars < 1 {
		bars = 1
	}
	return &Cava{bars: bars, latest: make([]int, bars)}
}

// Configure (re)starts the cava subprocess for the given format, or is a
// no-op if it's already running with that exact rate/channels. Safe to call
// on every track change — most tracks share the same rate/channels and won't
// cause a restart.
func (c *Cava) Configure(rate uint32, channels uint8, pcmTap <-chan []byte) {
	if rate == 0 || channels == 0 {
		return
	}
	c.mu.Lock()
	if c.cmd != nil && c.rate == rate && c.channels == channels {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	c.stopLocked()
	if err := c.start(rate, channels, pcmTap); err != nil {
		logger.L.Warn("visualizer: failed to start cava", "err", err)
	}
}

// Stop terminates the running cava subprocess, if any, and cleans up its FIFO.
func (c *Cava) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopLocked()
}

// stopLocked must be called with c.mu held.
func (c *Cava) stopLocked() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.stopped != nil {
		<-c.stopped
	}
	if c.dir != "" {
		_ = os.RemoveAll(c.dir)
	}
	c.cmd, c.cancel, c.stopped, c.dir, c.fifoPath = nil, nil, nil, "", ""
	c.rate, c.channels = 0, 0
	c.barsMu.Lock()
	for i := range c.latest {
		c.latest[i] = 0
	}
	c.barsMu.Unlock()
}

func (c *Cava) start(rate uint32, channels uint8, pcmTap <-chan []byte) error {
	dir, err := os.MkdirTemp("", "gotidal-cava-*")
	if err != nil {
		return fmt.Errorf("cava temp dir: %w", err)
	}
	fifoPath := filepath.Join(dir, "pcm.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("mkfifo: %w", err)
	}

	cfgPath := filepath.Join(dir, "config.ini")
	cfg := cavaConfig(c.bars, rate, channels, fifoPath)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("write cava config: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "cava", "-p", cfgPath) //nolint:gosec // G204: cfgPath is our own generated temp file, not user input
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return fmt.Errorf("cava stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return fmt.Errorf("start cava: %w", err)
	}

	c.mu.Lock()
	c.dir = dir
	c.fifoPath = fifoPath
	c.cmd = cmd
	c.cancel = cancel
	c.stopped = make(chan struct{})
	c.rate = rate
	c.channels = channels
	stopped := c.stopped
	c.mu.Unlock()

	go c.feedFIFO(ctx, fifoPath, pcmTap)
	go c.readBars(stdout)
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()
	return nil
}

// feedFIFO opens the FIFO for writing (blocks until cava opens its read end)
// and copies tapped PCM into it. Writes are best-effort: a write that would
// block past the tap channel's own backpressure just means cava falls behind
// a frame, never that audio playback stalls — this goroutine is entirely
// separate from the ALSA write path.
func (c *Cava) feedFIFO(ctx context.Context, path string, pcmTap <-chan []byte) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o600) //nolint:gosec // fixed name under our own temp dir, not user input
	if err != nil {
		if !errors.Is(err, os.ErrClosed) {
			logger.L.Debug("visualizer: fifo open failed", "err", err)
		}
		return
	}
	defer func() { _ = f.Close() }()

	for {
		select {
		case <-ctx.Done():
			return
		case buf, ok := <-pcmTap:
			if !ok {
				return
			}
			if _, err := f.Write(buf); err != nil {
				return // cava exited or the pipe broke; Configure will restart it
			}
		}
	}
}

// readBars parses cava's ascii raw output (one line per frame, bar values
// separated by ';') and updates the latest snapshot.
func (c *Cava) readBars(stdout io.Reader) {
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		vals := parseBarLine(sc.Text(), c.bars)
		if len(vals) == 0 {
			continue
		}
		c.barsMu.Lock()
		for i := range c.latest {
			if i < len(vals) {
				c.latest[i] = vals[i]
			} else {
				c.latest[i] = 0
			}
		}
		c.barsMu.Unlock()
	}
}

// asciiMaxRange must match the [output] ascii_max_range cavaConfig writes —
// it's the fixed ceiling cava's own values are already scaled against.
const asciiMaxRange = 1000

// Bars returns a copy of the most recent bar heights, scaled to [0, maxHeight].
//
// This intentionally does not re-normalize against its own running peak: with
// autosens off (see cavaConfig), cava's raw values already scale linearly
// with actual input loudness against the fixed asciiMaxRange ceiling. Any
// further auto-gain here would recreate the "everything reads as maxed out"
// problem that turning autosens off was meant to fix.
func (c *Cava) Bars(maxHeight int) []int {
	c.barsMu.RLock()
	defer c.barsMu.RUnlock()
	out := make([]int, len(c.latest))
	for i, v := range c.latest {
		h := v * maxHeight / asciiMaxRange
		out[i] = max(min(h, maxHeight), 0)
	}
	return out
}

// cavaConfig renders the [general]/[input]/[output] ini cava needs, mirroring
// the shape rmpc itself generates (rmpc/src/config/cava.rs) with gotidal's
// defaults substituted in for the [input] section — sensible fixed smoothing
// and sensitivity defaults since gotidal has no config file of its own (yet)
// for tuning these.
func cavaConfig(bars int, rate uint32, channels uint8, fifoPath string) string {
	var b strings.Builder
	// autosens=1 continuously re-gains each frame's tallest bar up toward
	// ascii_max_range regardless of how loud the input actually is, so a
	// quiet passage ends up reading exactly as "full" as a loud one — the
	// opposite of a meter that reflects real loudness. A fixed sensitivity
	// with autosens off preserves genuine relative dynamics instead.
	fmt.Fprintf(&b, "[general]\nframerate = 60\nbars = %d\nautosens = 0\nsensitivity = 400\n\n", bars)
	fmt.Fprintf(&b, "[input]\nmethod = fifo\nsource = %s\nsample_rate = %d\nsample_bits = 32\nchannels = %d\n\n",
		fifoPath, rate, channels)
	b.WriteString("[output]\nmethod = raw\nchannels = mono\ndata_format = ascii\nascii_max_range = 1000\nbar_delimiter = 59\n\n")
	// Between cava's own (rather sluggish) default of 77 and a too-twitchy
	// 30: 50 tracks the audio closely without visibly jittering frame to frame.
	b.WriteString("[smoothing]\nnoise_reduction = 50\nmonstercat = 1\nwaves = 0\n")
	return b.String()
}

// parseBarLine parses one line of cava's ascii raw output ("v1;v2;...;vn;")
// into up to `want` integer values.
func parseBarLine(line string, want int) []int {
	fields := strings.Split(strings.TrimRight(line, ";\r\n"), ";")
	out := make([]int, 0, want)
	for _, f := range fields {
		if f == "" {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			continue
		}
		out = append(out, v)
		if len(out) >= want {
			break
		}
	}
	return out
}
