package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	tabBarH = 1 // top tab-bar row
	nowBarH = 5 // bottom now-playing bar height (border 2 + 3 content rows)
	footerH = 1 // key bar

	// cellAspect approximates a terminal cell's height:width ratio (most
	// monospace fonts render cells roughly twice as tall as they are wide).
	// coverBoxDims uses it so a "square" cover box is square on screen, not
	// just square in cell count.
	cellAspect = 2.0
)

// coverBoxDims returns the largest cell width/height that (a) fits within
// availW×availH and (b) renders as a visual square given cellAspect. Width is
// the driving dimension; height is derived from it and clamped down (shrinking
// width to match) when availH is the tighter constraint.
func coverBoxDims(availW, availH int) (cols, rows int) {
	if availW <= 0 || availH <= 0 {
		return 0, 0
	}
	cols = availW
	rows = int(float64(cols) / cellAspect)
	if rows > availH {
		rows = availH
		cols = int(float64(rows) * cellAspect)
	}
	return max(cols, 1), max(rows, 1)
}

// visibleWindow returns the [start,end) slice of a list of `total` items that
// keeps `cursor` centered within a viewport of `height` rows.
func visibleWindow(cursor, total, height int) (start, end int) {
	if total == 0 {
		return 0, 0
	}
	start = max(cursor-height/2, 0)
	end = start + height
	if end > total {
		end = total
		start = max(end-height, 0)
	}
	return start, end
}

func formatTime(seconds float64) string {
	minutes := int(seconds) / 60
	secs := int(seconds) % 60
	return fmt.Sprintf("%d:%02d", minutes, secs)
}

// stripANSI returns s with all ANSI escape sequences removed, for measuring
// visible display width.
func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case inEsc:
			if r == 'm' {
				inEsc = false
			}
		case r == '\x1b':
			inEsc = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderPanel draws a rounded-border panel of the given outer width/height with
// `body` inside, then splices `title` onto the top border in the
// ╭─ TITLE ──╮ style. focused colors the border and title with the accent.
// An empty title leaves the top border plain (used for the now-playing bar).
func renderPanel(t Theme, title string, focused bool, w, h int, body string) string {
	style := t.Panel
	titleStyle := t.PanelTitle
	if focused {
		style = t.PanelFocus
		titleStyle = t.PanelTitleHot
	}
	// Inner content area is w-2 (borders) by h-2.
	inner := max(w-2, 1)
	innerH := max(h-2, 1)
	body = fitBlock(body, inner, innerH)

	rendered := style.Width(inner).Height(innerH).Render(body)
	if title == "" {
		return rendered
	}

	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}
	lines[0] = spliceTitle(lines[0], titleStyle.Render(" "+title+" "), 2)
	return strings.Join(lines, "\n")
}

// spliceTitle overwrites the run of border characters in `border` starting at
// display column `at` with `title`, preserving the corners and any border to
// the right of the title.
func spliceTitle(border, title string, at int) string {
	bw := lipgloss.Width(title)
	// Keep the first `at` cells (corner + dashes), then the title, then resume
	// the original border after at+bw cells.
	left := truncateStr(border, at)
	// Pad left in case the border was shorter (shouldn't happen for a panel).
	if lw := lipgloss.Width(left); lw < at {
		left += strings.Repeat("─", at-lw)
	}
	right := truncateLeftANSI(border, at+bw)
	return left + title + right
}

// fitBlock pads/truncates a multi-line body to exactly w columns by h rows.
func fitBlock(body string, w, h int) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, h)
	for i := range h {
		ln := ""
		if i < len(lines) {
			ln = lines[i]
		}
		ln = truncateStr(ln, w)
		if lw := lipgloss.Width(ln); lw < w {
			ln += strings.Repeat(" ", w-lw)
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// renderListPanel wraps a set of pre-rendered rows in a titled panel, scrolling
// to keep `cursor` visible within the panel's inner height.
func renderListPanel(t Theme, title string, focused bool, rows []string, cursor, w, h int) string {
	innerH := max(h-2, 1)
	start, end := visibleWindow(cursor, len(rows), innerH)
	body := strings.Join(rows[start:end], "\n")
	return renderPanel(t, title, focused, w, h, body)
}

// bodyHeight is the height available to the tab content, below the tab bar and
// above the now-playing bar and footer.
func (m *Model) bodyHeight() int {
	h := m.height - tabBarH - nowBarH - footerH
	if m.errText != "" || m.toast != "" {
		h--
	}
	return max(h, 1)
}
