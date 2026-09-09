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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

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
	run      *cavaRun
	rate     uint32
	channels uint8

	barsMu sync.RWMutex
	latest []int
}

// cavaRun is the liveness state of one cava subprocess. It is per-start, not
// per-Cava, so a goroutine belonging to an already-replaced process can never
// report on (or falsely condemn) its successor.
type cavaRun struct {
	// lastFrame is the UnixNano of the most recent line read from cava's
	// stdout. cava's raw output is framerate-paced and unconditional — it
	// emits a frame every ~1/framerate second whether or not the FIFO has any
	// audio in it (sleep_timer is pinned off in cavaConfig for exactly this
	// reason) — so a stale lastFrame means the process is wedged, full stop.
	lastFrame atomic.Int64
	// feederDone is set when feedFIFO stops writing, for any reason.
	feederDone atomic.Bool
}

// barStallTimeout is how long cava may go without emitting a single output
// frame before Configure treats it as wedged and restarts it. cava emits at
// framerate (60/s), so anything beyond a small multiple of a frame is already
// pathological; the slack here is for process startup, where lastFrame is
// seeded with the start time before cava has produced anything.
const barStallTimeout = 3 * time.Second

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
	sameFormat := c.cmd != nil && c.rate == rate && c.channels == channels
	stopped := c.stopped
	run := c.run
	c.mu.Unlock()

	if sameFormat {
		reason := stallReason(run, stopped)
		if reason == "" {
			return // still genuinely running with this format; nothing to do
		}
		logger.L.Debug("visualizer: restarting cava", "reason", reason, "rate", rate, "channels", channels)
	}

	c.mu.Lock()
	c.stopLocked()
	c.mu.Unlock()
	if err := c.start(rate, channels, pcmTap); err != nil {
		logger.L.Warn("visualizer: failed to start cava", "err", err)
	}
}

// stallReason reports why the currently running cava needs replacing, or ""
// if it is healthy.
//
// c.cmd being non-nil only means "nothing has explicitly stopped this
// instance", not "it is still working". Three distinct ways it can stop
// working, all of which used to leave Configure taking its same-rate no-op
// path forever — the visualizer frozen on its last frame until a track with a
// *different* sample rate happened to force a restart:
//
//   - the process died (stopped is closed);
//   - feedFIFO returned, so nothing is writing PCM any more;
//   - the process is alive and being fed but has stopped emitting frames —
//     e.g. wedged writing to a stdout nobody is draining because readBars
//     already gave up. Nothing observable distinguishes this from a healthy
//     cava except the output itself, which is why it is checked directly.
func stallReason(run *cavaRun, stopped <-chan struct{}) string {
	select {
	case <-stopped:
		return "process exited"
	default:
	}
	if run == nil {
		return "no run state"
	}
	if run.feederDone.Load() {
		return "pcm feeder stopped"
	}
	if since := time.Since(time.Unix(0, run.lastFrame.Load())); since > barStallTimeout {
		return "no output for " + since.Round(time.Millisecond).String()
	}
	return ""
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
	c.run = nil
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
	// Captured rather than left pointing at /dev/null: when cava misbehaves,
	// whatever it says about why is the only evidence there is. It must be
	// drained (see logStderr) — an undrained pipe would eventually block cava
	// mid-write, which is precisely the class of wedge stallReason exists to
	// catch.
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return fmt.Errorf("cava stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		_ = os.RemoveAll(dir)
		return fmt.Errorf("start cava: %w", err)
	}

	run := &cavaRun{}
	// Seeded with the start time so the stall window is measured from launch,
	// not from the zero time — otherwise the very first Configure after start
	// would see a "stale" lastFrame and restart the process it just spawned.
	run.lastFrame.Store(time.Now().UnixNano())

	c.mu.Lock()
	c.dir = dir
	c.fifoPath = fifoPath
	c.cmd = cmd
	c.cancel = cancel
	c.stopped = make(chan struct{})
	c.run = run
	c.rate = rate
	c.channels = channels
	stopped := c.stopped
	c.mu.Unlock()

	go c.feedFIFO(ctx, fifoPath, pcmTap, run)
	go c.readBars(stdout, run)
	go logStderr(stderr)
	go func() {
		_ = cmd.Wait()
		close(stopped)
	}()
	return nil
}

// logStderr drains cava's stderr into the debug log. Lines are logged rather
// than discarded so a wedged or exiting cava leaves a trace, and drained
// rather than ignored so cava can never block writing to it.
func logStderr(stderr io.Reader) {
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			logger.L.Debug("visualizer: cava stderr", "line", line)
		}
	}
}

// feedFIFO opens the FIFO for writing (blocks until cava opens its read end)
// and copies tapped PCM into it. Writes are best-effort: a write that would
// block past the tap channel's own backpressure just means cava falls behind
// a frame, never that audio playback stalls — this goroutine is entirely
// separate from the ALSA write path.
func (c *Cava) feedFIFO(ctx context.Context, path string, pcmTap <-chan []byte, run *cavaRun) {
	// Every return path marks this run's feeder dead, so Configure can tell
	// "cava is running and being fed" from "cava is running and starving"
	// without waiting for the process itself to exit (it never does).
	defer run.feederDone.Store(true)

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
//
// bufio.Reader rather than bufio.Scanner: a Scanner gives up permanently on
// any line past its 64KB token limit, and giving up here is not a harmless
// failure — with nothing draining stdout, cava blocks mid-write and stops
// reading its FIFO too, which looks exactly like the frozen visualizer this
// whole run-liveness machinery exists to recover from. A Reader has no such
// limit, and every line read (parseable or not) refreshes run.lastFrame,
// since any output at all proves the process is still ticking.
func (c *Cava) readBars(stdout io.Reader, run *cavaRun) {
	br := bufio.NewReader(stdout)
	for {
		line, err := br.ReadString('\n')
		if line == "" && err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
				logger.L.Debug("visualizer: cava output read failed", "err", err)
			}
			return
		}
		run.lastFrame.Store(time.Now().UnixNano())
		vals := parseBarLine(line, c.bars)
		if len(vals) == 0 {
			if err != nil {
				return
			}
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

// barsFloorDB is the bottom of each bar's displayed dB range — the same fix
// applied to the Peak meter (see visualizer.peakMeterFloorDB) applied here:
// a *linear* v/asciiMaxRange mapping compresses almost all of a real
// frequency band's dynamic range into the last few percent below the
// ceiling (quiet passages — a cappella verses, sparse arrangements — read as
// literally zero height, "no bar at all", while anything moderately present
// reads as pinned near the top, "always saturated"). A dB scale spreads
// that out the way a real spectrum analyzer does, so quiet content is still
// visible and loud content doesn't look maxed out by default.
const barsFloorDB = -36.0

// Bars returns a copy of the most recent bar heights, scaled to [0, maxHeight]
// on a dB scale (see barsFloorDB) against the fixed asciiMaxRange ceiling.
//
// This intentionally does not re-normalize against its own running peak: with
// autosens off (see cavaConfig), cava's raw values already scale with actual
// input loudness against that fixed ceiling. Any further auto-gain here
// would recreate the "everything reads as maxed out" problem turning
// autosens off was meant to fix.
func (c *Cava) Bars(maxHeight int) []int {
	c.barsMu.RLock()
	defer c.barsMu.RUnlock()
	out := make([]int, len(c.latest))
	for i, v := range c.latest {
		amp := float64(v) / float64(asciiMaxRange)
		db := barsFloorDB
		if amp > 0 {
			db = 20 * math.Log10(amp)
		}
		frac := max((db-barsFloorDB)/-barsFloorDB, 0)
		h := int(frac * float64(maxHeight))
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
	// with autosens off preserves genuine relative dynamics instead — but
	// only at a sane gain: 400 (a 4x fixed multiplier, no auto-adjustment to
	// rein it back in) clipped most real program material to the ceiling
	// almost constantly, and cava's own unity-gain default of 100 read the
	// opposite way, too quiet most of the time. 150 landed as the best
	// middle ground after listening at 100, 200, and 250.
	// sleep_timer is written explicitly (cava's own default is already 0/off)
	// so the run-liveness watchdog can rely on it: with a sleep timer set,
	// cava stops emitting frames after N seconds of silence, which
	// stallReason would — correctly by its own rule, wrongly in effect — read
	// as a wedged process and restart on a loop.
	fmt.Fprintf(&b, "[general]\nframerate = 60\nbars = %d\nautosens = 0\nsensitivity = 150\nsleep_timer = 0\n\n", bars)
	fmt.Fprintf(&b, "[input]\nmethod = fifo\nsource = %s\nsample_rate = %d\nsample_bits = 32\nchannels = %d\n\n",
		fifoPath, rate, channels)
	b.WriteString("[output]\nmethod = raw\nchannels = mono\ndata_format = ascii\nascii_max_range = 1000\nbar_delimiter = 59\n\n")
	// Between cava's own (rather sluggish) default of 77 and a too-twitchy
	// 30: 40 responds more immediately than 50 did (less of a perceived
	// "holds the last value too long" lag) while still not visibly jittering
	// frame to frame.
	b.WriteString("[smoothing]\nnoise_reduction = 40\nmonstercat = 1\nwaves = 0\n")
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
