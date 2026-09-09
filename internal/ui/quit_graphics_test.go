package ui

import (
	"bytes"
	"strings"
	"testing"
)

// A cover left on screen when the program quits used to be permanent: Sixel
// rasterizes straight into the terminal's character grid, so a placement that
// reaches the terminal after BubbleTea has already disabled the alternate
// screen paints directly onto the shell prompt, and nothing ever overwrites
// those cells again. clearGraphicsOverlays is quit()'s synchronous escape
// hatch, called before tea.Quit rather than racing an async reconcile against
// program teardown.
func TestClearGraphicsOverlaysErasesADrawnSixelCover(t *testing.T) {
	var buf bytes.Buffer
	m := Model{
		ttyOut: &buf,
		sixel: &sixelState{
			drawnKey:  "cover@100x100",
			drawnCol:  5,
			drawnRow:  2,
			drawnCols: 20,
			drawnRows: 10,
		},
	}

	m.clearGraphicsOverlays()

	if buf.Len() == 0 {
		t.Fatal("no clear escape was written for a drawn Sixel cover")
	}
	if !strings.Contains(buf.String(), "\x1b[2;5H") {
		t.Errorf("clear escape does not target the drawn position (row 2, col 5): %q", buf.String())
	}
	if m.sixel.drawnKey != "" {
		t.Error("drawnKey should be reset after clearing, so a later sync does not think it is still on screen")
	}
}

func TestClearGraphicsOverlaysErasesADrawnKittyCover(t *testing.T) {
	var buf bytes.Buffer
	m := Model{
		ttyOut: &buf,
		kitty:  &kittyState{drawnKey: "cover@40x20+5,2"},
	}

	m.clearGraphicsOverlays()

	if buf.Len() == 0 {
		t.Fatal("no clear escape was written for a drawn Kitty cover")
	}
	if m.kitty.drawnKey != "" {
		t.Error("drawnKey should be reset after clearing")
	}
}

// Nothing on screen, nothing to write — quitting from a tab with no cover (or
// before any cover was ever drawn) must not emit stray escapes.
func TestClearGraphicsOverlaysNoOpWhenNothingIsDrawn(t *testing.T) {
	var buf bytes.Buffer
	m := Model{
		ttyOut: &buf,
		kitty:  &kittyState{},
		sixel:  &sixelState{},
	}
	m.clearGraphicsOverlays()
	if buf.Len() != 0 {
		t.Errorf("wrote %q with nothing drawn", buf.String())
	}
}

// Constructed models (tests, or any path that skips the lazy allocation in
// syncKittyCover/syncSixelCover) must not panic on a nil kitty/sixel.
func TestClearGraphicsOverlaysHandlesNilState(t *testing.T) {
	m := Model{}
	m.clearGraphicsOverlays() // must not panic
}

// quit() must actually call through to the clear, not just happen to leave it
// unbroken — this is the regression itself: quit() used to return tea.Quit
// with no graphics cleanup step at all.
func TestQuitClearsGraphicsOverlays(t *testing.T) {
	m := newSmokeModel()
	var buf bytes.Buffer
	m.ttyOut = &buf
	m.sixel = &sixelState{drawnKey: "cover@100x100", drawnCols: 10, drawnRows: 5}

	_ = m.quit()

	if buf.Len() == 0 {
		t.Error("quit() did not clear a drawn Sixel cover")
	}
}
