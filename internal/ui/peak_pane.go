package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// peakChannelLabels names each channel's bar in the Peak-meter strip; a setup
// beyond stereo (rare in a home listening chain) just gets its 1-based index.
var peakChannelLabels = [...]string{"L", "R"}

// renderPeakBars renders one vertical LED-ladder bar per channel, h rows
// tall — a real VU/peak meter's rows are colored by fixed position (top
// third red, next amber, rest green) and light up bottom-to-top as the
// signal rises, rather than the whole bar changing color at once. An empty
// levels slice (nothing playing yet, or client mode, where there's no local
// PCM to meter) renders a dim placeholder instead, mirroring renderCavaBars.
func renderPeakBars(t Theme, levels []int, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(levels) == 0 {
		msg := t.RowDim.Render(truncateStr("peak meter: nothing playing", w))
		pad := strings.Repeat("\n", max(h/2, 0))
		return pad + msg
	}

	n := len(levels)
	const gap = 1
	barW := max((w-(n-1)*gap)/n, 1)

	labels := make([]rune, n)
	for c := range levels {
		if c < len(peakChannelLabels) {
			labels[c] = rune(peakChannelLabels[c][0])
		} else {
			labels[c] = rune('1' + c)
		}
	}

	// Plain foreground-only styles: t.Toast/t.Err carry a border/bold meant for
	// banner text, which would draw a box around every single bar cell here.
	green := lipgloss.NewStyle().Foreground(t.P.Green)
	red := lipgloss.NewStyle().Foreground(t.P.Rose)

	lines := make([]string, h)
	for r := range h {
		rowFromBottom := h - 1 - r
		frac := float64(rowFromBottom) / float64(max(h-1, 1))
		litStyle := green // comfortable headroom
		switch {
		case frac >= 0.85:
			litStyle = red // clipping territory
		case frac >= 0.6:
			litStyle = t.Amber // hot but not clipping (already foreground-only)
		}

		var sb strings.Builder
		for c, lvl := range levels {
			if c > 0 {
				sb.WriteByte(' ')
			}
			filled := lvl * h / 100
			fillRune, style := '░', t.RowFaint
			if rowFromBottom < filled {
				fillRune, style = '█', litStyle
			}
			cell := make([]rune, barW)
			for i := range cell {
				cell[i] = fillRune
			}
			if r == h-1 {
				cell[0] = labels[c]
			}
			sb.WriteString(style.Render(string(cell)))
		}
		lines[r] = sb.String()
	}
	return strings.Join(lines, "\n")
}
