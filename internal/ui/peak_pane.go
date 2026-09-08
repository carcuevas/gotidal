package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// peakChannelLabels names each channel's bar in the Peak-meter strip; a setup
// beyond stereo (rare in a home listening chain) just gets its 1-based index.
var peakChannelLabels = [...]string{"L", "R"}

// peakBarThickness is how many screen rows tall each channel's horizontal bar
// renders as (by simple repetition) — a single row read as too thin/flat;
// this gives it real visual weight.
const peakBarThickness = 2

// renderPeakBars renders one horizontal LED-ladder bar per channel, each
// peakBarThickness rows tall with a blank separator row between channels and
// the whole block vertically centered in h — a real VU/peak meter's columns
// are colored by fixed position (leftmost ~60% green/comfortable headroom,
// next ~25% amber/hot, rightmost 15% red/clipping territory) and light up
// left-to-right as the signal rises, rather than the whole bar changing
// color at once. An empty levels slice (nothing playing yet, or client mode,
// where there's no local PCM to meter) renders a dim placeholder instead,
// mirroring renderCavaBars.
func renderPeakBars(t Theme, levels []int, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(levels) == 0 {
		msg := t.RowDim.Render(truncateStr("peak meter: nothing playing", w))
		pad := strings.Repeat("\n", max(h/2, 0))
		return pad + msg
	}

	labels := make([]string, len(levels))
	labelW := 0
	for c := range levels {
		if c < len(peakChannelLabels) {
			labels[c] = peakChannelLabels[c] + " "
		} else {
			labels[c] = string(rune('1'+c)) + " "
		}
		labelW = max(labelW, lipgloss.Width(labels[c]))
	}
	barW := max(w-labelW, 1)

	// Plain foreground-only styles: t.Toast/t.Err carry a border/bold meant for
	// banner text, which would draw a box around every single bar cell here.
	green := lipgloss.NewStyle().Foreground(t.P.Green)
	red := lipgloss.NewStyle().Foreground(t.P.Rose)

	blank := strings.Repeat(" ", w)
	lines := make([]string, 0, len(levels)*(peakBarThickness+1))
	for c, lvl := range levels {
		if c > 0 {
			lines = append(lines, blank) // separator row between channels
		}
		filled := lvl * barW / 100
		var sb strings.Builder
		sb.WriteString(t.RowDim.Render(labels[c]))
		for col := range barW {
			frac := float64(col) / float64(max(barW-1, 1))
			litStyle := green // comfortable headroom
			switch {
			case frac >= 0.85:
				litStyle = red // clipping territory
			case frac >= 0.6:
				litStyle = t.Amber // hot but not clipping (already foreground-only)
			}
			ch, style := '░', t.RowFaint
			if col < filled {
				ch, style = '█', litStyle
			}
			sb.WriteString(style.Render(string(ch)))
		}
		line := sb.String()
		for range peakBarThickness {
			lines = append(lines, line)
		}
	}

	// Center the whole block vertically in h; fitBlock (the caller) pads any
	// remainder below, so only the top needs padding here.
	topPad := max((h-len(lines))/2, 0)
	out := make([]string, 0, topPad+len(lines))
	for range topPad {
		out = append(out, blank)
	}
	out = append(out, lines...)
	return strings.Join(out, "\n")
}
