package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// confirmDeletePlaylist raises the delete confirmation for pl. Nothing is sent
// to Tidal until the user answers — deletion there is permanent, with no
// undo and no trash to restore from, so it is the one destructive action in
// the app that does not act on a single keystroke.
func (m *Model) confirmDeletePlaylist(pl tidal.Playlist) {
	target := pl
	m.deleteTarget = &target
	m.overlay = OverlayDeletePlaylist
}

// updateDeletePlaylist drives the confirmation: only "y" (or "Y") goes ahead,
// everything else cancels. There is deliberately no cursor to move onto a
// "Yes" button and no Enter-to-confirm — a stray Enter left over from the
// list behind the prompt must not delete a playlist.
func (m Model) updateDeletePlaylist(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	target := m.deleteTarget
	m.overlay = OverlayNone
	m.deleteTarget = nil
	if target == nil {
		return m, nil
	}
	switch k.String() {
	case "y", "Y":
		cmd := m.deletePlaylistCmd(*target)
		return m, cmd
	default:
		return m, nil
	}
}

// deletePlaylistCmd deletes the playlist on Tidal and reports back so the
// local list can drop it.
func (m *Model) deletePlaylistCmd(pl tidal.Playlist) tea.Cmd {
	uuid := pl.UUID
	name := pl.Title
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		if err := client.DeletePlaylist(ctx, uuid); err != nil {
			return errMsg(err)
		}
		return playlistDeletedMsg{uuid: uuid, name: name}
	}
}

// applyPlaylistDeleted drops a deleted playlist from every piece of state that
// still points at it: the list itself, the cursor, the open detail view, and
// the live queue's origin tag — a queue still claiming to be "synced" with a
// playlist that no longer exists would offer to sync back to nothing.
func (m *Model) applyPlaylistDeleted(uuid string) {
	kept := m.playlists[:0]
	for _, pl := range m.playlists {
		if pl.UUID != uuid {
			kept = append(kept, pl)
		}
	}
	m.playlists = kept
	m.cursor = min(m.cursor, max(len(m.playlists)-1, 0))

	if m.openPlaylist != nil && m.openPlaylist.UUID == uuid {
		m.openPlaylist = nil
		m.detailTracks = nil
		m.detailCursor = 0
		m.detailFocus = false
	}
	if m.queuePlaylistUUID == uuid {
		m.queuePlaylistUUID = ""
		m.queueSource = ""
		m.queueDirty = false
	}
	m.markPlaylistsStale()
}

// renderDeletePlaylist renders the confirmation popup.
func (m *Model) renderDeletePlaylist(t Theme) string {
	if m.deleteTarget == nil {
		return ""
	}
	w := min(max(m.width/2, 38), 60)
	innerW := w - 2
	rows := []string{
		" " + t.Row.Render("Delete playlist"),
		" " + t.RowPlaying.Render(truncateStr(m.deleteTarget.Title, innerW-1)),
		"",
		" " + t.Amber.Render("This cannot be undone."),
		"",
		t.RowDim.Render(" y to delete · any other key cancels"),
	}
	body := strings.Join(rows, "\n")
	return renderPanel(t, "DELETE PLAYLIST", true, w, len(rows)+2, body)
}
