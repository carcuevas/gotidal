package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/carcuevas/gotidal/internal/buildinfo"
)

// settingsRowCount is the number of selectable rows in the Settings tab: the
// output-device row, the bit-perfect-quality row, the Data Saver row, the
// CD-recorder silence-gap row, and the Themes row (which opens the floating
// Themes picker — OverlayThemePicker — rather than browsing schemes inline).
// m.themeCursor indexes across all of it (0=device, 1=bit-perfect,
// 2=Data Saver, 3=gap, 4=Themes).
const settingsRowCount = 5

// updateSettings drives the Settings tab: j/k moves the cursor across its
// fixed row list; Enter activates whichever row is selected; t is a quick
// shortcut that applies the next theme in paletteOrder immediately, without
// opening the Themes picker.
func (m Model) updateSettings(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "h", keyLeft:
		m.focusMain = false
		return m, nil
	case keyEsc:
		m.focusMain = false
		return m, nil
	case keyUp, "k":
		if m.themeCursor > 0 {
			m.themeCursor--
		}
		return m, nil
	case keyDown, "j":
		if m.themeCursor < settingsRowCount-1 {
			m.themeCursor++
		}
		return m, nil
	case keyEnter:
		return m.activateSettingsRow()
	case "t":
		i := 0
		for j, name := range paletteOrder {
			if name == m.themeName {
				i = j
				break
			}
		}
		i = (i + 1) % len(paletteOrder)
		m.applyTheme(paletteOrder[i])
		return m, nil
	}
	return m, nil
}

// activateSettingsRow runs whatever the cursor is on: opens the output-device
// selector, toggles bit-perfect quality, toggles Data Saver, toggles the
// CD-recorder silence gap, or opens the floating Themes picker.
func (m Model) activateSettingsRow() (tea.Model, tea.Cmd) {
	switch m.themeCursor {
	case 0:
		m.openDeviceSelect()
		return m, nil
	case 1:
		return m.toggleBitPerfectMode()
	case 2:
		return m.toggleLowDataMode()
	case 3:
		return m.toggleInterTrackSilence()
	default:
		m.openThemePicker()
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

// enterSettings resets the Settings tab's row cursor to the top (Output
// device) each time the tab is entered.
func (m *Model) enterSettings() {
	m.themeCursor = 0
}

// openThemePicker raises the floating Themes overlay (OverlayThemePicker),
// positioned on the currently active scheme with its preview already live —
// matching the old inline picker's "highlighted row previews immediately"
// feel, just as its own popup instead of appended to the Settings row list.
func (m *Model) openThemePicker() {
	m.themePickerIndex = 0
	for i, name := range paletteOrder {
		if name == m.themeName {
			m.themePickerIndex = i
			break
		}
	}
	m.overlay = OverlayThemePicker
	m.preview(m.themePickerIndex)
}

// updateThemePickerOverlay handles the floating Themes popup: j/k moves with
// live preview, Enter commits the highlighted scheme, Esc cancels and
// reverts to whatever was active before opening.
func (m Model) updateThemePickerOverlay(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case keyEsc:
		m.cancelPreview()
		m.overlay = OverlayNone
	case keyUp, "k":
		if m.themePickerIndex > 0 {
			m.themePickerIndex--
			m.preview(m.themePickerIndex)
		}
	case keyDown, "j":
		if m.themePickerIndex < len(paletteOrder)-1 {
			m.themePickerIndex++
			m.preview(m.themePickerIndex)
		}
	case keyEnter:
		m.applyTheme(paletteOrder[m.themePickerIndex])
		m.overlay = OverlayNone
	}
	return m, nil
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
	return renderSettingsRow(t, w, icon, label, value, selected, false)
}

// renderSettingsRow is renderSettingsActionRow with a disabled state, for a
// setting another setting currently owns (bit-perfect quality under Data
// Saver). A disabled row still takes the cursor and still highlights when
// selected — pressing Enter on it explains why it won't change rather than
// silently doing nothing — but its text is faint so the list shows at a
// glance which rows are live.
func renderSettingsRow(t Theme, w int, icon, label, value string, selected, disabled bool) string {
	plain := " " + icon + " " + label
	pad := max(w-lipgloss.Width(plain)-lipgloss.Width(value)-1, 1)
	row := plain + strings.Repeat(" ", pad) + value
	if selected {
		st := lipgloss.NewStyle().Background(t.P.BgSel).Width(w)
		if disabled {
			st = st.Foreground(t.P.FgFaint)
		}
		return st.Render(row)
	}
	if disabled {
		return t.RowFaint.Render(truncateStr(row, w))
	}
	return t.RowDim.Render(truncateStr(row, w))
}

// renderSettingsList renders the Settings tab's fixed row list: output
// device, bit-perfect quality, Data Saver, CD-recorder silence gap, and
// Themes (which opens the floating Themes picker — see renderThemePickerOverlay).
func (m *Model) renderSettingsList(t Theme, w, h int) string {
	innerW := max(w-2, 1)
	rows := make([]string, 0, settingsRowCount+2)
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

	bitPerfectLabel := "Off (PipeWire)"
	if m.bitPerfectMode {
		bitPerfectLabel = "On (DAC)"
	}
	// Data Saver forces PipeWire, so while it is on this row reports what it
	// is locked to and why instead of a value the user cannot change.
	if m.lowDataMode {
		bitPerfectLabel = "Off — locked by Data Saver"
	}
	rows = append(rows, renderSettingsRow(t, innerW, "◆", "Bit-perfect quality", bitPerfectLabel, m.themeCursor == 1, m.lowDataMode))
	if m.themeCursor == 1 {
		cursorRow = len(rows) - 1
	}

	lowDataLabel := "Off"
	if m.lowDataMode {
		lowDataLabel = "On (PipeWire + lossy)"
	}
	rows = append(rows, renderSettingsActionRow(t, innerW, "◈", "Data Saver", lowDataLabel, m.themeCursor == 2))
	if m.themeCursor == 2 {
		cursorRow = len(rows) - 1
	}

	gapLabel := "Off (gapless)"
	if m.interTrackSilenceMs > 0 {
		gapLabel = fmt.Sprintf("On (%.1fs)", float64(m.interTrackSilenceMs)/1000)
	}
	rows = append(rows, renderSettingsActionRow(t, innerW, "◼", "CD-recorder silence gap", gapLabel, m.themeCursor == 3))
	if m.themeCursor == 3 {
		cursorRow = len(rows) - 1
	}

	themeLabel := paletteNames[m.themeName] + " ›"
	rows = append(rows, renderSettingsActionRow(t, innerW, "🎨", "Themes", themeLabel, m.themeCursor == 4))
	if m.themeCursor == 4 {
		cursorRow = len(rows) - 1
	}

	// Which build is running, so it can be read off the screen instead of
	// having to quit and run `gotidal -v`. Deliberately a footer rather than a
	// row: there is nothing to activate, so the cursor must not stop on it —
	// settingsRowCount stays at the five selectable rows above.
	rows = append(rows, "", t.RowFaint.Render(" gotidal "+buildinfo.Version()))

	return renderListPanel(t, "SETTINGS", m.focusMain, rows, cursorRow, w, h)
}

// renderThemePickerOverlay renders the floating Themes popup (OverlayThemePicker,
// opened from the Settings tab's Themes row): every built-in color scheme
// plus Auto, with a swatch preview per row. The active scheme is marked, the
// cursor row uses the cyan band, and a "previewing" hint shows on it.
func (m *Model) renderThemePickerOverlay(t Theme) string {
	w := min(max(m.width*2/3, 40), 56)
	innerW := w - 2
	rows := make([]string, 0, len(paletteOrder))

	for i, name := range paletteOrder {
		pal := resolvePalette(name)
		activeMark := " "
		if name == m.themeName {
			activeMark = "●"
		}
		label := paletteNames[name]
		body := " " + swatch(pal) + " " + label
		if i == m.themePickerIndex {
			hint := "previewing"
			plain := " " + activeMark + " ····· " + label
			pad := max(innerW-lipgloss.Width(plain)-len(hint)-1, 1)
			rowText := " " + activeMark + body + strings.Repeat(" ", pad) + hint
			rows = append(rows, lipgloss.NewStyle().Background(t.P.BgSel).Width(innerW).Render(rowText))
			continue
		}
		mark := t.RowFaint.Render(activeMark)
		if activeMark == "●" {
			mark = t.GreenT.Render(activeMark)
		}
		rows = append(rows, truncateStr(" "+mark+body, innerW))
	}

	h := min(len(rows)+2, m.height-4)
	body := strings.Join(rows, "\n")
	return renderPanel(t, "THEMES", true, w, max(h, 4), body)
}
