package ui

import (
	"testing"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// newQueueModel is a smoke model with track index 1 of 3 playing, matching
// production: doPlayTrack copies the track and records its queue position.
func newQueueModel() Model {
	m := newSmokeModel()
	// newSmokeModel points tracks and tracksOrder at one backing array;
	// production keeps them separate (playListIntoQueue copies into
	// tracksOrder, applyShuffle rebuilds tracks), and moveQueueItem mirrors
	// its swap into tracksOrder — which on a shared array would undo itself.
	m.tracksOrder = append([]tidal.Track(nil), m.tracks...)
	m.tracks = append([]tidal.Track(nil), m.tracks...)

	playing := m.tracks[1]
	m.currentTrack = &playing
	m.playingIndex = 1
	m.cursor = 1
	m.isPlaying = true
	m.duration = float64(playing.Duration)
	return m
}

// Reported: playing a track, visiting Search, then returning to the Queue
// showed the *first* queued track's artwork and lyrics. selectSection zeroed
// the shared cursor, and the Queue cover/lyrics panes follow the cursor.
func TestQueueCursorReturnsToPlayingTrackAfterTabSwitch(t *testing.T) {
	m := newQueueModel()

	sm, _ := m.selectSection(SecSearch)
	m = asModel(t, sm)
	// Search has its own cursor, so the shared one is free to be reset here.

	sm, _ = m.selectSection(SecQueue)
	m = asModel(t, sm)

	if m.cursor != 1 {
		t.Errorf("Queue cursor = %d after leaving and returning, want 1 (the playing track)", m.cursor)
	}
	ct := m.coverTrack()
	if ct == nil {
		t.Fatal("coverTrack() = nil while a track is playing")
	}
	if ct.ID != m.currentTrack.ID {
		t.Errorf("coverTrack() = %q (ID %d), want the playing track %q (ID %d)",
			ct.Title, ct.ID, m.currentTrack.Title, m.currentTrack.ID)
	}
}

// Reported: after that same tab switch the track ended and the UI moved to the
// next song, but the audio replayed the one just finished. nextIndex counted
// from the shared cursor, which the tab switch had reset to 0 — so "next"
// resolved to index 1, the track already playing.
func TestNextIndexIgnoresTheBrowseCursor(t *testing.T) {
	cases := []struct {
		name   string
		cursor int
	}{
		{"cursor reset to 0 by a tab switch", 0},
		{"cursor parked on the playing track", 1},
		{"cursor moved ahead by browsing", 2},
		{"cursor left out of range by an overlay", 99},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newQueueModel()
			m.cursor = c.cursor
			if got := m.nextIndex(); got != 2 {
				t.Errorf("nextIndex() = %d with cursor at %d, want 2 (the track after the playing one)", got, c.cursor)
			}
			if got := m.prevIndex(); got != 0 {
				t.Errorf("prevIndex() = %d with cursor at %d, want 0", got, c.cursor)
			}
		})
	}
}

// With nothing playing, next/previous still have to start somewhere — the
// selection is the only sensible base (e.g. a freshly restored session).
func TestAdvanceFallsBackToCursorWhenNothingIsPlaying(t *testing.T) {
	m := newSmokeModel()
	m.playingIndex = -1
	m.cursor = 1
	if got := m.nextIndex(); got != 2 {
		t.Errorf("nextIndex() = %d, want 2", got)
	}
}

func TestNextIndexStopsAtTheEndOfTheQueue(t *testing.T) {
	m := newQueueModel()
	m.playingIndex = len(m.tracks) - 1
	m.cursor = 0 // stale, must not resurrect a "next"
	if got := m.nextIndex(); got != -1 {
		t.Errorf("nextIndex() = %d on the last track, want -1", got)
	}
}

// Removing a track above the playing one shifts it down; the recorded position
// has to move with it or the next track is picked from the old numbering.
func TestRemoveFromQueueKeepsPlayingIndex(t *testing.T) {
	m := newQueueModel() // playing index 1 of [1,2,3]

	m.removeFromQueue(0)

	if m.playingIndex != 0 {
		t.Fatalf("playingIndex = %d after removing the track above, want 0", m.playingIndex)
	}
	if m.tracks[m.playingIndex].ID != m.currentTrack.ID {
		t.Errorf("playingIndex points at %q, want the playing track %q",
			m.tracks[m.playingIndex].Title, m.currentTrack.Title)
	}
	if got := m.nextIndex(); got != 1 {
		t.Errorf("nextIndex() = %d, want 1", got)
	}
}

func TestRemoveFromQueueBelowPlayingLeavesIndexAlone(t *testing.T) {
	m := newQueueModel()
	m.removeFromQueue(2)
	if m.playingIndex != 1 {
		t.Errorf("playingIndex = %d after removing a later track, want 1", m.playingIndex)
	}
}

// Removing the playing track stops playback, so there is no playing position
// left to record.
func TestRemovePlayingTrackClearsPlayingIndex(t *testing.T) {
	m := newQueueModel()
	m.removeFromQueue(1)
	if m.playingIndex != -1 {
		t.Errorf("playingIndex = %d after removing the playing track, want -1", m.playingIndex)
	}
}

func TestMoveQueueItemFollowsPlayingTrack(t *testing.T) {
	m := newQueueModel() // playing index 1

	m.moveQueueItem(1, -1) // move it up

	if m.playingIndex != 0 {
		t.Fatalf("playingIndex = %d after moving the playing track up, want 0", m.playingIndex)
	}
	if m.tracks[m.playingIndex].ID != m.currentTrack.ID {
		t.Errorf("playingIndex no longer points at the playing track")
	}
}

func TestMoveQueueItemIntoPlayingSlot(t *testing.T) {
	m := newQueueModel() // playing index 1

	m.moveQueueItem(0, 1) // swaps rows 0 and 1, pushing the playing track to 0

	if m.playingIndex != 0 {
		t.Errorf("playingIndex = %d, want 0", m.playingIndex)
	}
}

// applyShuffle rebuilds m.tracks, so the recorded position must be re-derived
// rather than left pointing into the old ordering.
func TestApplyShuffleResyncsPlayingIndex(t *testing.T) {
	m := newQueueModel()
	m.shuffleMode = ShuffleFisherYates

	m.applyShuffle()

	if m.playingIndex < 0 || m.playingIndex >= len(m.tracks) {
		t.Fatalf("playingIndex = %d after a reshuffle, out of range", m.playingIndex)
	}
	if m.tracks[m.playingIndex].ID != m.currentTrack.ID {
		t.Errorf("playingIndex points at %q, want the playing track %q",
			m.tracks[m.playingIndex].Title, m.currentTrack.Title)
	}
}

func TestClearQueueClearsPlayingIndex(t *testing.T) {
	m := newQueueModel()
	m.clearQueue()
	if m.playingIndex != -1 {
		t.Errorf("playingIndex = %d after clearing the queue, want -1", m.playingIndex)
	}
}

// Off the Queue tab there is no playing track to home in on, so the plain
// zeroing behaviour has to stay.
func TestResetSectionCursorOnlySnapsOnTheQueue(t *testing.T) {
	m := newQueueModel()
	m.section = SecFavSongs
	m.cursor = 5
	m.resetSectionCursor()
	if m.cursor != 0 {
		t.Errorf("cursor = %d on a non-Queue tab, want 0", m.cursor)
	}
}
