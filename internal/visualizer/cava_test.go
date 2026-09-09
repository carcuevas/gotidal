package visualizer

import (
	"testing"
	"time"
)

// Reported: after a seek, the frequency meter froze at its last frame and
// never recovered, even though playback (and the PCM tap) kept flowing. The
// seek itself was incidental — anything that kills the cava process or breaks
// its feed pipe once has the same effect, because Configure treated
// c.cmd != nil as "still running" without ever checking whether the process
// had actually died in the meantime.
func TestConfigureRestartsAfterTheProcessDies(t *testing.T) {
	if !Available() {
		t.Skip("cava binary not available")
	}

	c := New(8)
	tap := make(chan []byte, 4)
	t.Cleanup(c.Stop)

	c.Configure(44100, 2, tap)
	c.mu.Lock()
	firstCmd := c.cmd
	c.mu.Unlock()
	if firstCmd == nil {
		t.Fatal("Configure did not start a process")
	}

	// Kill the process out from under Configure, simulating a crash or the
	// FIFO write breaking — not going through Stop(), which is the
	// intentional-shutdown path this bug does not affect.
	if err := firstCmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill cava for the test: %v", err)
	}

	// Wait for the death to actually register (cmd.Wait() closing c.stopped
	// runs in its own goroutine) before asserting Configure notices it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		stopped := c.stopped
		c.mu.Unlock()
		select {
		case <-stopped:
		default:
			if time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			t.Fatal("killed process was never reaped")
		}
		break
	}

	// Same rate/channels as before: the pre-fix code took this as "already
	// running" and returned without ever looking at whether it still was.
	c.Configure(44100, 2, tap)

	c.mu.Lock()
	secondCmd := c.cmd
	c.mu.Unlock()
	if secondCmd == nil {
		t.Fatal("Configure did not restart cava after the process died")
	}
	if secondCmd == firstCmd {
		t.Error("Configure returned the same (dead) *exec.Cmd instead of starting a new process")
	}
}

// The ordinary case — the process is genuinely still running — must stay a
// cheap no-op, or every tick would restart cava for no reason.
func TestConfigureIsANoOpWhileStillRunning(t *testing.T) {
	if !Available() {
		t.Skip("cava binary not available")
	}

	c := New(8)
	tap := make(chan []byte, 4)
	t.Cleanup(c.Stop)

	c.Configure(44100, 2, tap)
	c.mu.Lock()
	first := c.cmd
	c.mu.Unlock()

	c.Configure(44100, 2, tap)
	c.mu.Lock()
	second := c.cmd
	c.mu.Unlock()

	if first != second {
		t.Error("Configure restarted a process that was still alive")
	}
}

// A genuine format change must still restart, dead or not.
func TestConfigureRestartsOnFormatChange(t *testing.T) {
	if !Available() {
		t.Skip("cava binary not available")
	}

	c := New(8)
	tap := make(chan []byte, 4)
	t.Cleanup(c.Stop)

	c.Configure(44100, 2, tap)
	c.mu.Lock()
	first := c.cmd
	c.mu.Unlock()

	c.Configure(48000, 2, tap)
	c.mu.Lock()
	second := c.cmd
	rate := c.rate
	c.mu.Unlock()

	if first == second {
		t.Error("Configure did not restart on a rate change")
	}
	if rate != 48000 {
		t.Errorf("rate = %d, want 48000", rate)
	}
}
