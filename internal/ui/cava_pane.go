package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// numCavaBars is the fixed bar count cava is configured to emit. The render
// side shows at most this many bars (each bar plus a following gap column —
// see renderCavaBars), one per two available columns; a narrower panel
// simply clips the tail rather than resampling, which is cheap and matches
// how a real spectrum analyzer's bars look when the window is too small. Set
// high enough to fill the meter strip's full width (the whole Queue-list
// column, not just the AlbumArt column it used to share) on typical terminal
// widths rather than leaving most of it blank past bar 24.
const numCavaBars = 80

// colorRGB8 extracts 8-bit-per-channel RGB from any lipgloss.TerminalColor —
// a plain hex Color, an ANSI palette index, or an AdaptiveColor — since all
// of them satisfy Go's standard color.Color (RGBA returns 16-bit-per-channel
// premultiplied values, so >>8 down-converts to the usual 0-255 range).
func colorRGB8(c lipgloss.TerminalColor) (r, g, b uint8) {
	rr, gg, bb, _ := c.RGBA()
	return uint8(rr >> 8), uint8(gg >> 8), uint8(bb >> 8)
}

// blendColor linearly interpolates between two colors at t∈[0,1] (0=a, 1=b),
// returning a concrete hex Color — the building block for a smooth gradient
// rather than snapping between a handful of fixed stops.
func blendColor(a, b lipgloss.TerminalColor, t float64) lipgloss.Color {
	ar, ag, ab := colorRGB8(a)
	br, bg, bb := colorRGB8(b)
	lerp := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", lerp(ar, br), lerp(ag, bg), lerp(ab, bb)))
}

// gradientColor returns the color at position frac∈[0,1] along an n-stop
// gradient (frac≈0 → stops[0], frac≈1 → stops[n-1]), linearly interpolating
// RGB between whichever two adjacent stops frac falls between. This is what
// makes the spectrum read as a continuous intensity ramp rather than a small
// palette repeating every len(stops) rows.
func gradientColor(stops []lipgloss.TerminalColor, frac float64) lipgloss.Color {
	n := len(stops)
	switch {
	case n == 0:
		return lipgloss.Color("")
	case n == 1 || frac <= 0:
		r, g, b := colorRGB8(stops[0])
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
	case frac >= 1:
		r, g, b := colorRGB8(stops[n-1])
		return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", r, g, b))
	}
	pos := frac * float64(n-1)
	i := min(int(pos), n-2)
	return blendColor(stops[i], stops[i+1], pos-float64(i))
}

// renderCavaBars renders bars (each 0-100) as columns of block glyphs, h rows
// tall, colored by a smooth gradient along the theme's Grad1→Grad4 stops
// (low amplitude reads as Grad1, the top of the strip as Grad4) — an
// intensity ramp, not 4 colors cycling. One bar per column, no gaps, so as
// many of the configured bars as fit in the pane's width are shown. An empty
// bars slice (cava not installed, or nothing playing yet) renders a dim
// placeholder instead.
func renderCavaBars(t Theme, bars []int, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	if len(bars) == 0 {
		msg := t.RowDim.Render(truncateStr("cava not installed", w))
		pad := strings.Repeat("\n", max(h/2, 0))
		return pad + msg
	}

	n := min(len(bars), w)
	grid := make([][]rune, h)
	for r := range grid {
		grid[r] = []rune(strings.Repeat(" ", w))
	}
	for i := range n {
		filled := bars[i] * h / 100
		for r := range min(filled, h) {
			grid[h-1-r][i] = '█'
		}
	}

	stops := gradColors(t)
	var sb strings.Builder
	for r := range grid {
		// r==0 is the strip's top (highest amplitude); r==h-1 is the bottom
		// (lowest). frac runs 0→1 bottom-to-top so Grad1 anchors the bottom
		// and Grad4 the top, matching gradColors' documented low→high order.
		frac := 1.0
		if h > 1 {
			frac = 1 - float64(r)/float64(h-1)
		}
		style := lipgloss.NewStyle().Foreground(gradientColor(stops, frac))
		sb.WriteString(style.Render(string(grid[r])))
		if r < h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
