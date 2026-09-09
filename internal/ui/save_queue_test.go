package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Ctrl+S used to be a two-key prefix chain whose only member was "Ctrl+S a".
// It is a direct binding now, so it must not arm a pending key — that would
// swallow whatever the user pressed next.
func TestCtrlSNoLongerArmsAPrefixChain(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	if got := asModel(t, nm); got.pendingKey != "" {
		t.Errorf("pendingKey = %q after Ctrl+S, want \"\" — the chain was removed", got.pendingKey)
	}
}

// Ctrl+S saves the queue, and asks for the playlist's name rather than
// inventing one silently.
func TestCtrlSPromptsForThePlaylistName(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue
	if len(m.tracks) == 0 {
		t.Fatal("fixture must have a non-empty queue")
	}

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := asModel(t, nm)
	if got.overlay != OverlayNewPlaylistName {
		t.Fatalf("overlay = %v after Ctrl+S, want the name prompt", got.overlay)
	}
	if got.newPlaylistInput.Placeholder == "" {
		t.Error("the prompt offers no suggested name")
	}
	if got.newPlaylistInput.Value() != "" {
		t.Errorf("prompt pre-filled with %q — the suggestion must be a placeholder, "+
			"so Enter is a deliberate choice rather than an accident",
			got.newPlaylistInput.Value())
	}
	if got.addToPlaylistTracks != nil {
		t.Error("addToPlaylistTracks must stay nil: this is the whole-queue flow")
	}
}

// Esc from a Ctrl+S prompt closes it outright. It must not fall back to the
// Add-to-Playlist picker, which the user never opened.
func TestEscapeFromACtrlSPromptClosesIt(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	nm2, _ := asModel(t, nm).handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got := asModel(t, nm2); got.overlay != OverlayNone {
		t.Errorf("overlay = %v after Esc, want it closed", got.overlay)
	}
}

// ...whereas Esc from the picker's "+ Create New Playlist…" row still steps
// back to the picker.
func TestEscapeFromThePickerPromptReturnsToThePicker(t *testing.T) {
	m := newSmokeModel()
	m.openNewPlaylistPrompt(OverlayAddToPlaylist)

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if got := asModel(t, nm); got.overlay != OverlayAddToPlaylist {
		t.Errorf("overlay = %v after Esc, want the picker", got.overlay)
	}
}

// An empty queue has nothing to save, so no prompt.
func TestCtrlSOnAnEmptyQueueDoesNotPrompt(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue
	m.tracks = nil

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	got := asModel(t, nm)
	if got.overlay == OverlayNewPlaylistName {
		t.Error("prompted for a name with nothing in the queue")
	}
	if got.errText == "" {
		t.Error("no explanation shown for the ignored Ctrl+S")
	}
}

// Confirming the prompt saves the queue under the typed name.
func TestNamePromptSavesTheQueue(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlS})
	typed := asModel(t, nm)
	for _, r := range "Road trip" {
		next, _ := typed.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		typed = asModel(t, next)
	}
	if got := typed.newPlaylistInput.Value(); got != "Road trip" {
		t.Fatalf("typed name = %q, want %q", got, "Road trip")
	}

	done, cmd := typed.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if got := asModel(t, done); got.overlay != OverlayNone {
		t.Errorf("overlay = %v after Enter, want it closed", got.overlay)
	}
	if cmd == nil {
		t.Fatal("Enter produced no save command")
	}
}

// The keybinding reference must describe the binding that actually exists.
func TestHelpOverlayDocumentsCtrlSAsADirectSave(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 100, 40

	help := stripANSI(m.renderHelpOverlay(m.theme))
	if strings.Contains(help, "Ctrl+S a") {
		t.Error("the keybinding reference still lists the removed \"Ctrl+S a\" chain")
	}
	if !strings.Contains(help, "Ctrl+S") {
		t.Error("the keybinding reference does not mention Ctrl+S at all")
	}
}

// Saving the queue stays reachable from the palette too.
func TestSaveQueueStillReachableFromTheCommandPalette(t *testing.T) {
	for _, it := range allPaletteItems() {
		if strings.HasPrefix(it.label, "Save queue as playlist") {
			return
		}
	}
	t.Fatal("no \"Save queue as playlist\" command in the palette")
}
