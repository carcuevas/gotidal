package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// beginAddTrackToPlaylist opens the Add to Playlist picker for a single
// track — the action sheet's "Add to playlist…" entry, previously a no-op
// placeholder. Loads the user's playlists first if they aren't already
// cached, exactly like beginSaveToExisting for the whole queue.
func (m Model) beginAddTrackToPlaylist(track tidal.Track) (tea.Model, tea.Cmd) {
	m.overlay = OverlayAddToPlaylist
	m.cursor = 0
	m.addToPlaylistTracks = []tidal.Track{track}
	if len(m.playlists) == 0 || m.playlistsStale {
		cmd := m.reloadPlaylistsCmd()
		return m, cmd
	}
	return m, nil
}

// openNewPlaylistPrompt raises the name-entry overlay: the "+ Create New
// Playlist…" row shared by the whole-queue and per-track Add to Playlist
// flows, and Ctrl+S's direct save-the-queue path. back is the overlay Esc
// returns to — the picker for the former, OverlayNone for the latter.
//
// The suggested name is a placeholder, not pre-filled text, so it is never
// submitted by accident — the user has to actually type something or
// explicitly accept the suggestion by pressing Enter on an empty field.
func (m *Model) openNewPlaylistPrompt(back Overlay) {
	ti := textinput.New()
	ti.Placeholder = m.suggestedNewPlaylistName()
	ti.Prompt = ""
	ti.CharLimit = 100
	ti.Focus()
	m.newPlaylistInput = ti
	m.newPlaylistReturn = back
	m.overlay = OverlayNewPlaylistName
}

// suggestedNewPlaylistName proposes a starting point for the name prompt.
func (m *Model) suggestedNewPlaylistName() string {
	switch len(m.addToPlaylistTracks) {
	case 0:
		return m.suggestedQueueName()
	case 1:
		return m.addToPlaylistTracks[0].Title
	default:
		return m.addToPlaylistTracks[0].Title + " and more"
	}
}

// updateNewPlaylistName drives the name-entry overlay: typing, Esc to cancel
// back to the picker, Enter to create the playlist and add whichever tracks
// this flow was opened for.
func (m Model) updateNewPlaylistName(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case keyEsc:
		m.overlay = m.newPlaylistReturn
		if m.overlay == OverlayNone {
			m.addToPlaylistTracks = nil
		}
		return m, nil
	case keyEnter:
		name := strings.TrimSpace(m.newPlaylistInput.Value())
		if name == "" {
			name = strings.TrimSpace(m.newPlaylistInput.Placeholder)
		}
		m.overlay = OverlayNone
		if name == "" {
			return m, nil
		}
		if m.addToPlaylistTracks != nil {
			tracks := m.addToPlaylistTracks
			m.addToPlaylistTracks = nil
			cmd := m.createPlaylistWithTracksCmd(name, tracks)
			return m, cmd
		}
		// No per-track set — this is the whole-queue flow, which keeps its
		// existing behaviour: creating from m.tracks retags the queue as
		// backed by the new playlist (queueSavedMsg), unlike the per-track
		// path above.
		cmd := m.saveQueueCmd(name)
		return m, cmd
	}
	var cmd tea.Cmd
	m.newPlaylistInput, cmd = m.newPlaylistInput.Update(k)
	return m, cmd
}

// renderNewPlaylistName renders the small name-entry popup.
func (m *Model) renderNewPlaylistName(t Theme) string {
	w := min(max(m.width/2, 34), 56)
	back := " Enter to create · Esc to go back"
	if m.newPlaylistReturn == OverlayNone {
		back = " Enter to create · Esc to cancel"
	}
	rows := []string{
		" " + t.KeyBarKey.Render("♫ ") + m.newPlaylistInput.View(),
		"",
		t.RowDim.Render(back),
	}
	body := strings.Join(rows, "\n")
	return renderPanel(t, "NEW PLAYLIST", true, w, len(rows)+2, body)
}

// addTracksToExistingPlaylistCmd appends tracks to an already-saved playlist
// and reports success with a plain toast — this must not touch queue-source
// state (queueSavedMsg does that, and is reserved for the whole-live-queue
// flow): adding one track to some playlist has nothing to do with whatever
// the live queue happens to be right now.
func (m *Model) addTracksToExistingPlaylistCmd(uuid, name string, tracks []tidal.Track) tea.Cmd {
	tracksCopy := append([]tidal.Track(nil), tracks...)
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		ids := make([]int, len(tracksCopy))
		for i := range tracksCopy {
			ids[i] = tracksCopy[i].ID
		}
		if err := client.AddTracksToPlaylist(ctx, uuid, ids); err != nil {
			return errMsg(err)
		}
		return playlistTracksAddedMsg{uuid: uuid, name: name, count: len(ids)}
	}
}

// createPlaylistWithTracksCmd creates a brand-new playlist named name and
// adds tracks to it, reporting success with a plain toast — the per-track
// "+ Create New Playlist…" counterpart of saveQueueCmd, which does the same
// thing for the whole live queue but reports via queueSavedMsg instead (see
// addTracksToExistingPlaylistCmd for why the two must stay separate).
func (m *Model) createPlaylistWithTracksCmd(name string, tracks []tidal.Track) tea.Cmd {
	tracksCopy := append([]tidal.Track(nil), tracks...)
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		uuid, err := client.CreatePlaylist(ctx, name, "Saved from gotidal")
		if err != nil {
			return errMsg(err)
		}
		ids := make([]int, len(tracksCopy))
		for i := range tracksCopy {
			ids[i] = tracksCopy[i].ID
		}
		if err := client.AddTracksToPlaylist(ctx, uuid, ids); err != nil {
			return errMsg(err)
		}
		return playlistTracksAddedMsg{uuid: uuid, name: name, count: len(ids), created: true}
	}
}
