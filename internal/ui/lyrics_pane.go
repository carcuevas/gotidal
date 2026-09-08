package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/carcuevas/gotidal/internal/lyrics"
	"github.com/carcuevas/gotidal/internal/store"
	"github.com/carcuevas/gotidal/internal/tidal"
)

// lyricsState holds the Lyrics pane's data for whichever track it was last
// fetched for. trackID guards against a slow fetch for a since-skipped track
// landing after the user has already moved on.
type lyricsState struct {
	trackID  int
	lines    []lyrics.Line
	plain    string
	loading  bool
	notFound bool
}

// lyricsLoadedMsg delivers a completed (or failed) lyrics lookup.
type lyricsLoadedMsg struct {
	trackID int
	result  *lyrics.Lyrics
	err     error
}

// fetchLyricsCmd checks the bbolt cache first, then falls back to LRCLIB.
// Both a hit and a cached "not found" are delivered as lyricsLoadedMsg so the
// caller doesn't need two code paths.
func fetchLyricsCmd(ctx context.Context, s *store.SecretsStore, t tidal.Track) tea.Cmd {
	trackID := t.ID
	if s != nil {
		if raw, found, ok := s.GetCachedLyrics(trackID); ok {
			if !found {
				return func() tea.Msg { return lyricsLoadedMsg{trackID: trackID, result: &lyrics.Lyrics{Found: false}} }
			}
			return func() tea.Msg {
				return lyricsLoadedMsg{trackID: trackID, result: &lyrics.Lyrics{Found: true, Lines: lyrics.ParseLRC(raw)}}
			}
		}
	}
	artist, title, album, dur := t.Artist.Name, t.Title, t.Album.Title, t.Duration
	return func() tea.Msg {
		res, err := lyrics.FetchSynced(ctx, artist, title, album, dur)
		if err != nil {
			return lyricsLoadedMsg{trackID: trackID, err: err}
		}
		if s != nil {
			if res.Found && len(res.Lines) > 0 {
				_ = s.CacheLyrics(trackID, rawFromLines(res.Lines), true)
			} else if !res.Found {
				_ = s.CacheLyrics(trackID, "", false)
			}
		}
		return lyricsLoadedMsg{trackID: trackID, result: res}
	}
}

// rawFromLines re-serializes parsed lines back to LRC text for caching, so a
// cache hit can be re-parsed the same way a fresh fetch would be.
func rawFromLines(lines []lyrics.Line) string {
	var b strings.Builder
	for _, ln := range lines {
		minutes := int(ln.TimeSec) / 60
		seconds := ln.TimeSec - float64(minutes*60)
		fmt.Fprintf(&b, "[%02d:%05.2f]%s\n", minutes, seconds, ln.Text)
	}
	return b.String()
}

// renderLyricsBody renders the lyrics pane's content: synced lines with the
// active one highlighted and vertically centered, a plain-text fallback, a
// loading spinner, or "No lyrics found".
func renderLyricsBody(t Theme, ls lyricsState, posSec float64, w, h int) string {
	switch {
	case ls.loading:
		return t.RowDim.Render("Loading lyrics…")
	case ls.notFound:
		return t.RowDim.Render("No lyrics found.")
	case len(ls.lines) > 0:
		active := lyrics.ActiveIndex(ls.lines, posSec)
		// Wrap every line to the pane's width first (rather than truncating
		// with "…") so a long line is never cut off — it just takes more
		// screen rows. wrapRows tracks which original lyric line each
		// resulting screen row belongs to, since one line can now span
		// several; activeRowStart is the first screen row of the active
		// line, so centering/highlighting operate on rows, not raw lines.
		type wrapRow struct {
			text    string
			lineIdx int
		}
		var wrapped []wrapRow
		activeRowStart := 0
		for i, ln := range ls.lines {
			if i == active {
				activeRowStart = len(wrapped)
			}
			for _, wl := range wrapText(ln.Text, w) {
				wrapped = append(wrapped, wrapRow{text: wl, lineIdx: i})
			}
		}
		start, end := visibleWindow(activeRowStart, len(wrapped), h)
		var rows []string
		for i := start; i < end; i++ {
			r := wrapped[i]
			if r.lineIdx == active {
				// Width(w) so the highlight band fills the pane's full width,
				// not just as wide as the text — matching every other
				// selection-band row in the app (e.g. renderSettingsActionRow)
				// rather than looking like a narrow color-only underline.
				rows = append(rows, t.LyricsActive.Width(w).Render(r.text))
			} else {
				rows = append(rows, t.RowDim.Render(r.text))
			}
		}
		return strings.Join(rows, "\n")
	case ls.plain != "":
		lines := strings.Split(ls.plain, "\n")
		start, end := visibleWindow(0, len(lines), h)
		var rows []string
		for i := start; i < end; i++ {
			rows = append(rows, t.RowDim.Render(truncateStr(lines[i], w)))
		}
		return strings.Join(rows, "\n")
	default:
		return t.RowDim.Render("No lyrics found.")
	}
}

// wrapText splits s into lines of at most w display columns, breaking at
// word boundaries so a long lyric line spans multiple screen rows instead of
// being cut off with truncateStr's "…". A single word wider than w on its
// own (rare) is hard-truncated rather than left overflowing.
func wrapText(s string, w int) []string {
	if w < 1 {
		return []string{s}
	}
	words := strings.Fields(s)
	if len(words) == 0 {
		return []string{""}
	}
	var lines []string
	cur := words[0]
	for _, word := range words[1:] {
		if ansi.StringWidth(cur)+1+ansi.StringWidth(word) <= w {
			cur += " " + word
			continue
		}
		lines = append(lines, cur)
		cur = word
	}
	lines = append(lines, cur)
	for i, ln := range lines {
		if ansi.StringWidth(ln) > w {
			lines[i] = truncateStr(ln, w)
		}
	}
	return lines
}
