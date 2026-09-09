package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

func aKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")} }

// Reported: Enter on the artist drill-down's "▶ Play all tracks"/
// "★ Top tracks" rows silently populated the queue without starting
// playback — the icons themselves promised playback that never came, and the
// user had to press Enter a second time on the new first track. Every
// producer of tracksMsg (these rows, radio, an album, a mix) represents
// "play this," so the handler now starts playback itself.
func TestTracksMsgAutoPlaysTheFirstTrack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.player = nil // doPlayTrack must not require a real player to build its command

	tracks := []tidal.Track{{ID: 111, Title: "First"}, {ID: 222, Title: "Second"}}

	nm, cmd := m.Update(tracksMsg(tracks))
	if cmd == nil {
		t.Fatal("expected a command to start playback")
	}
	got := asModel(t, nm)
	if got.currentTrack == nil || got.currentTrack.ID != 111 {
		t.Errorf("currentTrack after tracksMsg = %+v, want track 111 (the first loaded)", got.currentTrack)
	}
}

// "a" on the artist drill-down's synthetic rows must fetch and append,
// leaving whatever is already queued/playing untouched — the non-destructive
// counterpart to Enter (which loads and plays, replacing the queue).
func TestArtistPlayAllTracksAAddsWithoutReplacing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":1,"title":"Deep Cut"}]}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.showArtist = true
	m.artistID = 42
	m.artistName = "Test Artist"
	m.artistCursor = 0 // "▶ Play all tracks"
	before := len(m.tracks)

	nm, cmd := m.updateArtist(aKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	enq, ok := msg.(enqueuePlaylistMsg)
	if !ok {
		t.Fatalf("expected enqueuePlaylistMsg, got %T: %+v", msg, msg)
	}
	if len(enq.tracks) != 1 || enq.tracks[0].ID != 1 {
		t.Errorf("unexpected tracks: %+v", enq.tracks)
	}

	got := asModel(t, nm)
	final, _ := got.Update(enq)
	f := asModel(t, final)
	if len(f.tracks) != before+1 {
		t.Errorf("queue has %d tracks, want %d (existing %d + 1)", len(f.tracks), before+1, before)
	}
}

func TestArtistTopTracksAAddsWithoutReplacing(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"id":2,"title":"Hit Song"}]}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.showArtist = true
	m.artistID = 42
	m.artistName = "Test Artist"
	m.artistCursor = 1 // "★ Top tracks"

	_, cmd := m.updateArtist(aKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	enq, ok := msg.(enqueuePlaylistMsg)
	if !ok {
		t.Fatalf("expected enqueuePlaylistMsg, got %T", msg)
	}
	if len(enq.tracks) != 1 || enq.tracks[0].ID != 2 {
		t.Errorf("unexpected tracks: %+v", enq.tracks)
	}
	if !contains(gotPath, "toptracks") && !contains(gotPath, "top") {
		t.Logf("path was %q (not asserting exact shape, just that a request happened)", gotPath)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// "a" (and other track-scoped keys) on the artist view's own top-level rows
// or album list must not fall back to acting on an unrelated queue track —
// selectedTrack() has to know these rows aren't tracks.
func TestSelectedTrackExcludesArtistTopLevelRows(t *testing.T) {
	m := newSmokeModel()
	m.showArtist = true
	m.artistAlbum = nil
	m.artistCursor = 0
	if len(m.tracks) == 0 {
		t.Fatal("fixture needs queue tracks for this to be meaningful")
	}

	got := m.selectedTrack()
	if got != nil {
		t.Errorf("selectedTrack() = %+v while browsing the artist's own rows, want nil", got)
	}
}

// A track inside an album opened from the artist drill-down IS a real track,
// and must be found by selectedTrack (mirrors the Playlists detail case).
func TestSelectedTrackFindsTrackInsideArtistAlbumDetail(t *testing.T) {
	m := newSmokeModel()
	m.showArtist = true
	album := tidal.Album{ID: 9, Title: "Some Album"}
	m.artistAlbum = &album
	m.artistAlbumTracks = []tidal.Track{{ID: 501, Title: "Track One"}, {ID: 502, Title: "Track Two"}}
	m.artistAlbumCursor = 1

	got := m.selectedTrack()
	if got == nil || got.ID != 502 {
		t.Errorf("selectedTrack() = %+v, want track 502", got)
	}
}

// "i" on a Search artist result adds it to favorites.
func TestIFavoritesAnArtistOnSearch(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecSearch
	m.searchResults = tidal.SearchResults{Artists: []tidal.Artist{{ID: 77, Name: "Some Artist"}}}
	m.searchCursor = 0 // the flattened rows put a lone artist result at 0

	_, cmd := m.commonKeys(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(favoriteArtistAddedMsg)
	if !ok {
		t.Fatalf("expected favoriteArtistAddedMsg, got %T: %+v", msg, msg)
	}
	if added.name != "Some Artist" {
		t.Errorf("unexpected name: %q", added.name)
	}
	if gotPath == "" {
		t.Error("AddFavoriteArtist endpoint was never called")
	}
}

// "i" on a Search album result adds it to favorites.
func TestIFavoritesAnAlbumOnSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecSearch
	m.searchResults = tidal.SearchResults{Albums: []tidal.Album{{ID: 88, Title: "Some Album"}}}
	m.searchCursor = 0

	_, cmd := m.commonKeys(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(favoriteAlbumAddedMsg)
	if !ok {
		t.Fatalf("expected favoriteAlbumAddedMsg, got %T: %+v", msg, msg)
	}
	if added.title != "Some Album" {
		t.Errorf("unexpected title: %q", added.title)
	}
}

// "i" removes on the Favorite Artists tab, where every row is already a
// favorite — mirrors the Favorite Albums case.
func TestIRemovesArtistOnFavArtistsTab(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecFavArtists
	m.favArtists = []tidal.Artist{{ID: 11, Name: "Artist One"}, {ID: 22, Name: "Artist Two"}}
	m.cursor = 0

	nm, cmd := m.updateFavArtists(iKey())
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	removed, ok := msg.(favoriteArtistRemovedMsg)
	if !ok {
		t.Fatalf("expected favoriteArtistRemovedMsg, got %T: %+v", msg, msg)
	}
	if removed.artistID != 11 {
		t.Errorf("artistID = %d, want 11", removed.artistID)
	}

	got := asModel(t, nm)
	final, _ := got.Update(removed)
	f := asModel(t, final)
	if len(f.favArtists) != 1 || f.favArtists[0].ID != 22 {
		t.Errorf("favArtists after removal = %+v, want just artist 22", f.favArtists)
	}
}

// The action sheet's "Favorite artist"/"Favorite album" entries operate on
// the current track's own artist/album.
func TestActionSheetFavoritesTrackArtist(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	track := m.tracks[0]
	track.Artist = tidal.Artist{ID: 33, Name: "Track Artist"}
	m.sheetTrack = &track

	nm, cmd := m.runSheetAction(actFavoriteArtist)
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(favoriteArtistAddedMsg)
	if !ok {
		t.Fatalf("expected favoriteArtistAddedMsg, got %T: %+v", msg, msg)
	}
	if added.name != "Track Artist" {
		t.Errorf("unexpected name: %q", added.name)
	}
	_ = nm
}

func TestActionSheetFavoritesTrackAlbum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	track := m.tracks[0]
	track.Album.ID = 44
	track.Album.Title = "Track Album"
	m.sheetTrack = &track

	_, cmd := m.runSheetAction(actFavoriteAlbum)
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg := cmd()
	added, ok := msg.(favoriteAlbumAddedMsg)
	if !ok {
		t.Fatalf("expected favoriteAlbumAddedMsg, got %T: %+v", msg, msg)
	}
	if added.title != "Track Album" {
		t.Errorf("unexpected title: %q", added.title)
	}
}

// A track with no artist/album ID (shouldn't normally happen, but the sheet
// must not panic or fire a bogus request) is a safe no-op.
func TestActionSheetFavoriteArtistAlbumNoIDIsANoOp(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	track := tidal.Track{ID: 1, Title: "No Artist/Album"}
	m.sheetTrack = &track

	_, cmd := m.runSheetAction(actFavoriteArtist)
	if cmd != nil {
		cmd()
	}
	_, cmd2 := m.runSheetAction(actFavoriteAlbum)
	if cmd2 != nil {
		cmd2()
	}
	if called {
		t.Error("no network call should happen when the track has no artist/album ID")
	}
}
