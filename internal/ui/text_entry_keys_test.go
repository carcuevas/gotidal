package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// key builds a single-rune KeyMsg.
func key(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// Reported: typing a "t" while naming a new playlist changed the colour theme.
// "t" was a global binding guarded only against the Search input, so every
// other text prompt in the app saw it as a command. Theme changes now live on
// the Settings tab.
func TestTypingTInAPromptDoesNotChangeTheTheme(t *testing.T) {
	m := newSmokeModel()
	m.openNewPlaylistPrompt(OverlayAddToPlaylist)
	before := m.themeName

	nm, _ := m.handleKey(key('t'))
	got := asModel(t, nm)

	if got.themeName != before {
		t.Errorf("theme changed to %q while typing a playlist name (was %q)", got.themeName, before)
	}
	if v := got.newPlaylistInput.Value(); v != "t" {
		t.Errorf("playlist name input = %q, want %q — the keystroke should be literal text", v, "t")
	}
}

// "t" is gone as a global binding everywhere, not just in prompts.
func TestTIsNoLongerAGlobalThemeShortcut(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue
	before := m.themeName

	nm, _ := m.handleKey(key('t'))
	if got := asModel(t, nm); got.themeName != before {
		t.Errorf("theme changed to %q from the Queue tab (was %q)", got.themeName, before)
	}
}

// ...but the Settings tab keeps it, which is where changing the theme now
// lives (alongside the Themes row's picker).
func TestSettingsTabStillCyclesTheThemeWithT(t *testing.T) {
	m := newSmokeModel()
	m.section = SecSettings
	m.focusMain = true
	before := m.themeName

	nm, _ := m.handleKey(key('t'))
	got := asModel(t, nm)
	if got.themeName == before {
		t.Errorf("theme did not change on the Settings tab (still %q)", before)
	}
	if len(paletteOrder) > 1 && got.themeName != paletteOrder[1] && before == paletteOrder[0] {
		t.Errorf("theme = %q, want the next palette %q", got.themeName, paletteOrder[1])
	}
}

// The same guard gap made "q" quit the application out from under someone
// half-way through naming a playlist — the guard only knew about the Search
// input and the command palette.
func TestTypingQInAPromptDoesNotQuit(t *testing.T) {
	m := newSmokeModel()
	m.openNewPlaylistPrompt(OverlayAddToPlaylist)

	nm, cmd := m.handleKey(key('q'))
	if cmd != nil {
		if _, isQuit := cmd().(tea.QuitMsg); isQuit {
			t.Fatal("\"q\" quit the app while typing a playlist name")
		}
	}
	if v := asModel(t, nm).newPlaylistInput.Value(); v != "q" {
		t.Errorf("playlist name input = %q, want %q", v, "q")
	}
}

// And ":" opened the command palette on top of the prompt.
func TestTypingColonInAPromptDoesNotOpenTheCommandPalette(t *testing.T) {
	m := newSmokeModel()
	m.openNewPlaylistPrompt(OverlayAddToPlaylist)

	nm, _ := m.handleKey(key(':'))
	got := asModel(t, nm)
	if got.overlay != OverlayNewPlaylistName {
		t.Errorf("overlay = %v, want the name prompt to still be open", got.overlay)
	}
	if v := got.newPlaylistInput.Value(); v != ":" {
		t.Errorf("playlist name input = %q, want %q", v, ":")
	}
}

// The Spotify-import prompt is the other text overlay the guard never knew
// about; it must behave the same way.
func TestTypingQInTheSpotifyImportPromptDoesNotQuit(t *testing.T) {
	m := newSmokeModel()
	m.overlay = OverlayImportSpotify

	if m.textEntryFocused() != true {
		t.Error("the Spotify-import prompt is a text field but is not treated as one")
	}
}

// Outside any prompt, "q" must still quit — the guard must not swallow it.
func TestQStillQuitsOutsideAPrompt(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue

	_, cmd := m.handleKey(key('q'))
	if cmd == nil {
		t.Fatal("\"q\" produced no command outside a prompt")
	}
	if _, isQuit := cmd().(tea.QuitMsg); !isQuit {
		t.Error("\"q\" no longer quits outside a prompt")
	}
}
