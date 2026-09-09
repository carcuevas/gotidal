package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/buildinfo"
)

// The running build should be readable off the Settings tab, without having to
// quit and run `gotidal -v`.
func TestSettingsListShowsTheVersion(t *testing.T) {
	m := newSmokeModel()
	m.section = SecSettings

	out := m.renderSettingsList(m.theme, 60, 24)

	if !strings.Contains(out, buildinfo.Version()) {
		t.Errorf("Settings list does not show the version %q; got:\n%s", buildinfo.Version(), out)
	}
	if !strings.Contains(out, "gotidal") {
		t.Errorf("version line is not labelled; got:\n%s", out)
	}
}

// The version is a footer, not a row: there is nothing to activate, so the
// cursor must not be able to stop on it.
func TestSettingsCursorCannotReachTheVersionFooter(t *testing.T) {
	if settingsRowCount != 5 {
		t.Fatalf("settingsRowCount = %d; the version footer must not be counted as a selectable row", settingsRowCount)
	}

	m := newSmokeModel()
	m.section = SecSettings
	m.themeCursor = settingsRowCount - 1

	// Pressing down on the last row must stay put rather than walking onto
	// the footer.
	nm, _ := m.updateSettings(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	got := asModel(t, nm)
	if got.themeCursor != settingsRowCount-1 {
		t.Errorf("cursor moved to %d past the last selectable row %d", got.themeCursor, settingsRowCount-1)
	}

	// And the last row still activates the Themes picker, not the footer.
	nm, _ = got.updateSettings(tea.KeyMsg{Type: tea.KeyEnter})
	if asModel(t, nm).overlay != OverlayThemePicker {
		t.Error("the last selectable row should still open the Themes picker")
	}
}
