package ui

import (
	"fmt"
	"image"
	"os"
	"strings"
	"sync"

	sixel "github.com/mattn/go-sixel"
	"golang.org/x/sys/unix"
)

// SixelSupported reports whether the running terminal supports Sixel
// graphics. foot is the primary target: its own changelog shows extensive
// Sixel work (aspect ratio, color palette, scrolling fixes) and never once
// mentions the Kitty graphics protocol, confirming Sixel — not Kitty — is
// foot's real image protocol.
func SixelSupported() bool {
	term := os.Getenv("TERM")
	if term == "foot" || term == "foot-direct" {
		return true
	}
	// $TERM alone isn't reliable: foot.ini commonly overrides it for wider
	// app compatibility (Omarchy's own default config sets
	// term=xterm-256color), erasing every trace of "foot" from the
	// environment despite the terminal genuinely being foot. Fall back to
	// checking whether an ancestor process actually is the foot binary.
	return ancestorIsFoot()
}

// defaultCellPxW/H is the fallback terminal cell size (in pixels) used when
// TIOCGWINSZ doesn't report real pixel dimensions (some terminals and
// multiplexers leave ws_xpixel/ws_ypixel at 0). Roughly matches a common
// monospace terminal font at a typical point size.
const (
	defaultCellPxW = 9.0
	defaultCellPxH = 18.0
)

// cellPixelSize returns the terminal's actual cell size in pixels, queried
// via TIOCGWINSZ on fd — this is what makes a Sixel image crisp instead of
// terminal-rescaled: Sixel has no cell-box placement primitive like Kitty
// does, so the image must be encoded at the real target pixel size up front.
// Falls back to defaultCellPxW/H when the terminal doesn't report it.
func cellPixelSize(fd int) (w, h float64) {
	ws, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return defaultCellPxW, defaultCellPxH
	}
	return float64(ws.Xpixel) / float64(ws.Col), float64(ws.Ypixel) / float64(ws.Row)
}

// sixelState memoizes the encoded cover and tracks what is currently drawn,
// so syncSixelCover only re-encodes/re-transmits on a real change — mirrors
// kittyState, but Sixel has no "upload once, place many times" primitive:
// the pixel size is baked into the encoded data itself, so a geometry change
// forces a full re-encode, not just a cheap re-placement.
type sixelState struct {
	mu sync.Mutex

	encodeKey string // cover UUID + target pixel size the cached escape was built for
	escape    string // cached encode for encodeKey

	drawnKey             string // encodeKey + screen position currently on screen ("" = nothing/cleared)
	drawnCol, drawnRow   int
	drawnCols, drawnRows int // cell box size currently on screen, for clearing

	stale bool
}

// sixelEncode renders img as a Sixel escape sequence sized to pxW×pxH
// pixels. Returns "" on any encode failure (e.g. a decoded image with zero
// bounds) rather than writing partial/garbage data to the terminal.
func sixelEncode(img image.Image, pxW, pxH int) string {
	if img == nil || pxW <= 0 || pxH <= 0 {
		return ""
	}
	var buf strings.Builder
	enc := sixel.NewEncoder(&buf)
	enc.Width = pxW
	enc.Height = pxH
	enc.Colors = 256
	enc.Dither = true
	if err := enc.Encode(img); err != nil {
		return ""
	}
	return buf.String()
}

// sixelPlaceAt wraps a pre-encoded Sixel escape with cursor save/move/
// restore so it draws at the box's absolute screen position without
// disturbing the cursor the text frame left behind. This is the Sixel
// equivalent of kittyPlaceAt, adapted to Sixel having no separate
// "placement" primitive of its own: the image is simply rasterized wherever
// the cursor is when the terminal processes it.
func sixelPlaceAt(col, row int, escape string) string {
	if escape == "" {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "\x1b7\x1b[%d;%dH", row, col)
	sb.WriteString(escape)
	sb.WriteString("\x1b8")
	return sb.String()
}

// sixelClearBox erases a previously drawn Sixel image by overwriting its
// cell box with plain spaces. Sixel has no separate compositing layer or
// "delete placement" primitive the way Kitty graphics does (see
// kittyClearCover) — the image lives in the same character grid as text, so
// blanking it the same way any other stale content would be blanked is the
// standard way to clear it.
func sixelClearBox(col, row, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	blank := strings.Repeat(" ", cols)
	var sb strings.Builder
	sb.WriteString("\x1b7")
	for i := range rows {
		fmt.Fprintf(&sb, "\x1b[%d;%dH%s", row+i, col, blank)
	}
	sb.WriteString("\x1b8")
	return sb.String()
}
