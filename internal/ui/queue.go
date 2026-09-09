package ui

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// enqueueEnd appends a track to the end of the live queue and marks it edited.
func (m *Model) enqueueEnd(t tidal.Track) {
	m.tracksOrder = append(m.tracksOrder, t)
	m.tracks = append(m.tracks, t)
	m.queueDirty = true
	_ = m.store.SavePlaylist(m.tracks)
}

// enqueueNext inserts a track immediately after the current cursor position so
// it plays next, marking the queue edited.
func (m *Model) enqueueNext(t tidal.Track) {
	pos := min(m.cursor+1, len(m.tracks))
	m.tracks = insertTrack(m.tracks, pos, t)
	m.tracksOrder = append(m.tracksOrder, t)
	m.queueDirty = true
	_ = m.store.SavePlaylist(m.tracks)
}

// playListIntoQueue loads a list of tracks into the live queue (as an ad-hoc,
// unsaved queue) and plays from index i. Used by sections that play through a
// list — Favorite Songs, Recently Played, etc.
func (m *Model) playListIntoQueue(list []tidal.Track, i int) tea.Cmd {
	if i < 0 || i >= len(list) {
		return nil
	}
	m.tracksOrder = append([]tidal.Track(nil), list...)
	m.shuffleMode = ShuffleOff
	m.applyShuffle()
	m.queueSource = ""
	m.queuePlaylistUUID = ""
	m.queueDirty = false
	m.cursor = i
	_ = m.store.SavePlaylist(m.tracks)
	track := m.tracks[i]
	// doPlayTrack reads the cursor to record playingIndex; it is correct here.
	_ = m.store.CacheTrack(track.ID, track)
	return m.playTrackCmd(track)
}

// stopCurrentTrack halts actual playback (not just pausing) and clears the
// now-playing state — used whenever the queue ends up with nothing left in
// it, so the Lyrics/AlbumArt panes stop showing a track that's no longer
// queued. A no-op in client mode (the daemon owns the player) or when
// nothing is set as the current track.
func (m *Model) stopCurrentTrack() {
	if m.clientMode || m.currentTrack == nil {
		return
	}
	if m.player != nil && !m.player.IsPaused() {
		_ = m.player.Pause()
	}
	m.currentTrack = nil
	m.playingIndex = -1
	m.isPlaying = false
	m.stopped = false
	m.currPos = 0
	m.duration = 0
	m.currentQuality = ""
	// coverTrack()/renderAlbumArtPane's Kitty/Sixel path already stop
	// showing anything once currentTrack is nil (coverBoxRect gates on it),
	// but the ASCII/Unicode fallback path renders m.coverImage directly
	// regardless — and renderLyricsPane renders m.lyricsState directly,
	// same story. Neither is otherwise tied to currentTrack, so both need
	// clearing here explicitly or they'd keep showing the just-stopped
	// track's cover/lyrics.
	m.coverImage = nil
	m.coverCacheKey = ""
	m.lyricsState = lyricsState{}
	// Same reasoning as the cover/lyrics reset above: renderCavaBars/
	// renderPeakBars draw whatever's in these fields directly, and without
	// this they'd otherwise keep showing the last frame's bar heights frozen
	// on screen (isPlaying=false stops the tick that would refresh them, but
	// doesn't clear what's already there) rather than falling back to their
	// "nothing playing" placeholder.
	//
	// Clearing m.cavaBars/peakBars alone isn't enough on its own: a
	// barTickMsg already in flight when this runs fires right after and
	// re-populates them from Cava/PeakMeter's own internal decaying state,
	// which nothing has told to reset — overwriting the nil with a
	// still-decaying (not yet zero) value, which then freezes there since
	// isPlaying=false stops the tick loop from running again. Stop()ping
	// both zeroes that internal state too (and, for Cava, tears down the
	// now-pointless subprocess), so that in-flight tick reads back zeros.
	if m.cava != nil {
		m.cava.Stop()
	}
	if m.peak != nil {
		m.peak.Stop()
	}
	m.cavaBars = nil
	m.peakBars = nil
	m.pushState()
}

// clearQueue empties the live queue and, if a track was actively playing,
// stops it too (see stopCurrentTrack).
func (m *Model) clearQueue() {
	m.tracks = nil
	m.tracksOrder = nil
	m.shufflePlayed = nil
	m.cursor = 0
	m.playingIndex = -1
	m.queueSource = ""
	m.queuePlaylistUUID = ""
	m.queueDirty = false
	_ = m.store.SavePlaylist(m.tracks)
	m.stopCurrentTrack()
}

// removeFromQueue drops the track at index i from the live queue, marks the
// queue edited, and keeps the cursor in range (landing on whatever shifted
// into i, or the previous track if i was the last one — both already fall
// out of the plain index-clamp below). The currently-playing audio is
// otherwise unaffected (it is already buffered) — unless the removed track
// was the one actually playing, in which case it's stopped too (see
// stopCurrentTrack), whether or not the queue is now empty: playback
// doesn't just carry on for a track no longer in the queue. The cursor's
// new position (if any) still gets an artwork preview as normal — Queue's
// existing hover-preview convention (coverTrack/hoveredTrack) already
// handles that with no extra code needed here.
func (m *Model) removeFromQueue(i int) {
	if i < 0 || i >= len(m.tracks) {
		return
	}
	removed := m.tracks[i]
	m.tracks = append(m.tracks[:i], m.tracks[i+1:]...)

	// Mirror the removal in the unshuffled order so a later re-shuffle is
	// consistent (remove the first matching ID).
	for j := range m.tracksOrder {
		if m.tracksOrder[j].ID == removed.ID {
			m.tracksOrder = append(m.tracksOrder[:j], m.tracksOrder[j+1:]...)
			break
		}
	}

	if m.cursor >= len(m.tracks) {
		m.cursor = max(len(m.tracks)-1, 0)
	}
	// Everything after i shifted down one, so the recorded playing position
	// has to move with it or the next track would be picked from the old
	// numbering. Removing the playing track itself is handled by
	// stopCurrentTrack below, which clears the index outright.
	if m.playingIndex > i {
		m.playingIndex--
	}
	removedWasPlaying := m.currentTrack != nil && m.currentTrack.ID == removed.ID
	m.queueDirty = true
	_ = m.store.SavePlaylist(m.tracks)
	if len(m.tracks) == 0 || removedWasPlaying {
		m.stopCurrentTrack()
	}
}

// moveQueueItem swaps the track at index i with its neighbor at i+delta
// (delta=-1 for rmpc's MoveUp, +1 for MoveDown), keeping the cursor on the
// moved track. Mirrors the swap into tracksOrder by ID, the same
// best-effort approach removeFromQueue already uses for duplicate IDs.
func (m *Model) moveQueueItem(i, delta int) {
	j := i + delta
	if i < 0 || j < 0 || i >= len(m.tracks) || j >= len(m.tracks) {
		return
	}
	m.tracks[i], m.tracks[j] = m.tracks[j], m.tracks[i]

	oi, oj := -1, -1
	for k := range m.tracksOrder {
		if oi < 0 && m.tracksOrder[k].ID == m.tracks[i].ID {
			oi = k
		}
		if oj < 0 && m.tracksOrder[k].ID == m.tracks[j].ID {
			oj = k
		}
	}
	if oi >= 0 && oj >= 0 {
		m.tracksOrder[oi], m.tracksOrder[oj] = m.tracksOrder[oj], m.tracksOrder[oi]
	}

	m.cursor = j
	// The two rows swapped, so follow the playing track into its new slot.
	switch m.playingIndex {
	case i:
		m.playingIndex = j
	case j:
		m.playingIndex = i
	}
	m.queueDirty = true
	_ = m.store.SavePlaylist(m.tracks)
}

// reshuffleQueue performs a one-shot Fisher-Yates reshuffle of the live queue
// order (rmpc's queue Shuffle), independent of the persistent shuffle toggle.
func (m *Model) reshuffleQueue() {
	if len(m.tracks) < 2 {
		return
	}
	current := m.tracks[m.cursor]
	rand.Shuffle(len(m.tracks), func(i, j int) { m.tracks[i], m.tracks[j] = m.tracks[j], m.tracks[i] })
	for i := range m.tracks {
		if m.tracks[i].ID == current.ID {
			m.cursor = i
			break
		}
	}
	m.syncPlayingIndex()
	m.queueDirty = true
	_ = m.store.SavePlaylist(m.tracks)
}

// loadQueueFromPlaylist replaces the live queue with a playlist's tracks and
// records its origin so the hybrid header can show the synced/edited state.
func (m *Model) loadQueueFromPlaylist(tracks []tidal.Track, pl tidal.Playlist) {
	m.tracksOrder = tracks
	m.shuffleMode = ShuffleOff
	m.applyShuffle()
	m.queueSource = "playlist:" + pl.Title
	m.queuePlaylistUUID = pl.UUID
	m.queueDirty = false
	_ = m.store.SavePlaylist(m.tracks)
}

// queueTotalDuration sums the (server-reported, not live-measured) durations
// of every track currently in the queue, in seconds.
func (m *Model) queueTotalDuration() float64 {
	var total float64
	for i := range m.tracks {
		total += float64(m.tracks[i].Duration)
	}
	return total
}

// formatDuration renders seconds as H:MM:SS once it runs an hour or longer,
// or M:SS otherwise (formatTime's own range) — unlike a single track, a
// queue total routinely runs well past an hour.
func formatDuration(seconds float64) string {
	total := int(seconds)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// queueHeader returns the queue panel's title — track count and total
// duration, plus an optional colored status suffix reflecting the hybrid
// model's state.
func (m *Model) queueHeader(t Theme) string {
	meta := fmt.Sprintf("%d tracks · %s", len(m.tracks), formatDuration(m.queueTotalDuration()))
	name, isPlaylist := strings.CutPrefix(m.queueSource, "playlist:")
	switch {
	case isPlaylist && m.queueDirty:
		return "QUEUE · " + name + " · " + meta + " " + t.Amber.Render("· edited — : save")
	case isPlaylist:
		return "QUEUE · " + name + " · " + meta + " " + t.GreenT.Render("· synced")
	case m.queueSource == "radio":
		return "QUEUE · " + meta + " " + t.Amber.Render("· radio · unsaved — : save")
	default:
		return "QUEUE · " + meta
	}
}

// saveQueueAsNew saves the current queue as a brand-new playlist, asking for
// its name first — Ctrl+S, and the command palette's "Save queue as
// playlist…". The prompt's placeholder is suggestedQueueName(), so accepting
// the suggestion is still one keystroke, but the name is no longer chosen
// silently on the user's behalf. Enter in the prompt runs saveQueueCmd, which
// retags the queue as backed by the new playlist.
func (m Model) saveQueueAsNew() (tea.Model, tea.Cmd) {
	if len(m.tracks) == 0 {
		m.errText = "Queue is empty — nothing to save"
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
	}
	// nil marks the whole-queue flow, as opposed to the per-track "Add to
	// playlist…" one — see updateNewPlaylistName.
	m.addToPlaylistTracks = nil
	m.openNewPlaylistPrompt(OverlayNone)
	return m, textinput.Blink
}

// suggestedQueueName proposes a playlist name from the queue's origin or the
// currently playing track.
func (m *Model) suggestedQueueName() string {
	if name, ok := strings.CutPrefix(m.queueSource, "playlist:"); ok && name != "" {
		return name
	}
	if m.currentTrack != nil {
		return m.currentTrack.Title + " radio"
	}
	if len(m.tracks) > 0 {
		return m.tracks[0].Title + " mix"
	}
	return "gotidal queue"
}

// saveQueueCmd creates a new playlist named `name` and adds every queue track.
func (m *Model) saveQueueCmd(name string) tea.Cmd {
	tracks := append([]tidal.Track(nil), m.tracks...)
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		uuid, err := client.CreatePlaylist(ctx, name, "Saved from gotidal")
		if err != nil {
			return errMsg(err)
		}
		ids := make([]int, len(tracks))
		for i := range tracks {
			ids[i] = tracks[i].ID
		}
		if err := client.AddTracksToPlaylist(ctx, uuid, ids); err != nil {
			return errMsg(err)
		}
		return queueSavedMsg{uuid: uuid, name: name, count: len(ids), created: true}
	}
}

// saveQueueToExisting appends the queue to an already-saved playlist.
func (m *Model) saveQueueToExistingCmd(uuid, name string) tea.Cmd {
	tracks := append([]tidal.Track(nil), m.tracks...)
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		ids := make([]int, len(tracks))
		for i := range tracks {
			ids[i] = tracks[i].ID
		}
		if err := client.AddTracksToPlaylist(ctx, uuid, ids); err != nil {
			return errMsg(err)
		}
		return queueSavedMsg{uuid: uuid, name: name, count: len(ids)}
	}
}

// toastCmd flashes a green confirmation and schedules its clearing.
func toastClearCmd() tea.Cmd {
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return clearToastMsg{} })
}

// insertTrack returns s with t inserted at index i.
func insertTrack(s []tidal.Track, i int, t tidal.Track) []tidal.Track {
	s = append(s, tidal.Track{})
	copy(s[i+1:], s[i:])
	s[i] = t
	return s
}
