package ui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// sortPlaylists orders the list the way it is displayed: alphabetically by
// title, case-insensitively, ties broken by UUID so the order is stable
// across refetches. Applied everywhere m.playlists is assigned rather than at
// render time, so the cursor indexes the same order the user sees.
//
// Tidal's own endpoint orders by date, which puts a playlist somewhere
// different every time one is edited — fine as a feed, useless as a list you
// navigate by name.
func sortPlaylists(pls []tidal.Playlist) {
	sort.SliceStable(pls, func(i, j int) bool {
		a, b := strings.ToLower(pls[i].Title), strings.ToLower(pls[j].Title)
		if a != b {
			return a < b
		}
		return pls[i].UUID < pls[j].UUID
	})
}

// applyPlaylistUpserted folds a create-or-append back into the cached list so
// the Playlists tab reflects it immediately.
//
// Without this the list was fetched once per session and never again: a
// playlist created from Ctrl+S, the command palette, the action sheet's "Add
// to playlist…", or a Spotify import simply did not appear on the Playlists
// tab until gotidal was restarted, and appending to an existing one left its
// track count stale. Applied locally rather than by refetching because
// Tidal's list endpoint is not reliably read-your-writes right after a
// create; markPlaylistsStale schedules the authoritative refresh instead.
func (m *Model) applyPlaylistUpserted(uuid, name string, added int, created bool) {
	defer m.markPlaylistsStale()

	if uuid == "" {
		return
	}
	for i := range m.playlists {
		if m.playlists[i].UUID == uuid {
			m.playlists[i].NumberOfTracks += added
			if name != "" {
				m.playlists[i].Title = name
			}
			sortPlaylists(m.playlists)
			return
		}
	}
	if !created {
		// An append to a playlist that isn't in the cache — the cache is
		// simply incomplete (never loaded, or created elsewhere). The refresh
		// deferred above picks it up with a real track count; inventing a row
		// from an append's delta would show the wrong one.
		return
	}
	m.playlists = append(m.playlists, tidal.Playlist{
		UUID:           uuid,
		Title:          name,
		NumberOfTracks: added,
	})
	sortPlaylists(m.playlists)
}

// markPlaylistsStale flags the cached list for a refetch the next time the
// Playlists tab (or a playlist picker) needs it. The local edits above keep
// the UI correct in the meantime; this is what eventually reconciles it with
// changes made in another client, and with Tidal's own track counts.
func (m *Model) markPlaylistsStale() { m.playlistsStale = true }

// reloadPlaylistsCmd refetches the user's playlists.
func (m *Model) reloadPlaylistsCmd() tea.Cmd {
	client := m.client
	ctx := m.ctx
	return func() tea.Msg {
		pls, err := client.GetUserPlaylists(ctx)
		if err != nil {
			return errMsg(err)
		}
		return playlistsMsg(pls)
	}
}
