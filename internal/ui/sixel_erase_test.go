package ui

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Reported (foot only): navigating away from the Queue to Playlists/Artists/
// Albums/Songs/Mixes sometimes left a black rectangle where the album art had
// been. Erasing a Sixel cover means painting blanks over cells BubbleTea
// believes it owns — and the erase runs from a command, concurrently with the
// renderer writing the frame for that same navigation. Lose that race and the
// blanks land on top of the new tab, where BubbleTea's line diff will never
// repaint them because its model of the screen still says those lines are
// correct. Kitty is unaffected: it deletes an image placement by ID rather
// than touching the text layer.
func TestSixelEraseReportsItselfSoTheCallerCanRepaint(t *testing.T) {
	var buf bytes.Buffer
	m := newSmokeModel()
	m.sixelSupported = true
	m.ttyOut = &buf
	// Away from the Queue, so coverBoxRect reports no cover box and
	// syncSixelCover takes its erase branch.
	m.section = SecPlaylists
	m.sixel = &sixelState{
		drawnKey:  "cover@100x100",
		drawnCol:  5,
		drawnRow:  2,
		drawnCols: 20,
		drawnRows: 10,
	}

	if !m.syncSixelCover(true) {
		t.Fatal("syncSixelCover did not report erasing the cover")
	}
	if !strings.Contains(buf.String(), "\x1b[2;5H") {
		t.Errorf("no erase escape targeting the drawn position: %q", buf.String())
	}

	// Once erased, there is nothing left to erase — the repaint must not
	// repeat on every subsequent Update.
	buf.Reset()
	if m.syncSixelCover(true) {
		t.Error("syncSixelCover reported a second erase with nothing on screen")
	}
	if buf.Len() != 0 {
		t.Errorf("wrote escapes with nothing on screen: %q", buf.String())
	}
}

// The reported erase must actually reach BubbleTea as a full-repaint request;
// returning nil there is what left the blanks on screen.
func TestUpdateRepaintsAfterErasingTheSixelCover(t *testing.T) {
	var buf bytes.Buffer
	m := newSmokeModel()
	m.sixelSupported = true
	m.ttyOut = &buf
	// Already navigated away from the Queue, with the cover still on screen —
	// the state the reconcile command runs in. An inert message keeps the
	// batch down to that one command, so what it returns is unambiguous.
	m.section = SecPlaylists
	m.sixel = &sixelState{
		drawnKey:  "cover@100x100",
		drawnCol:  5,
		drawnRow:  2,
		drawnCols: 20,
		drawnRows: 10,
	}

	_, cmd := m.Update(struct{}{})
	if cmd == nil {
		t.Fatal("Update returned no command, so the cover is never reconciled")
	}
	if got, want := cmd(), tea.ClearScreen(); got != want {
		t.Errorf("reconcile returned %#v, want a repaint request after erasing the Sixel cover", got)
	}
}
