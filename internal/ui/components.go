package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// Sample-rate thresholds for the hi-res badge colours. Each is the bottom of
// its family, so the 44.1 kHz-derived rate colours the same as its 48 kHz
// twin (176.4 with 192, 88.2 with 96).
const (
	rateHiRes96  = 88200
	rateHiRes192 = 176400
)

// gradColors returns the four-stop CAVA gradient as a cycling slice, falling
// back gracefully if any stop is not a plain color.
func gradColors(t Theme) []lipgloss.TerminalColor {
	return []lipgloss.TerminalColor{t.P.Grad1, t.P.Grad2, t.P.Grad3, t.P.Grad4}
}

// rowOpts controls how renderTrackRow draws a single track line.
type rowOpts struct {
	selected   bool
	playing    bool
	fav        bool
	showIndex  bool
	showArtist bool
	showAlbum  bool // appends " — <album>" after the artist; no-op without showArtist
	index      int
	width      int
	duration   int // seconds; 0 hides the duration column
}

// renderTrackRow renders one track line: cursor glyph, optional index, title,
// optional artist, favorite heart, and right-aligned duration — all themed and
// truncated to o.width.
func renderTrackRow(t Theme, tr tidal.Track, o rowOpts) string {
	cur := " "
	curStyle := t.RowFaint
	if o.playing {
		cur = "♪"
		curStyle = t.RowPlaying
	} else if o.selected {
		cur = "›"
		curStyle = t.RowPlaying
	}

	var idx string
	if o.showIndex {
		idx = t.RowFaint.Render(fmt.Sprintf("%2d ", o.index))
	}

	title := tr.Title
	titleStyle := t.Row
	if o.playing {
		titleStyle = t.RowPlaying
	}

	fav := "  "
	if o.fav {
		fav = " " + t.Fav.Render("♥")
	}

	var dur string
	if o.duration > 0 {
		dur = " " + t.RowDim.Render(formatTime(float64(o.duration)))
	}

	// Fixed-width left gutter (leading space + cursor + space + index) and the
	// right-aligned columns (fav + duration). The title/artist "middle" is
	// truncated to whatever space remains so the duration always survives.
	prefix := " " + curStyle.Render(cur) + " " + idx
	fixed := lipgloss.Width(prefix) + lipgloss.Width(fav) + lipgloss.Width(dur)
	midRoom := max(o.width-fixed, 1)

	mid := titleStyle.Render(title)
	if o.showArtist && tr.Artist.Name != "" {
		mid += t.RowFaint.Render(" — ") + t.RowDim.Render(tr.Artist.Name)
		if o.showAlbum && tr.Album.Title != "" {
			mid += t.RowFaint.Render(" — ") + t.RowAlbum.Render(tr.Album.Title)
		}
	}
	mid = truncateStr(mid, midRoom)

	// Pad between the middle and the right-aligned fav+duration columns.
	pad := max(o.width-lipgloss.Width(prefix)-lipgloss.Width(mid)-lipgloss.Width(fav)-lipgloss.Width(dur), 0)
	line := prefix + mid + fav + strings.Repeat(" ", pad) + dur

	if o.selected {
		// Nested fg colors would reset the selection background mid-line, so
		// the band is rendered over the plain text with a uniform foreground.
		return t.RowSel.Width(o.width).Render(stripANSI(line))
	}
	return line
}

// renderKeyBar renders the footer hint bar: each item is [key, label]; keys are
// cyan, labels dim, separated by faint pipes. Truncated to width w.
func renderKeyBar(t Theme, items [][2]string, w int) string {
	var sb strings.Builder
	for i, it := range items {
		if i > 0 {
			sb.WriteString(t.KeyBarSep.Render(" │ "))
		}
		sb.WriteString(t.KeyBarKey.Render(it[0]))
		sb.WriteString(" ")
		sb.WriteString(t.KeyBarLabel.Render(it[1]))
	}
	return truncateStr(" "+sb.String(), w)
}

// renderNowPlayingBar renders the persistent bottom now-playing bar: the cyan
// track title, dim artist, and a thin progress readout. Returns a multi-line
// block sized to width w.
func (m *Model) renderNowPlayingBar(t Theme, w int) string {
	inner := max(w-2, 10)

	if m.currentTrack == nil {
		empty := t.RowDim.Render("  Nothing playing")
		body := empty + strings.Repeat(" ", max(inner-lipgloss.Width(empty), 0))
		return renderPanel(t, "", false, w, 3, body)
	}

	// Right-aligned status (volume / device / shuffle) on the title row.
	status := m.nowBarStatus(t)
	titleRoom := max(inner-lipgloss.Width(status)-1, 1)
	title := t.RowPlaying.Render(truncateStr(m.currentTrack.Title, titleRoom))
	pad := max(inner-lipgloss.Width(title)-lipgloss.Width(status), 0)
	head := title + strings.Repeat(" ", pad) + status

	badge := ""
	if text, style, ok := qualityBadge(t, m.currentQuality, m.currentRate, m.bitPerfect, m.dacModeActive); ok {
		badge = style.Render(text)
	}
	artistRoom := max(inner-lipgloss.Width(badge)-1, 1)
	artist := t.RowDim.Render(truncateStr(m.currentTrack.Artist.Name, artistRoom))
	artistPad := max(inner-lipgloss.Width(artist)-lipgloss.Width(badge), 0)
	artistRow := artist + strings.Repeat(" ", artistPad) + badge

	percent := 0.0
	if m.duration > 0 {
		percent = m.currPos / m.duration
	}
	bar := m.progress.ViewAs(percent)
	timeStr := t.RowDim.Render(fmt.Sprintf(" %s / %s", formatTime(m.currPos), formatTime(m.duration)))

	body := strings.Join([]string{head, artistRow, bar + timeStr}, "\n")
	return renderPanel(t, "", false, w, 5, body)
}

// qualityBadge decides the now-playing quality badge's text and style. Split
// out of renderNowPlayingBar so the choice can be asserted directly: lipgloss
// drops colour when there is no TTY, so a test that only inspects the rendered
// string cannot tell two colours apart.
//
// ok is false when there is no tier to report.
func qualityBadge(t Theme, q tidal.Quality, rate uint32, bitPerfect, dacMode bool) (text string, style lipgloss.Style, ok bool) {
	text = q.Label()
	if text == "" {
		return "", lipgloss.Style{}, false
	}

	switch {
	case q == tidal.QualityHigh || q == tidal.QualityLow:
		// HIGH/LOW are lossy AAC tiers — nothing was "converted" away from
		// bit-perfect, they were never bit-perfect to begin with (granted
		// directly by Tidal, e.g. under Data Saver). Styled like an
		// error/warning (t.Err — themed red/rose) rather than the faint style
		// every other badge state uses, so a lossy stream is something you'd
		// actually notice at a glance, not read past. A high sample rate does
		// not redeem it, so this case comes first.
		return text + " (lossy)", t.Err, true

	case q == tidal.QualityHiRes && rate >= rateHiRes192:
		// 176.4/192 kHz reaching the device — the top of what Tidal serves.
		return text, t.QualityHiRes192, true

	case q == tidal.QualityHiRes && rate >= rateHiRes96:
		// 88.2/96 kHz: still hi-res, but not the 192 kHz family, so it gets
		// its own colour rather than being indistinguishable from it.
		return text, t.QualityHiRes96, true

	case !dacMode:
		// PipeWire is the output path by the user's own choice here, not a
		// fallback anything was forced into — SetDACMode(false) is exactly
		// how PipeWire mode is turned on. bitPerfect is always false in this
		// mode (PipeWire's own graph may still resample or mix downstream, a
		// possibility we have no way to confirm or rule out from here), so
		// tagging every single PipeWire-routed track "(converted)" claimed a
		// downgrade regardless of whether the stream's own resolution ever
		// actually changed — this tier and rate are exactly what was
		// requested and granted; only the guarantee of an untouched path to
		// the DAC is what PipeWire mode gives up.
		return text, t.RowFaint, true

	case !bitPerfect:
		// dacMode was on, so this is the real compromise: our own hw:
		// format negotiation had to fall back to plughw:, which resamples or
		// reformats to something the device will accept. Unlike the PipeWire
		// case above, this one is a verified format change, so the badge
		// says so.
		return text + " (converted)", t.RowFaint, true
	}
	return text, t.RowFaint, true
}

// nowBarStatus renders the compact volume / device / shuffle readout shown at
// the right of the now-playing bar.
//
// The device is reported bare (e.g. "hw:2,0") rather than labelled, because
// this string is subtracted from the room available for the track title. The
// "hw:"/"plughw:" prefix already identifies it. When the plughw: fallback
// engaged, the device actually opened is shown instead of the requested one,
// so the readout never claims a direct hw: path that isn't in use.
func (m *Model) nowBarStatus(t Theme) string {
	vol := fmt.Sprintf("vol %.0f%%", m.volume)
	parts := []string{vol}
	if m.shuffleMode != ShuffleOff {
		parts = append(parts, "shuffle "+m.shuffleMode.String())
	}
	parts = append(parts, m.displayDevice())
	return t.RowDim.Render(strings.Join(parts, "  ·  "))
}

// autoDeviceLabel is shown wherever no device has been resolved yet.
const autoDeviceLabel = "auto"

// displayDevice reports the ALSA device to show the user: the one actually
// opened when known (which can differ from the requested one on a plughw:
// fallback), else the requested device, else autoDeviceLabel.
func (m *Model) displayDevice() string {
	dev := m.currentDevice
	if m.activeDevice != "" {
		dev = m.activeDevice
	}
	if dev == "" {
		dev = autoDeviceLabel
	}
	return dev
}
