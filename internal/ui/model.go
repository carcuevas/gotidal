package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carcuevas/gotidal/internal/logger"
	"github.com/carcuevas/gotidal/internal/mpris"
	"github.com/carcuevas/gotidal/internal/player"
	"github.com/carcuevas/gotidal/internal/sanitize"
	"github.com/carcuevas/gotidal/internal/spotify"
	"github.com/carcuevas/gotidal/internal/store"
	"github.com/carcuevas/gotidal/internal/tidal"
	"github.com/carcuevas/gotidal/internal/visualizer"
)

// ShuffleMode controls how the track list is shuffled.
type ShuffleMode int

const (
	ShuffleOff         ShuffleMode = iota // original order
	ShuffleRandom                         // random pick on each advance
	ShuffleFisherYates                    // pre-shuffled with Fisher-Yates
)

func (s ShuffleMode) String() string {
	switch s {
	case ShuffleRandom:
		return "Random"
	case ShuffleFisherYates:
		return "Shuffle"
	default:
		return "Off"
	}
}

// Overlay is the active modal layer over the main view. OverlayNone means the
// section pane (or sidebar) has focus.
type Overlay int

const (
	OverlayNone Overlay = iota
	OverlayCommandPalette
	OverlayActionSheet
	OverlayDeviceSelect
	OverlayAddToPlaylist
	OverlayNewPlaylistName
	OverlayDeletePlaylist
	OverlayImportSpotify
	OverlayHelp
	OverlaySongInfo
	OverlayThemePicker
)

//nolint:recvcheck // tea.Model requires value-receiver Init/Update/View; helper methods mutate via pointer receiver
type Model struct {
	//nolint:containedctx // the long-lived TUI model holds the app context for command goroutines
	ctx    context.Context
	client *tidal.Client
	store  *store.SecretsStore
	player *player.Player

	// Navigation: which tab is active and the active modal Overlay. focusMain is
	// always true post-init (there is no sidebar to cede focus to — every
	// render*Pane function still takes it so the "focused" row styling renders
	// the same way it always has). prevSection records where to return after the
	// artist drill-down.
	section     Section
	overlay     Overlay
	focusMain   bool
	prevSection Section

	// pendingKey holds a leading key of a two-key sequence (rmpc's "g"/"o"
	// prefix chains, e.g. gt/gT, oI/oo) awaiting its second key.
	pendingKey string

	// Action sheet overlay state.
	sheetTrack  *tidal.Track
	sheetCursor int

	// Command palette overlay state.
	paletteInput  textinput.Model
	paletteCursor int

	// Add-to-playlist overlay state. addToPlaylistTracks is nil for the
	// original "save the whole live queue" flow (beginSaveToExisting), which
	// keeps using m.tracks directly and reports success via queueSavedMsg
	// (retagging the queue's own source) — unchanged. It is set to a specific
	// track set by beginAddTrackToPlaylist (the per-track "Add to
	// playlist…" action-sheet entry), in which case a plain confirmation
	// toast is shown instead: adding one track to some playlist must not
	// silently relabel whatever the live queue happens to be right now.
	// newPlaylistInput is the name field for the "+ Create New Playlist…"
	// row both flows share.
	// newPlaylistReturn is the overlay Esc backs out to: the picker when the
	// prompt was reached through it, OverlayNone when it was opened directly
	// (Ctrl+S), where "go back" would otherwise raise a picker the user never
	// asked for.
	addToPlaylistTracks []tidal.Track
	newPlaylistInput    textinput.Model
	newPlaylistReturn   Overlay

	// playlistsStale marks the cached playlist list as needing a refetch —
	// see markPlaylistsStale. Set by every create/append/delete, cleared when
	// a fresh list arrives.
	playlistsStale bool

	// deleteTarget is the playlist the confirmation overlay is asking about.
	// Deleting is irreversible on Tidal's side, so the target is captured when
	// the prompt opens rather than re-read from the cursor on confirm — the
	// list must not be able to shift underneath a pending "yes".
	deleteTarget *tidal.Playlist

	// Spotify-import overlay state: the URL input, the resolved source, the
	// matched/"not available" rows, the flow stage, and the list cursor.
	importInput  textinput.Model
	importSource *spotify.Source
	importRows   []importRow
	importStage  importStage
	importCursor int
	importError  string

	// themeCursor is the Settings tab's own row cursor (device, bit-perfect,
	// Data Saver, silence gap, Themes — see settingsRowCount). themePickerIndex
	// is the separate cursor within the floating Themes overlay (OverlayThemePicker,
	// opened from the Themes row), indexing paletteOrder directly.
	// previewPalette (below) holds the live-previewed scheme while either
	// cursor moves.
	themeCursor      int
	themePickerIndex int

	errText string // transient error shown in status bar; cleared after display

	// Data
	tracks []tidal.Track
	mixes  []tidal.Mix
	cursor int

	// Search — own list so results don't clobber My Music. searchResults holds
	// the grouped multi-category results; searchCursor indexes the flattened
	// list of selectable result rows (see searchRows).
	searchInput   textinput.Model
	searchTracks  []tidal.Track // legacy flat track list (still used by URL resolver path)
	searchResults tidal.SearchResults
	searchCursor  int
	searchLoading bool

	// Artist view — the selected artist's albums plus two synthetic quick-play
	// rows ("Play all tracks", "Top tracks"). Reached from the action sheet.
	artistID      int // 0 = none
	artistName    string
	artistAlbums  []tidal.Album
	artistCursor  int
	artistLoading bool
	showArtist    bool // true while the transient artist drill-down is open
	// Album drill-down within the artist view: when an album is opened its
	// tracks are listed here (artistAlbum != nil) before being loaded to queue.
	artistAlbum       *tidal.Album
	artistAlbumTracks []tidal.Track
	artistAlbumCursor int

	// Library data loaded on demand for the favorites/playlists sections.
	favSongs   []tidal.Track // the favorite-songs list (SecFavSongs)
	playlists  []tidal.Playlist
	favArtists []tidal.Artist
	favAlbums  []tidal.Album
	history    []tidal.Track

	// Playlists detail pane: which playlist is open and its tracks. The index
	// column uses m.cursor; detailCursor indexes the open playlist's tracks and
	// detailFocus toggles between the index (false) and detail (true) columns.
	openPlaylist *tidal.Playlist
	playlistName string
	detailTracks []tidal.Track
	detailCursor int
	detailFocus  bool

	// Hybrid queue/playlist model. queueSource describes the queue's origin
	// ("playlist:<name>", "radio", or ""); queuePlaylistUUID is the saved
	// playlist it was loaded from (if any); queueDirty marks unsaved edits.
	queueSource       string
	queuePlaylistUUID string
	queueDirty        bool
	// pendingQueueSource is applied to queueSource when the next tracksMsg
	// lands (e.g. set to "radio" before a GetTrackRadio request resolves).
	pendingQueueSource string

	// toast is a transient green confirmation flash, cleared after a delay.
	toast string

	// Terminal size
	width  int
	height int

	// Device selection
	devices       []player.DeviceInfo
	currentDevice string // hw device string, "" = auto-detect

	// bitPerfectMode mirrors Player.SetDACMode: true (the default) opens the
	// ALSA hw: device directly for bit-perfect output and restricts the
	// device picker to ALSA cards; false plays through PipeWire's "default"
	// PCM instead, and the device picker lists PipeWire sinks (any output
	// PipeWire manages — laptop speakers, HDMI, Bluetooth — not just a
	// recognized DAC) — see toggleBitPerfectMode in keys.go.
	bitPerfectMode bool

	// lowDataMode forces PipeWire output and a lossy (HIGH, falling back to
	// LOW) stream request instead of the normal quality ladder — a single
	// toggle for "I'm on a hotspot/metered connection and away from my DAC",
	// so bandwidth and DAC-exclusivity aren't two things to remember
	// separately. preLowDataBitPerfect remembers bitPerfectMode from just
	// before enabling it, so disabling restores whatever DAC/PipeWire choice
	// was in effect before, rather than leaving PipeWire forced on. See
	// toggleLowDataMode in keys.go.
	lowDataMode          bool
	preLowDataBitPerfect bool

	// interTrackSilenceMs is the persisted inter-track silence gap (0 =
	// gapless, the default) — see commandPalette's toggle entry and
	// Player.SetInterTrackSilenceMs.
	interTrackSilenceMs uint32

	// Player UI
	currentTrack   *tidal.Track
	currentQuality tidal.Quality // granted stream quality tier for currentTrack
	// activeDevice is the ALSA device actually opened, which differs from
	// currentDevice when the plughw: fallback engaged. bitPerfect is false in
	// that case: ALSA's plug layer is resampling/remixing, so the quality
	// badge and device label must not claim untouched output.
	activeDevice string
	bitPerfect   bool
	volume       float64
	isPlaying    bool
	// stopped is true after an explicit Stop, distinct from a plain Pause: it
	// means the player is parked on currentTrack rather than genuinely paused
	// mid-listen, so the next Play (togglePlay, a media key, or an MPRIS
	// PlayPause) should start whatever is currently selected instead of just
	// resuming currentTrack — otherwise selecting a different track after Stop
	// and pressing Play silently replays the stopped track instead. Cleared by
	// doPlayTrack, the single entry point every playback start funnels through.
	stopped   bool
	advancing bool   // true while auto-advancing to next track; suppresses re-trigger
	skipGen   uint64 // monotonic counter; incremented on every doPlayTrack call
	progress  progress.Model
	currPos   float64
	duration  float64

	// prefetchedNextTrackID/prefetchedNextInfo cache a stream proactively
	// resolved by maybePrefetchNext once the current track is within
	// prefetchLeadSec of ending, so trackDoneMsg can hand playbackLoop's
	// gapless transition (see mpv.go) an already-known URL instead of racing
	// a fresh GetStreamURL round-trip against its fixed 5-second handoff
	// window — often too slow to land in time if only started once the
	// current track has already finished. 0 until a prefetch completes (or
	// after being consumed/invalidated — see doPlayTrack, trackDoneMsg).
	prefetchedNextTrackID int
	prefetchedNextInfo    tidal.StreamInfo

	// barsTicking is true while the fast Cava-refresh tick is scheduled. It
	// lapses when playback stops and is restarted by the 1s tick on resume, so
	// the UI doesn't re-render while idle.
	barsTicking bool

	// MPRIS media key commands (nil in client mode)
	mprisCh <-chan mpris.Event

	// Favorited track IDs (populated from GetFavorites; toggled by "f")
	favorites map[int]bool

	// currentRate is the sample rate the device negotiated for the playing
	// track, 0 when nothing is playing. Read from Player.Format on each tick.
	currentRate uint32

	// dacModeActive is whether the currently-open device was opened in DAC
	// (hw:) mode, from Player.AudioPath. Lets qualityBadge tell "not
	// bit-perfect because PipeWire is the chosen output" apart from "not
	// bit-perfect because the hw: negotiation itself had to fall back to
	// plughw:" — only the latter is an actual format compromise.
	dacModeActive bool

	// playingIndex is the index in m.tracks of the track actually playing,
	// or -1 when nothing is.
	//
	// This exists because m.cursor cannot serve that role: it is a shared
	// browse cursor that switching tabs, opening the device picker and
	// plain queue navigation all rewrite (selectSection resets it to 0 on
	// every tab change). Advancing the queue from m.cursor therefore
	// mis-fired after any of those — visiting Search and coming back left
	// the cursor at 0, so the "next" track resolved to the one already
	// playing and it played a second time while the UI moved on. It is also
	// what the Queue cursor is restored to when that tab is re-entered, so
	// the artwork and lyrics follow the playing track rather than row 0.
	playingIndex int

	// Shuffle
	shuffleMode   ShuffleMode
	tracksOrder   []tidal.Track // original order, saved when shuffle is enabled
	shufflePlayed []int         // indices already played (for ShuffleRandom deduplication)

	// openURL is a tidal:// or https://tidal.com/ URL passed at startup (e.g. from
	// "Open in desktop app"). Consumed once during Init.
	openURL string

	// clientMode is true when a parent gotidal instance is already running.
	// Playback commands are forwarded over D-Bus instead of driving the local player.
	clientMode  bool
	mprisClient *mpris.Client

	// localPlaylist is true when the client has loaded a playlist locally
	// (from a mix, radio, or URL) that has not yet been sent to the server.
	// While true, parentStateMsg will not overwrite m.tracks so the user can
	// browse the local list freely before committing it to the server.
	localPlaylist bool

	// mprisServer is non-nil in normal mode; used to push live state to clients.
	mprisServer *mpris.Server

	// playlistRestored is true when a playlist was loaded from the bbolt cache
	// on startup. Prevents favoritesLoadedMsg from overwriting the restored list.
	playlistRestored bool

	// restorePosition is the playback position (seconds) to seek to when the
	// next track starts. Set from the persisted last position on startup,
	// consumed once by nowPlayingMsg.
	restorePosition float64

	// Cover art panel.
	coverImage    image.Image // nil while loading or unavailable
	coverCacheKey string      // UUID of the currently displayed cover

	// kittySupported is set once at startup; when true the Now-Playing cover is
	// drawn with the Kitty graphics protocol at absolute coordinates. Mutually
	// exclusive with sixelSupported in practice (see KittySupported/
	// SixelSupported); Unicode block art is the fallback when neither applies.
	kittySupported bool
	sixelSupported bool

	// kitty caches the expensive PNG-encode + tracks what was last emitted so
	// the image escape is only re-encoded/re-sent on a real change (new cover
	// or resize), not on every animation frame. Pointer-backed so the
	// value-receiver Update can mutate it. sixel is the equivalent cache for
	// the Sixel path (see sixelState — its cache invalidation rules differ:
	// a geometry change forces a full re-encode, not just a re-placement).
	kitty *kittyState
	sixel *sixelState

	// ttyOut is where graphics escapes (Kitty or Sixel) are written. They
	// cannot go through the View string — BubbleTea's renderer truncates and
	// de-dupes lines, which mangles or drops them — so they are written out
	// of band, after the frame that reserves the box has been painted.
	ttyOut io.Writer

	// Theme / color scheme.
	// themeName is the registry key persisted to the store; palette is the
	// resolved Palette; theme holds the styles derived from it. previewPalette
	// is non-nil only while the theme picker is live-previewing a scheme that
	// has not yet been committed — View renders through it when set.
	themeName      string
	palette        Palette
	theme          Theme
	previewPalette *Palette

	// CAVA spectrum visualizer. cava is nil in client mode (there is no local
	// player/PCM to tap) and when the cava binary isn't installed; either way
	// the Cava pane just shows its placeholder. cavaBars is the latest
	// display-scaled (0-100) snapshot, refreshed on the fast bar tick.
	// cavaTap/peakTap are this model's two subscriber channels fed by a single
	// visualizer.Broadcast goroutine reading player.TapPCM() — the raw tap is
	// single-consumer, so Cava and PeakMeter each need their own fed copy
	// rather than racing to read the same channel.
	cava     *visualizer.Cava
	cavaBars []int
	cavaTap  chan []byte

	// Peak/VU meter: an alternative to the Cava spectrum, computed directly
	// from the same decoded PCM (see internal/visualizer.PeakMeter) with no
	// external dependency, so — unlike Cava — it works even when the cava
	// binary isn't installed. peak is nil in client mode, same reasoning as
	// cava. showPeakMeter selects which of the two renderMeterPane draws when
	// a meter is shown at all; it defaults to false (Cava spectrum).
	// meterHidden hides the meter strip entirely, giving that space back to
	// Lyrics (see queueLayout). "v" (toggleVisualizer, also reachable via the
	// command palette) cycles Cava spectrum → Peak meter → hidden → …
	peak          *visualizer.PeakMeter
	peakBars      []int
	peakTap       chan []byte
	showPeakMeter bool
	meterHidden   bool

	// Synced-lyrics panel state for the currently displayed track.
	lyricsState lyricsState

	// headless is true for the daemon (WithoutGraphics): there is no Lyrics
	// or Cava pane to show anything in, so both are skipped entirely rather
	// than doing work — a network fetch, a subprocess — nothing will display.
	headless bool
}

// loadTheme resolves the persisted theme name (or the default) into the model's
// palette/theme fields. Called from the constructors.
func loadTheme(s *store.SecretsStore) (name string, pal Palette, th Theme) {
	name = defaultThemeName
	if saved, err := s.LoadTheme(); err == nil && saved != "" {
		name = saved
	}
	pal = resolvePalette(name)
	return name, pal, pal.Theme()
}

// activeTheme returns the theme View should render with: the live preview while
// the picker is open, otherwise the committed theme. In client mode the focus
// accent is overridden with a steel-blue tint so the instance reads as distinct.
func (m *Model) activeTheme() Theme {
	pal := m.palette
	if m.previewPalette != nil {
		pal = *m.previewPalette
	}
	if m.clientMode {
		return clientTint(pal).Theme()
	}
	if m.previewPalette != nil {
		return pal.Theme()
	}
	return m.theme
}

// WithoutGraphics disables terminal graphics for this model. The daemon runs
// the same model with tea.WithoutRenderer() and no TTY, where writing image
// escapes to stdout would corrupt its log output.
func (m Model) WithoutGraphics() Model {
	m.kittySupported = false
	m.sixelSupported = false
	m.ttyOut = nil
	m.headless = true
	// The daemon has no Cava/Peak pane to draw into — disable both rather
	// than doing work (a subprocess, PCM decoding) nothing will ever display.
	if m.cava != nil {
		m.cava.Stop()
		m.cava = nil
	}
	if m.peak != nil {
		m.peak.Stop()
		m.peak = nil
	}
	return m
}

func InitialModel(ctx context.Context, client *tidal.Client, s *store.SecretsStore, srv *mpris.Server, openURL string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search for a song..."
	ti.CharLimit = 156
	ti.Width = 30

	p := player.NewPlayer()

	vol := 50.0
	if v, err := s.LoadVolume(); err == nil {
		vol = v
	}
	_ = p.SetVolume(vol)

	currentDevice := ""
	if dev, err := s.LoadDevice(); err == nil {
		currentDevice = dev
		p.SetDevice(dev)
	}

	interTrackSilenceMs, _ := s.LoadInterTrackSilenceMs()
	p.SetInterTrackSilenceMs(interTrackSilenceMs)

	bitPerfectMode, _ := s.LoadBitPerfectMode()
	p.SetDACMode(bitPerfectMode)
	lowDataMode, _ := s.LoadLowDataMode()

	var mprisCh <-chan mpris.Event
	if srv != nil {
		mprisCh = srv.Commands
	}

	themeName, palette, theme := loadTheme(s)

	// player.TapPCM() is single-consumer; fan it out to Cava and PeakMeter's
	// own subscriber channels so each gets every buffer independently instead
	// of racing to read the same one (see the cavaTap/peakTap field docs).
	cavaTap := make(chan []byte, 4)
	peakTap := make(chan []byte, 4)
	go visualizer.Broadcast(p.TapPCM(), cavaTap, peakTap)

	var cava *visualizer.Cava
	if visualizer.Available() {
		cava = visualizer.New(numCavaBars)
	}
	peak := visualizer.NewPeakMeter()

	return Model{
		ctx:                 ctx,
		client:              client,
		store:               s,
		player:              p,
		searchInput:         ti,
		section:             SecQueue,
		focusMain:           true,
		playingIndex:        -1,
		volume:              vol,
		currentDevice:       currentDevice,
		bitPerfectMode:      bitPerfectMode,
		lowDataMode:         lowDataMode,
		interTrackSilenceMs: interTrackSilenceMs,
		bitPerfect:          true,
		dacModeActive:       true,
		progress:            progressWithTheme(theme, 40),
		mprisCh:             mprisCh,
		favorites:           make(map[int]bool),
		openURL:             openURL,
		mprisServer:         srv,
		kittySupported:      KittySupported(),
		sixelSupported:      SixelSupported(),
		ttyOut:              os.Stdout,
		kitty:               &kittyState{},
		sixel:               &sixelState{},
		themeName:           themeName,
		palette:             palette,
		theme:               theme,
		cava:                cava,
		cavaTap:             cavaTap,
		peak:                peak,
		peakTap:             peakTap,
	}
}

// ClientModel creates a TUI model that forwards all playback actions to an
// already-running gotidal instance via the provided mprisClient. The local
// player is not started. The UI is tinted to indicate client mode.
func ClientModel(ctx context.Context, client *tidal.Client, s *store.SecretsStore, mprisClient *mpris.Client, openURL string) Model {
	ti := textinput.New()
	ti.Placeholder = "Search for a song..."
	ti.CharLimit = 156
	ti.Width = 30

	p := player.NewPlayer()

	vol := 50.0
	if v, err := s.LoadVolume(); err == nil {
		vol = v
	}
	_ = p.SetVolume(vol)

	currentDevice := ""
	if dev, err := s.LoadDevice(); err == nil {
		currentDevice = dev
		p.SetDevice(dev)
	}

	bitPerfectMode, _ := s.LoadBitPerfectMode()
	lowDataMode, _ := s.LoadLowDataMode()

	themeName, palette, theme := loadTheme(s)

	return Model{
		ctx:            ctx,
		client:         client,
		store:          s,
		player:         p,
		searchInput:    ti,
		section:        SecQueue,
		focusMain:      true,
		playingIndex:   -1,
		volume:         vol,
		currentDevice:  currentDevice,
		bitPerfectMode: bitPerfectMode,
		lowDataMode:    lowDataMode,
		bitPerfect:     true,
		dacModeActive:  true,
		progress:       progressWithTheme(clientTint(palette).Theme(), 40),
		favorites:      make(map[int]bool),
		openURL:        openURL,
		clientMode:     true,
		mprisClient:    mprisClient,
		kittySupported: KittySupported(),
		sixelSupported: SixelSupported(),
		ttyOut:         os.Stdout,
		kitty:          &kittyState{},
		sixel:          &sixelState{},
		themeName:      themeName,
		palette:        palette,
		theme:          theme,
	}
}

// applyShuffle reorders m.tracks according to the current shuffle mode and
// resets the played-index history. Call whenever the mode changes or a new
// track list is loaded.
func (m *Model) applyShuffle() {
	switch m.shuffleMode {
	case ShuffleFisherYates:
		shuffled := make([]tidal.Track, len(m.tracksOrder))
		copy(shuffled, m.tracksOrder)
		rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		m.tracks = shuffled
	default:
		m.tracks = make([]tidal.Track, len(m.tracksOrder))
		copy(m.tracks, m.tracksOrder)
	}
	m.shufflePlayed = nil
	// m.tracks was just rebuilt, so the recorded position is stale.
	m.syncPlayingIndex()
}

// resetSectionCursor puts the shared browse cursor where it belongs after a
// tab switch (or anything else that used to blindly zero it).
//
// For the Queue that is the playing track, not row 0: the Queue cover and
// lyrics panes follow the cursor (see hoveredTrack), so zeroing it made
// leaving the tab and coming back show the first queued track's artwork and
// lyrics while a different track was playing.
func (m *Model) resetSectionCursor() {
	if m.section == SecQueue && m.playingIndex >= 0 && m.playingIndex < len(m.tracks) {
		m.cursor = m.playingIndex
		return
	}
	m.cursor = 0
}

// advanceBase is the index that next/previous count from: the track actually
// playing, or — when nothing is playing — wherever the cursor sits, so
// pressing next on a freshly restored session still starts from the selection.
//
// It must not be m.cursor while a track is playing; see playingIndex for why.
func (m *Model) advanceBase() int {
	if m.playingIndex >= 0 && m.playingIndex < len(m.tracks) {
		return m.playingIndex
	}
	return m.cursor
}

// syncPlayingIndex re-locates the playing track in m.tracks by ID, for use
// wherever the queue is reordered or replaced wholesale. Sets -1 when nothing
// is playing or the track is no longer queued. With a duplicated track ID the
// first occurrence wins, which is also how the rest of the queue code
// (removeFromQueue, moveQueueItem) resolves duplicates.
func (m *Model) syncPlayingIndex() {
	m.playingIndex = -1
	if m.currentTrack == nil {
		return
	}
	for i := range m.tracks {
		if m.tracks[i].ID == m.currentTrack.ID {
			m.playingIndex = i
			return
		}
	}
}

// prevIndex returns the index of the previously played track. In shuffle
// modes it pops from the play history stack; otherwise it steps back from the
// playing track. Returns -1 if there is no previous track.
func (m *Model) prevIndex() int {
	if len(m.tracks) == 0 {
		return -1
	}
	if len(m.shufflePlayed) > 0 {
		prev := m.shufflePlayed[len(m.shufflePlayed)-1]
		m.shufflePlayed = m.shufflePlayed[:len(m.shufflePlayed)-1]
		if prev >= 0 && prev < len(m.tracks) {
			return prev
		}
	}
	prev := m.advanceBase() - 1
	if prev >= 0 {
		return prev
	}
	return -1
}

// nextIndex returns the index of the next track to play given the playing
// track and shuffle mode. Returns -1 if there is no next track.
func (m *Model) nextIndex() int {
	if len(m.tracks) == 0 {
		return -1
	}
	switch m.shuffleMode {
	case ShuffleRandom:
		// Pick a random index that has not been played yet.
		played := make(map[int]bool, len(m.shufflePlayed))
		for _, i := range m.shufflePlayed {
			played[i] = true
		}
		// Build candidate list.
		var candidates []int
		for i := range m.tracks {
			if !played[i] {
				candidates = append(candidates, i)
			}
		}
		if len(candidates) == 0 {
			return -1
		}
		return candidates[rand.IntN(len(candidates))] //nolint:gosec // G404: playlist shuffle does not need crypto-grade randomness
	default:
		// ShuffleOff and ShuffleFisherYates both advance linearly through
		// the (possibly pre-shuffled) slice.
		next := m.advanceBase() + 1
		if next < len(m.tracks) {
			return next
		}
		return -1
	}
}

// prefetchLeadSec is how far (in seconds) ahead of a track's natural end
// maybePrefetchNext starts resolving the next track's stream — generously
// ahead of playbackLoop's fixed 5-second post-EOF gapless handoff window
// (see mpv.go), since the resolve itself (an API round-trip, sometimes
// walking Tidal's quality ladder through more than one attempt) can easily
// take longer than that window on its own if only started once the current
// track has already finished.
const prefetchLeadSec = 10.0

// maybePrefetchNext proactively resolves the next queued track's stream once
// the current one is within prefetchLeadSec of ending, caching it in
// prefetchedNextTrackID/prefetchedNextInfo so trackDoneMsg can hand
// playbackLoop's gapless transition an already-known URL instead of racing a
// fresh GetStreamURL round-trip against its handoff window. No-op in client
// mode (the daemon owns playback), with no next track, before the lead time,
// or if this track has already been prefetched (or a prefetch for it is
// already in flight).
func (m *Model) maybePrefetchNext() tea.Cmd {
	if m.clientMode || m.duration <= 0 || m.duration-m.currPos > prefetchLeadSec {
		return nil
	}
	next := m.nextIndex()
	if next < 0 {
		return nil
	}
	track := m.tracks[next]
	if track.ID == m.prefetchedNextTrackID {
		return nil
	}
	m.prefetchedNextTrackID = track.ID
	client := m.client
	ctx := m.ctx
	lowData := m.lowDataMode
	gen := m.skipGen
	trackID := track.ID
	return func() tea.Msg {
		info, err := client.GetStreamURL(ctx, trackID, lowData)
		if err != nil {
			logger.L.Debug("prefetch next track failed — trackDoneMsg will resolve it fresh instead", "trackID", trackID, "err", err)
			return nil
		}
		return nextTrackPrefetchedMsg{trackID: trackID, info: info, gen: gen}
	}
}

// playTrackCmd returns a tea.Cmd that starts playback of track.
// In normal mode it streams via the local player and returns nowPlayingMsg.
// In client mode it resolves the stream URL and forwards it to the parent
// instance via MPRIS, then returns nil (no local playback state to track).
func (m *Model) playTrackCmd(track tidal.Track) tea.Cmd {
	return m.doPlayTrack(track, m.player.Play, nil)
}

// playNextTrackCmd is like playTrackCmd but uses PlayNext to transition
// without closing the ALSA device, avoiding pops between playlist tracks.
// Resolves the stream URL fresh — see playNextTrackFromPrefetchCmd for the
// common case where maybePrefetchNext already resolved it in advance.
func (m *Model) playNextTrackCmd(track tidal.Track) tea.Cmd {
	return m.doPlayTrack(track, m.player.PlayNext, nil)
}

// playNextTrackFromPrefetchCmd is playNextTrackCmd with the stream already
// resolved (by maybePrefetchNext, while the previous track was still
// playing) — skips the GetStreamURL round-trip entirely, which is often too
// slow to land within playbackLoop's fixed 5-second gapless handoff window
// (see mpv.go) if only started once the previous track has already ended.
func (m *Model) playNextTrackFromPrefetchCmd(track tidal.Track, info tidal.StreamInfo) tea.Cmd {
	return m.doPlayTrack(track, m.player.PlayNext, &info)
}

// doPlayTrack starts playback of track via playFn (Play for a fresh start,
// PlayNext for a gapless transition). prefetched, when non-nil, is an
// already-resolved stream (see playNextTrackFromPrefetchCmd) that skips the
// GetStreamURL round-trip; otherwise it's resolved here.
func (m *Model) doPlayTrack(track tidal.Track, playFn func(player.StreamSource) (<-chan struct{}, error), prefetched *tidal.StreamInfo) tea.Cmd {
	if m.clientMode {
		mc := m.mprisClient
		if m.localPlaylist && len(m.tracks) > 0 {
			// Find the index of this track in the local playlist.
			idx := 0
			for i := range m.tracks {
				if m.tracks[i].ID == track.ID {
					idx = i
					break
				}
			}
			tracksJSON := mpris.MarshalTracks(m.tracks)
			m.localPlaylist = false
			return func() tea.Msg {
				if err := mc.SendPlaylist(tracksJSON, idx); err != nil {
					return errMsg(err)
				}
				return nil
			}
		}
		trackID := track.ID
		return func() tea.Msg {
			if err := mc.SendTrackID(trackID); err != nil {
				return errMsg(err)
			}
			return nil
		}
	}
	m.currentTrack = &track
	// Record where this track sits in the queue, so advancing never has to
	// consult the shared browse cursor (see playingIndex). Callers that know
	// the index have already pointed the cursor at it, which resolves
	// duplicate IDs exactly; anything else — playing straight out of search
	// results, say — falls back to an ID lookup, and to -1 when the track is
	// not queued at all.
	m.playingIndex = -1
	if m.cursor >= 0 && m.cursor < len(m.tracks) && m.tracks[m.cursor].ID == track.ID {
		m.playingIndex = m.cursor
	} else {
		for i := range m.tracks {
			if m.tracks[i].ID == track.ID {
				m.playingIndex = i
				break
			}
		}
	}
	m.isPlaying = true
	m.stopped = false
	m.skipGen++
	m.advancing = true // suppresses any stale trackDoneMsg until nowPlayingMsg resets it
	m.restorePosition = 0
	m.currPos = 0
	// This track is now "current" — any prefetch cached against it (see
	// maybePrefetchNext) has just been consumed via prefetched above, or is
	// stale/irrelevant if it wasn't. Either way it doesn't describe *this*
	// track's own successor yet, so don't let it linger.
	m.prefetchedNextTrackID = 0
	_ = m.store.SaveLastTrackID(track.ID)
	gen := m.skipGen
	ctx := m.ctx
	client := m.client
	lowData := m.lowDataMode
	return func() tea.Msg {
		// Fetch fresh track metadata in parallel with the stream URL so we
		// always have a cover UUID even when the cached entry predates cover support.
		type freshResult struct {
			track *tidal.Track
			err   error
		}
		freshCh := make(chan freshResult, 1)
		if track.Album.Cover == "" {
			go func() {
				t, err := client.GetTrack(ctx, strconv.Itoa(track.ID))
				freshCh <- freshResult{t, err}
			}()
		} else {
			freshCh <- freshResult{&track, nil}
		}

		var info tidal.StreamInfo
		if prefetched != nil {
			info = *prefetched
			logger.L.Info("stream resolved (prefetched)", "trackID", track.ID, "ext", info.Ext, "quality", info.Quality)
		} else {
			var err error
			info, err = client.GetStreamURL(ctx, track.ID, lowData)
			if err != nil {
				logger.L.Error("GetStreamURL failed", "trackID", track.ID, "err", err)
				return skipErrMsg{err: err, gen: gen}
			}
			logger.L.Info("stream resolved", "trackID", track.ID, "ext", info.Ext, "quality", info.Quality)
		}
		src := player.StreamSource{
			URLs:           info.URLs,
			BitDepth:       info.BitDepth,
			SampleRate:     info.SampleRate,
			InitURLs:       info.InitURLs,
			SegmentSeconds: info.SegmentSeconds,
		}
		// A fragmented stream cannot report its own length, so convert the
		// manifest's duration into the sample count the player tracks
		// position against.
		if info.DurationSec > 0 && info.SampleRate > 0 {
			src.TotalSamples = uint64(info.DurationSec * float64(info.SampleRate))
		}
		done, err := playFn(src)
		if err != nil {
			logger.L.Error("playFn failed", "trackID", track.ID, "err", err)
			return playbackFailedMsg{err: err, gen: gen}
		}
		logger.L.Debug("doPlayTrack: playFn returned, calling SetDuration", "trackID", track.ID, "trackDuration", track.Duration)
		if track.Duration > 0 {
			m.player.SetDuration(float64(track.Duration))
		}

		res := <-freshCh
		if res.err == nil && res.track != nil {
			return nowPlayingMsg{done: done, track: res.track, gen: gen, quality: info.Quality}
		}
		return nowPlayingMsg{done: done, gen: gen, quality: info.Quality}
	}
}

// tidalURLID extracts the last path segment (before any query string) from a
// URL string, which is the resource ID for Tidal API calls.
func tidalURLID(rawURL string) string {
	// Strip query string.
	if i := strings.IndexByte(rawURL, '?'); i >= 0 {
		rawURL = rawURL[:i]
	}
	parts := strings.Split(strings.TrimRight(rawURL, "/"), "/")
	return parts[len(parts)-1]
}

// resolveQuery turns a search query into a list of tracks.
// It handles:
//   - tidal:// deep-link URLs (tidal://track/ID, tidal://album/ID, tidal://mix/ID)
//   - Tidal web URLs         (tidal.com/browse/track/ID, etc.)
//   - Plain text             (title, artist, album — Tidal search covers all three)
//
// For plain-text queries the store cache is checked first to avoid redundant
// API calls. Pass nil for s to skip caching.
func resolveQuery(ctx context.Context, client *tidal.Client, s *store.SecretsStore, query string) ([]tidal.Track, error) {
	// Normalise tidal:// deep links to a recognisable path so the checks below work.
	// tidal://track/12345  →  tidal.com/track/12345
	// tidal://album/67890  →  tidal.com/album/67890
	// tidal://mix/abcdef   →  tidal.com/mix/abcdef
	if after, ok := strings.CutPrefix(query, "tidal://"); ok {
		query = "tidal.com/" + after
	}

	if strings.Contains(query, "tidal.com/") && strings.Contains(query, "/track/") {
		track, err := client.GetTrack(ctx, tidalURLID(query))
		if err != nil {
			return nil, err
		}
		return []tidal.Track{*track}, nil
	}
	if strings.Contains(query, "tidal.com/") && strings.Contains(query, "/album/") {
		return client.GetAlbumTracks(ctx, tidalURLID(query))
	}
	if strings.Contains(query, "tidal.com/") && strings.Contains(query, "/mix/") {
		return client.GetMixTracks(ctx, tidalURLID(query))
	}

	// Spotify URL: resolve the source and match each track on Tidal, returning
	// the matched Tidal tracks. Unmatched tracks are dropped on this quick path
	// (the import overlay shows them as "not available").
	if spotify.IsSpotifyURL(query) {
		src, err := spotify.Resolve(ctx, spotifyHTTPClient(), query)
		if err != nil {
			return nil, err
		}
		rows := matchSpotifyTracks(ctx, client, src.Tracks)
		return matchedTracks(rows), nil
	}

	// Plain-text search — check cache first.
	if s != nil {
		var cached []tidal.Track
		if found, err := s.LoadSearchResults(query, &cached); err == nil && found {
			return cached, nil
		}
	}
	tracks, err := client.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	if s != nil {
		_ = s.CacheSearchResults(query, tracks)
	}
	return tracks, nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// barTickCmd schedules the Cava-refresh tick at roughly cava's own configured
// framerate (30fps / ~33ms) so the visualizer doesn't visibly lag behind the
// audio it's tracking.
func barTickCmd() tea.Cmd {
	return tea.Tick(33*time.Millisecond, func(t time.Time) tea.Msg {
		return barTickMsg(t)
	})
}

// ensureBarsTicking returns the command to (re)start the fast Cava-refresh tick if
// it has lapsed, marking it running. Returns nil when it is already scheduled.
func (m *Model) ensureBarsTicking() []tea.Cmd {
	if m.barsTicking {
		return nil
	}
	m.barsTicking = true
	return []tea.Cmd{barTickCmd()}
}

// waitForTrackDone returns a command that blocks until the given done channel
// is closed (i.e. the track finished naturally), then sends a trackDoneMsg.
// Callers should pass the channel returned by player.Play() directly so there
// is no race between stop() clearing the old channel and Play() setting a new one.
func waitForTrackDone(done <-chan struct{}, gen uint64) tea.Cmd {
	return func() tea.Msg {
		<-done
		return trackDoneMsg{gen: gen}
	}
}

// watchPlayerPaused returns a command that blocks until the player reports it
// forced itself back into the paused state, then sends a playerPausedMsg. The
// handler re-registers it so the stream continues for the session's lifetime.
// In client mode there is no local player to watch.
func (m Model) watchPlayerPaused() tea.Cmd {
	if m.player == nil {
		return nil
	}
	ch := m.player.PausedEvents()
	return func() tea.Msg {
		err, ok := <-ch
		if !ok {
			return nil
		}
		return playerPausedMsg{err: err}
	}
}

// listenMPRIS returns a command that blocks until the next MPRIS event
// arrives, then re-registers itself so the stream continues.
func listenMPRIS(ch <-chan mpris.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			// Channel closed — MPRIS server shut down; stop listening.
			return nil
		}
		return mprisMsg(ev)
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		// Restore persisted playlist before the API call for favorites completes.
		func() tea.Msg {
			if m.store == nil {
				return nil
			}
			var cached []tidal.Track
			if err := m.store.LoadPlaylist(&cached); err == nil && len(cached) > 0 {
				return cachedPlaylistMsg(cached)
			}
			return nil
		},
		// Restore the recently-played history persisted last session.
		func() tea.Msg {
			if m.store == nil {
				return nil
			}
			var hist []tidal.Track
			if err := m.store.LoadHistory(&hist); err == nil && len(hist) > 0 {
				return historyLoadedMsg(hist)
			}
			return nil
		},
		func() tea.Msg {
			tracks, err := m.client.GetFavorites(m.ctx, 50)
			if err != nil {
				return errMsg(err)
			}
			return favoritesLoadedMsg(tracks)
		},
		func() tea.Msg {
			mixes, err := m.client.GetMixes(m.ctx)
			if err != nil {
				return errMsg(err)
			}
			return mixesMsg(mixes)
		},
		// Prefetch the library sections so the sidebar counts are correct at
		// launch instead of showing 0 until the section is first opened.
		func() tea.Msg {
			pls, err := m.client.GetUserPlaylists(m.ctx)
			if err != nil {
				return errMsg(err)
			}
			return playlistsMsg(pls)
		},
		func() tea.Msg {
			artists, err := m.client.GetFavoriteArtists(m.ctx, 200)
			if err != nil {
				return errMsg(err)
			}
			return favArtistsMsg(artists)
		},
		func() tea.Msg {
			albums, err := m.client.GetFavoriteAlbums(m.ctx, 200)
			if err != nil {
				return errMsg(err)
			}
			return favAlbumsMsg(albums)
		},
		m.waitForContextCancel(),
		tickCmd(),
		// The fast equaliser tick is started on demand by the 1s tick / playback
		// start (ensureBarsTicking) so an idle UI doesn't re-render 12×/s.
	}
	if !m.clientMode {
		cmds = append(cmds, listenMPRIS(m.mprisCh))
		if c := m.watchPlayerPaused(); c != nil {
			cmds = append(cmds, c)
		}
	} else {
		cmds = append(cmds, pollParentState(m.mprisClient))
	}
	if m.openURL != "" {
		u := m.openURL
		cmds = append(cmds, func() tea.Msg {
			tracks, err := resolveQuery(m.ctx, m.client, m.store, u)
			if err != nil {
				return errMsg(err)
			}
			return openURLTracksMsg(tracks)
		})
	}
	return tea.Batch(cmds...)
}

func (m Model) waitForContextCancel() tea.Cmd {
	return func() tea.Msg {
		<-m.ctx.Done()
		if m.player != nil {
			m.player.Close()
		}
		if m.store != nil {
			m.store.Close()
		}
		// A SIGINT/SIGTERM exit skips quit()'s key-press path entirely, so it
		// needs the same synchronous clear or a Sixel cover is left smeared
		// on the shell prompt exactly as it was on a normal 'q' quit (see
		// clearGraphicsOverlays). This closure was captured at Init(), before
		// any interaction, but kitty/sixel/ttyOut are all set by the
		// constructor rather than lazily, so the pointers here are the same
		// ones later syncs mutate — not a stale nil snapshot.
		m.clearGraphicsOverlays()
		return tea.Quit()
	}
}

// pushState publishes the current track and playlist to the MPRIS server so
// client instances can read it. Called whenever playback state changes.
func (m *Model) pushState() {
	if m.mprisServer == nil {
		return
	}
	m.mprisServer.SetState(mpris.PlayerState{
		CurrentTrackJSON: mpris.MarshalTracks(m.currentTrack),
		PlaylistJSON:     mpris.MarshalTracks(m.tracks),
		Position:         m.currPos,
		Duration:         m.duration,
		Volume:           m.volume,
		Device:           m.currentDevice,
		ShuffleMode:      m.shuffleMode.String(),
		Quality:          string(m.currentQuality),
		ActiveDevice:     m.activeDevice,
		BitPerfect:       m.bitPerfect,
	}, m.isPlaying)
}

// pollParentState returns a tea.Cmd that fetches the parent's state once and
// delivers it as a parentStateMsg. Used by the client TUI on each tick.
func pollParentState(mc *mpris.Client) tea.Cmd {
	return func() tea.Msg {
		ps, err := mc.GetState()
		if err != nil {
			// Parent may have gone away; deliver an empty state rather than an error.
			return parentStateMsg{}
		}
		return parentStateMsg(ps)
	}
}

// fetchCoverCmd downloads the album cover for the given track and delivers a
// coverLoadedMsg. It is a no-op when cover is empty.
func fetchCoverCmd(cover string) tea.Cmd {
	if cover == "" {
		return nil
	}
	return func() tea.Msg {
		img, err := fetchCoverImage(tidal.CoverURL(cover, "640x640"))
		if err != nil {
			// Cover art is decorative — a 404 on one size variant, a
			// transient CDN hiccup — and now fetches on every queue-cursor
			// move (the AlbumArt pane is always visible), so this must not
			// interrupt the user with an error toast. The pane already
			// handles a nil image gracefully.
			logger.L.Debug("cover fetch failed", "cover", cover, "err", err)
			return nil
		}
		return coverLoadedMsg{key: cover, img: img}
	}
}

// maybeUpdateCover checks whether the cover for t differs from the currently
// cached key. If so it clears the cached image and returns a fetch command.
// If the key is the same (fetch already in-flight or image already loaded),
// it does nothing. Call this whenever m.currentTrack changes.
func (m *Model) maybeUpdateCover(t *tidal.Track) tea.Cmd {
	if t == nil {
		return nil
	}
	cover := t.Album.Cover
	if cover == "" || cover == m.coverCacheKey {
		return nil
	}
	m.coverImage = nil
	m.coverCacheKey = cover
	return fetchCoverCmd(cover)
}

// Update handles a message and then schedules a reconcile of the Kitty cover
// image.
//
// The graphics sync is funnelled through this one wrapper rather than the
// individual message handlers: update returns from dozens of places and the
// image must be reconciled after every one of them, not just the handful that
// obviously touch the cover.
//
// It is scheduled as a command rather than run inline because BubbleTea writes
// the new frame only *after* Update returns. Drawing inline would put the image
// on screen just in time for the text frame to paint over it; the command runs
// once that write has been issued.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	// The Cava animation tick fires every ~33ms and never touches a line the
	// AlbumArt column shares with anything else, so it's the one message
	// type excluded from forcing a Sixel redraw (see syncSixelCover) —
	// forcing it there too would mean writing the encoded image ~30x/sec.
	_, isBarTick := msg.(barTickMsg)
	sync := func() tea.Msg {
		nm.syncKittyCover()
		if nm.syncSixelCover(!isBarTick) {
			// Erasing the Sixel art blanks cells the text frame may already
			// own by the time this command runs — see syncSixelCover's erase
			// branch. A full repaint is the only way to put back whatever
			// those blanks landed on, and it costs one redraw per navigation
			// away from the cover, not per frame.
			return tea.ClearScreen()
		}
		return nil
	}
	if cmd == nil {
		return nm, sync
	}
	// Batch, not Sequence: the sync must not queue behind a slow command such
	// as a network fetch.
	return nm, tea.Batch(cmd, sync)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.rebuildProgress()
		// Force the cover to be re-emitted: a resize can leave stale Kitty
		// placements behind even when the cover box lands on the same cells.
		if m.kitty != nil {
			m.kitty.stale = true
		}

	case nowPlayingMsg:
		if msg.gen != m.skipGen {
			return m, nil // stale — a newer doPlayTrack superseded this one
		}
		m.advancing = false
		m.currentQuality = msg.quality
		// Update currentTrack with refreshed metadata (includes cover UUID).
		if msg.track != nil {
			m.currentTrack = msg.track
			_ = m.store.CacheTrack(msg.track.ID, *msg.track)
		}
		// Reset position and seed duration from track metadata so the progress
		// bar is correct before the first tick fires.
		if m.restorePosition == 0 {
			m.currPos = 0
		}
		if m.currentTrack != nil && m.currentTrack.Duration > 0 {
			m.duration = float64(m.currentTrack.Duration)
		}
		logger.L.Debug("nowPlayingMsg: seeded progress",
			"trackID", func() int {
				if m.currentTrack != nil {
					return m.currentTrack.ID
				}
				return 0
			}(),
			"currPos", m.currPos,
			"duration", m.duration,
			"restorePosition", m.restorePosition,
		)
		m.pushState()
		if m.currentTrack != nil {
			m.recordHistory(*m.currentTrack)
		}
		if m.restorePosition > 0 {
			pos := m.restorePosition
			m.restorePosition = 0
			_ = m.player.Seek(pos)
		}
		bars := m.ensureBarsTicking()
		cmds := make([]tea.Cmd, 0, 3+len(bars))
		cmds = append(cmds, waitForTrackDone(msg.done, msg.gen), m.maybeUpdateCover(m.coverTrack()))
		if m.currentTrack != nil && !m.headless {
			m.lyricsState = lyricsState{trackID: m.currentTrack.ID, loading: true}
			cmds = append(cmds, fetchLyricsCmd(m.ctx, m.store, *m.currentTrack))
		}
		cmds = append(cmds, bars...)
		return m, tea.Batch(cmds...)

	case coverLoadedMsg:
		if msg.key == m.coverCacheKey {
			m.coverImage = msg.img
		}

	case lyricsLoadedMsg:
		if m.currentTrack == nil || msg.trackID != m.currentTrack.ID {
			break // stale — the track changed while this fetch was in flight
		}
		if msg.err != nil || msg.result == nil {
			m.lyricsState = lyricsState{trackID: msg.trackID, notFound: true}
			break
		}
		m.lyricsState = lyricsState{
			trackID:  msg.trackID,
			lines:    msg.result.Lines,
			plain:    msg.result.Plain,
			notFound: !msg.result.Found,
		}

	case barTickMsg:
		// A liveness check here too, not just on the once-a-second tickMsg:
		// Configure() is cheap when the running instance is still alive (a
		// mutex lock and a non-blocking channel check), so paying that ~30x/s
		// is negligible, and it shortens the window a crashed/broken-pipe
		// cava process can sit undetected from up to a second down to about
		// one frame. Reported specifically after rapid seeking on a high
		// bitrate track — likely several stream reopens in quick succession
		// (see the interruptible-seek and read-ahead-prime work in
		// internal/player) — so recovering fast here matters more than usual.
		if m.cava != nil && !m.clientMode {
			if rate, channels := m.player.Format(); rate > 0 {
				m.cava.Configure(rate, channels, m.cavaTap)
			}
			m.cavaBars = m.cava.Bars(100)
		}
		if m.peak != nil {
			m.peakBars = m.peak.Levels(100)
		}
		// This tick only needs to run while playing — when stopped there's
		// nothing new to show and re-rendering ~30×/s is wasted work. Drop to
		// the 1s logo tick when idle; the 1s tick restarts this one when
		// playback resumes.
		if m.isPlaying {
			return m, barTickCmd()
		}
		m.barsTicking = false
		return m, nil

	case tickMsg:
		if m.isPlaying && !m.clientMode {
			m.currPos, _ = m.player.GetPosition()
			m.duration, _ = m.player.GetDuration()
			// Track the device actually opened: the plughw: fallback can
			// engage mid-session (e.g. on resume), and the badge/device
			// label must stop claiming bit-perfect output when it does.
			// dacModeActive distinguishes that real compromise from PipeWire
			// simply being the mode in use — see qualityBadge.
			m.activeDevice, m.bitPerfect, m.dacModeActive = m.player.AudioPath()
			logger.L.Debug("tick: progress state",
				"currPos", m.currPos,
				"duration", m.duration,
				"isPlaying", m.isPlaying,
				// The badge colours itself from these two; logging them is the
				// only way to tell a wrong colour from wrong inputs.
				"quality", m.currentQuality,
				"rate", m.currentRate,
				"bitPerfect", m.bitPerfect,
			)
			_ = m.store.SaveLastPosition(m.currPos)
			m.pushState()
			// Best-effort: (re)configure the meters for the current stream's
			// format. A no-op when already running with this rate/channels.
			rate, channels := m.player.Format()
			// Kept for the quality badge, which colours itself by the rate
			// actually reaching the device rather than the tier requested.
			m.currentRate = rate
			if m.cava != nil {
				m.cava.Configure(rate, channels, m.cavaTap)
			}
			if m.peak != nil {
				m.peak.Configure(channels, m.peakTap)
			}
		}
		cmds := []tea.Cmd{tickCmd()}
		// Restart the fast equaliser tick if playback resumed while it was idle
		// (covers client-mode playback and MPRIS-driven resume).
		if m.isPlaying {
			cmds = append(cmds, m.ensureBarsTicking()...)
		}
		if m.clientMode {
			cmds = append(cmds, pollParentState(m.mprisClient))
		}
		if cmd := m.maybePrefetchNext(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case parentStateMsg:
		ps := mpris.PlayerState(msg)
		// Update current track from parent.
		var coverCmd tea.Cmd
		if ps.CurrentTrackJSON != "" {
			var t tidal.Track
			if err := json.Unmarshal([]byte(ps.CurrentTrackJSON), &t); err == nil {
				// Crosses a D-Bus boundary rather than internal/tidal, so it
				// misses that package's sanitizing decoder.
				sanitize.Strings(&t)
				m.currentTrack = &t
				m.syncPlayingIndex()
				coverCmd = m.maybeUpdateCover(m.coverTrack())
			}
		} else {
			m.currentTrack = nil
		}
		// Update playlist ("My Music") from parent if non-empty.
		// Skip when the user has loaded a local playlist they haven't sent yet.
		if !m.localPlaylist && ps.PlaylistJSON != "" && ps.PlaylistJSON != "null" {
			var tracks []tidal.Track
			if err := json.Unmarshal([]byte(ps.PlaylistJSON), &tracks); err == nil && len(tracks) > 0 {
				sanitize.Strings(&tracks)
				// Only replace the list when it actually changed to avoid
				// clobbering the cursor position on every tick.
				if len(tracks) != len(m.tracks) || (len(tracks) > 0 && tracks[0].ID != m.tracks[0].ID) {
					m.tracksOrder = tracks
					m.applyShuffle() // re-syncs playingIndex against the new list
					// Don't yank the user out of a view they're actively browsing
					// (search results, artist view, device select). The updated
					// playlist is still applied underneath, so it's there when they
					// Tab back to Browse.
				}
			}
		}
		m.isPlaying = ps.PlaybackStatus == "Playing"
		m.currPos = ps.Position
		m.duration = ps.Duration
		if ps.Volume > 0 {
			m.volume = ps.Volume
		}
		if ps.Device != "" {
			m.currentDevice = ps.Device
		}
		// Mirror the parent's audio path so the client's badge and device
		// label describe the stream the parent is actually rendering.
		m.currentQuality = tidal.Quality(ps.Quality)
		m.activeDevice = ps.ActiveDevice
		m.bitPerfect = ps.BitPerfect
		if ps.ShuffleMode != "" {
			switch ps.ShuffleMode {
			case "Random":
				m.shuffleMode = ShuffleRandom
			case "Shuffle":
				m.shuffleMode = ShuffleFisherYates
			default:
				m.shuffleMode = ShuffleOff
			}
		}
		if coverCmd != nil {
			return m, coverCmd
		}

	case skipErrMsg:
		if msg.gen != m.skipGen {
			break
		}
		m.errText = msg.err.Error()
		m.advancing = false
		m.shufflePlayed = append(m.shufflePlayed, m.advanceBase())
		next := m.nextIndex()
		if next >= 0 {
			m.advancing = true
			m.cursor = next
			track := m.tracks[next]
			m.currPos = 0
			m.duration = 0
			_ = m.store.CacheTrack(track.ID, track)
			// Delay before retrying to avoid hammering the API when many
			// consecutive tracks fail (e.g. during rate-limiting).
			return m, tea.Batch(
				tea.Tick(2*time.Second, func(time.Time) tea.Msg {
					return playNextMsg{track: track, gen: m.skipGen}
				}),
				tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} }),
			)
		}
		m.isPlaying = false
		m.currentQuality = ""
		m.pushState()
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })

	case playbackFailedMsg:
		if msg.gen != m.skipGen {
			break // stale — a newer skip superseded this attempt
		}
		// Roll back the optimistic state doPlayTrack set before calling the
		// player, so the UI stops claiming a track is playing.
		m.errText = msg.err.Error()
		m.advancing = false
		m.isPlaying = false
		m.currentTrack = nil
		m.currPos = 0
		m.duration = 0
		m.pushState()
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })

	case playerPausedMsg:
		// The player paused itself; adopt its state rather than our own guess.
		m.isPlaying = false
		if msg.err != nil {
			m.errText = msg.err.Error()
		}
		m.pushState()
		return m, tea.Batch(
			m.watchPlayerPaused(),
			tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} }),
		)

	case playNextMsg:
		if msg.gen != m.skipGen {
			break // stale — a newer skip superseded this delayed advance
		}
		cmd := m.playNextTrackCmd(msg.track)
		return m, cmd

	case nextTrackPrefetchedMsg:
		if msg.gen != m.skipGen {
			break // stale — a newer skip superseded the track we prefetched for
		}
		m.prefetchedNextTrackID = msg.trackID
		m.prefetchedNextInfo = msg.info

	case trackDoneMsg:
		if msg.gen != m.skipGen {
			break // stale — from a track that was already skipped past
		}
		if !m.advancing {
			m.shufflePlayed = append(m.shufflePlayed, m.advanceBase())
			next := m.nextIndex()
			if next >= 0 {
				m.advancing = true
				m.cursor = next
				track := m.tracks[next]
				m.currPos = 0
				m.duration = 0
				_ = m.store.CacheTrack(track.ID, track)
				// Use maybePrefetchNext's cached resolve when it's actually
				// for this track — skips the GetStreamURL round-trip, which
				// is often too slow to land within playbackLoop's fixed
				// 5-second gapless handoff window if only started now, after
				// the previous track has already ended. Falls back to the
				// normal on-demand resolve otherwise (e.g. a short track the
				// lead time never got a chance to fire for).
				var cmd tea.Cmd
				if track.ID == m.prefetchedNextTrackID {
					cmd = m.playNextTrackFromPrefetchCmd(track, m.prefetchedNextInfo)
				} else {
					cmd = m.playNextTrackCmd(track)
				}
				return m, cmd
			}
			m.isPlaying = false
			// The queue is exhausted; drop the granted tier so the badge
			// doesn't linger describing the track that just finished.
			m.currentQuality = ""
			m.pushState()
		}

	case cachedPlaylistMsg:
		if len(msg) > 0 {
			m.tracksOrder = msg
			m.applyShuffle()
			m.playlistRestored = true
			if lastID, err := m.store.LoadLastTrackID(); err == nil && lastID != 0 {
				for i := range m.tracks {
					if m.tracks[i].ID == lastID {
						m.cursor = i
						break
					}
				}
			}
			if lastPos, err := m.store.LoadLastPosition(); err == nil && lastPos > 0 {
				m.restorePosition = lastPos
				m.currPos = lastPos
			}
		}

	case historyLoadedMsg:
		if len(m.history) == 0 {
			m.history = []tidal.Track(msg)
		}

	case favoritesLoadedMsg:
		tracks := []tidal.Track(msg)
		// Keep the favorite-songs list (for the Songs section) and the lookup
		// map (for the ♥ indicators) in sync.
		m.favSongs = tracks
		for i := range tracks {
			m.favorites[tracks[i].ID] = true
		}
		// Only replace the track list with favorites when no playlist was
		// restored from cache — otherwise we'd clobber the user's last session.
		if !m.playlistRestored {
			m.tracksOrder = tracks
			m.shuffleMode = ShuffleOff
			m.applyShuffle()
			m.section = SecQueue
			m.cursor = 0
			_ = m.store.SavePlaylist(m.tracks)
		}
		m.pushState()

	case searchResultsMsg:
		m.searchTracks = msg
		m.searchCursor = 0
		m.searchLoading = false
		m.searchInput.Blur()

	case searchGroupedMsg:
		m.searchResults = tidal.SearchResults(msg)
		m.searchCursor = 0
		m.searchLoading = false
		m.searchInput.Blur()

	case tracksMsg:
		m.tracksOrder = msg
		m.shuffleMode = ShuffleOff
		m.applyShuffle()
		// Record the queue's origin: a pending source (e.g. "radio") if one was
		// set before the request, otherwise this is an ad-hoc load with no
		// saved-playlist backing.
		m.queueSource = m.pendingQueueSource
		m.pendingQueueSource = ""
		m.queuePlaylistUUID = ""
		m.queueDirty = false
		// Don't yank focus away if the user is in the Search section.
		if m.section != SecSearch {
			m.section = SecQueue
			m.showArtist = false
			m.focusMain = true
			m.searchInput.Blur()
			m.cursor = 0
		}
		// In client mode, mark this as a local playlist so parentStateMsg
		// doesn't overwrite it before the user commits it to the server.
		if m.clientMode {
			m.localPlaylist = true
		}
		_ = m.store.SavePlaylist(m.tracks)
		m.pushState()
		// Every producer of tracksMsg represents the user choosing something
		// to listen to — an artist's "▶ Play all tracks"/"★ Top tracks",
		// radio, an album, a mix — so it should load AND play, the way Enter
		// on a saved playlist already does (see playPlaylistFrom), rather
		// than silently repopulating the queue and leaving a second Enter
		// press (on the new first track) as the only way to actually start
		// it. Reported specifically for the artist rows, whose "▶"/"★" icons
		// already implied playback was supposed to start.
		if len(m.tracks) > 0 {
			cmd := m.playTrackCmd(m.tracks[0])
			return m, cmd
		}
		return m, nil

	case favoriteMsg:
		if msg.added {
			m.favorites[msg.trackID] = true
		} else {
			delete(m.favorites, msg.trackID)
		}

	case favoriteAlbumAddedMsg:
		m.toast = fmt.Sprintf("✓ Added %q to favorite albums", msg.title)
		return m, toastClearCmd()

	case favoriteAlbumRemovedMsg:
		for i := range m.favAlbums {
			if m.favAlbums[i].ID == msg.albumID {
				m.favAlbums = append(m.favAlbums[:i], m.favAlbums[i+1:]...)
				break
			}
		}
		if m.cursor >= len(m.favAlbums) {
			m.cursor = max(len(m.favAlbums)-1, 0)
		}
		m.toast = fmt.Sprintf("✓ Removed %q from favorite albums", msg.title)
		return m, toastClearCmd()

	case favoriteArtistAddedMsg:
		m.toast = fmt.Sprintf("✓ Added %q to favorite artists", msg.name)
		return m, toastClearCmd()

	case favoriteArtistRemovedMsg:
		for i := range m.favArtists {
			if m.favArtists[i].ID == msg.artistID {
				m.favArtists = append(m.favArtists[:i], m.favArtists[i+1:]...)
				break
			}
		}
		if m.cursor >= len(m.favArtists) {
			m.cursor = max(len(m.favArtists)-1, 0)
		}
		m.toast = fmt.Sprintf("✓ Removed %q from favorite artists", msg.name)
		return m, toastClearCmd()

	case openURLTracksMsg:
		if len(msg) == 0 {
			break
		}
		m.tracksOrder = msg
		m.shuffleMode = ShuffleOff
		m.applyShuffle()
		m.section = SecQueue
		m.showArtist = false
		m.focusMain = true
		m.cursor = 0
		_ = m.store.SavePlaylist(m.tracks)
		// Auto-play the first track.
		track := m.tracks[0]
		_ = m.store.CacheTrack(track.ID, track)
		cmd := m.playTrackCmd(track)
		return m, cmd

	case mixesMsg:
		m.mixes = msg

	case playlistsMsg:
		m.playlists = msg
		sortPlaylists(m.playlists)
		m.playlistsStale = false

	case favArtistsMsg:
		m.favArtists = msg

	case favAlbumsMsg:
		m.favAlbums = msg

	case playlistDetailMsg:
		m.detailTracks = msg.tracks
		m.playlistName = msg.title
		m.detailCursor = 0
		m.detailFocus = true

	case artistAlbumsMsg:
		m.artistID = msg.artistID
		m.artistName = msg.artistName
		m.artistAlbums = msg.albums
		m.artistCursor = 0
		m.artistLoading = false
		m.artistAlbum = nil
		m.artistAlbumTracks = nil
		m.showArtist = true
		m.focusMain = true

	case artistAlbumTracksMsg:
		album := msg.album
		m.artistAlbum = &album
		m.artistAlbumTracks = msg.tracks
		m.artistAlbumCursor = 0
		m.artistLoading = false

	case errMsg:
		m.errText = msg.Error()
		// Clear the artist-view spinner if a fetch failed while loading it,
		// otherwise it would hang on "Loading artist...".
		m.artistLoading = false
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return clearErrMsg{} })

	case clearErrMsg:
		m.errText = ""

	case queueSavedMsg:
		m.queuePlaylistUUID = msg.uuid
		m.queueSource = "playlist:" + msg.name
		m.queueDirty = false
		m.applyPlaylistUpserted(msg.uuid, msg.name, msg.count, msg.created)
		m.toast = fmt.Sprintf("✓ Saved %q — %d tracks", msg.name, msg.count)
		return m, toastClearCmd()

	case playlistTracksAddedMsg:
		m.applyPlaylistUpserted(msg.uuid, msg.name, msg.count, msg.created)
		m.toast = fmt.Sprintf("✓ Added to %q — %d track(s)", msg.name, msg.count)
		return m, toastClearCmd()

	case playlistDeletedMsg:
		m.applyPlaylistDeleted(msg.uuid)
		m.toast = fmt.Sprintf("✓ Deleted playlist %q", msg.name)
		return m, toastClearCmd()

	case enqueuePlaylistMsg:
		for i := range msg.tracks {
			m.enqueueEnd(msg.tracks[i])
		}
		m.toast = fmt.Sprintf("✓ Added %q — %d tracks to queue", msg.title, len(msg.tracks))
		return m, toastClearCmd()

	case spotifyResolvedMsg:
		if m.overlay != OverlayImportSpotify {
			return m, nil // overlay was dismissed while resolving
		}
		if msg.err != nil {
			m.importStage = importStageInput
			m.importInput.Focus()
			m.importError = msg.err.Error()
			return m, nil
		}
		m.importSource = msg.src
		m.importRows = msg.rows
		m.importCursor = 0
		m.importError = ""
		m.importStage = importStageReview
		return m, nil

	case clearToastMsg:
		m.toast = ""

	case playPlaylistMsg:
		m.tracksOrder = msg.tracks
		m.shuffleMode = ShuffleOff
		m.applyShuffle()
		m.section = SecQueue
		m.showArtist = false
		m.focusMain = true
		m.searchInput.Blur()
		idx := msg.startIndex
		if idx < 0 || idx >= len(m.tracks) {
			idx = 0
		}
		m.cursor = idx
		_ = m.store.SavePlaylist(m.tracks)
		track := m.tracks[idx]
		_ = m.store.CacheTrack(track.ID, track)
		cmd := m.playTrackCmd(track)
		return m, cmd

	case mprisMsg:
		ev := mpris.Event(msg)
		switch ev.Cmd {
		case mpris.CmdPlayPause:
			// If nothing is playing (restored session), or the player was
			// explicitly Stopped, start the cursor track instead of resuming
			// whatever Stop parked — see togglePlay/stopPlayback in keys.go.
			if (m.currentTrack == nil || m.stopped) && len(m.tracks) > 0 {
				track := m.tracks[m.cursor]
				_ = m.store.CacheTrack(track.ID, track)
				return m, tea.Batch(m.playTrackCmd(track), listenMPRIS(m.mprisCh))
			}
			_ = m.player.Pause()
			// Read back the real state — see togglePlay in keys.go.
			m.isPlaying = !m.player.IsPaused()
		case mpris.CmdNext:
			m.shufflePlayed = append(m.shufflePlayed, m.advanceBase())
			next := m.nextIndex()
			if next >= 0 {
				m.advancing = true
				m.cursor = next
				track := m.tracks[next]
				m.currPos = 0
				m.duration = 0
				_ = m.store.CacheTrack(track.ID, track)
				return m, tea.Batch(m.playNextTrackCmd(track), listenMPRIS(m.mprisCh))
			}
		case mpris.CmdPrevious:
			prev := m.prevIndex()
			if prev >= 0 {
				m.advancing = false
				m.cursor = prev
				track := m.tracks[prev]
				m.currPos = 0
				m.duration = 0
				_ = m.store.CacheTrack(track.ID, track)
				return m, tea.Batch(m.playTrackCmd(track), listenMPRIS(m.mprisCh))
			}
		case mpris.CmdPlayTrackID:
			trackID := ev.TrackID
			return m, tea.Batch(
				func() tea.Msg {
					track, err := m.client.GetTrack(m.ctx, strconv.Itoa(trackID))
					if err != nil {
						return errMsg(err)
					}
					return openURLTracksMsg([]tidal.Track{*track})
				},
				listenMPRIS(m.mprisCh),
			)
		case mpris.CmdOpenURL:
			u := ev.URL
			return m, tea.Batch(
				func() tea.Msg {
					tracks, err := resolveQuery(m.ctx, m.client, m.store, u)
					if err != nil {
						return errMsg(err)
					}
					return openURLTracksMsg(tracks)
				},
				listenMPRIS(m.mprisCh),
			)
		case mpris.CmdPlayPlaylist:
			playlistJSON := ev.PlaylistJSON
			startIdx := ev.PlaylistStartIndex
			return m, tea.Batch(
				func() tea.Msg {
					var tracks []tidal.Track
					if err := json.Unmarshal([]byte(playlistJSON), &tracks); err != nil || len(tracks) == 0 {
						return errMsg(fmt.Errorf("invalid playlist from client: %w", err))
					}
					sanitize.Strings(&tracks)
					return playPlaylistMsg{tracks: tracks, startIndex: startIdx}
				},
				listenMPRIS(m.mprisCh),
			)
		case mpris.CmdSetDevice:
			m.currentDevice = ev.Device
			m.player.SetDevice(ev.Device)
			_ = m.store.SaveDevice(ev.Device)
		}
		return m, listenMPRIS(m.mprisCh)
	}

	if m.section == SecSearch {
		m.searchInput, cmd = m.searchInput.Update(msg)
	}

	return m, cmd
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	t := m.activeTheme()

	mainW := max(m.width, 1)
	bodyH := m.bodyHeight()

	main := m.renderMain(t, mainW, bodyH)
	tabBar := m.renderTabBar(t, m.width)

	parts := []string{tabBar, main}
	switch {
	case m.toast != "":
		parts = append(parts, t.GreenT.Render(" "+truncateStr(m.toast, m.width-2)))
	case m.errText != "":
		parts = append(parts, t.Err.Render(" ! "+truncateStr(m.errText, m.width-3)))
	}
	parts = append(parts,
		m.renderNowPlayingBar(t, m.width),
		m.footerKeyBar(t, m.width),
	)

	view := lipgloss.JoinVertical(lipgloss.Left, parts...)

	if m.overlay != OverlayNone {
		view = m.renderOverlay(t, view)
	}

	// The Kitty cover is NOT appended to the view: BubbleTea's standard
	// renderer truncates each line to the terminal width and skips lines
	// unchanged since the last frame, which mangles or silently drops a long
	// graphics escape. It is written straight to the TTY from Update instead
	// (see syncKittyCover).
	return view
}

// kittyCoverOverlay returns the Kitty escape that draws the cover image over the
// blank box reserved in the Now-Playing pane. Coordinates are 1-indexed screen
// cells derived from the layout: the main pane begins after the sidebar + gap,
// the panel border adds one column/row, and the cover box is the pane's first
// inner rows.
// clearGraphicsOverlays unconditionally erases any Kitty or Sixel image
// currently on screen, synchronously — regardless of which tab is active.
//
// This exists for quit(). The normal reconcile (syncKittyCover/
// syncSixelCover) only clears when coverBoxRect reports the art should not be
// shown, which is still "shown" right up until the moment the program exits —
// quitting doesn't change m.section. Worse, that reconcile runs as an async
// tea.Cmd batched alongside whatever command triggered it (see the Update
// wrapper), including tea.Quit itself, so there is no guarantee it completes
// its terminal write before BubbleTea tears down the alternate screen.
//
// That race is what left a cover image permanently smeared over the shell
// prompt after quitting: a Sixel image is rasterized directly into the
// terminal's character grid with no separate compositing layer, so a
// placement that lands after the alt screen is already disabled paints
// straight onto the primary screen the shell owns, and nothing ever overwrites
// those cells again until the user's own commands scroll past them. Kitty
// placements are deleted by ID and don't fail this way, but are cleared here
// too for symmetry, and so a placement isn't left wasting terminal memory.
func (m *Model) clearGraphicsOverlays() {
	if m.kitty != nil {
		m.kitty.mu.Lock()
		if m.kitty.drawnKey != "" {
			m.writeGfx(kittyClearCover())
			m.kitty.drawnKey = ""
		}
		m.kitty.mu.Unlock()
	}
	if m.sixel != nil {
		m.sixel.mu.Lock()
		if m.sixel.drawnKey != "" {
			m.writeGfx(sixelClearBox(m.sixel.drawnCol, m.sixel.drawnRow, m.sixel.drawnCols, m.sixel.drawnRows))
			m.sixel.drawnKey = ""
		}
		m.sixel.mu.Unlock()
	}
}

// syncKittyCover reconciles the on-screen Kitty cover with what the current
// model state says should be there, writing escapes straight to the TTY.
//
// This deliberately bypasses the View string. BubbleTea's standard renderer
// treats a frame as lines of text: it truncates each line to the terminal width
// and skips any line identical to the previous frame. A graphics escape is
// thousands of bytes on a single line and carries no visible width, so routing
// it through View meant it was regularly truncated or skipped outright — the
// image would fail to appear until an unrelated keypress changed that line's
// text, and stale placements were never cleared because the clear escape was
// dropped the same way.
//
// The expensive PNG encode still runs only when the cover or box geometry
// changes, and nothing is written when the desired state already matches what
// is on screen, so idle frames stay silent.
func (m *Model) syncKittyCover() {
	if !m.kittySupported || m.ttyOut == nil {
		return
	}
	if m.kitty == nil {
		// Constructed models allocate this up front; this covers a Model built
		// as a literal (tests) so graphics are not silently disabled.
		m.kitty = &kittyState{}
	}
	ks := m.kitty
	// Held across the whole reconcile, not just the write: the decision of what
	// to draw and the drawing of it must be atomic, or two concurrent syncs can
	// both observe the same "already drawn" state and emit overlapping
	// transmissions.
	ks.mu.Lock()
	defer ks.mu.Unlock()

	col, row, panelW, imgRows, ok := m.coverBoxRect()
	if ok {
		// coverBoxDims sized panelW/imgRows assuming a fixed cellAspect
		// (2.0); the terminal's real cell aspect ratio is rarely exactly
		// that. Kitty's placement (kittyPlaceAt) stretches the transmitted
		// square image to fill exactly panelW×imgRows cells, so passing it
		// the assumed-square box as-is would visibly distort the cover on
		// any terminal/font where the real ratio differs. Shrink to the
		// largest true square in real pixels first — the same fix
		// syncSixelCover already applies, just converted back to cell units
		// since Kitty's placement is cell-based, not pixel-based.
		cellW, cellH := cellPixelSize(m.ttyFd())
		sidePx := min(float64(panelW)*cellW, float64(imgRows)*cellH)
		panelW = max(int(sidePx/cellW), 1)
		imgRows = max(int(sidePx/cellH), 1)
	}
	if !ok || !m.useKittyCover() {
		// Cover should not be shown: clear it once, then stay quiet.
		if ks.drawnKey != "" || ks.stale {
			ks.drawnKey = ""
			ks.stale = false
			m.writeGfx(kittyClearCover())
		}
		return
	}

	key := fmt.Sprintf("%s@%dx%d+%d,%d", m.coverCacheKey, panelW, imgRows, col, row)
	if ks.drawnKey == key && !ks.stale {
		return // already on screen unchanged — write nothing
	}

	var out strings.Builder
	// Upload the image only when the cover itself changed. A resize keeps the
	// same pixels and only moves the box, so it re-places the image the
	// terminal already holds — a few dozen bytes instead of a fresh ~1MB
	// transmission per drag frame, which is what the terminal could not keep up
	// with (it rendered partially-received images).
	if ks.encodeKey != m.coverCacheKey {
		ks.escape = kittyTransmit(m.coverImage)
		ks.encodeKey = m.coverCacheKey
		out.WriteString(ks.escape)
	}
	// Delete the previous placement before re-placing: placements stack rather
	// than replace, so without this the old copy is stranded where the box used
	// to be. Everything goes out in one write so a delete cannot land without
	// the placement that replaces it.
	if ks.drawnKey != "" || ks.stale {
		out.WriteString(kittyClearCover())
	}
	out.WriteString(kittyPlaceAt(col, row, panelW, imgRows))

	if !m.writeGfx(out.String()) {
		// Nothing reached the terminal — don't record it as drawn, or the next
		// sync will skip the redraw and leave the box empty. The upload is
		// dropped from the cache too, since it may have been truncated.
		ks.encodeKey = ""
		ks.drawnKey = ""
		return
	}
	ks.drawnKey = key
	ks.stale = false
}

// syncSixelCover is syncKittyCover's Sixel counterpart — same out-of-band
// write, but two real differences from Kitty:
//
//  1. Sixel bakes its target pixel size into the encoded data itself (no
//     "upload once, place many times"), so a geometry change forces a full
//     re-encode, not just a cheap re-placement.
//  2. Sixel rasterizes straight into the character grid — there is no
//     separate compositing layer the way Kitty graphics has. BubbleTea's
//     renderer diffs and repaints whole *lines*, and the AlbumArt column is
//     horizontally joined onto the same lines as the Queue list and Cava
//     strip: whenever the queue's selected row (or anything else sharing a
//     line with the art) changes, BubbleTea repaints that entire line with
//     plain spaces — silently punching blank gaps into the image. forceRedraw
//     re-places the (already-encoded, so cheap) image on every such Update to
//     paint back over whatever BubbleTea's frame just clobbered; it is false
//     only for the high-frequency Cava animation tick, which never touches a
//     line the art shares with anything else.
//
// It reports whether it erased the image this call, which the caller must
// turn into a full repaint — see the erase branch below.
func (m *Model) syncSixelCover(forceRedraw bool) (erased bool) {
	if !m.sixelSupported || m.ttyOut == nil {
		return false
	}
	if m.sixel == nil {
		m.sixel = &sixelState{}
	}
	ss := m.sixel
	ss.mu.Lock()
	defer ss.mu.Unlock()

	col, row, cols, rows, ok := m.coverBoxRect()
	if !ok || !m.useSixelCover() {
		if ss.drawnKey != "" || ss.stale {
			out := sixelClearBox(ss.drawnCol, ss.drawnRow, ss.drawnCols, ss.drawnRows)
			ss.drawnKey = ""
			ss.stale = false
			m.writeGfx(out)
			// The erase paints blanks over cells BubbleTea believes it owns,
			// and this runs from a command — concurrently with the renderer
			// writing the frame for the very Update that navigated away. Win
			// that race and the blanks are harmlessly overpainted; lose it
			// and they land on top of the new tab as a black rectangle that
			// nothing repaints, since BubbleTea's line diff has no idea the
			// screen no longer matches its model. Hence the erase is reported
			// up so the caller can force a full repaint. (Kitty needs none of
			// this: deleting a placement by ID leaves the text layer alone.)
			return true
		}
		return false
	}

	// coverBoxDims chose cols/rows assuming a fixed cellAspect (2.0); the
	// terminal's *real* cell aspect ratio, queried below, is almost never
	// exactly that. Multiplying cols/rows by the real per-axis cell size
	// independently would bake that mismatch straight into a stretched
	// image, so instead take the largest true square (equal width and
	// height in real pixels) that fits within the reserved cell box — it
	// may not fill both dimensions of a box sized under the wrong
	// assumption, but it is never distorted.
	cellW, cellH := cellPixelSize(m.ttyFd())
	side := min(float64(cols)*cellW, float64(rows)*cellH)
	pxW, pxH := int(side), int(side)
	encodeKey := fmt.Sprintf("%s@%dx%d", m.coverCacheKey, pxW, pxH)

	moved := ss.drawnKey != encodeKey || ss.drawnCol != col || ss.drawnRow != row
	if !moved && !ss.stale && !forceRedraw {
		return erased // nothing changed and nothing else could have clobbered it
	}

	if ss.encodeKey != encodeKey {
		logger.L.Debug("sixel cover encode", "cols", cols, "rows", rows, "cellW", cellW, "cellH", cellH, "pxW", pxW, "pxH", pxH)
		ss.escape = sixelEncode(m.coverImage, pxW, pxH)
		ss.encodeKey = encodeKey
	}
	if ss.escape == "" {
		return erased
	}

	var out strings.Builder
	// Only clear-then-redraw when the box actually moved/resized (leaving a
	// stale copy behind at the old spot); a same-spot forced redraw just
	// re-rasterizes over the identical cells, no separate clear needed.
	if (moved || ss.stale) && ss.drawnKey != "" {
		out.WriteString(sixelClearBox(ss.drawnCol, ss.drawnRow, ss.drawnCols, ss.drawnRows))
	}
	out.WriteString(sixelPlaceAt(col, row, ss.escape))

	if !m.writeGfx(out.String()) {
		ss.encodeKey = ""
		ss.drawnKey = ""
		return erased
	}
	ss.drawnKey = encodeKey
	ss.drawnCol, ss.drawnRow, ss.drawnCols, ss.drawnRows = col, row, cols, rows
	ss.stale = false
	return false
}

// ttyFd returns the file descriptor backing ttyOut for ioctl queries
// (cellPixelSize), or -1 if it isn't a real file (e.g. a test's in-memory
// buffer) — cellPixelSize already falls back gracefully on an invalid fd.
func (m *Model) ttyFd() int {
	f, ok := m.ttyOut.(*os.File)
	if !ok {
		return -1
	}
	return int(f.Fd())
}

// writeGfx writes a graphics escape to the TTY, reporting whether it landed. A
// failed write is not surfaced to the user: the cover is decorative, and the
// terminal is the same channel any error message would have to travel over.
//
// The whole transmission is written in one call. io.WriteString on an *os.File
// already loops over short writes, but the sequence must not be split across
// calls: BubbleTea's renderer flushes frames to this same descriptor from its
// own goroutine, and a text frame landing between two halves of a Kitty
// transmission corrupts the image.
func (m *Model) writeGfx(s string) bool {
	if s == "" {
		return true
	}
	if _, err := io.WriteString(m.ttyOut, s); err != nil {
		logger.L.Debug("kitty cover write failed", "err", err)
		return false
	}
	return true
}

// coverBoxRect returns the 1-indexed screen position and cell size of the
// square AlbumArt box for the active tab, and whether it is shown at all. The
// Queue tab places it at the top of the left column, below the tab bar.
func (m *Model) coverBoxRect() (col, row, cols, rows int, ok bool) {
	// showArtist is a transient overlay on top of whichever section opened
	// it — most often Queue — and does not change m.section itself
	// (openArtistFor saves it in prevSection to restore later). Checking
	// m.section alone meant this kept reporting the Queue's art box as
	// on-screen while the artist drill-down was actually showing, so a
	// stale Sixel cover — which has no compositing layer, just cells it was
	// rasterized into — stayed rendered on top of the drill-down's own,
	// differently laid out screen instead of being cleared.
	if m.section != SecQueue || m.showArtist {
		return 0, 0, 0, 0, false
	}
	g := m.queueLayout(max(m.width, 1), m.bodyHeight())
	if !g.showLeft || g.albumArtCols <= 0 || g.albumArtRows <= 0 {
		return 0, 0, 0, 0, false
	}
	innerW := max(g.leftW-2, 1)
	padLeft := max((innerW-g.albumArtCols)/2, 0)
	col = 1 /*1-indexed*/ + 1 /*panel left border*/ + padLeft
	row = tabBarH + 1 /*panel top border*/ + 1 /*1-indexed*/
	return col, row, g.albumArtCols, g.albumArtRows, true
}

// footerKeyBar returns the context-sensitive key hint bar for the current
// section.
func (m *Model) footerKeyBar(t Theme, w int) string {
	base := [][2]string{
		{"1-9", "Tabs"},
		{"j/k", "Move"},
		{"↵", "Play"},
		{"p", "Pause"},
		{"Ctrl+X", "Actions"},
		{":", "Command"},
		{"/", "Search"},
		{"?", "Help"},
		{"q", "Quit"},
	}
	switch m.section {
	case SecQueue:
		base = [][2]string{
			{"j/k", "Move"},
			{"↵", "Play"},
			{"d", "Remove"},
			{"D", "Clear"},
			{"Ctrl+S", "Save"},
			{"Ctrl+X", "Actions"},
			{":", "Command"},
			{"q", "Quit"},
		}
	case SecPlaylists:
		base = [][2]string{
			{"j/k", "Move"},
			{"↵", "Open"},
			{"a", "Add to queue"},
			{"d", "Delete"},
			{":", "Command"},
			{"q", "Quit"},
		}
	case SecSearch:
		base = [][2]string{
			{"↵", "Search/Play"},
			{"j/k", "Move"},
			{"Ctrl+X", "Actions"},
			{"F", "Fav"},
			{":", "Command"},
			{"q", "Quit"},
		}
	case SecSettings:
		base = [][2]string{
			{"j/k", "Move"}, {"↵", "Select"}, {"t", "Cycle theme"}, {"Esc", "Back"}, {"q", "Quit"},
		}
	default:
	}
	return renderKeyBar(t, base, w)
}

// renderOverlay composites the active overlay popup over a dimmed base view.
// Step 3 wires only the device-select overlay; later steps add the others.
func (m *Model) renderOverlay(t Theme, base string) string {
	var popup string
	anchorCentered := true
	switch m.overlay {
	case OverlayDeviceSelect:
		popup = m.renderDeviceSelect(t)
	case OverlayCommandPalette:
		popup = m.renderCommandPalette(t)
	case OverlayAddToPlaylist:
		popup = m.renderAddToPlaylist(t)
	case OverlayNewPlaylistName:
		popup = m.renderNewPlaylistName(t)
	case OverlayDeletePlaylist:
		popup = m.renderDeletePlaylist(t)
	case OverlayImportSpotify:
		popup = m.renderImportSpotify(t)
	case OverlayActionSheet:
		popup = m.renderActionSheet(t)
		anchorCentered = false
	case OverlayHelp:
		popup = m.renderHelpOverlay(t)
	case OverlaySongInfo:
		popup = m.renderSongInfoOverlay(t)
	case OverlayThemePicker:
		popup = m.renderThemePickerOverlay(t)
	default:
		return base
	}
	if popup == "" {
		return base
	}
	pw := lipgloss.Width(popup)
	ph := strings.Count(popup, "\n") + 1

	x := max((m.width-pw)/2, 0)
	y := max((m.height-ph)/2, 0)
	if !anchorCentered {
		// Anchor the action sheet near the selected row: near the left edge,
		// vertically tracking the cursor but clamped on-screen.
		x = min(4, max(m.width-pw-1, 0))
		y = min(max(m.cursor+2, 1), max(m.height-ph-2, 1))
	}
	return PlaceOverlay(x, y, popup, dim(t, base))
}

// renderMain renders the main content pane for the current section, sized to
// exactly w columns by h rows.
func (m *Model) renderMain(t Theme, w, h int) string {
	// The transient artist drill-down overlays whatever section is active.
	if m.artistViewActive() {
		return m.renderArtistPane(t, w, h)
	}
	switch m.section {
	case SecMixes:
		return m.renderMixesPane(t, w, h)
	case SecSearch:
		return m.renderSearchPane(t, w, h)
	case SecQueue:
		return m.renderQueuePane(t, w, h)
	case SecFavSongs:
		return m.renderFavSongsPane(t, w, h)
	case SecPlaylists:
		return m.renderPlaylistsPane(t, w, h)
	case SecFavArtists:
		return m.renderFavArtistsPane(t, w, h)
	case SecFavAlbums:
		return m.renderFavAlbumsPane(t, w, h)
	case SecHistory:
		return m.renderHistoryPane(t, w, h)
	case SecSettings:
		return m.renderSettingsList(t, w, h)
	default:
		return renderPanel(t, sectionTitle(m.section), m.focusMain, w, h,
			t.RowDim.Render("Coming soon."))
	}
}
