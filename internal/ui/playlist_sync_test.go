package ui

import (
	"testing"

	"github.com/carcuevas/gotidal/internal/tidal"
)

func titles(pls []tidal.Playlist) []string {
	out := make([]string, len(pls))
	for i := range pls {
		out[i] = pls[i].Title
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Reported: a newly created playlist did not show up on the Playlists tab.
// The list was fetched once per session and cached forever after — nothing
// folded a create back into it, and loadSection's "already loaded" check
// meant revisiting the tab never refetched either.
func TestCreatingAPlaylistAddsItToTheList(t *testing.T) {
	m := newSmokeModel()
	m.playlists = []tidal.Playlist{{UUID: "a", Title: "Alpha", NumberOfTracks: 3}}

	m.applyPlaylistUpserted("new", "Beta", 7, true)

	if got, want := titles(m.playlists), []string{"Alpha", "Beta"}; !equalStrings(got, want) {
		t.Errorf("playlists = %v, want %v", got, want)
	}
	if m.playlists[1].NumberOfTracks != 7 {
		t.Errorf("new playlist has %d tracks, want 7", m.playlists[1].NumberOfTracks)
	}
}

// Appending to an existing playlist must update its track count in place, not
// add a second row for it.
func TestAppendingToAPlaylistUpdatesItsCount(t *testing.T) {
	m := newSmokeModel()
	m.playlists = []tidal.Playlist{{UUID: "a", Title: "Alpha", NumberOfTracks: 3}}

	m.applyPlaylistUpserted("a", "Alpha", 2, false)

	if len(m.playlists) != 1 {
		t.Fatalf("playlists = %v, want the one row updated in place", titles(m.playlists))
	}
	if m.playlists[0].NumberOfTracks != 5 {
		t.Errorf("track count = %d, want 5 (3 + 2 appended)", m.playlists[0].NumberOfTracks)
	}
}

// An append to something not in the cache must not invent a row whose track
// count is only the delta — the refetch will bring the real one.
func TestAppendingToAnUncachedPlaylistAddsNoRow(t *testing.T) {
	m := newSmokeModel()
	m.playlists = []tidal.Playlist{{UUID: "a", Title: "Alpha", NumberOfTracks: 3}}

	m.applyPlaylistUpserted("elsewhere", "Somewhere Else", 2, false)

	if len(m.playlists) != 1 {
		t.Errorf("playlists = %v, want the unknown playlist left to the refetch", titles(m.playlists))
	}
	if !m.playlistsStale {
		t.Error("the list was not marked for refetch")
	}
}

// Every mutation must mark the cache stale so the tab refetches.
func TestMutationsMarkThePlaylistCacheStale(t *testing.T) {
	for name, mutate := range map[string]func(*Model){
		"create": func(m *Model) { m.applyPlaylistUpserted("new", "Beta", 1, true) },
		"append": func(m *Model) { m.applyPlaylistUpserted("a", "Alpha", 1, false) },
		"delete": func(m *Model) { m.applyPlaylistDeleted("a") },
	} {
		m := newSmokeModel()
		m.playlists = []tidal.Playlist{{UUID: "a", Title: "Alpha"}}
		m.playlistsStale = false

		mutate(&m)

		if !m.playlistsStale {
			t.Errorf("%s did not mark the playlist cache stale", name)
		}
	}
}

// A stale cache must actually cause a refetch when the tab is opened; a fresh
// one must not.
func TestLoadSectionRefetchesAStalePlaylistCache(t *testing.T) {
	m := newSmokeModel()
	m.playlists = []tidal.Playlist{{UUID: "a", Title: "Alpha"}}

	m.playlistsStale = false
	if cmd := m.loadSection(SecPlaylists); cmd != nil {
		t.Error("refetched a cache that was still fresh")
	}

	m.playlistsStale = true
	if cmd := m.loadSection(SecPlaylists); cmd == nil {
		t.Error("did not refetch a stale cache — a new playlist would stay invisible")
	}
}

// Reported alongside: the list came back in Tidal's date order. It is shown
// alphabetically now, case-insensitively.
func TestPlaylistsAreSortedAlphabetically(t *testing.T) {
	pls := []tidal.Playlist{
		{UUID: "1", Title: "zebra"},
		{UUID: "2", Title: "Apple"},
		{UUID: "3", Title: "banana"},
		{UUID: "4", Title: "Cherry"},
	}
	sortPlaylists(pls)

	want := []string{"Apple", "banana", "Cherry", "zebra"}
	if got := titles(pls); !equalStrings(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Same-title playlists keep a stable relative order across refetches.
func TestPlaylistSortIsStableForDuplicateTitles(t *testing.T) {
	pls := []tidal.Playlist{
		{UUID: "b", Title: "Mix"},
		{UUID: "a", Title: "Mix"},
	}
	sortPlaylists(pls)
	if pls[0].UUID != "a" || pls[1].UUID != "b" {
		t.Errorf("duplicate titles ordered %s,%s — want a stable uuid tiebreak", pls[0].UUID, pls[1].UUID)
	}
}

// A freshly fetched list is sorted on arrival, not at render time, so the
// cursor indexes the order the user sees.
func TestFetchedPlaylistsArriveSorted(t *testing.T) {
	m := newSmokeModel()
	m.playlistsStale = true

	next, _ := m.Update(playlistsMsg([]tidal.Playlist{
		{UUID: "1", Title: "Zulu"},
		{UUID: "2", Title: "alpha"},
	}))
	got := asModel(t, next)

	if want := []string{"alpha", "Zulu"}; !equalStrings(titles(got.playlists), want) {
		t.Errorf("order = %v, want %v", titles(got.playlists), want)
	}
	if got.playlistsStale {
		t.Error("a fresh list did not clear the stale flag")
	}
}
