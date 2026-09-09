package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carcuevas/gotidal/internal/player"
	"github.com/carcuevas/gotidal/internal/tidal"
)

// openActionSheet raises the contextual action sheet for a track.
func (m *Model) openActionSheet(t tidal.Track) {
	m.sheetTrack = &t
	m.sheetCursor = 0
	m.overlay = OverlayActionSheet
}

// updateOverlay routes keys while a modal overlay is active.
func (m Model) updateOverlay(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.overlay {
	case OverlayDeviceSelect:
		return m.updateDeviceSelect(k)
	case OverlayActionSheet:
		return m.updateActionSheet(k)
	case OverlayCommandPalette:
		return m.updateCommandPalette(k)
	case OverlayAddToPlaylist:
		return m.updateAddToPlaylist(k)
	case OverlayNewPlaylistName:
		return m.updateNewPlaylistName(k)
	case OverlayDeletePlaylist:
		return m.updateDeletePlaylist(k)
	case OverlayImportSpotify:
		return m.updateImportSpotify(k)
	case OverlayThemePicker:
		return m.updateThemePickerOverlay(k)
	default: // OverlayHelp, OverlaySongInfo: Esc/any key closes
		m.overlay = OverlayNone
		return m, nil
	}
}

// renderHelpOverlay renders a static reference of the global keybindings
// (rmpc's "?" / ShowHelp).
func (m *Model) renderHelpOverlay(t Theme) string {
	rows := [][2]string{
		{"1-9", "Switch tab"},
		{"Tab / gt", "Next tab"},
		{"Shift+Tab / gT", "Previous tab"},
		{"j/k, ↑/↓", "Move"},
		{"gg / G", "Top / bottom"},
		{"Ctrl+u/d", "Half page up/down"},
		{"Ctrl+b/f", "Page up/down"},
		{"Enter", "Play / open / confirm"},
		{"p", "Play / pause"},
		{"s", "Stop"},
		{"f / b", "Seek forward / back"},
		{"> / <", "Next / previous track"},
		{". / ,", "Volume up / down"},
		{"x / X", "Toggle shuffle / reshuffle queue"},
		{"a / A", "Add to queue / add all"},
		{"d / D", "Remove from queue / clear queue (Queue tab)"},
		{"d", "Delete playlist (Playlists tab, asks first)"},
		{"Ctrl+S", "Save the queue as a new playlist"},
		{"K / J", "Move queue item up / down"},
		{"F", "Toggle favorite"},
		{"r", "Start radio from selection"},
		{"y", "Copy Tidal link"},
		{"oo", "Select output device"},
		{"oI", "Current song info"},
		{"Ctrl+X", "Actions menu"},
		{"9, then t", "Cycle theme (Settings tab)"},
		{"v", "Toggle meter: Peak / Cava spectrum"},
		{": / Ctrl+P", "Command palette"},
		{"/", "Search"},
		{"q / Ctrl+C", "Quit"},
	}
	w := min(max(m.width-10, 40), 60)
	innerW := w - 2
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		key := t.KeyBarKey.Render(r[0])
		pad := max(innerW-lipgloss.Width(r[0])-lipgloss.Width(r[1])-1, 1)
		lines = append(lines, key+strings.Repeat(" ", pad)+t.RowDim.Render(r[1]))
	}
	h := min(len(lines)+2, m.height-2)
	return renderPanel(t, "KEYBINDINGS", true, w, max(h, 4), strings.Join(lines, "\n"))
}

// renderSongInfoOverlay renders track/album/quality details for the current
// track (rmpc's oI / ShowCurrentSongInfo).
func (m *Model) renderSongInfoOverlay(t Theme) string {
	tr := m.currentTrack
	if tr == nil {
		return ""
	}
	w := min(max(m.width*2/3, 40), 64)
	innerW := w - 2
	rows := [][2]string{
		{"Title", tr.Title},
		{"Artist", tr.Artist.Name},
		{"Album", tr.Album.Title},
		{"Duration", formatTime(float64(tr.Duration))},
		{"Quality", string(m.currentQuality)},
		{"Device", m.activeDevice},
	}
	if !m.bitPerfect {
		rows = append(rows, [2]string{"Bit-perfect", "no (plughw: fallback)"})
	} else {
		rows = append(rows, [2]string{"Bit-perfect", "yes"})
	}
	var lines []string
	for _, r := range rows {
		label := t.RowDim.Render(r[0] + ":")
		lines = append(lines, label+" "+truncateStr(t.Row.Render(r[1]), innerW-lipgloss.Width(r[0])-2))
	}
	h := len(lines) + 2
	return renderPanel(t, "SONG INFO", true, w, h, strings.Join(lines, "\n"))
}

// updateAddToPlaylist handles the "save queue to existing playlist" picker.
// updateAddToPlaylist drives the existing-playlist picker. Row 0 is always
// "+ Create New Playlist…"; rows 1..N are m.playlists[0..N-1]. Which tracks
// end up added — and how success is reported — depends on
// m.addToPlaylistTracks: see its field doc.
func (m Model) updateAddToPlaylist(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case keyEsc:
		m.overlay = OverlayNone
		m.addToPlaylistTracks = nil
		return m, nil
	case keyUp, "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case keyDown, "j":
		if m.cursor < len(m.playlists) {
			m.cursor++
		}
		return m, nil
	case keyEnter:
		if m.cursor == 0 {
			m.openNewPlaylistPrompt(OverlayAddToPlaylist)
			return m, nil
		}
		idx := m.cursor - 1
		if idx < 0 || idx >= len(m.playlists) {
			m.overlay = OverlayNone
			return m, nil
		}
		pl := m.playlists[idx]
		m.overlay = OverlayNone
		if m.addToPlaylistTracks != nil {
			tracks := m.addToPlaylistTracks
			m.addToPlaylistTracks = nil
			cmd := m.addTracksToExistingPlaylistCmd(pl.UUID, pl.Title, tracks)
			return m, cmd
		}
		cmd := m.saveQueueToExistingCmd(pl.UUID, pl.Title)
		return m, cmd
	}
	return m, nil
}

// renderAddToPlaylist renders the playlist picker popup: a "+ Create New
// Playlist…" row followed by the user's existing playlists.
func (m *Model) renderAddToPlaylist(t Theme) string {
	w := min(max(m.width/2, 34), 56)
	innerW := w - 2
	newRow := " + Create New Playlist…"
	var rows []string
	if m.cursor == 0 {
		rows = append(rows, t.CmdItemSel.Width(innerW).Render(stripANSI(newRow)))
	} else {
		rows = append(rows, t.KeyBarKey.Render(truncateStr(newRow, innerW)))
	}
	for i := range m.playlists {
		pl := m.playlists[i]
		label := fmt.Sprintf(" ≡  %s  %s", pl.Title, t.RowFaint.Render(fmt.Sprintf("%d tracks", pl.NumberOfTracks)))
		if i+1 == m.cursor {
			rows = append(rows, t.CmdItemSel.Width(innerW).Render(stripANSI(fmt.Sprintf(" ≡  %s  %d tracks", pl.Title, pl.NumberOfTracks))))
		} else {
			rows = append(rows, truncateStr(label, innerW))
		}
	}
	h := min(len(rows)+2, m.height-4)
	body := strings.Join(rows, "\n")
	return renderPanel(t, "ADD TO PLAYLIST", true, w, max(h, 4), body)
}

// sheetAction is one row in the contextual action sheet.
type sheetAction struct {
	icon  string
	label string
	hint  string // trailing hotkey hint
	group string // non-empty marks a group header above this action
	id    actionID
}

type actionID int

const (
	actPlayNow actionID = iota
	actPlayNext
	actAddQueue
	actRemoveQueue
	actAddPlaylist
	actRadio
	actGoArtist
	actGoAlbum
	actFavorite
	actFavoriteArtist
	actFavoriteAlbum
	actCopyLink
)

// actionSheetItems returns the action list for the sheet's current track,
// adapting the favorite label to the track's current state.
func (m *Model) actionSheetItems() []sheetAction {
	favLabel := "Favorite"
	if m.sheetTrack != nil && m.favorites[m.sheetTrack.ID] {
		favLabel = "Unfavorite"
	}
	artist := ""
	album := ""
	if m.sheetTrack != nil {
		artist = m.sheetTrack.Artist.Name
		album = m.sheetTrack.Album.Title
	}
	items := []sheetAction{
		{icon: "▸", label: "Play now", id: actPlayNow},
		{icon: "⏭", label: "Play next", hint: "n", id: actPlayNext},
		{icon: "＋", label: "Add to queue", hint: "e", id: actAddQueue},
	}
	// "Remove from queue" only makes sense while browsing the Queue.
	if m.section == SecQueue {
		items = append(items, sheetAction{icon: "✕", label: "Remove from queue", hint: "x", id: actRemoveQueue})
	}
	items = append(items,
		sheetAction{icon: "≡", label: "Add to playlist…", hint: "▸", id: actAddPlaylist},
		sheetAction{icon: "∿", label: "Start radio from this", hint: "r", id: actRadio},
		sheetAction{icon: "♫", label: "Artist · " + artist, hint: "a", group: "GO TO", id: actGoArtist},
		sheetAction{icon: "⊞", label: "Album · " + album, hint: "A", id: actGoAlbum},
		sheetAction{icon: "♥", label: favLabel, hint: "f", group: "MORE", id: actFavorite},
		sheetAction{icon: "♥", label: "Favorite artist · " + artist, id: actFavoriteArtist},
		sheetAction{icon: "♥", label: "Favorite album · " + album, id: actFavoriteAlbum},
		sheetAction{icon: "⎘", label: "Copy Tidal link", hint: "c", id: actCopyLink},
	)
	return items
}

// updateActionSheet handles navigation and selection within the action sheet.
func (m Model) updateActionSheet(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.actionSheetItems()
	switch k.String() {
	case keyEsc, "o":
		m.overlay = OverlayNone
		return m, nil
	case keyUp, "k":
		if m.sheetCursor > 0 {
			m.sheetCursor--
		}
		return m, nil
	case keyDown, "j":
		if m.sheetCursor < len(items)-1 {
			m.sheetCursor++
		}
		return m, nil
	case keyEnter:
		return m.runSheetAction(items[m.sheetCursor].id)
	case "n":
		return m.runSheetAction(actPlayNext)
	case "e":
		return m.runSheetAction(actAddQueue)
	case "r":
		return m.runSheetAction(actRadio)
	case "a":
		return m.runSheetAction(actGoArtist)
	case "A":
		return m.runSheetAction(actGoAlbum)
	case "f":
		return m.runSheetAction(actFavorite)
	case "c":
		return m.runSheetAction(actCopyLink)
	case "x":
		return m.runSheetAction(actRemoveQueue)
	}
	return m, nil
}

// runSheetAction executes a chosen action on the sheet's track and closes the
// sheet.
func (m Model) runSheetAction(id actionID) (tea.Model, tea.Cmd) {
	if m.sheetTrack == nil {
		m.overlay = OverlayNone
		return m, nil
	}
	track := *m.sheetTrack
	m.overlay = OverlayNone

	switch id {
	case actPlayNow:
		_ = m.store.CacheTrack(track.ID, track)
		cmd := m.playTrackCmd(track)
		return m, cmd
	case actPlayNext:
		m.enqueueNext(track)
		return m, nil
	case actAddQueue:
		m.enqueueEnd(track)
		return m, nil
	case actRemoveQueue:
		if m.section == SecQueue {
			m.removeFromQueue(m.cursor)
		}
		return m, nil
	case actAddPlaylist:
		return m.beginAddTrackToPlaylist(track)
	case actRadio:
		cmd := m.radioFrom(track)
		return m, cmd
	case actGoArtist:
		return m.openArtistFor(&track)
	case actGoAlbum:
		cmd := m.openAlbum(track.Album.ID)
		return m, cmd
	case actFavorite:
		cmd := m.toggleFavorite(track)
		return m, cmd
	case actFavoriteArtist:
		if track.Artist.ID == 0 {
			return m, nil
		}
		cmd := m.addFavoriteArtistCmd(track.Artist)
		return m, cmd
	case actFavoriteAlbum:
		if track.Album.ID == 0 {
			return m, nil
		}
		cmd := m.addFavoriteAlbumCmd(tidal.Album{ID: track.Album.ID, Title: track.Album.Title})
		return m, cmd
	case actCopyLink:
		return m.copyTrackLink(track)
	}
	return m, nil
}

// updateDeviceSelect handles the device-picker overlay.
func (m Model) updateDeviceSelect(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.overlay = OverlayNone
		// This overlay borrows the shared cursor for the device list, so
		// closing it has to hand the cursor back rather than zero it — on the
		// Queue that means the playing track (see resetSectionCursor).
		m.resetSectionCursor()
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.devices)-1 {
			m.cursor++
		}
	case keyEnter:
		if len(m.devices) == 0 {
			m.overlay = OverlayNone
			return m, nil
		}
		chosen := m.devices[m.cursor]
		m.currentDevice = chosen.HWName
		m.overlay = OverlayNone
		m.resetSectionCursor()
		if !m.bitPerfectMode {
			// A PipeWire sink switch is a local system-audio change, not a
			// player command — it applies immediately regardless of client
			// mode, and there's nothing to forward to a remote daemon for it.
			if err := player.SetDefaultPipeWireSink(chosen.HWName); err != nil {
				m.errText = err.Error()
			}
			return m, nil
		}
		if m.clientMode {
			mc := m.mprisClient
			return m, func() tea.Msg {
				if err := mc.SendDevice(chosen.HWName); err != nil {
					return errMsg(err)
				}
				return nil
			}
		}
		m.player.SetDevice(chosen.HWName)
		_ = m.store.SaveDevice(chosen.HWName)
	}
	return m, nil
}

// renderDeviceSelect renders the device-picker popup: ALSA DAC-class cards in
// bit-perfect mode, PipeWire sinks otherwise (see toggleBitPerfectMode) — the
// title names which one so it's never ambiguous which kind of device a row
// picks.
func (m *Model) renderDeviceSelect(t Theme) string {
	w := min(max(m.width-12, 30), 70)
	var rows []string
	if len(m.devices) == 0 {
		noneMsg := "No playback devices found."
		if !m.bitPerfectMode {
			noneMsg = "No PipeWire sinks found — is pactl installed?"
		}
		rows = append(rows, t.RowDim.Render(noneMsg))
	} else {
		for i, d := range m.devices {
			cur := "  "
			if m.cursor == i {
				cur = t.RowPlaying.Render("› ")
			}
			check := ""
			if d.HWName == m.currentDevice {
				check = t.GreenT.Render(" ✓")
			}
			line := fmt.Sprintf("%s%s  %s%s", cur, d.HWName, t.RowDim.Render(d.LongName), check)
			if m.cursor == i {
				rows = append(rows, t.RowSel.Width(w-2).Render(stripANSI(line)))
			} else {
				rows = append(rows, line)
			}
		}
	}
	h := min(len(rows)+2, m.height-4)
	body := strings.Join(rows, "\n")
	title := "SELECT DAC"
	if !m.bitPerfectMode {
		title = "SELECT OUTPUT (PIPEWIRE)"
	}
	return renderPanel(t, title, true, w, max(h, 4), body)
}

// renderActionSheet renders the contextual action sheet popup. Its title is the
// track name; selected row uses the cyan band; group headers separate sections.
func (m *Model) renderActionSheet(t Theme) string {
	items := m.actionSheetItems()
	w := min(max(m.width/2, 34), 52)
	innerW := w - 2

	var rows []string
	for i, it := range items {
		if it.group != "" {
			rows = append(rows, t.CmdGroup.Render(it.group))
		}
		// Plain text content (icon + label + right-aligned hint), measured
		// without styling so it lays out the same selected or not.
		plain := " " + it.icon + "  " + it.label
		if it.hint != "" {
			pad := max(innerW-lipgloss.Width(plain)-len([]rune(it.hint))-1, 1)
			plain += strings.Repeat(" ", pad) + it.hint
		}
		if i == m.sheetCursor {
			rows = append(rows, t.CmdItemSel.Width(innerW).Render(plain))
			continue
		}
		icon := t.RowDim.Render(it.icon)
		line := " " + icon + "  " + it.label
		if it.hint != "" {
			hint := t.CmdHint.Render(it.hint)
			pad := max(innerW-lipgloss.Width(" "+it.icon+"  "+it.label)-lipgloss.Width(hint)-1, 1)
			line += strings.Repeat(" ", pad) + hint
		}
		rows = append(rows, line)
	}

	title := "ACTIONS"
	if m.sheetTrack != nil {
		title = truncateStr(m.sheetTrack.Title, innerW-2)
	}
	h := len(rows) + 2
	body := strings.Join(rows, "\n")
	return renderPanel(t, title, true, w, h, body)
}
