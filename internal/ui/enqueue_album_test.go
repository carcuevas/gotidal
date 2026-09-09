package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// albumTracksServer answers GetAlbumTracks with a fixed two-track album.
func albumTracksServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"id":11,"title":"Track A"},
			{"id":22,"title":"Track B"}
		]}`))
	}))
}

// assertEnqueuesAlbum runs "a" through handler and checks the resulting
// command yields the album's tracks.
func assertEnqueuesAlbum(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("\"a\" produced no command — the album was not enqueued")
	}
	msg := cmd()
	enq, ok := msg.(enqueuePlaylistMsg)
	if !ok {
		if e, isErr := msg.(errMsg); isErr {
			t.Fatalf("command returned an error: %v", error(e))
		}
		t.Fatalf("expected enqueuePlaylistMsg, got %T: %+v", msg, msg)
	}
	if len(enq.tracks) != 2 || enq.tracks[0].ID != 11 || enq.tracks[1].ID != 22 {
		t.Errorf("unexpected tracks: %+v", enq.tracks)
	}
}

// Reported: "a" on a Search *Album* row did nothing, even though "a" on a
// Search Playlist row enqueues the whole playlist. An album row has no single
// track for commonKeys' selectedTrack() to find, and unlike playlists and
// mixes there was no album-shaped fallback.
func TestEnqueueAlbumFromSearchRow(t *testing.T) {
	srv := albumTracksServer(t)
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecSearch
	m.searchInput.Blur()
	m.searchResults = tidal.SearchResults{
		Albums: []tidal.Album{{ID: 555, Title: "Some Album"}},
	}
	m.searchCursor = 0

	_, cmd := m.updateSearchKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	assertEnqueuesAlbum(t, cmd)
}

// The same key on the Favorite Albums tab, where every row is an album too.
func TestEnqueueAlbumFromFavoritesTab(t *testing.T) {
	srv := albumTracksServer(t)
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecFavAlbums
	m.favAlbums = []tidal.Album{{ID: 555, Title: "Some Album"}}
	m.cursor = 0

	_, cmd := m.commonKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	assertEnqueuesAlbum(t, cmd)
}

// And on an album row inside the artist drill-down, whose own "a" handler
// returns before commonKeys ever sees the key.
func TestEnqueueAlbumFromArtistDiscography(t *testing.T) {
	srv := albumTracksServer(t)
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.showArtist = true
	m.artistID = 9
	m.artistName = "Someone"
	m.artistAlbums = []tidal.Album{{ID: 555, Title: "Some Album"}}
	m.artistCursor = 2 // rows 0 and 1 are "play all"/"top tracks"

	_, cmd := m.updateArtist(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	assertEnqueuesAlbum(t, cmd)
}

// The album path must not steal "a" from track rows: on the Queue tab there is
// no album under the cursor, so the pre-existing enqueue-the-track behaviour
// stands.
func TestEnqueueAlbumDoesNotHijackTrackRows(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue
	m.cursor = 0
	before := len(m.tracks)

	nm, _ := m.commonKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	got := asModel(t, nm)
	if len(got.tracks) != before+1 {
		t.Errorf("queue has %d tracks after \"a\" on a track row, want %d", len(got.tracks), before+1)
	}
}
