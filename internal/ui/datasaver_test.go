package ui

import (
	"strings"
	"testing"

	"github.com/carcuevas/gotidal/internal/player"
)

// Data Saver forces PipeWire output, so bit-perfect quality is not the user's
// to set while it is on — the two would otherwise contradict each other, with
// the Settings row claiming "On (DAC)" for output that isn't.
func TestBitPerfectToggleIsLockedByDataSaver(t *testing.T) {
	m := newSmokeModel()
	m.lowDataMode = true
	m.bitPerfectMode = false

	nm, _ := m.toggleBitPerfectMode()
	got := asModel(t, nm)

	if got.bitPerfectMode {
		t.Error("bit-perfect mode was switched on while Data Saver is on")
	}
	if !strings.Contains(got.toast, "locked") {
		t.Errorf("toast = %q, want it to say the setting is locked", got.toast)
	}
}

// The lock is only for the Data Saver case; with it off the toggle still
// works. NewPlayer is pure struct initialisation (no ALSA, no D-Bus), so the
// real toggle can run here.
func TestBitPerfectToggleIsNotLockedWithoutDataSaver(t *testing.T) {
	m := newSmokeModel()
	m.player = player.NewPlayer()
	m.lowDataMode = false
	m.bitPerfectMode = false

	nm, _ := m.toggleBitPerfectMode()
	got := asModel(t, nm)

	if !got.bitPerfectMode {
		t.Error("bit-perfect mode did not turn on with Data Saver off")
	}
	if strings.Contains(got.toast, "locked") {
		t.Errorf("toast = %q, the toggle should not be locked with Data Saver off", got.toast)
	}
}

// Turning Data Saver on takes the setting over and off hands it back with the
// value it had, so a user who was on their DAC before a hotspot detour lands
// back on it.
func TestDataSaverRestoresBitPerfectOnRelease(t *testing.T) {
	m := newSmokeModel()
	m.player = player.NewPlayer()
	m.bitPerfectMode = true
	m.lowDataMode = false

	nm, _ := m.toggleLowDataMode()
	on := asModel(t, nm)
	if !on.lowDataMode {
		t.Fatal("Data Saver did not turn on")
	}
	if on.bitPerfectMode {
		t.Error("Data Saver must force bit-perfect off — it forces PipeWire output")
	}

	nm, _ = on.toggleLowDataMode()
	off := asModel(t, nm)
	if off.lowDataMode {
		t.Fatal("Data Saver did not turn off")
	}
	if !off.bitPerfectMode {
		t.Error("bit-perfect should be restored to the value it had before Data Saver")
	}
}

// The Settings list has to say why the row won't change, not just show a value.
func TestSettingsRowShowsBitPerfectLockedByDataSaver(t *testing.T) {
	m := newSmokeModel()
	m.section = SecSettings
	m.width, m.height = 120, 40

	m.lowDataMode = false
	if out := m.renderSettingsList(m.theme, 60, 20); !strings.Contains(out, "Off (PipeWire)") {
		t.Errorf("with Data Saver off, expected the normal value; got:\n%s", out)
	}

	m.lowDataMode = true
	out := m.renderSettingsList(m.theme, 60, 20)
	if !strings.Contains(out, "locked by Data Saver") {
		t.Errorf("with Data Saver on, expected the row to say it is locked; got:\n%s", out)
	}
}
