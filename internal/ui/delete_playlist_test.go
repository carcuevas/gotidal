package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// The two fixture playlists, named so goconst stays quiet about the repeats.
const (
	uuidMorning = "uuid-a"
	uuidEvening = "uuid-b"
)

func playlistsModel(t *testing.T) Model {
	t.Helper()
	m := newSmokeModel()
	m.section = SecPlaylists
	m.focusMain = true
	m.playlists = []tidal.Playlist{
		{UUID: uuidMorning, Title: "Morning"},
		{UUID: uuidEvening, Title: "Evening"},
	}
	m.cursor = 1
	return m
}

// "d" on a playlist row must ask before doing anything — deleting on Tidal is
// permanent, with no undo and no trash.
func TestDeletePlaylistAsksFirst(t *testing.T) {
	m := playlistsModel(t)

	nm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got := asModel(t, nm)
	if got.overlay != OverlayDeletePlaylist {
		t.Fatalf("overlay = %v after \"d\", want the confirmation", got.overlay)
	}
	if cmd != nil {
		t.Error("\"d\" issued a command before the user confirmed")
	}
	if got.deleteTarget == nil || got.deleteTarget.UUID != uuidEvening {
		t.Errorf("confirmation targets %+v, want the row under the cursor (uuid-b)", got.deleteTarget)
	}
	if body := stripANSI(got.renderDeletePlaylist(got.theme)); !strings.Contains(body, "Evening") {
		t.Errorf("the prompt does not name the playlist being deleted: %q", body)
	}
}

// Anything but "y" cancels, and nothing is sent.
func TestDeletePlaylistCancels(t *testing.T) {
	for _, k := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("n")},
		{Type: tea.KeyEnter},
	} {
		m := playlistsModel(t)
		nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
		nm2, cmd := asModel(t, nm).handleKey(k)
		got := asModel(t, nm2)

		if cmd != nil {
			t.Errorf("%v issued a delete command instead of cancelling", k)
		}
		if got.overlay != OverlayNone {
			t.Errorf("%v left overlay = %v, want it closed", k, got.overlay)
		}
		if len(got.playlists) != 2 {
			t.Errorf("%v removed a playlist locally: %+v", k, got.playlists)
		}
	}
}

// Enter is deliberately not a confirm: it is the key that opens a playlist in
// the list behind the prompt, so a stray one must not delete anything.
func TestEnterDoesNotConfirmADelete(t *testing.T) {
	m := playlistsModel(t)
	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	_, cmd := asModel(t, nm).handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("Enter confirmed a deletion")
	}
}

// "y" goes through to the API and the playlist then disappears locally.
func TestDeletePlaylistConfirmed(t *testing.T) {
	var deleted atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted.Store(r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	m := playlistsModel(t)
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	nm2, cmd := asModel(t, nm).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("\"y\" produced no delete command")
	}
	msg := cmd()
	del, ok := msg.(playlistDeletedMsg)
	if !ok {
		if e, isErr := msg.(errMsg); isErr {
			t.Fatalf("delete returned an error: %v", error(e))
		}
		t.Fatalf("expected playlistDeletedMsg, got %T: %+v", msg, msg)
	}
	if del.uuid != uuidEvening {
		t.Errorf("deleted uuid = %q, want uuid-b", del.uuid)
	}
	if path, _ := deleted.Load().(string); !strings.Contains(path, uuidEvening) {
		t.Errorf("DELETE went to %q, want a path naming uuid-b", path)
	}

	final, _ := asModel(t, nm2).Update(del)
	got := asModel(t, final)
	if len(got.playlists) != 1 || got.playlists[0].UUID != uuidMorning {
		t.Errorf("playlists = %+v, want only uuid-a left", got.playlists)
	}
	if got.cursor > 0 {
		t.Errorf("cursor = %d, want it clamped into the shortened list", got.cursor)
	}
}

// A queue loaded from the deleted playlist must stop claiming to be synced
// with it — there is nothing left to sync to.
func TestDeletingTheQueuesSourcePlaylistUntagsTheQueue(t *testing.T) {
	m := playlistsModel(t)
	m.queuePlaylistUUID = uuidEvening
	m.queueSource = "playlist:Evening"
	m.openPlaylist = &tidal.Playlist{UUID: uuidEvening, Title: "Evening"}
	m.detailTracks = m.tracks
	m.detailFocus = true

	m.applyPlaylistDeleted(uuidEvening)

	if m.queuePlaylistUUID != "" || m.queueSource != "" {
		t.Errorf("queue still tagged to the deleted playlist: uuid=%q source=%q",
			m.queuePlaylistUUID, m.queueSource)
	}
	if m.openPlaylist != nil || m.detailFocus {
		t.Error("the deleted playlist's detail view is still open")
	}
}

// Deleting an unrelated playlist must leave the queue's tag alone.
func TestDeletingAnotherPlaylistLeavesTheQueueTagged(t *testing.T) {
	m := playlistsModel(t)
	m.queuePlaylistUUID = uuidMorning
	m.queueSource = "playlist:Morning"

	m.applyPlaylistDeleted(uuidEvening)

	if m.queuePlaylistUUID != uuidMorning || m.queueSource != "playlist:Morning" {
		t.Errorf("queue tag disturbed: uuid=%q source=%q", m.queuePlaylistUUID, m.queueSource)
	}
}

// "d" inside a playlist's detail view is the track-level key, not this one.
func TestDeleteKeyDoesNotFireInThePlaylistDetailView(t *testing.T) {
	m := playlistsModel(t)
	m.detailFocus = true
	m.detailTracks = m.tracks

	nm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if got := asModel(t, nm); got.overlay == OverlayDeletePlaylist {
		t.Error("\"d\" in the detail view opened the delete-playlist confirmation")
	}
}
