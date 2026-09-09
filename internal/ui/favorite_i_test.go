package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

func iKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")} }

// Reported: "i" should favorite a track/album/playlist regardless of tab.
// This covers the track case — "F"'s existing behaviour, now also bound to
// "i" — on two tabs that previously had gaps: Search (already worked) and,
// via the Mixes/FavAlbums selectedTrack() fix, tabs where the cursor doesn't
// index m.tracks.
func TestIFavoritesATrackOnSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecSearch
	m.searchResults = tidal.SearchResults{Tracks: []tidal.Track{{ID: 555, Title: "Song"}}}
	m.searchCursor = 0

	nm, cmd := m.commonKeys(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	fav, ok := msg.(favoriteMsg)
	if !ok {
		t.Fatalf("expected favoriteMsg, got %T: %+v", msg, msg)
	}
	if fav.trackID != 555 || !fav.added {
		t.Errorf("unexpected favoriteMsg: %+v", fav)
	}
	_ = nm
}

// On the Queue tab, "i" must behave exactly like "F" always has.
func TestIFavoritesTheSelectedQueueTrack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecQueue
	m.cursor = 0

	_, cmd := m.commonKeys(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	fav, ok := msg.(favoriteMsg)
	if !ok {
		t.Fatalf("expected favoriteMsg, got %T", msg)
	}
	if fav.trackID != m.tracks[0].ID {
		t.Errorf("trackID = %d, want %d", fav.trackID, m.tracks[0].ID)
	}
}

// "i" on the Mixes tab must do nothing (there is no selected track — a mix
// row is not one), rather than acting on an unrelated queue track.
func TestIDoesNothingOnMixesTab(t *testing.T) {
	m := newSmokeModel()
	m.section = SecMixes
	m.mixes = []tidal.Mix{{ID: "mix1", Title: "Daily Mix 1"}}
	m.cursor = 0

	_, cmd := m.commonKeys(iKey())
	if cmd != nil {
		t.Error("expected no command — a mix row is not favoritable as a track")
	}
}

// "i" on the Favorite Albums tab removes the selected album — there is
// nothing to "add," since every row there is already a favorite.
func TestIRemovesAlbumOnFavAlbumsTab(t *testing.T) {
	var removedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		removedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecFavAlbums
	m.favAlbums = []tidal.Album{{ID: 111, Title: "Album One"}, {ID: 222, Title: "Album Two"}}
	m.cursor = 0

	nm, cmd := m.updateFavAlbums(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	removed, ok := msg.(favoriteAlbumRemovedMsg)
	if !ok {
		t.Fatalf("expected favoriteAlbumRemovedMsg, got %T: %+v", msg, msg)
	}
	if removed.albumID != 111 {
		t.Errorf("albumID = %d, want 111", removed.albumID)
	}
	if removedPath == "" || removedPath == "/" {
		t.Error("RemoveFavoriteAlbum endpoint was never called")
	}

	got := asModel(t, nm)
	final, _ := got.Update(removed)
	f := asModel(t, final)
	if len(f.favAlbums) != 1 || f.favAlbums[0].ID != 222 {
		t.Errorf("favAlbums after removal = %+v, want just album 222", f.favAlbums)
	}
}

// "i" in the artist drill-down's discography list adds the selected album —
// there is no existing favorited-state to toggle off from there.
func TestIAddsAlbumInArtistDrilldown(t *testing.T) {
	var addedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		addedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.showArtist = true
	m.artistAlbum = nil
	m.artistAlbums = []tidal.Album{{ID: 333, Title: "Discography Album"}}
	m.artistCursor = 2 // 0 and 1 are "Play all"/"Top tracks"; 2 is the first album

	nm, cmd := m.updateArtist(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(favoriteAlbumAddedMsg)
	if !ok {
		t.Fatalf("expected favoriteAlbumAddedMsg, got %T: %+v", msg, msg)
	}
	if added.title != "Discography Album" {
		t.Errorf("unexpected title: %q", added.title)
	}
	if addedPath == "" {
		t.Error("AddFavoriteAlbum endpoint was never called")
	}
	_ = nm
}
