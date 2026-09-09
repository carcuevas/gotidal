package ui

import (
	"testing"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// Reported: opening the artist drill-down from a track playing (Ctrl+X → a)
// left the Queue tab's album art rasterized on top of the drill-down's own,
// differently laid out screen. coverBoxRect only checked m.section, but
// showArtist is a transient overlay on top of whichever section opened it
// (openArtistFor saves the original in prevSection rather than changing
// m.section) — so it kept reporting the Queue's art box as on-screen while
// the drill-down was what was actually showing, and the stale Sixel image —
// which has no separate compositing layer, just cells it was rasterized into
// — was never told to clear.
func TestCoverBoxRectHiddenDuringArtistDrilldown(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 120, 40
	m.section = SecQueue

	if _, _, _, _, ok := m.coverBoxRect(); !ok {
		t.Fatal("fixture problem: the Queue tab itself must report a cover box for this test to mean anything")
	}

	m.showArtist = true
	if _, _, _, _, ok := m.coverBoxRect(); ok {
		t.Error("coverBoxRect() reported the Queue's art box as visible while the artist drill-down is showing")
	}

	// And it must come back once the drill-down closes.
	m.showArtist = false
	if _, _, _, _, ok := m.coverBoxRect(); !ok {
		t.Error("coverBoxRect() did not report the Queue's art box again after leaving the artist drill-down")
	}
}

// The drill-down's own album-detail sub-view (m.artistAlbum != nil) is still
// part of showArtist, so it must be hidden the same way.
func TestCoverBoxRectHiddenDuringArtistAlbumDetail(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 120, 40
	m.section = SecQueue
	m.showArtist = true
	album := tidal.Album{ID: 1, Title: "Some Album"}
	m.artistAlbum = &album

	if _, _, _, _, ok := m.coverBoxRect(); ok {
		t.Error("coverBoxRect() reported the Queue's art box as visible during the artist album detail sub-view")
	}
}
