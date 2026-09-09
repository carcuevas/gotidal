package visualizer

import (
	"testing"
	"time"
)

// Reported (three times, each earlier fix insufficient): after seeking inside
// a 192kHz track the frequency meter froze, and *stayed* frozen — restarting
// the track and switching to another track of the same rate both left it
// frozen, while switching to a lower-rate track fixed it instantly. That last
// detail is the whole diagnosis: only a rate change reaches the restart path,
// so cava's process was still alive (nothing else would have kept Configure
// on its same-format no-op) while producing nothing. The liveness check must
// therefore look at cava's actual output, not just at whether it exited.
func TestStallReasonAliveAndProducing(t *testing.T) {
	run := &cavaRun{}
	run.lastFrame.Store(time.Now().UnixNano())

	if got := stallReason(run, make(chan struct{})); got != "" {
		t.Errorf("stallReason = %q, want \"\" for a healthy run", got)
	}
}

func TestStallReasonProcessExited(t *testing.T) {
	run := &cavaRun{}
	run.lastFrame.Store(time.Now().UnixNano())
	stopped := make(chan struct{})
	close(stopped)

	if got := stallReason(run, stopped); got == "" {
		t.Error("stallReason = \"\" for an exited process, want a reason")
	}
}

// feedFIFO returning (a broken pipe, a closed tap) leaves the process running
// but starved. Nothing in the process state reflects that, so the feeder has
// to report it itself.
func TestStallReasonFeederStopped(t *testing.T) {
	run := &cavaRun{}
	run.lastFrame.Store(time.Now().UnixNano())
	run.feederDone.Store(true)

	if got := stallReason(run, make(chan struct{})); got == "" {
		t.Error("stallReason = \"\" after the feeder stopped, want a reason")
	}
}

// The case the earlier fixes all missed: alive, still being fed, but silent.
func TestStallReasonNoOutput(t *testing.T) {
	run := &cavaRun{}
	run.lastFrame.Store(time.Now().Add(-2 * barStallTimeout).UnixNano())

	if got := stallReason(run, make(chan struct{})); got == "" {
		t.Errorf("stallReason = \"\" for a process that has emitted nothing in %v, want a reason", 2*barStallTimeout)
	}
}

// A run that has not yet been recorded (Configure racing a failed start) must
// restart rather than be assumed healthy.
func TestStallReasonNilRun(t *testing.T) {
	if got := stallReason(nil, make(chan struct{})); got == "" {
		t.Error("stallReason = \"\" for a nil run, want a reason")
	}
}

// End to end against a real cava: wedge the run the way the reported bug does
// — process alive, same rate — and Configure must replace it.
func TestConfigureRestartsAWedgedProcess(t *testing.T) {
	if !Available() {
		t.Skip("cava binary not available")
	}

	c := New(8)
	tap := make(chan []byte, 4)
	t.Cleanup(c.Stop)

	c.Configure(44100, 2, tap)
	c.mu.Lock()
	first, run, stopped := c.cmd, c.run, c.stopped
	c.mu.Unlock()
	if first == nil || run == nil {
		t.Fatal("Configure did not start a process")
	}
	select {
	case <-stopped:
		t.Skip("cava exited on its own; the alive-but-silent case can't be exercised")
	default:
	}

	// Backdate the last output frame past the stall window: the process is
	// still very much alive, it just isn't producing anything.
	run.lastFrame.Store(time.Now().Add(-2 * barStallTimeout).UnixNano())

	c.Configure(44100, 2, tap)

	c.mu.Lock()
	second := c.cmd
	c.mu.Unlock()
	if second == first {
		t.Error("Configure kept a wedged (alive but silent) cava instead of restarting it")
	}
}

// Same shape, via the feeder rather than the output.
func TestConfigureRestartsAfterTheFeederStops(t *testing.T) {
	if !Available() {
		t.Skip("cava binary not available")
	}

	c := New(8)
	tap := make(chan []byte, 4)
	t.Cleanup(c.Stop)

	c.Configure(44100, 2, tap)
	c.mu.Lock()
	first, run := c.cmd, c.run
	c.mu.Unlock()
	if first == nil || run == nil {
		t.Fatal("Configure did not start a process")
	}

	run.feederDone.Store(true)
	c.Configure(44100, 2, tap)

	c.mu.Lock()
	second := c.cmd
	c.mu.Unlock()
	if second == first {
		t.Error("Configure kept a starved cava instead of restarting it")
	}
}
