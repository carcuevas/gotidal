package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// numCavaBars is the fixed bar count cava is configured to emit. The render
// side shows at most this many columns, one bar per column; a narrower panel
// simply clips the tail rather than resampling, which is cheap and matches
// how a real spectrum analyzer's bars look when the window is too small.
const numCavaBars = 24

// renderCavaBars renders bars (each 0-100) as a column of block glyphs, h rows
// tall, colored along the theme's gradient by row. An empty bars slice (cava
// not installed, or nothing playing yet) renders a dim placeholder instead.
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

	palette := gradColors(t)
	var sb strings.Builder
	for r := range grid {
		style := lipgloss.NewStyle().Foreground(palette[r%len(palette)])
		sb.WriteString(style.Render(string(grid[r])))
		if r < h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
