package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// settingsExtraRows is the number of selectable rows before the theme list in
// the Settings tab: the output-device row and the CD-recorder silence-gap
// row. m.themeCursor indexes across all of it (0=device, 1=gap, 2+=themes).
const settingsExtraRows = 2

// updateSettings drives the Settings tab: j/k moves the cursor across the
// device row, the silence-gap row, and the theme list (live-previewing while
// on a theme row); Enter activates whichever row is selected; Esc reverts any
// theme preview.
func (m Model) updateSettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	total := settingsExtraRows + len(paletteOrder)
	switch k.String() {
	case "h", keyLeft:
		m.cancelPreview()
		m.focusMain = false
		return m, nil
	case keyEsc:
		m.cancelPreview()
		m.focusMain = false
		return m, nil
	case keyUp, "k":
		if m.themeCursor > 0 {
			m.themeCursor--
			m.syncSettingsPreview()
		}
		return m, nil
	case keyDown, "j":
		if m.themeCursor < total-1 {
			m.themeCursor++
			m.syncSettingsPreview()
		}
		return m, nil
	case keyEnter:
		return m.activateSettingsRow()
	case "t":
		// Cycle within the theme list regardless of where the cursor is.
		i := 0
		for j, name := range paletteOrder {
			if name == m.themeName {
				i = j
				break
			}
		}
		i = (i + 1) % len(paletteOrder)
		m.themeCursor = settingsExtraRows + i
		m.preview(i)
		return m, nil
	}
	return m, nil
}

// syncSettingsPreview live-previews the theme under the cursor when it's on
// a theme row, or cancels any preview when it's on the device/gap rows —
// those have no "preview then commit" concept, they act immediately.
func (m *Model) syncSettingsPreview() {
	if m.themeCursor >= settingsExtraRows {
		m.preview(m.themeCursor - settingsExtraRows)
	} else {
		m.cancelPreview()
	}
}

// activateSettingsRow runs whatever the cursor is on: opens the output-device
// selector, toggles the CD-recorder silence gap, or applies the highlighted
// theme.
func (m Model) activateSettingsRow() (tea.Model, tea.Cmd) {
	switch m.themeCursor {
	case 0:
		m.openDeviceSelect()
		return m, nil
	case 1:
		return m.toggleInterTrackSilence()
	default:
		m.applyTheme(paletteOrder[m.themeCursor-settingsExtraRows])
		return m, nil
	}
}

// preview sets the live-preview palette for the scheme at index i.
func (m *Model) preview(i int) {
	pal := resolvePalette(paletteOrder[i])
	m.previewPalette = &pal
}

// cancelPreview drops the live preview, reverting to the committed theme.
func (m *Model) cancelPreview() {
	m.previewPalette = nil
}

// enterSettings positions the picker cursor on the active theme and starts a
// preview so the highlighted row matches what's on screen.
func (m *Model) enterSettings() {
	m.themeCursor = settingsExtraRows
	for i, name := range paletteOrder {
		if name == m.themeName {
			m.themeCursor = settingsExtraRows + i
			break
		}
	}
}

// swatch renders five small color blocks sampled from a palette.
func swatch(p Palette) string {
	cells := []lipgloss.TerminalColor{p.Bg2, p.Fg, p.Cyan, p.Purple, p.Amber}
	var sb strings.Builder
	for _, c := range cells {
		sb.WriteString(lipgloss.NewStyle().Foreground(c).Render("█"))
	}
	return sb.String()
}

// renderSettingsActionRow renders a simple icon+label+value row (the device
// and silence-gap rows), matching the theme rows' selection-band styling.
func renderSettingsActionRow(t Theme, w int, icon, label, value string, selected bool) string {
	plain := " " + icon + " " + label
	pad := max(w-lipgloss.Width(plain)-lipgloss.Width(value)-1, 1)
	row := plain + strings.Repeat(" ", pad) + value
	if selected {
		return lipgloss.NewStyle().Background(t.P.BgSel).Width(w).Render(row)
	}
	return t.RowDim.Render(truncateStr(row, w))
}

// renderThemePicker renders the Settings tab: the output-device row, the
// CD-recorder silence-gap row, then the theme picker. The active scheme is
// marked, the cursor row uses the cyan band, and a "live preview" hint shows
// while it's on a theme row.
func (m *Model) renderThemePicker(t Theme, w, h int) string {
	innerW := max(w-2, 1)
	rows := make([]string, 0, len(paletteOrder)+settingsExtraRows+4)
	rows = append(rows,
		t.RowFaint.Render(" j/k Move · ↵ Select · t Cycle theme · Esc Back"),
		"",
	)

	cursorRow := 0 // rows-slice index the cursor is on, tracked as we append
	dev := m.displayDevice()
	rows = append(rows, renderSettingsActionRow(t, innerW, "◆", "Output device", dev, m.themeCursor == 0))
	if m.themeCursor == 0 {
		cursorRow = len(rows) - 1
	}

	gapLabel := "Off (gapless)"
	if m.interTrackSilenceMs > 0 {
		gapLabel = fmt.Sprintf("On (%.1fs)", float64(m.interTrackSilenceMs)/1000)
	}
	rows = append(rows, renderSettingsActionRow(t, innerW, "◼", "CD-recorder silence gap", gapLabel, m.themeCursor == 1))
	if m.themeCursor == 1 {
		cursorRow = len(rows) - 1
	}

	rows = append(rows, "")

	for i, name := range paletteOrder {
		cursor := settingsExtraRows + i
		pal := resolvePalette(name)
		activeMark := " "
		if name == m.themeName {
			activeMark = "●"
		}
		label := paletteNames[name]
		body := " " + swatch(pal) + " " + label
		if cursor == m.themeCursor {
			hint := "previewing"
			plain := " " + activeMark + " ····· " + label
			pad := max(innerW-lipgloss.Width(plain)-len(hint)-1, 1)
			rowText := " " + activeMark + body + strings.Repeat(" ", pad) + hint
			rows = append(rows, lipgloss.NewStyle().Background(t.P.BgSel).Width(innerW).Render(rowText))
			cursorRow = len(rows) - 1
			continue
		}
		mark := t.RowFaint.Render(activeMark)
		if activeMark == "●" {
			mark = t.GreenT.Render(activeMark)
		}
		rows = append(rows, truncateStr(" "+mark+body, innerW))
	}

	return renderListPanel(t, "SETTINGS", m.focusMain, rows, cursorRow, w, h)
}
