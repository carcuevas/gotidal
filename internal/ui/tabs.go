package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Section is the active tab — the primary navigation axis, presented as a
// numbered top tab bar (rmpc-style) rather than a sidebar.
type Section int

const (
	SecNowPlaying Section = iota
	SecQueue
	SecPlaylists
	SecFavSongs
	SecFavArtists
	SecFavAlbums
	SecHistory // Recently Played
	SecMixes   // Daily Mixes
	SecSearch
	SecSettings // theme picker
)

// tabEntry is one entry in the top tab bar.
type tabEntry struct {
	section Section
	icon    string
	label   string
}

// tabEntries is the fixed, numbered tab order (1-9), matching rmpc's own
// numbered SwitchToTab bindings.
var tabEntries = []tabEntry{
	{section: SecQueue, icon: "≣", label: "Queue"},
	{section: SecPlaylists, icon: "≡", label: "Playlists"},
	{section: SecFavArtists, icon: "◎", label: "Artists"},
	{section: SecFavAlbums, icon: "⊞", label: "Albums"},
	{section: SecFavSongs, icon: "♥", label: "Songs"},
	{section: SecMixes, icon: "✦", label: "Mixes"},
	{section: SecSearch, icon: "/", label: "Search"},
	{section: SecHistory, icon: "↺", label: "History"},
	{section: SecSettings, icon: "◐", label: "Settings"},
}

// sectionCount returns the badge count for a section, or -1 to omit it.
func (m *Model) sectionCount(sec Section) int {
	switch sec {
	case SecQueue:
		return len(m.tracks)
	case SecPlaylists:
		return len(m.playlists)
	case SecFavSongs:
		return len(m.favSongs)
	case SecFavArtists:
		return len(m.favArtists)
	case SecFavAlbums:
		return len(m.favAlbums)
	case SecHistory:
		return len(m.history)
	default:
		return -1
	}
}

// renderTabBar draws the single-line, numbered top tab bar (rmpc-style),
// truncated to exactly w columns. The active tab is highlighted; a CLIENT
// badge is appended in client mode.
func (m *Model) renderTabBar(t Theme, w int) string {
	var parts []string
	for i, e := range tabEntries {
		label := lipgloss.NewStyle().Render(" ") + itoaTab(i+1) + " " + e.icon + " " + e.label + " "
		if e.section == m.section {
			parts = append(parts, t.SideItemActive.Render(label))
		} else {
			parts = append(parts, t.SideItem.Render(label))
		}
	}
	line := strings.Join(parts, t.KeyBarSep.Render("│"))

	badge := ""
	if m.clientMode {
		badge = t.SideItemActive.Render(" ⇄ CLIENT")
	}
	// Reserve room for the badge before truncating the tab list, so a narrow
	// terminal clips tabs rather than clipping off the CLIENT indicator.
	avail := max(w-lipgloss.Width(badge), 0)
	return truncateStr(line, avail) + badge
}

// itoaTab renders a tab number 1-9 without pulling in strconv at every call site.
func itoaTab(n int) string {
	if n < 0 || n > 9 {
		return "?"
	}
	return string(rune('0' + n))
}

// tabIndexOf returns the index of a section within tabEntries.
func tabIndexOf(sec Section) int {
	for i, e := range tabEntries {
		if e.section == sec {
			return i
		}
	}
	return 0
}

// sectionTitle returns the panel title for a section.
func sectionTitle(sec Section) string {
	switch sec {
	case SecNowPlaying:
		return "NOW PLAYING"
	case SecQueue:
		return "QUEUE"
	case SecPlaylists:
		return "PLAYLISTS"
	case SecFavSongs:
		return "SONGS"
	case SecFavArtists:
		return "ARTISTS"
	case SecFavAlbums:
		return "ALBUMS"
	case SecHistory:
		return "RECENTLY PLAYED"
	case SecMixes:
		return "DAILY MIXES"
	case SecSearch:
		return "SEARCH"
	case SecSettings:
		return "SETTINGS"
	default:
		return ""
	}
}
