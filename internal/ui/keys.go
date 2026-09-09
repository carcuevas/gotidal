package ui

import (
	"fmt"
	"strconv"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/player"
	"github.com/carcuevas/gotidal/internal/tidal"
)

// Frequently-compared key strings, hoisted to constants (goconst).
const (
	keyEsc   = "esc"
	keyUp    = "up"
	keyDown  = "down"
	keyEnter = "enter"
	keyLeft  = "left"
	keyRight = "right"
)

// errClientModeUnavailable is shown by every player-affecting toggle that
// only makes sense against a local player (client mode forwards playback to
// a remote daemon instead) — hoisted to a constant (goconst).
const errClientModeUnavailable = "Not available in client mode — the daemon owns the player"

// handleKey is the top-level key dispatcher. Order of precedence:
//  1. the second key of a pending two-key sequence (rmpc's g/o/Ctrl+S chains)
//  2. global keys (quit, tab switch, command palette, help, ...)
//  3. an active overlay
//  4. a focused search input
//  5. rmpc's list-navigation keys (top/bottom/half-page/page), applied
//     uniformly across whichever list currently has the cursor
//  6. the active tab's own handler
func (m Model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pendingKey != "" {
		return m.handlePendingKey(k)
	}
	if cmd, done := m.handleGlobalKey(k); done {
		return m, cmd
	}

	if m.overlay != OverlayNone {
		return m.updateOverlay(k)
	}

	// A focused search input consumes typing; global controls already handled.
	if m.searchInput.Focused() {
		switch k.String() {
		case keyEsc:
			m.searchInput.Blur()
			return m, nil
		case keyEnter, keyUp, keyDown:
			// fall through to section handler (search nav / submit)
		default:
			var cmd tea.Cmd
			m.searchInput, cmd = m.searchInput.Update(k)
			return m, cmd
		}
	}

	if cmd, handled := m.handleGenericNav(k); handled {
		return m, cmd
	}
	return m.updateSection(k)
}

// handleGlobalKey handles keys that work regardless of tab/overlay. It
// returns (cmd, true) when it consumed the key. It mutates through the pointer.
func (m *Model) handleGlobalKey(k tea.KeyMsg) (tea.Cmd, bool) {
	switch k.String() {
	case "ctrl+c":
		return m.quit(), true
	case "q":
		// In a focused text input, "q" is literal text — don't quit.
		if m.searchInput.Focused() || m.overlay == OverlayCommandPalette {
			return nil, false
		}
		return m.quit(), true

	case "ctrl+p", ":":
		if m.overlay == OverlayCommandPalette {
			return nil, true
		}
		if m.searchInput.Focused() {
			return nil, false
		}
		m.openCommandPalette()
		return nil, true

	case "?":
		if m.searchInput.Focused() || m.overlay != OverlayNone {
			return nil, false
		}
		m.overlay = OverlayHelp
		return nil, true

	case "t":
		if m.searchInput.Focused() || m.section == SecSettings {
			return nil, false // Settings owns "t"; input treats it as text
		}
		m.cycleTheme()
		return nil, true

	case "v":
		if m.searchInput.Focused() || m.overlay != OverlayNone {
			return nil, false
		}
		nm, cmd := m.toggleVisualizer()
		*m = nm.(Model) //nolint:forcetypeassert // toggleVisualizer always returns a Model
		return cmd, true

	case "ctrl+x":
		if m.searchInput.Focused() || m.overlay != OverlayNone {
			return nil, false
		}
		if t := m.selectedTrack(); t != nil {
			m.openActionSheet(*t)
		}
		return nil, true

	case "tab":
		// Deliberately not guarded by searchInput.Focused(): a literal tab
		// character has no legitimate use in a search query, so Tab always
		// switches tabs immediately rather than waiting for Esc first.
		if m.overlay != OverlayNone {
			return nil, false
		}
		return m.cycleTab(1), true
	case "shift+tab":
		if m.overlay != OverlayNone {
			return nil, false
		}
		return m.cycleTab(-1), true

	case "g", "o", "ctrl+s":
		if m.searchInput.Focused() || m.overlay != OverlayNone {
			return nil, false
		}
		m.pendingKey = k.String()
		return nil, true

	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if m.searchInput.Focused() || m.overlay != OverlayNone {
			return nil, false
		}
		n, _ := strconv.Atoi(k.String())
		if n >= 1 && n <= len(tabEntries) {
			return m.gotoTab(tabEntries[n-1].section), true
		}
		return nil, true

	case "D":
		// Clearing the queue is meaningful from any tab, not just while
		// looking at the Queue tab itself (unlike "d", single-item removal,
		// which needs a cursor position within the queue list to mean
		// anything).
		if m.searchInput.Focused() || m.overlay != OverlayNone {
			return nil, false
		}
		m.clearQueue()
		return m.syncQueueCover(), true
	}
	return nil, false
}

// handlePendingKey resolves the second key of a two-key sequence started by
// handleGlobalKey (rmpc's g/o/Ctrl+S prefix chains). An unrecognized second
// key silently cancels the sequence, matching rmpc's own behavior.
func (m Model) handlePendingKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	prefix := m.pendingKey
	m.pendingKey = ""
	key := k.String()

	switch prefix {
	case "g":
		switch key {
		case "g":
			if cur, _ := m.activeCursorRef(); cur != nil {
				*cur = 0
			}
			cmd := m.syncQueueCover()
			return m, cmd
		case "t":
			cmd := m.cycleTab(1)
			return m, cmd
		case "T":
			cmd := m.cycleTab(-1)
			return m, cmd
		}
	case "o":
		switch key {
		case "I":
			m.openSongInfo()
			return m, nil
		case "o":
			m.openDeviceSelect()
			return m, nil
		}
	case "ctrl+s":
		if key == "a" {
			return m.saveQueueAsNew()
		}
	}
	return m, nil
}

// activeCursorRef returns a pointer to whichever cursor field indexes the
// list currently on screen, and that list's length — the single source of
// truth handleGenericNav (gg/G/half-page/page) and gg use to work uniformly
// across every tab. Returns (nil, 0) for contexts with no single linear
// cursor (overlays, the Settings theme picker), so callers fall through to
// that context's own key handler instead.
func (m *Model) activeCursorRef() (cur *int, length int) {
	switch {
	case m.overlay != OverlayNone:
		return nil, 0
	case m.showArtist && m.artistAlbum != nil:
		return &m.artistAlbumCursor, len(m.artistAlbumTracks)
	case m.showArtist:
		return &m.artistCursor, len(m.artistAlbums) + 2
	case m.section == SecSearch:
		return &m.searchCursor, len(m.searchRows())
	case m.section == SecPlaylists && m.detailFocus:
		return &m.detailCursor, len(m.detailTracks)
	case m.section == SecPlaylists:
		return &m.cursor, len(m.playlists)
	case m.section == SecFavSongs:
		return &m.cursor, len(m.favSongs)
	case m.section == SecFavArtists:
		return &m.cursor, len(m.favArtists)
	case m.section == SecFavAlbums:
		return &m.cursor, len(m.favAlbums)
	case m.section == SecHistory:
		return &m.cursor, len(m.history)
	case m.section == SecSettings:
		return nil, 0
	default: // Queue, Mixes
		return &m.cursor, m.currentListLen()
	}
}

// handleGenericNav applies rmpc's list-navigation keys (bottom, half-page,
// full-page) uniformly via activeCursorRef. "gg" (top) is handled directly in
// handlePendingKey since it arrives as a two-key sequence. Returns
// handled=false for any other key, or when the current context has no single
// linear cursor, so the caller falls through to the tab's own handler.
func (m *Model) handleGenericNav(k tea.KeyMsg) (tea.Cmd, bool) {
	cur, length := m.activeCursorRef()
	if cur == nil {
		return nil, false
	}
	page := max(m.bodyHeight(), 2)
	top := max(length-1, 0)
	switch k.String() {
	case "G":
		*cur = top
	case "ctrl+u":
		*cur = max(*cur-page/2, 0)
	case "ctrl+d":
		*cur = min(*cur+page/2, top)
	case "ctrl+b", "pgup":
		*cur = max(*cur-page, 0)
	case "ctrl+f", "pgdown":
		*cur = min(*cur+page, top)
	default:
		return nil, false
	}
	return m.syncQueueCover(), true
}

func (m *Model) quit() tea.Cmd {
	if m.currentTrack != nil {
		_ = m.store.SaveLastPosition(m.currPos)
	}
	if m.player != nil {
		m.player.Close()
	}
	if m.cava != nil {
		m.cava.Stop()
	}
	m.store.Close()
	return tea.Quit
}

// openDeviceSelect populates the device list and raises the device overlay.
// In bit-perfect mode this lists ALSA DAC-class cards (today's behavior); in
// PipeWire mode it instead lists PipeWire sinks — any output PipeWire
// manages, not just a recognized DAC — since bit-perfect mode's D-Bus
// reservation and raw hw: open would just fight PipeWire for the device on
// most of those. See toggleBitPerfectMode.
func (m *Model) openDeviceSelect() {
	var devs []player.DeviceInfo
	var err error
	if m.bitPerfectMode {
		devs, err = player.ListDevices()
	} else {
		devs, err = player.ListPipeWireSinks()
		if err == nil {
			if def, derr := player.DefaultPipeWireSink(); derr == nil {
				m.currentDevice = def
			}
		}
	}
	if err != nil {
		m.errText = err.Error()
		return
	}
	m.devices = devs
	m.overlay = OverlayDeviceSelect
	m.cursor = 0
	for i, d := range devs {
		if d.HWName == m.currentDevice {
			m.cursor = i
			break
		}
	}
}

// openSongInfo raises the current-song-info overlay (rmpc's oI /
// ShowCurrentSongInfo).
func (m *Model) openSongInfo() {
	if m.currentTrack == nil {
		return
	}
	m.overlay = OverlaySongInfo
}

// cycleTheme advances to the next palette in paletteOrder and commits it.
func (m *Model) cycleTheme() {
	i := 0
	for j, name := range paletteOrder {
		if name == m.themeName {
			i = j
			break
		}
	}
	next := paletteOrder[(i+1)%len(paletteOrder)]
	m.applyTheme(next)
}

// applyTheme commits a palette by name and persists it.
func (m *Model) applyTheme(name string) {
	m.themeName = name
	m.palette = resolvePalette(name)
	m.theme = m.palette.Theme()
	m.previewPalette = nil
	m.rebuildProgress()
	_ = m.store.SaveTheme(name)
}

// rebuildProgress rebuilds the progress bar with the active theme's gradient at
// the current width.
func (m *Model) rebuildProgress() {
	t := m.activeTheme()
	barWidth := max(m.width-22, 10)
	m.progress = progressWithTheme(t, barWidth)
}

// gotoTab switches to sec via the same path selectSection has always used
// (data loads, focus, cursor reset), adapted for a pointer-receiver caller.
func (m *Model) gotoTab(sec Section) tea.Cmd {
	next, cmd := m.selectSection(sec)
	*m = next.(Model) //nolint:forcetypeassert // selectSection always returns the concrete Model it was called on
	return cmd
}

// cycleTab moves to the next (delta=1) or previous (delta=-1) tab in
// tabEntries order, wrapping around.
func (m *Model) cycleTab(delta int) tea.Cmd {
	n := len(tabEntries)
	i := ((tabIndexOf(m.section)+delta)%n + n) % n
	return m.gotoTab(tabEntries[i].section)
}

// selectSection switches to a tab, resets its cursor, and fires any
// data-load command it needs.
func (m Model) selectSection(sec Section) (tea.Model, tea.Cmd) {
	m.section = sec
	m.showArtist = false
	m.focusMain = true
	m.cursor = 0
	if sec == SecPlaylists {
		m.detailFocus = false
	}
	if sec == SecSettings {
		m.enterSettings()
	}

	switch sec {
	case SecSearch:
		m.searchInput.Focus()
	default:
		m.searchInput.Blur()
	}
	cmd := m.loadSection(sec)
	return m, tea.Batch(cmd, m.syncQueueCover())
}

// loadSection returns the command to (re)load a tab's data, or nil when the
// tab reuses already-loaded data. Favorites/playlists loaders arrive in later
// steps; for now only the always-available tabs are wired.
func (m *Model) loadSection(sec Section) tea.Cmd {
	switch sec {
	case SecMixes:
		if len(m.mixes) > 0 {
			return nil
		}
		return func() tea.Msg {
			mixes, err := m.client.GetMixes(m.ctx)
			if err != nil {
				return errMsg(err)
			}
			return mixesMsg(mixes)
		}
	case SecPlaylists:
		if len(m.playlists) > 0 {
			return nil
		}
		return func() tea.Msg {
			pls, err := m.client.GetUserPlaylists(m.ctx)
			if err != nil {
				return errMsg(err)
			}
			return playlistsMsg(pls)
		}
	case SecFavArtists:
		if len(m.favArtists) > 0 {
			return nil
		}
		return func() tea.Msg {
			artists, err := m.client.GetFavoriteArtists(m.ctx, 200)
			if err != nil {
				return errMsg(err)
			}
			return favArtistsMsg(artists)
		}
	case SecFavAlbums:
		if len(m.favAlbums) > 0 {
			return nil
		}
		return func() tea.Msg {
			albums, err := m.client.GetFavoriteAlbums(m.ctx, 200)
			if err != nil {
				return errMsg(err)
			}
			return favAlbumsMsg(albums)
		}
	default:
		// SecHistory uses the in-memory m.history; others reuse loaded data.
		return nil
	}
}

// updateSection routes keys to the active tab's handler. Esc backs out one
// level: album → artist albums → close artist → playlist detail.
func (m Model) updateSection(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if k.String() == keyEsc {
		switch {
		case m.showArtist && m.artistAlbum != nil:
			m.artistAlbum = nil
			m.artistAlbumTracks = nil
			m.artistAlbumCursor = 0
		case m.showArtist:
			m.showArtist = false
		case m.section == SecPlaylists && m.detailFocus:
			m.detailFocus = false
		}
		return m, nil
	}

	if m.showArtist {
		return m.updateArtist(k)
	}

	switch m.section {
	case SecSearch:
		return m.updateSearchKeys(k)
	case SecFavSongs:
		return m.updateFavSongs(k)
	case SecPlaylists:
		return m.updatePlaylists(k)
	case SecFavArtists:
		return m.updateFavArtists(k)
	case SecFavAlbums:
		return m.updateFavAlbums(k)
	case SecHistory:
		return m.updateHistory(k)
	case SecSettings:
		return m.updateSettings(k)
	default:
		// Queue, Now Playing, Mixes.
		return m.updateListKeys(k)
	}
}

// updateListKeys handles the common track-list tabs (Queue, Now Playing,
// Mixes): cursor movement, playback, and queue-only actions (rmpc scopes
// Delete/DeleteAll/MoveUp/MoveDown to the queue).
func (m Model) updateListKeys(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case keyUp, "k":
		if m.cursor > 0 {
			m.cursor--
		}
		cmd := m.syncQueueCover()
		return m, cmd
	case keyDown, "j":
		maxIdx := m.currentListLen()
		if m.cursor < maxIdx-1 {
			m.cursor++
		}
		cmd := m.syncQueueCover()
		return m, cmd
	case "d":
		if m.section == SecQueue {
			m.removeFromQueue(m.cursor)
			cmd := m.syncQueueCover()
			return m, cmd
		}
		return m, nil
	case "K":
		if m.section == SecQueue {
			m.moveQueueItem(m.cursor, -1)
		}
		return m, nil
	case "J":
		if m.section == SecQueue {
			m.moveQueueItem(m.cursor, 1)
		}
		return m, nil
	case keyEnter:
		if m.section == SecMixes && len(m.mixes) > 0 {
			mix := m.mixes[m.cursor]
			return m, func() tea.Msg {
				tracks, err := m.client.GetMixTracks(m.ctx, mix.ID)
				if err != nil {
					return errMsg(err)
				}
				return tracksMsg(tracks)
			}
		}
		if t := m.selectedTrack(); t != nil {
			_ = m.store.CacheTrack(t.ID, *t)
			cmd := m.playTrackCmd(*t)
			return m, cmd
		}
		return m, nil
	}
	return m.commonKeys(k)
}

// commonKeys handles keys shared by every tab (playback transport, volume,
// shuffle, queueing). Returns the model unchanged for unknown keys.
func (m Model) commonKeys(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "p":
		return m.togglePlay()
	case "s":
		return m.stopPlayback()
	case "f":
		m.seekBy(10)
	case "b":
		m.seekBy(-10)
	case ".":
		m.setVolume(m.volume + 5)
	case ",":
		m.setVolume(m.volume - 5)
	case "x":
		m.cycleShuffle()
	case "X":
		if m.section == SecQueue {
			m.reshuffleQueue()
		}
	case ">":
		return m.skipNext()
	case "<":
		return m.skipPrev()
	case "a":
		if t := m.selectedTrack(); t != nil {
			m.enqueueEnd(*t)
		}
	case "A":
		m.enqueueAllVisible()
	case "F":
		if t := m.selectedTrack(); t != nil {
			cmd := m.toggleFavorite(*t)
			return m, cmd
		}
	case "r":
		if t := m.selectedTrack(); t != nil {
			cmd := m.radioFrom(*t)
			return m, cmd
		}
	case "y":
		return m.copyLink()
	}
	return m, nil
}

// --- shared action helpers ---

func (m *Model) seekBy(delta float64) {
	if !m.clientMode && m.player != nil && m.currentTrack != nil {
		if err := m.player.Seek(m.currPos + delta); err != nil {
			m.errText = err.Error()
		}
	}
}

func (m *Model) setVolume(v float64) {
	if m.clientMode {
		return
	}
	m.volume = max(min(v, 100), 0)
	_ = m.player.SetVolume(m.volume)
	_ = m.store.SaveVolume(m.volume)
}

// interTrackSilenceDefaultMs is the gap length the palette toggle applies —
// long enough for a CD/DAT recorder's own silence-based auto-track-detection
// to reliably key off, short enough not to be intrusive when listening live.
const interTrackSilenceDefaultMs = 2000

// toggleInterTrackSilence flips the inter-track silence gap between off
// (gapless, the default) and interTrackSilenceDefaultMs — a niche,
// off-by-default setting for feeding a downstream recorder's own
// silence-based auto-track-detection; see Player.SetInterTrackSilenceMs.
func (m Model) toggleInterTrackSilence() (tea.Model, tea.Cmd) {
	if m.clientMode {
		m.errText = errClientModeUnavailable
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
	}
	if m.interTrackSilenceMs == 0 {
		m.interTrackSilenceMs = interTrackSilenceDefaultMs
		m.toast = fmt.Sprintf("CD-recorder silence gap: ON (%.1fs) — gapless playback is now off", float64(interTrackSilenceDefaultMs)/1000)
	} else {
		m.interTrackSilenceMs = 0
		m.toast = "CD-recorder silence gap: OFF (gapless)"
	}
	m.player.SetInterTrackSilenceMs(m.interTrackSilenceMs)
	_ = m.store.SaveInterTrackSilenceMs(m.interTrackSilenceMs)
	return m, toastClearCmd()
}

// toggleBitPerfectMode flips between bit-perfect ALSA hw: output (the
// default — reserves and opens the DAC directly, so the device picker only
// offers real DAC-class ALSA cards) and PipeWire mode (plays through
// PipeWire's "default" PCM instead, unlocking every output PipeWire manages
// — laptop speakers, HDMI, Bluetooth — at the cost of bit-perfectness, so
// playback works even without a DAC connected); see Player.SetDACMode. Takes
// effect starting with the next track, not the one currently playing.
func (m Model) toggleBitPerfectMode() (tea.Model, tea.Cmd) {
	if m.clientMode {
		m.errText = errClientModeUnavailable
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
	}
	m.bitPerfectMode = !m.bitPerfectMode
	m.player.SetDACMode(m.bitPerfectMode)
	_ = m.store.SaveBitPerfectMode(m.bitPerfectMode)
	if m.bitPerfectMode {
		m.toast = "Bit-perfect quality: ON (DAC) — applies to your next track"
	} else {
		m.toast = "Bit-perfect quality: OFF (PipeWire) — applies to your next track"
	}
	return m, toastClearCmd()
}

// toggleLowDataMode flips low-data mode: a single toggle for "I'm on a
// hotspot/metered connection and away from my DAC" that forces PipeWire
// output (like toggleBitPerfectMode's OFF state) and a lossy stream request
// (see tidal.Client.GetStreamURL's lowData argument), instead of making
// bandwidth and DAC-exclusivity two settings to remember separately.
// Disabling it restores whatever bitPerfectMode was in effect just before
// enabling, rather than leaving PipeWire forced on. Both effects apply
// starting with the next track, not the one currently playing.
func (m Model) toggleLowDataMode() (tea.Model, tea.Cmd) {
	if m.clientMode {
		m.errText = errClientModeUnavailable
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
	}
	m.lowDataMode = !m.lowDataMode
	if m.lowDataMode {
		m.preLowDataBitPerfect = m.bitPerfectMode
		m.bitPerfectMode = false
		m.toast = "Data Saver: ON (PipeWire + lossy) — applies to your next track"
	} else {
		m.bitPerfectMode = m.preLowDataBitPerfect
		m.toast = "Data Saver: OFF — applies to your next track"
	}
	m.player.SetDACMode(m.bitPerfectMode)
	_ = m.store.SaveLowDataMode(m.lowDataMode)
	_ = m.store.SaveBitPerfectMode(m.bitPerfectMode)
	return m, toastClearCmd()
}

// toggleVisualizer cycles the meter strip anchored under Lyrics through Cava
// spectrum (default — frequency content, requires the cava binary) → Peak
// meter (real signal level, no external dependency) → hidden (gives that
// space back to Lyrics — see queueLayout) → Cava spectrum again. Skips the
// Cava state entirely when the cava binary isn't installed, cycling between
// just Peak and hidden instead.
func (m Model) toggleVisualizer() (tea.Model, tea.Cmd) {
	if m.clientMode {
		m.errText = "Not available in client mode — no local audio to meter"
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
	}
	switch {
	case !m.meterHidden && !m.showPeakMeter: // Cava -> Peak
		m.showPeakMeter = true
		m.toast = "Meter: Peak"
	case !m.meterHidden && m.showPeakMeter: // Peak -> hidden
		m.meterHidden = true
		m.showPeakMeter = false
		m.toast = "Meter: Off"
	default: // hidden -> Cava, or Peak if cava isn't installed
		m.meterHidden = false
		if m.cava == nil {
			m.showPeakMeter = true
			m.toast = "Meter: Peak (cava not installed)"
		} else {
			m.toast = "Meter: Cava spectrum"
		}
	}
	return m, toastClearCmd()
}

func (m *Model) cycleShuffle() {
	if m.shuffleMode == ShuffleOff {
		m.shuffleMode = ShuffleFisherYates
	} else {
		m.shuffleMode = ShuffleOff
	}
	m.applyShuffle()
	m.cursor = 0
}

// enqueueAllVisible appends every track in the tab's current list to the
// queue (rmpc's AddAll), matching whichever list selectedTrack() reads from.
func (m *Model) enqueueAllVisible() {
	var list []tidal.Track
	switch {
	case m.section == SecSearch:
		list = m.searchResults.Tracks
	case m.section == SecFavSongs:
		list = m.favSongs
	case m.section == SecHistory:
		list = m.history
	case m.section == SecPlaylists && m.detailFocus:
		list = m.detailTracks
	case m.showArtist && m.artistAlbum != nil:
		list = m.artistAlbumTracks
	default:
		list = m.tracks
	}
	for i := range list {
		m.enqueueEnd(list[i])
	}
}

func (m Model) togglePlay() (tea.Model, tea.Cmd) {
	if m.clientMode {
		mc := m.mprisClient
		return m, func() tea.Msg {
			if err := mc.SendPlayPause(); err != nil {
				return errMsg(err)
			}
			return nil
		}
	}
	if m.currentTrack == nil || m.stopped {
		if t := m.selectedTrack(); t != nil {
			_ = m.store.CacheTrack(t.ID, *t)
			cmd := m.playTrackCmd(*t)
			return m, cmd
		}
		return m, nil
	}
	_ = m.player.Pause()
	// Read the state back rather than assuming the flip landed where we
	// expected: the player can force itself back to paused on its own (a
	// failed ALSA reacquire on resume), and toggling blindly from a stale
	// belief inverts play/pause for the rest of the track.
	m.isPlaying = !m.player.IsPaused()
	m.pushState()
	if m.isPlaying {
		return m, tea.Batch(m.ensureBarsTicking()...)
	}
	return m, nil
}

// stopPlayback implements rmpc's Stop (distinct from Pause, which the player
// has no separate state for): pause and rewind to the beginning.
func (m Model) stopPlayback() (tea.Model, tea.Cmd) {
	if m.clientMode || m.player == nil || m.currentTrack == nil {
		return m, nil
	}
	if !m.player.IsPaused() {
		_ = m.player.Pause()
	}
	_ = m.player.Seek(0)
	m.isPlaying = false
	m.stopped = true
	m.currPos = 0
	m.pushState()
	return m, nil
}

func (m Model) skipNext() (tea.Model, tea.Cmd) {
	if len(m.tracks) == 0 {
		return m, nil
	}
	m.shufflePlayed = append(m.shufflePlayed, m.cursor)
	next := m.nextIndex()
	if next < 0 {
		return m, nil
	}
	m.advancing = true
	m.cursor = next
	track := m.tracks[next]
	m.currPos = 0
	m.duration = 0
	_ = m.store.CacheTrack(track.ID, track)
	cmd := m.playNextTrackCmd(track)
	return m, cmd
}

func (m Model) skipPrev() (tea.Model, tea.Cmd) {
	if len(m.tracks) == 0 {
		return m, nil
	}
	prev := m.prevIndex()
	if prev < 0 {
		return m, nil
	}
	m.advancing = false
	m.cursor = prev
	track := m.tracks[prev]
	m.currPos = 0
	m.duration = 0
	_ = m.store.CacheTrack(track.ID, track)
	cmd := m.playTrackCmd(track)
	return m, cmd
}

func (m *Model) radioFrom(t tidal.Track) tea.Cmd {
	id := t.ID
	m.pendingQueueSource = "radio" // tracksMsg marks the resulting queue unsaved
	return func() tea.Msg {
		tracks, err := m.client.GetTrackRadio(m.ctx, id)
		if err != nil {
			return errMsg(err)
		}
		return tracksMsg(tracks)
	}
}

func (m *Model) toggleFavorite(t tidal.Track) tea.Cmd {
	id := t.ID
	isFav := m.favorites[id]
	return func() tea.Msg {
		var err error
		if isFav {
			err = m.client.RemoveFavorite(m.ctx, id)
		} else {
			err = m.client.AddFavorite(m.ctx, id)
		}
		if err != nil {
			return errMsg(err)
		}
		return favoriteMsg{trackID: id, added: !isFav}
	}
}

func (m Model) copyLink() (tea.Model, tea.Cmd) {
	if t := m.selectedTrack(); t != nil {
		return m.copyTrackLink(*t)
	}
	return m, nil
}

// copyTrackLink copies a track's Tidal URL to the clipboard and flashes a
// confirmation in the status line.
func (m Model) copyTrackLink(t tidal.Track) (tea.Model, tea.Cmd) {
	link := fmt.Sprintf("https://tidal.com/track/%d", t.ID)
	if err := clipboard.WriteAll(link); err != nil {
		m.errText = err.Error()
		return m, nil
	}
	m.errText = "Copied link to clipboard"
	return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })
}

// openAlbum loads an album's tracks into the queue.
func (m *Model) openAlbum(albumID int) tea.Cmd {
	if albumID == 0 {
		return nil
	}
	id := strconv.Itoa(albumID)
	return func() tea.Msg {
		tracks, err := m.client.GetAlbumTracks(m.ctx, id)
		if err != nil {
			return errMsg(err)
		}
		return tracksMsg(tracks)
	}
}

// openArtistFor opens the transient artist drill-down for a track's artist.
func (m Model) openArtistFor(t *tidal.Track) (tea.Model, tea.Cmd) {
	if t == nil {
		t = m.currentTrack
	}
	if t == nil || t.Artist.ID == 0 {
		return m, nil
	}
	artistID := t.Artist.ID
	artistName := t.Artist.Name
	m.prevSection = m.section
	m.artistLoading = true
	m.showArtist = true
	return m, func() tea.Msg {
		albums, err := m.client.GetArtistAlbums(m.ctx, artistID)
		if err != nil {
			return errMsg(err)
		}
		return artistAlbumsMsg{artistID: artistID, artistName: artistName, albums: albums}
	}
}

// updateArtist handles the transient artist drill-down sub-view.
func (m Model) updateArtist(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// When an album is open, navigate its track list instead of the album list.
	if m.artistAlbum != nil {
		return m.updateArtistAlbum(k)
	}
	switch k.String() {
	case keyUp, "k":
		if m.artistCursor > 0 {
			m.artistCursor--
		}
		return m, nil
	case keyDown, "j":
		if m.artistCursor < len(m.artistAlbums)+2-1 {
			m.artistCursor++
		}
		return m, nil
	case keyEnter:
		artistID := m.artistID
		switch m.artistCursor {
		case 0: // ▶ Play all tracks
			m.artistLoading = true
			return m, func() tea.Msg {
				tracks, err := m.client.GetArtistAllTracks(m.ctx, artistID)
				if err != nil {
					return errMsg(err)
				}
				return tracksMsg(tracks)
			}
		case 1: // ★ Top tracks
			return m, func() tea.Msg {
				tracks, err := m.client.GetArtistTopTracks(m.ctx, artistID, 100)
				if err != nil {
					return errMsg(err)
				}
				return tracksMsg(tracks)
			}
		default:
			// Open the album to view its tracks (does not load the queue yet).
			if idx := m.artistCursor - 2; idx >= 0 && idx < len(m.artistAlbums) {
				album := m.artistAlbums[idx]
				albumID := strconv.Itoa(album.ID)
				m.artistLoading = true
				return m, func() tea.Msg {
					tracks, err := m.client.GetAlbumTracks(m.ctx, albumID)
					if err != nil {
						return errMsg(err)
					}
					return artistAlbumTracksMsg{album: album, tracks: tracks}
				}
			}
		}
		return m, nil
	}
	return m.commonKeys(k)
}

// updateArtistAlbum navigates the track list of an album opened inside the
// artist drill-down. Esc backs out to the album list; Enter loads the album
// into the queue and plays from the selected track.
func (m Model) updateArtistAlbum(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case keyEsc:
		m.artistAlbum = nil
		m.artistAlbumTracks = nil
		m.artistAlbumCursor = 0
		return m, nil
	case keyUp, "k":
		if m.artistAlbumCursor > 0 {
			m.artistAlbumCursor--
		}
		return m, nil
	case keyDown, "j":
		if m.artistAlbumCursor < len(m.artistAlbumTracks)-1 {
			m.artistAlbumCursor++
		}
		return m, nil
	case keyEnter:
		tracks := m.artistAlbumTracks
		i := m.artistAlbumCursor
		m.showArtist = false
		m.artistAlbum = nil
		m.section = SecQueue
		cmd := m.playListIntoQueue(tracks, i)
		return m, cmd
	}
	return m.commonKeys(k)
}

// --- selection helpers ---

// selectedTrack returns the track under the cursor in the active context, or
// the current track as a fallback. Returns nil when nothing is selectable.
func (m *Model) selectedTrack() *tidal.Track {
	switch {
	case m.section == SecSearch:
		return m.selectedTrackForSearch()
	case m.section == SecFavSongs && len(m.favSongs) > 0 && m.cursor < len(m.favSongs):
		t := m.favSongs[m.cursor]
		return &t
	case m.section == SecHistory && len(m.history) > 0 && m.cursor < len(m.history):
		t := m.history[m.cursor]
		return &t
	case m.section == SecPlaylists && m.detailFocus && len(m.detailTracks) > 0 && m.detailCursor < len(m.detailTracks):
		t := m.detailTracks[m.detailCursor]
		return &t
	case len(m.tracks) > 0 && m.cursor >= 0 && m.cursor < len(m.tracks):
		t := m.tracks[m.cursor]
		return &t
	case m.currentTrack != nil:
		return m.currentTrack
	default:
		return nil
	}
}

// currentListLen returns the length of the list the main cursor indexes for
// the active tab (Queue/Mixes only — other tabs have their own cursor and
// track their own list length directly).
func (m *Model) currentListLen() int {
	switch m.section {
	case SecMixes:
		return len(m.mixes)
	default:
		return len(m.tracks)
	}
}
