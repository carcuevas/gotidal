package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

func jKey() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")} }
func enter() tea.KeyMsg  { return tea.KeyMsg{Type: tea.KeyEnter} }
func escKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEsc} }

// Reported: the action sheet's "Add to playlist…" entry did nothing at all
// (a literal no-op placeholder — actAddPlaylist never wired past its own
// comment). It must actually open the picker now.
func TestActAddPlaylistOpensThePicker(t *testing.T) {
	m := newSmokeModel()
	track := m.tracks[0]
	m.sheetTrack = &track
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Chill"}}

	nm, _ := m.runSheetAction(actAddPlaylist)
	got := asModel(t, nm)

	if got.overlay != OverlayAddToPlaylist {
		t.Fatalf("overlay = %v, want OverlayAddToPlaylist", got.overlay)
	}
	if len(got.addToPlaylistTracks) != 1 || got.addToPlaylistTracks[0].ID != track.ID {
		t.Errorf("addToPlaylistTracks = %+v, want just %+v", got.addToPlaylistTracks, track)
	}
}

// The picker's "+ Create New Playlist…" row must be reachable and lead to the
// name prompt, for both the per-track and whole-queue flows.
func TestAddToPlaylistCreateNewRowOpensNamePrompt(t *testing.T) {
	m := newSmokeModel()
	m.overlay = OverlayAddToPlaylist
	m.cursor = 0
	m.addToPlaylistTracks = []tidal.Track{m.tracks[0]}
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Chill"}}

	nm, _ := m.updateAddToPlaylist(enter())
	got := asModel(t, nm)
	if got.overlay != OverlayNewPlaylistName {
		t.Fatalf("overlay = %v, want OverlayNewPlaylistName", got.overlay)
	}
	if !got.newPlaylistInput.Focused() {
		t.Error("the name input should be focused")
	}
}

// Moving past row 0 must land on the first real playlist, not skip it or
// double-count the new row.
func TestAddToPlaylistNavigationAccountsForTheCreateRow(t *testing.T) {
	m := newSmokeModel()
	m.overlay = OverlayAddToPlaylist
	m.cursor = 0
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Chill"}, {UUID: "p2", Title: "Focus"}}

	nm, _ := m.updateAddToPlaylist(jKey())
	got := asModel(t, nm)
	if got.cursor != 1 {
		t.Fatalf("cursor = %d, want 1 (first playlist)", got.cursor)
	}

	nm, _ = got.updateAddToPlaylist(jKey())
	got = asModel(t, nm)
	if got.cursor != 2 {
		t.Fatalf("cursor = %d, want 2 (second playlist)", got.cursor)
	}

	// One more "j" must not move past the last playlist.
	nm, _ = got.updateAddToPlaylist(jKey())
	got = asModel(t, nm)
	if got.cursor != 2 {
		t.Errorf("cursor = %d, want to stay at 2 (last row)", got.cursor)
	}
}

// Selecting an existing playlist for a single track must add just that track
// and must NOT retag the live queue's source — that would silently claim the
// queue is now "backed by" a playlist the user only added one unrelated song
// to.
func TestSelectExistingPlaylistForATrackDoesNotRetagQueue(t *testing.T) {
	var gotIDs []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/items") {
			// AddTracksToPlaylist's endpoint; body format doesn't matter to
			// this test beyond confirming a call happened.
			gotIDs = append(gotIDs, 1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	origSource := m.queueSource
	m.overlay = OverlayAddToPlaylist
	m.cursor = 1 // first (only) playlist row, past the create-new row
	m.addToPlaylistTracks = []tidal.Track{{ID: 999, Title: "Solo"}}
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Chill"}}

	nm, cmd := m.updateAddToPlaylist(enter())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(playlistTracksAddedMsg)
	if !ok {
		t.Fatalf("expected playlistTracksAddedMsg, got %T: %+v", msg, msg)
	}
	if added.count != 1 || added.name != "Chill" {
		t.Errorf("unexpected message: %+v", added)
	}
	if len(gotIDs) == 0 {
		t.Error("AddTracksToPlaylist endpoint was never called")
	}

	got := asModel(t, nm)
	final, _ := got.Update(added)
	f := asModel(t, final)
	if f.queueSource != origSource {
		t.Errorf("queueSource changed to %q after adding a single track to a playlist — the live queue must be untouched", f.queueSource)
	}
	if f.queuePlaylistUUID != "" {
		t.Errorf("queuePlaylistUUID = %q, want empty — this was not a whole-queue save", f.queuePlaylistUUID)
	}
}

// The whole-queue flow (addToPlaylistTracks == nil) must keep its existing
// behaviour untouched: selecting an existing playlist there still goes
// through saveQueueToExistingCmd and queueSavedMsg.
func TestSelectExistingPlaylistForTheQueueKeepsOldBehaviour(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.overlay = OverlayAddToPlaylist
	m.cursor = 1
	m.addToPlaylistTracks = nil // the whole-queue path
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Chill"}}

	_, cmd := m.updateAddToPlaylist(enter())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	if _, ok := msg.(queueSavedMsg); !ok {
		t.Fatalf("expected queueSavedMsg (unchanged whole-queue behaviour), got %T", msg)
	}
}

// Creating a new playlist for a single track must add only that track and,
// like the existing-playlist case above, must not retag the queue.
func TestCreateNewPlaylistForATrackDoesNotRetagQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/playlists") && r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"uuid":"new-uuid"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	origSource := m.queueSource
	m.overlay = OverlayNewPlaylistName
	m.addToPlaylistTracks = []tidal.Track{{ID: 999, Title: "Solo"}}
	m.openNewPlaylistPrompt(OverlayAddToPlaylist)
	m.newPlaylistInput.SetValue("My New Playlist")

	nm, cmd := m.updateNewPlaylistName(enter())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(playlistTracksAddedMsg)
	if !ok {
		t.Fatalf("expected playlistTracksAddedMsg, got %T: %+v", msg, msg)
	}
	if added.name != "My New Playlist" || added.count != 1 {
		t.Errorf("unexpected message: %+v", added)
	}

	got := asModel(t, nm)
	if got.overlay != OverlayNone {
		t.Errorf("overlay = %v, want OverlayNone after creating", got.overlay)
	}
	final, _ := got.Update(added)
	f := asModel(t, final)
	if f.queueSource != origSource {
		t.Errorf("queueSource changed after creating a playlist for a single track: %q", f.queueSource)
	}
}

// Esc from the name prompt must go back to the picker, not discard the whole
// flow — the user might just have mistyped and want another crack at picking
// or creating.
func TestEscFromNamePromptReturnsToPicker(t *testing.T) {
	m := newSmokeModel()
	m.overlay = OverlayNewPlaylistName
	m.addToPlaylistTracks = []tidal.Track{m.tracks[0]}
	m.openNewPlaylistPrompt(OverlayAddToPlaylist)

	nm, _ := m.updateNewPlaylistName(escKey())
	got := asModel(t, nm)
	if got.overlay != OverlayAddToPlaylist {
		t.Errorf("overlay = %v, want OverlayAddToPlaylist (back to the picker)", got.overlay)
	}
}

// An empty name must not create anything.
func TestEmptyNameDoesNotCreateAPlaylist(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.overlay = OverlayNewPlaylistName
	m.addToPlaylistTracks = []tidal.Track{m.tracks[0]}
	ti := m.newPlaylistInput
	ti.Placeholder = ""
	m.newPlaylistInput = ti

	nm, cmd := m.updateNewPlaylistName(enter())
	got := asModel(t, nm)
	if got.overlay != OverlayNone {
		t.Errorf("overlay = %v, want OverlayNone", got.overlay)
	}
	if cmd != nil {
		cmd()
	}
	if called {
		t.Error("an empty name must not create a playlist")
	}
}

// Esc from the picker must clear addToPlaylistTracks — otherwise a later,
// unrelated "Save queue to existing playlist…" invocation could pick up a
// stale per-track set left over from a cancelled action-sheet flow.
func TestEscFromPickerClearsAddToPlaylistTracks(t *testing.T) {
	m := newSmokeModel()
	m.overlay = OverlayAddToPlaylist
	m.addToPlaylistTracks = []tidal.Track{m.tracks[0]}

	nm, _ := m.updateAddToPlaylist(escKey())
	got := asModel(t, nm)
	if got.addToPlaylistTracks != nil {
		t.Errorf("addToPlaylistTracks = %+v, want nil after Esc", got.addToPlaylistTracks)
	}
}
