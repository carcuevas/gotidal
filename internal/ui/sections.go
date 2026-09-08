package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// artistViewActive reports whether the transient artist drill-down is showing.
func (m *Model) artistViewActive() bool { return m.showArtist }

// renderQueuePane renders the Queue tab: a left column (AlbumArt / Lyrics /
// meter strip, top to bottom) and the queue track list on the right, running
// the full body height down to the progress bar — mirroring rmpc's own
// default Queue tab split, with the meter strip anchored under Lyrics rather
// than carved out of the list.
func (m *Model) renderQueuePane(t Theme, w, h int) string {
	g := m.queueLayout(w, h)
	listW := w
	if g.showLeft {
		listW = g.listW
	}

	innerW := max(listW-2, 1)
	rows := make([]string, 0, len(m.tracks))
	for i := range m.tracks {
		tr := m.tracks[i]
		rows = append(rows, renderTrackRow(t, tr, rowOpts{
			selected:   m.focusMain && i == m.cursor,
			playing:    m.currentTrack != nil && m.currentTrack.ID == tr.ID && m.isPlaying,
			fav:        m.favorites[tr.ID],
			showIndex:  true,
			showArtist: true,
			index:      i + 1,
			width:      innerW,
			duration:   tr.Duration,
		}))
	}
	if len(rows) == 0 {
		rows = append(rows, t.RowDim.Render("Queue is empty. Search or open a mix to add tracks."))
	}
	listPanel := renderListPanel(t, m.queueHeader(t), m.focusMain, rows, m.cursor, listW, h)
	if !g.showLeft {
		return listPanel
	}

	leftCol := []string{m.renderAlbumArtPane(t, g)}
	if g.showLyrics {
		leftCol = append(leftCol, m.renderLyricsPane(t, g.leftW, g.lyricsOuterH))
	}
	if g.showCava {
		leftCol = append(leftCol, m.renderMeterPane(t, g.leftW, g.cavaOuterH))
	}
	left := lipgloss.JoinVertical(lipgloss.Left, leftCol...)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, listPanel)
}

// minQueueLeftPaneW and minQueueLeftBodyH are the smallest tab width and body
// height that can hold the left column (AlbumArt/Lyrics/meter) at all. Below
// either, the whole column is hidden rather than squashed into a sliver.
const (
	minQueueLeftPaneW = 70
	minQueueLeftBodyH = 12
	cavaOuterHeight   = 9 // frameless — 9 bar rows, no border
	lyricsMinOuterH   = 6
)

// queueGeom describes the Queue tab's left-column layout. Panel *OuterH
// fields include the panel's own 2-row border.
type queueGeom struct {
	showLeft bool
	leftW    int
	listW    int

	albumArtOuterH int
	albumArtCols   int // inner (square) content cols
	albumArtRows   int // inner (square) content rows

	showCava   bool
	cavaOuterH int

	showLyrics   bool
	lyricsOuterH int
}

// queueLayout computes the Queue tab's geometry for outer size w×h. AlbumArt
// is sized to just contain its natural square (driven by width, via
// coverBoxDims) rather than stretching to fill the column. The meter strip
// (Cava or Peak, see renderMeterPane) gets a fixed height reserved at the
// bottom of the left column, below Lyrics; Lyrics then gets whatever's left.
// On a short terminal the meter strip is the first to go (it needs Lyrics to
// still have a usable height left over, not just any leftover), then Lyrics
// itself; on a narrow or very short terminal the whole left column hides,
// matching the old cover-only panel. The track list on the right always runs
// the tab's full height, unaffected by any of this.
func (m *Model) queueLayout(w, h int) queueGeom {
	var g queueGeom
	if w < minQueueLeftPaneW || h < minQueueLeftBodyH {
		g.listW = w
		return g
	}
	g.showLeft = true
	g.leftW = min(max(w/3, 28), 50)
	g.listW = w - g.leftW

	innerW := max(g.leftW-2, 1)
	// Bound the art's height budget generously (the full column height, minus
	// its own border) — coverBoxDims already caps rows at innerW/cellAspect,
	// so this only ever constrains the square on a terminal too short for
	// even that.
	//
	// The cover itself is drawn a bit smaller than the full column width
	// (artShrinkNum/artShrinkDen) so the panel reads as a frame around the
	// art rather than the art filling it edge to edge; renderAlbumArtPane
	// centers the shrunk square within innerW.
	const artShrinkNum, artShrinkDen = 9, 10
	artW := max(innerW*artShrinkNum/artShrinkDen, 1)
	g.albumArtCols, g.albumArtRows = coverBoxDims(artW, max(h-2, 1))
	g.albumArtOuterH = g.albumArtRows + 2

	remH := h - g.albumArtOuterH
	if remH >= cavaOuterHeight+lyricsMinOuterH {
		g.showCava = true
		g.cavaOuterH = cavaOuterHeight
		remH -= g.cavaOuterH
	}
	if remH >= lyricsMinOuterH {
		g.showLyrics = true
		g.lyricsOuterH = remH
	} else {
		// Too little left for a usable Lyrics panel — give it back to
		// AlbumArt rather than rendering an unreadable sliver.
		g.albumArtOuterH += remH
	}
	return g
}

// renderAlbumArtPane renders the square cover-art panel for the track under
// the Queue cursor. The crisp Kitty image (when supported) is written to the
// TTY separately (see syncKittyCover); this draws the reserved blank box or
// the Unicode block-art fallback, centered within the panel's full width.
func (m *Model) renderAlbumArtPane(t Theme, g queueGeom) string {
	innerW := max(g.leftW-2, 1)
	padLeft := max((innerW-g.albumArtCols)/2, 0)
	pad := strings.Repeat(" ", padLeft)

	var b strings.Builder
	if m.useKittyCover() || m.useSixelCover() {
		for range g.albumArtRows {
			b.WriteString(pad)
			b.WriteString(strings.Repeat(" ", g.albumArtCols))
			b.WriteByte('\n')
		}
	} else {
		cover := coverPanelLines(m.coverImage, "", "", "", g.albumArtCols, g.albumArtRows)
		for _, ln := range cover {
			b.WriteString(pad)
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return renderPanel(t, "", false, g.leftW, g.albumArtOuterH, strings.TrimRight(b.String(), "\n"))
}

// renderMeterPane renders whichever audio meter is currently selected for the
// strip anchored under Lyrics, at the same width as the left column: the
// Cava spectrum (the default) or the Peak/VU meter, toggled via the command
// palette (see toggleVisualizer in keys.go).
func (m *Model) renderMeterPane(t Theme, w, h int) string {
	if m.showPeakMeter {
		return m.renderPeakPane(t, w, h)
	}
	return m.renderCavaPane(t, w, h)
}

// renderCavaPane renders the CAVA spectrum-visualizer strip: bar heights from
// the most recent frame cava reported (see internal/visualizer), or a static
// placeholder when cava isn't running (not installed, or nothing playing).
// Deliberately frameless (no border) — it sits directly beneath Lyrics rather
// than in its own boxed "VU meter" panel.
func (m *Model) renderCavaPane(t Theme, w, h int) string {
	return fitBlock(renderCavaBars(t, m.cavaBars, w, h), w, h)
}

// renderPeakPane renders the Peak/VU meter strip — see renderPeakBars.
// Deliberately frameless, matching renderCavaPane, since it shares the same
// strip beneath Lyrics.
func (m *Model) renderPeakPane(t Theme, w, h int) string {
	return fitBlock(renderPeakBars(t, m.peakBars, w, h), w, h)
}

// renderLyricsPane renders the synced-lyrics panel: the line whose timestamp
// is closest to m.currPos is highlighted, mirroring rmpc's own Lyrics pane.
func (m *Model) renderLyricsPane(t Theme, w, h int) string {
	innerW := max(w-2, 1)
	innerH := max(h-2, 1)
	body := renderLyricsBody(t, m.lyricsState, m.currPos, innerW, innerH)
	return renderPanel(t, " Lyrics ", false, w, h, body)
}

// hoveredTrack is the track the Queue cover should show: the one under the
// cursor, falling back to the currently-playing track.
func (m *Model) hoveredTrack() *tidal.Track {
	if m.cursor >= 0 && m.cursor < len(m.tracks) {
		return &m.tracks[m.cursor]
	}
	return m.currentTrack
}

// coverTrack is the track whose cover should currently be displayed: the
// hovered Queue row, otherwise the playing track.
func (m *Model) coverTrack() *tidal.Track {
	if m.section == SecQueue {
		return m.hoveredTrack()
	}
	return m.currentTrack
}

// syncQueueCover fetches the cover for the track under the Queue cursor when it
// differs from the one displayed. A no-op off the Queue or when the cover is
// unchanged (maybeUpdateCover dedupes by UUID).
func (m *Model) syncQueueCover() tea.Cmd {
	if m.section != SecQueue {
		return nil
	}
	return m.maybeUpdateCover(m.coverTrack())
}

// renderMixesPane renders the Daily Mixes list.
func (m *Model) renderMixesPane(t Theme, w, h int) string {
	innerW := max(w-2, 1)
	rows := make([]string, 0, len(m.mixes))
	for i, mix := range m.mixes {
		cur := "  "
		nameStyle := t.Row
		if m.focusMain && i == m.cursor {
			cur = t.RowPlaying.Render("› ")
			nameStyle = t.RowPlaying
		}
		line := cur + nameStyle.Render(mix.Title)
		if mix.SubTitle != "" {
			line += t.RowDim.Render(" · " + mix.SubTitle)
		}
		line = truncateStr(line, innerW)
		if m.focusMain && i == m.cursor {
			rows = append(rows, t.RowSel.Width(innerW).Render(stripANSI(line)))
		} else {
			rows = append(rows, line)
		}
	}
	if len(rows) == 0 {
		rows = append(rows, t.RowDim.Render("No mixes loaded yet."))
	}
	return renderListPanel(t, "DAILY MIXES", m.focusMain, rows, m.cursor, w, h)
}

// renderArtistPane renders the transient artist drill-down: two synthetic
// quick-play rows followed by the artist's albums. When an album is open it
// shows that album's track list instead.
func (m *Model) renderArtistPane(t Theme, w, h int) string {
	if m.artistAlbum != nil {
		return m.renderArtistAlbumPane(t, w, h)
	}
	innerW := max(w-2, 1)
	var rows []string
	if m.artistLoading {
		rows = append(rows, t.RowDim.Render("Loading album…"))
	} else {
		total := len(m.artistAlbums) + 2
		for i := range total {
			var label string
			switch i {
			case 0:
				label = "▶ Play all tracks"
			case 1:
				label = "★ Top tracks"
			default:
				a := m.artistAlbums[i-2]
				year := ""
				if len(a.ReleaseDate) >= 4 {
					year = " (" + a.ReleaseDate[:4] + ")"
				}
				label = fmt.Sprintf("%s%s — %d tracks", a.Title, year, a.NumberOfTracks)
			}
			cur := "  "
			style := t.Row
			if i == m.artistCursor {
				cur = t.RowPlaying.Render("› ")
				style = t.RowPlaying
			}
			line := truncateStr(cur+style.Render(label), innerW)
			if i == m.artistCursor {
				rows = append(rows, t.RowSel.Width(innerW).Render(stripANSI(line)))
			} else {
				rows = append(rows, line)
			}
		}
	}
	title := "ARTIST · " + strings.ToUpper(m.artistName)
	return renderListPanel(t, title, true, rows, m.artistCursor, w, h)
}

// renderArtistAlbumPane lists the tracks of an album opened inside the artist
// drill-down.
func (m *Model) renderArtistAlbumPane(t Theme, w, h int) string {
	innerW := max(w-2, 1)
	rows := make([]string, 0, len(m.artistAlbumTracks))
	for i := range m.artistAlbumTracks {
		tr := m.artistAlbumTracks[i]
		rows = append(rows, renderTrackRow(t, tr, rowOpts{
			selected:  i == m.artistAlbumCursor,
			playing:   m.currentTrack != nil && m.currentTrack.ID == tr.ID && m.isPlaying,
			fav:       m.favorites[tr.ID],
			showIndex: true,
			index:     i + 1,
			width:     innerW,
			duration:  tr.Duration,
		}))
	}
	if len(rows) == 0 {
		rows = append(rows, t.RowDim.Render("No tracks."))
	}
	title := "ALBUM · " + strings.ToUpper(m.artistAlbum.Title)
	return renderListPanel(t, title, true, rows, m.artistAlbumCursor, w, h)
}

// useKittyCover reports whether the Now-Playing cover should be drawn with the
// Kitty graphics protocol (written straight to the TTY) instead of block art. Kitty is
// only safe when no overlay is covering the pane, since the popup would not
// hide a terminal-drawn image.
func (m *Model) useKittyCover() bool {
	return m.kittySupported && m.coverImage != nil && m.overlay == OverlayNone
}

// useSixelCover reports whether the AlbumArt cover should be drawn with the
// Sixel protocol — see useKittyCover for the shared reasoning (an overlay
// popup wouldn't hide a terminal-drawn image, so graphics are suppressed
// while one is open).
func (m *Model) useSixelCover() bool {
	return m.sixelSupported && m.coverImage != nil && m.overlay == OverlayNone
}
