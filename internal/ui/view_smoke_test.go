package ui

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carcuevas/gotidal/internal/store"
	"github.com/carcuevas/gotidal/internal/tidal"
)

// asModel is a test helper for the (tea.Model -> Model) assertion.
func asModel(t *testing.T, m tea.Model) Model {
	t.Helper()
	mm, ok := m.(Model)
	if !ok {
		t.Fatalf("expected ui.Model, got %T", m)
	}
	return mm
}

const srcRadio = "radio"

// newSmokeModel builds a Model without the store/client/player dependencies so
// the render path can be exercised in isolation.
func newSmokeModel() Model {
	pal := paletteGoTidal
	ti := textinput.New()
	tracks := []tidal.Track{
		{ID: 1, Title: "May These Noises", Artist: tidal.Artist{ID: 9, Name: "Pierce The Veil"}, Duration: 78},
		{ID: 2, Title: "Hell Above", Artist: tidal.Artist{ID: 9, Name: "Pierce The Veil"}, Duration: 212},
		{ID: 3, Title: "King For A Day", Artist: tidal.Artist{ID: 9, Name: "Pierce The Veil"}, Duration: 230},
	}
	return Model{
		store:       &store.SecretsStore{}, // nil db => Save* methods no-op
		searchInput: ti,
		section:     SecQueue,
		focusMain:   true,
		volume:      80,
		themeName:   "gotidal",
		palette:     pal,
		theme:       pal.Theme(),
		progress:    progressWithTheme(pal.Theme(), 40),
		favorites:   map[int]bool{2: true},
		tracks:      tracks,
		tracksOrder: tracks,
		mixes: []tidal.Mix{
			{ID: "m1", Title: "Daily Mix 1", SubTitle: "Pierce The Veil, …"},
		},
	}
}

// TestViewRendersAllSectionsAndSizes asserts View() never panics across every
// section, overlay, and a range of terminal sizes (including degenerate ones).
func TestViewRendersAllSectionsAndSizes(t *testing.T) {
	sections := []Section{
		SecNowPlaying, SecQueue, SecPlaylists, SecFavSongs, SecFavArtists,
		SecFavAlbums, SecHistory, SecMixes, SecSearch, SecSettings,
	}
	overlays := []Overlay{OverlayNone, OverlayDeviceSelect, OverlayCommandPalette, OverlayActionSheet}
	sizes := [][2]int{{120, 40}, {80, 24}, {60, 20}, {40, 12}, {30, 10}, {20, 6}, {1, 1}, {0, 0}}

	for _, sec := range sections {
		for _, ov := range overlays {
			for _, sz := range sizes {
				m := newSmokeModel()
				m.section = sec
				m.overlay = ov
				m.width, m.height = sz[0], sz[1]
				if ov == OverlayActionSheet && len(m.tracks) > 0 {
					tr := m.tracks[0]
					m.sheetTrack = &tr
				}
				if ov == OverlayCommandPalette {
					m.openCommandPalette()
				}
				// Should not panic.
				out := m.View()
				_ = out
			}
		}
	}
}

// TestViewSidebarFocus exercises the (now-inert) focusMain flag and the
// artist drill-down, guarding against a render panic either way.
func TestViewSidebarFocus(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 100, 30

	m.focusMain = false
	_ = m.View()

	m.focusMain = true
	m.showArtist = true
	m.artistName = "Pierce The Veil"
	m.artistAlbums = []tidal.Album{{ID: 1, Title: "Collide With The Sky", ReleaseDate: "2012-07-17", NumberOfTracks: 13}}
	if !strings.Contains(stripANSI(m.View()), "Collide With The Sky") {
		t.Errorf("artist pane should list the album title")
	}
}

// TestActionSheetRenders confirms the action sheet lists its actions over the
// queue without panic, at several cursor positions.
func TestActionSheetRenders(t *testing.T) {
	for _, cur := range []int{0, 4, 8} {
		m := newSmokeModel()
		m.width, m.height = 96, 26
		tr := m.tracks[1]
		m.sheetTrack = &tr
		m.sheetCursor = cur
		m.overlay = OverlayActionSheet
		out := stripANSI(m.View())
		for _, want := range []string{"Play now", "Add to queue", "Start radio", "Copy Tidal link"} {
			if !strings.Contains(out, want) {
				t.Errorf("cursor %d: action sheet missing %q", cur, want)
			}
		}
	}
}

// TestCommandPaletteFilterAndJump verifies fuzzy filtering and a "jump to"
// command switching sections.
func TestCommandPaletteFilterAndJump(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 96, 26
	m.openCommandPalette()
	m.paletteInput.SetValue("mixes")
	items := filterPaletteItems(m.paletteInput.Value())
	if len(items) == 0 {
		t.Fatalf("expected at least one match for %q", "mixes")
	}
	out := stripANSI(m.renderCommandPalette(m.theme))
	if !strings.Contains(out, "Go to Mixes") {
		t.Errorf("palette should show the Mixes jump entry, got:\n%s", out)
	}

	// Selecting the first match should switch sections and close the overlay.
	res, _ := items[0].run(m)
	nm, ok := res.(Model)
	if !ok {
		t.Fatalf("palette run should return a Model, got %T", res)
	}
	if nm.overlay != OverlayNone {
		t.Errorf("running a palette item should close the overlay")
	}
	if nm.section != SecMixes {
		t.Errorf("jump-to-mixes should select SecMixes, got %v", nm.section)
	}
}

// TestLibrarySectionsRender populates the favorites/playlists/history sections
// and renders each, asserting their content appears.
func TestLibrarySectionsRender(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 104, 28
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Late Night Drive", NumberOfTracks: 23}}
	m.openPlaylist = &m.playlists[0]
	m.playlistName = "Late Night Drive"
	m.detailTracks = m.tracks
	m.favArtists = []tidal.Artist{{ID: 9, Name: "Pierce The Veil"}}
	m.favAlbums = []tidal.Album{{ID: 5, Title: "Collide With The Sky", ReleaseDate: "2012-01-01", NumberOfTracks: 13}}
	m.history = m.tracks

	cases := []struct {
		sec  Section
		want string
	}{
		{SecPlaylists, "Late Night Drive"},
		{SecFavArtists, "Pierce The Veil"},
		{SecFavAlbums, "Collide With The Sky"},
		{SecHistory, "May These Noises"},
	}
	for _, c := range cases {
		m.section = c.sec
		out := stripANSI(m.View())
		if !strings.Contains(out, c.want) {
			t.Errorf("section %v should contain %q", c.sec, c.want)
		}
	}
}

// TestMaybePrefetchNext verifies the proactive next-track prefetch only
// fires within prefetchLeadSec of the current track ending, and only once
// per upcoming track (not on every tick while it's still in flight/cached).
func TestMaybePrefetchNext(t *testing.T) {
	m := newSmokeModel()
	m.cursor = 0
	next := m.nextIndex()
	if next < 0 {
		t.Fatal("smoke model needs at least two tracks for this test")
	}
	wantID := m.tracks[next].ID

	m.duration = 200
	m.currPos = 150 // 50s remaining — well outside prefetchLeadSec
	if cmd := m.maybePrefetchNext(); cmd != nil {
		t.Errorf("should not prefetch before the lead time, got a non-nil cmd")
	}
	if m.prefetchedNextTrackID != 0 {
		t.Errorf("should not have touched prefetchedNextTrackID yet, got %d", m.prefetchedNextTrackID)
	}

	m.currPos = 200 - prefetchLeadSec + 1 // just inside the lead time
	cmd := m.maybePrefetchNext()
	if cmd == nil {
		t.Fatal("should prefetch once within the lead time")
	}
	if m.prefetchedNextTrackID != wantID {
		t.Errorf("prefetchedNextTrackID = %d, want %d (the actual next track)", m.prefetchedNextTrackID, wantID)
	}

	if cmd := m.maybePrefetchNext(); cmd != nil {
		t.Errorf("should not re-trigger a prefetch already cached/in flight for this track")
	}
}

// TestQueueHeaderShowsTotalDuration verifies the Queue header includes the
// track count and total duration (78+212+230s = 8:40 for the smoke model's
// three tracks), not just the bare "QUEUE" title or playlist-sync status.
func TestQueueHeaderShowsTotalDuration(t *testing.T) {
	m := newSmokeModel()
	got := m.queueHeader(m.theme)
	for _, want := range []string{"3 tracks", "8:40"} {
		if !strings.Contains(got, want) {
			t.Errorf("queueHeader() = %q, want it to contain %q", got, want)
		}
	}
}

// TestRenderTrackRowShowsAlbum verifies showAlbum appends the album title
// after the artist (and only when showArtist is also set — an album name
// with no artist to anchor it to would be a dangling " — ").
func TestRenderTrackRowShowsAlbum(t *testing.T) {
	th := paletteGoTidal.Theme()
	tr := tidal.Track{
		Title:  "Rot",
		Artist: tidal.Artist{Name: "Lacey Sturm"},
	}
	tr.Album.Title = "Life Screams"
	out := stripANSI(renderTrackRow(th, tr, rowOpts{showArtist: true, showAlbum: true, width: 80}))
	if !strings.Contains(out, "Lacey Sturm") || !strings.Contains(out, "Life Screams") {
		t.Errorf("renderTrackRow with showAlbum should show both artist and album, got %q", out)
	}

	out = stripANSI(renderTrackRow(th, tr, rowOpts{showArtist: false, showAlbum: true, width: 80}))
	if strings.Contains(out, "Life Screams") {
		t.Errorf("showAlbum without showArtist should not show the album, got %q", out)
	}
}

// TestQueueHybridStates checks the queue header reflects synced/edited/unsaved
// origins and that an enqueue marks the queue dirty.
func TestQueueHybridStates(t *testing.T) {
	m := newSmokeModel()
	th := m.theme

	m.queueSource = "playlist:Late Night"
	m.queuePlaylistUUID = "p1"
	m.queueDirty = false
	if got := stripANSI(m.queueHeader(th)); !strings.Contains(got, "synced") {
		t.Errorf("synced header: %q", got)
	}

	m.enqueueEnd(m.tracks[0])
	if !m.queueDirty {
		t.Errorf("enqueue should mark the queue dirty")
	}
	if got := stripANSI(m.queueHeader(th)); !strings.Contains(got, "edited") {
		t.Errorf("edited header: %q", got)
	}

	m.queueSource = srcRadio
	m.queuePlaylistUUID = ""
	m.queueDirty = false
	if got := stripANSI(m.queueHeader(th)); !strings.Contains(got, "unsaved") {
		t.Errorf("radio header: %q", got)
	}
}

// TestQueueSavedMsg confirms a save confirmation flips origin to a synced
// playlist and raises the toast.
func TestQueueSavedMsg(t *testing.T) {
	m := newSmokeModel()
	m.queueSource = srcRadio
	updated, _ := m.Update(queueSavedMsg{uuid: "new", name: "My Mix", count: 3})
	nm, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update should return a Model")
	}
	if nm.queuePlaylistUUID != "new" || nm.queueDirty || nm.queueSource != "playlist:My Mix" {
		t.Errorf("unexpected post-save state: src=%q uuid=%q dirty=%v", nm.queueSource, nm.queuePlaylistUUID, nm.queueDirty)
	}
	if !strings.Contains(nm.toast, "My Mix") {
		t.Errorf("expected save toast, got %q", nm.toast)
	}
}

// TestGroupedSearch renders grouped results and verifies the flattened cursor
// activates the right entity (artist row → drill-down).
func TestGroupedSearch(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 100, 26
	m.section = SecSearch
	m.searchInput.Blur()
	m.searchResults = tidal.SearchResults{
		Tracks:  []tidal.Track{{ID: 1, Title: "King For A Day", Artist: tidal.Artist{Name: "PTV"}}},
		Artists: []tidal.Artist{{ID: 9, Name: "Pierce The Veil"}},
		Albums:  []tidal.Album{{ID: 5, Title: "Collide With The Sky", NumberOfTracks: 13}},
	}
	out := stripANSI(m.renderSearchPane(m.theme, 80, 20))
	for _, want := range []string{"SONGS", "ARTISTS", "ALBUMS", "Collide With The Sky"} {
		if !strings.Contains(out, want) {
			t.Errorf("grouped search missing %q", want)
		}
	}

	// Flattened order is tracks, artists, albums: index 1 is the artist.
	m.searchCursor = 1
	res, _ := m.activateSearchRow()
	nm, ok := res.(Model)
	if !ok {
		t.Fatalf("activate should return a Model")
	}
	if !nm.showArtist {
		t.Errorf("activating an artist row should open the artist drill-down")
	}
}

// TestSearchPlaylistRow verifies search results include a Playlists group and
// that "a" on a playlist row triggers an enqueue command rather than being
// silently ignored (playlists have no single track for commonKeys to find).
func TestSearchPlaylistRow(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 100, 26
	m.section = SecSearch
	m.searchInput.Blur()
	m.searchResults = tidal.SearchResults{
		Tracks:    []tidal.Track{{ID: 1, Title: "King For A Day", Artist: tidal.Artist{Name: "PTV"}}},
		Playlists: []tidal.Playlist{{UUID: "pl1", Title: "Warped Tour Essentials", NumberOfTracks: 40}},
	}
	out := stripANSI(m.renderSearchPane(m.theme, 80, 20))
	for _, want := range []string{"PLAYLISTS", "Warped Tour Essentials"} {
		if !strings.Contains(out, want) {
			t.Errorf("grouped search missing %q", want)
		}
	}

	// Flattened order is tracks then playlists: index 1 is the playlist.
	m.searchCursor = 1
	res, cmd := m.updateSearchKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Errorf("\"a\" on a playlist row should return an enqueue command")
	}
	if _, ok := res.(Model); !ok {
		t.Fatalf("expected ui.Model, got %T", res)
	}
}

// TestPlaylistTabEnqueueAll verifies "a" on the Playlists tab's list (not yet
// drilled into a playlist's detail view) enqueues that playlist directly.
func TestPlaylistTabEnqueueAll(t *testing.T) {
	m := newSmokeModel()
	m.section = SecPlaylists
	m.detailFocus = false
	m.playlists = []tidal.Playlist{{UUID: "p1", Title: "Late Night Drive", NumberOfTracks: 23}}
	m.cursor = 0

	res, cmd := m.updatePlaylists(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Errorf("\"a\" on a playlist row should return an enqueue command")
	}
	if _, ok := res.(Model); !ok {
		t.Fatalf("expected ui.Model, got %T", res)
	}
}

// TestThemePickerPreviewCommitRevert verifies the floating Themes popup
// live-previews on move, commits on Enter, and reverts on Esc.
func TestThemePickerPreviewCommitRevert(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 96, 24
	m.section = SecSettings
	m.enterSettings()
	m.themeCursor = 4 // Themes row
	m.openThemePicker()
	if m.overlay != OverlayThemePicker {
		t.Fatalf("openThemePicker should raise OverlayThemePicker, got %v", m.overlay)
	}
	if paletteOrder[m.themePickerIndex] != "gotidal" {
		t.Fatalf("openThemePicker should land on the active theme, got %q", paletteOrder[m.themePickerIndex])
	}

	// Move down → live preview set, but committed theme unchanged.
	res, _ := m.updateThemePickerOverlay(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, res)
	if m.previewPalette == nil {
		t.Errorf("moving the cursor should set a live preview")
	}
	if m.themeName != "gotidal" {
		t.Errorf("preview must not commit the theme yet")
	}

	// Esc → revert and close.
	res, _ = m.updateThemePickerOverlay(tea.KeyMsg{Type: tea.KeyEsc})
	m = asModel(t, res)
	if m.previewPalette != nil {
		t.Errorf("Esc should cancel the preview")
	}
	if m.overlay != OverlayNone {
		t.Errorf("Esc should close the Themes popup")
	}

	// Reopen, move + Enter → commit and close.
	m.openThemePicker()
	res, _ = m.updateThemePickerOverlay(tea.KeyMsg{Type: tea.KeyDown})
	m = asModel(t, res)
	committed := paletteOrder[m.themePickerIndex]
	res, _ = m.updateThemePickerOverlay(tea.KeyMsg{Type: tea.KeyEnter})
	m = asModel(t, res)
	if m.themeName != committed {
		t.Errorf("Enter should commit %q, got %q", committed, m.themeName)
	}
	if m.previewPalette != nil {
		t.Errorf("commit should clear the preview")
	}
	if m.overlay != OverlayNone {
		t.Errorf("Enter should close the Themes popup")
	}
}

// TestNoBrokenGlyphsAndDurations guards the ANSI-aware truncation: styled rows
// must never be chopped mid-escape (which produces U+FFFD) and the duration
// column must survive even when titles are long.
func TestNoBrokenGlyphsAndDurations(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 100, 16
	m.currentTrack = &tidal.Track{ID: 1, Title: "A Very Long Track Title That Should Be Truncated Hard", Artist: tidal.Artist{Name: "Some Artist"}, Duration: 412}
	m.isPlaying = true
	m.currPos, m.duration = 90, 412

	out := m.View()
	if strings.ContainsRune(out, '�') {
		t.Errorf("output contains the replacement glyph — a styled string was truncated mid-escape")
	}
	// Durations of the seeded queue tracks must be present.
	for _, want := range []string{"1:18", "3:32", "3:50"} {
		if !strings.Contains(stripANSI(out), want) {
			t.Errorf("queue row should show duration %q", want)
		}
	}
}

// TestRowDurationSurvivesNarrow asserts the duration column survives a narrow
// pane (the title is truncated instead).
func TestRowDurationSurvivesNarrow(t *testing.T) {
	tr := tidal.Track{Title: "An Extremely Long Song Title That Will Not Fit", Artist: tidal.Artist{Name: "Artist Name Here"}, Duration: 245}
	row := stripANSI(renderTrackRow(paletteGoTidal.Theme(), tr, rowOpts{showIndex: true, index: 1, showArtist: true, width: 40, duration: 245}))
	if !strings.Contains(row, "4:05") {
		t.Errorf("duration should survive narrow width, got %q", row)
	}
}

// TestQueueCoverComposes renders the Queue with a cover image and asserts the
// layout stays a clean rectangle (no Kitty-style bleed): every visible line
// must be the same display width.
func TestQueueCoverComposes(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 120, 120))
	for y := range 120 {
		for x := range 120 {
			if (x+y)/8%2 == 0 {
				img.Set(x, y, color.RGBA{230, 180, 90, 255})
			} else {
				img.Set(x, y, color.RGBA{40, 60, 110, 255})
			}
		}
	}
	m := newSmokeModel()
	m.width, m.height = 120, 28
	m.section = SecQueue
	m.coverImage = img
	m.cursor = 1
	m.isPlaying = true
	m.currPos, m.duration = 130, 441

	out := m.View()
	if strings.ContainsRune(out, '�') {
		t.Errorf("cover render produced a replacement glyph")
	}
	// All non-empty lines must share one width (rectangular composition).
	width := -1
	for ln := range strings.SplitSeq(out, "\n") {
		w := lipgloss.Width(ln)
		if w == 0 {
			continue
		}
		if width == -1 {
			width = w
		} else if w != width {
			t.Fatalf("ragged layout: line width %d != %d (cover art bled outside the pane)", w, width)
		}
	}
}

// TestFavSongsDistinctFromQueue ensures the Songs section shows the favorite
// songs, not the playback queue.
func TestFavSongsDistinctFromQueue(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 90, 16
	m.favSongs = []tidal.Track{{ID: 99, Title: "A Liked Song", Artist: tidal.Artist{Name: "Fav Artist"}}}
	m.section = SecFavSongs
	out := stripANSI(m.View())
	if !strings.Contains(out, "A Liked Song") {
		t.Errorf("Songs section should list favorite songs")
	}
	if strings.Contains(out, "May These Noises") {
		t.Errorf("Songs section must not show the queue tracks")
	}
	if got := m.sectionCount(SecFavSongs); got != 1 {
		t.Errorf("favorite songs count = %d, want 1", got)
	}
}

// TestRemoveFromQueue drops the cursor track and marks the queue edited.
func TestRemoveFromQueue(t *testing.T) {
	m := newSmokeModel()
	before := len(m.tracks)
	id := m.tracks[1].ID
	m.cursor = 1
	m.removeFromQueue(1)
	if len(m.tracks) != before-1 {
		t.Fatalf("expected %d tracks after removal, got %d", before-1, len(m.tracks))
	}
	for _, tr := range m.tracks {
		if tr.ID == id {
			t.Errorf("removed track %d is still in the queue", id)
		}
	}
	if !m.queueDirty {
		t.Errorf("removal should mark the queue dirty")
	}
}

// TestHistoryEnterLoadsQueue ensures playing from Recently Played loads the
// history into the queue (so auto-advance has something to follow), not just a
// single track.
func TestHistoryEnterLoadsQueue(t *testing.T) {
	m := newSmokeModel()
	m.tracks = nil
	m.tracksOrder = nil
	m.section = SecHistory
	m.history = []tidal.Track{
		{ID: 10, Title: "H1"}, {ID: 11, Title: "H2"}, {ID: 12, Title: "H3"},
	}
	m.cursor = 1
	res, _ := m.updateHistory(tea.KeyMsg{Type: tea.KeyEnter})
	nm := asModel(t, res)
	if len(nm.tracks) != len(m.history) {
		t.Fatalf("history play should load %d tracks into the queue, got %d", len(m.history), len(nm.tracks))
	}
	if nm.cursor != 1 || nm.tracks[1].ID != 11 {
		t.Errorf("queue should be positioned at the chosen track (id 11), got cursor %d id %d", nm.cursor, nm.tracks[nm.cursor].ID)
	}
}

// TestArtistAlbumDrill verifies opening an album inside the artist view shows
// its tracks, and playing one loads the album into the queue.
func TestArtistAlbumDrill(t *testing.T) {
	m := newSmokeModel()
	m.width, m.height = 100, 20
	m.showArtist = true
	m.artistName = "Lacey Sturm"
	m.artistAlbum = &tidal.Album{ID: 5, Title: "Life Screams"}
	m.artistAlbumTracks = []tidal.Track{{ID: 1, Title: "Mercy"}, {ID: 2, Title: "Rot"}}

	if !strings.Contains(stripANSI(m.renderArtistPane(m.theme, 80, 14)), "Mercy") {
		t.Errorf("album sub-view should list the album's tracks")
	}

	m.artistAlbumCursor = 1
	res, _ := m.updateArtist(tea.KeyMsg{Type: tea.KeyEnter})
	nm := asModel(t, res)
	if nm.section != SecQueue {
		t.Errorf("playing an album track should switch to the queue")
	}
	if len(nm.tracks) != 2 || nm.cursor != 1 {
		t.Errorf("album should load into the queue at the chosen track, got %d tracks cursor %d", len(nm.tracks), nm.cursor)
	}
	if nm.showArtist || nm.artistAlbum != nil {
		t.Errorf("the artist drill-down should close after playing")
	}
}

// TestClearQueue empties the queue and stops whatever was playing — an
// empty queue has no "current track" left for Lyrics/AlbumArt to show.
func TestClearQueue(t *testing.T) {
	m := newSmokeModel()
	if len(m.tracks) == 0 {
		t.Fatal("precondition: queue should be non-empty")
	}
	m.currentTrack = &m.tracks[0]
	m.isPlaying = true
	m.coverImage = image.NewRGBA(image.Rect(0, 0, 8, 8))
	m.coverCacheKey = "some-cover-uuid"
	m.lyricsState = lyricsState{trackID: m.tracks[0].ID, plain: "some lyrics"}
	m.cavaBars = []int{50, 60, 70}
	m.peakBars = []int{40, 45}
	m.clearQueue()
	if len(m.tracks) != 0 || len(m.tracksOrder) != 0 {
		t.Errorf("clearQueue should empty the queue, got %d/%d", len(m.tracks), len(m.tracksOrder))
	}
	if m.queueSource != "" || m.queueDirty {
		t.Errorf("clearQueue should reset queue origin")
	}
	if m.currentTrack != nil {
		t.Errorf("clearQueue should stop the current track, currentTrack is still set")
	}
	if m.isPlaying {
		t.Errorf("clearQueue should stop playback")
	}
	if m.coverImage != nil || m.coverCacheKey != "" {
		t.Errorf("clearQueue should clear the stale cover — AlbumArt's ASCII fallback renders m.coverImage directly, not gated on currentTrack")
	}
	if m.lyricsState.trackID != 0 || m.lyricsState.plain != "" {
		t.Errorf("clearQueue should clear the stale lyrics — the Lyrics pane renders m.lyricsState directly, not gated on currentTrack, got %+v", m.lyricsState)
	}
	if m.cavaBars != nil || m.peakBars != nil {
		t.Errorf("clearQueue should clear the stale meter bars — got cavaBars=%v peakBars=%v", m.cavaBars, m.peakBars)
	}
}

// TestRemoveLastTrackStopsPlayback verifies that removing the only
// remaining track (rather than clearing the whole queue via "D") also stops
// playback — same reasoning as TestClearQueue.
func TestRemoveLastTrackStopsPlayback(t *testing.T) {
	m := newSmokeModel()
	m.tracks = m.tracks[:1]
	m.tracksOrder = m.tracks
	m.currentTrack = &m.tracks[0]
	m.isPlaying = true
	m.removeFromQueue(0)
	if len(m.tracks) != 0 {
		t.Fatalf("expected an empty queue, got %d tracks", len(m.tracks))
	}
	if m.currentTrack != nil {
		t.Errorf("removing the last track should stop the current track, currentTrack is still set")
	}
	if m.isPlaying {
		t.Errorf("removing the last track should stop playback")
	}
}

// TestRemovePlayingTrackFromMiddleStopsPlayback verifies that removing the
// currently-playing track stops playback even when other tracks remain in
// the queue afterward (not just when it empties the queue entirely) —
// playback shouldn't just carry on for a track no longer in the queue.
func TestRemovePlayingTrackFromMiddleStopsPlayback(t *testing.T) {
	m := newSmokeModel()
	if len(m.tracks) < 3 {
		t.Fatal("smoke model needs at least three tracks for this test")
	}
	// An independent copy, not &m.tracks[1] — aliasing directly into the
	// slice's backing array would let removeFromQueue's in-place shift
	// silently corrupt what currentTrack points to before this test's own
	// check runs. doPlayTrack (the real, only production call site) always
	// copies this way too: m.currentTrack = &track from a value parameter.
	playing := m.tracks[1]
	m.currentTrack = &playing
	m.isPlaying = true
	m.cursor = 1
	m.removeFromQueue(1)
	if len(m.tracks) != 2 {
		t.Fatalf("expected 2 tracks remaining, got %d", len(m.tracks))
	}
	if m.currentTrack != nil {
		t.Errorf("removing the playing track should stop it, currentTrack is still set")
	}
	if m.isPlaying {
		t.Errorf("removing the playing track should stop playback, even with tracks still queued")
	}
}

// TestHistoryHasCount ensures Recently Played reports a sidebar count.
func TestHistoryHasCount(t *testing.T) {
	m := newSmokeModel()
	m.history = m.tracks
	if got := m.sectionCount(SecHistory); got != len(m.tracks) {
		t.Errorf("history count = %d, want %d", got, len(m.tracks))
	}
}

// TestClientTintRenders confirms client mode renders without panic and the
// theme tint applies.
func TestClientTintRenders(t *testing.T) {
	m := newSmokeModel()
	m.clientMode = true
	m.width, m.height = 90, 28
	out := m.View()
	if !strings.Contains(stripANSI(out), "CLIENT") {
		t.Errorf("client mode should show the CLIENT badge")
	}
}
