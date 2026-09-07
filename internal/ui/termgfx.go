package ui

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/draw"
)

// KittySupported reports whether the running terminal supports the Kitty
// terminal graphics protocol. Ghostty, Kitty, and WezTerm qualify.
//
// foot does NOT: despite superficially "kitty-branded" env vars floating
// around in some setups, foot's own changelog shows it implements the kitty
// *keyboard* protocol and OSC-99 desktop notifications, but never the kitty
// *graphics* protocol — foot's actual image protocol is Sixel instead (see
// SixelSupported).
func KittySupported() bool {
	switch os.Getenv("TERM_PROGRAM") {
	case "ghostty", "WezTerm":
		return true
	}
	return os.Getenv("KITTY_WINDOW_ID") != ""
}

// ancestorIsFoot walks up the process tree (Linux only, via /proc) looking
// for "foot" — covers both a WM keybinding that execs `foot ... gotidal`
// directly (parent is foot) and running gotidal from an interactive shell
// prompt inside an already-open foot window (parent is the shell, foot is
// further up). Bounded to a shallow walk; any read failure (non-Linux, a
// vanished pid) just stops the walk and reports no match.
func ancestorIsFoot() bool {
	pid := os.Getpid()
	for range 10 {
		ppid, comm, ok := procParent(pid)
		if !ok {
			return false
		}
		if comm == "foot" {
			return true
		}
		if ppid <= 1 {
			return false
		}
		pid = ppid
	}
	return false
}

// procParent reads /proc/<pid>/stat for its parent pid and executable name.
// The name is delimited by the first '(' and the *last* ')' in the line
// (not the first) since comm can itself contain parentheses or spaces.
func procParent(pid int) (ppid int, comm string, ok bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, "", false
	}
	s := string(data)
	open := strings.IndexByte(s, '(')
	closeIdx := strings.LastIndexByte(s, ')')
	if open < 0 || closeIdx < 0 || closeIdx < open || closeIdx+2 > len(s) {
		return 0, "", false
	}
	comm = s[open+1 : closeIdx]
	fields := strings.Fields(s[closeIdx+1:])
	if len(fields) < 2 {
		return 0, "", false
	}
	ppidVal, err := strconv.Atoi(fields[1]) // fields[0] is state, fields[1] is ppid
	if err != nil {
		return 0, "", false
	}
	return ppidVal, comm, true
}

// kittyChunkSize is the maximum base64 payload length per Kitty APC chunk.
const kittyChunkSize = 4096

// kittyState memoizes the encoded cover image and tracks what is currently
// drawn on screen, so syncKittyCover only re-encodes/re-transmits on a real
// change.
//
// BubbleTea runs every command in its own goroutine, so several syncs can be in
// flight at once (a drag-resize fires a burst of WindowSizeMsg). mu serializes
// them: a Kitty transmission is a stateful sequence of escapes, and interleaving
// two of them on the same file descriptor corrupts both.
type kittyState struct {
	mu sync.Mutex

	encodeKey string // cover UUID + box geometry the cached escape was built for
	escape    string // cached draw escape for encodeKey
	drawnKey  string // the encodeKey currently displayed (""=nothing/cleared)

	// stale marks that whatever is on screen can no longer be trusted — set on
	// resize, where the terminal may keep placements at their old coordinates.
	// The next frame clears and redraws unconditionally.
	stale bool
}

// kittyClearCover returns the Kitty escape that removes the cover's placements
// from the screen. Kitty images live outside the terminal's cell grid, so
// redrawing the text frame does not erase them — this must be emitted whenever
// the cover moves or stops being shown, or a stale copy lingers at the old
// coordinates.
//
// d=i (lowercase) deletes the placements but keeps the uploaded image data, so
// the next placement can reuse it without re-transmitting; the uppercase D
// variant would free the data and invalidate that cache. It also targets only
// our image id rather than every image on screen, so it cannot clobber graphics
// belonging to a multiplexer or another pane.
func kittyClearCover() string {
	return fmt.Sprintf("\x1b_Ga=d,d=i,i=%d,q=2\x1b\\", kittyCoverImageID)
}

// kittyCoverImageID is the Kitty image id the cover is transmitted under.
// Reusing a fixed id lets a redraw re-place the already-uploaded image instead
// of re-sending it, and lets a delete target just this image.
const kittyCoverImageID = 9137

// kittyTransmit returns the escape that uploads img to the terminal under
// kittyCoverImageID without placing it on screen (a=t). Transmission is the
// expensive half — a few hundred KB of base64 — so it is done once per cover
// and reused by every subsequent placement.
func kittyTransmit(img image.Image) string {
	if img == nil {
		return ""
	}
	// Scale to a generous pixel size for sharpness; the terminal maps it onto
	// the cols×rows cell box regardless of pixel dimensions.
	const targetPx = 640
	scaled := image.NewRGBA(image.Rect(0, 0, targetPx, targetPx))
	draw.CatmullRom.Scale(scaled, scaled.Bounds(), img, img.Bounds(), draw.Over, nil)

	var buf bytes.Buffer
	if err := png.Encode(&buf, scaled); err != nil {
		return ""
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	var sb strings.Builder
	for i := 0; i < len(encoded); i += kittyChunkSize {
		end := min(i+kittyChunkSize, len(encoded))
		chunk := encoded[i:end]
		more := 1
		if end >= len(encoded) {
			more = 0
		}
		if i == 0 {
			fmt.Fprintf(&sb, "\x1b_Ga=t,f=100,i=%d,q=2,m=%d;%s\x1b\\", kittyCoverImageID, more, chunk)
		} else {
			fmt.Fprintf(&sb, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
	}
	return sb.String()
}

// kittyPlaceAt returns the escape that places the already-transmitted cover
// image so it occupies exactly cols×rows cells at the absolute 1-indexed screen
// coordinates (row, col). This is a few dozen bytes, so it is cheap enough to
// re-issue on every frame of a drag-resize.
//
// The cursor is saved and restored around the placement so it does not disturb
// the text frame underneath.
func kittyPlaceAt(col, row, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	var sb strings.Builder
	// Save cursor, move to the box's top-left, place, restore cursor.
	fmt.Fprintf(&sb, "\x1b7\x1b[%d;%dH", row, col)
	fmt.Fprintf(&sb, "\x1b_Ga=p,i=%d,c=%d,r=%d,q=2\x1b\\", kittyCoverImageID, cols, rows)
	sb.WriteString("\x1b8") // restore cursor
	return sb.String()
}
