// Package sanitize strips terminal control characters out of text that
// arrives from a remote source (the Tidal API, LRCLIB, Spotify page scrapes)
// before any of it is rendered into the user's terminal.
//
// A TUI writes remote strings straight into a terminal that interprets escape
// sequences, and neither lipgloss nor BubbleTea filters them — lipgloss's
// width helpers deliberately treat escapes as zero-width and pass them
// through, so a title or a lyric line can smuggle in whatever it likes. A
// track, album or playlist named
//
//	Chill Mix\x1b]52;c;<base64>\x07
//
// writes to the user's clipboard on any terminal with OSC 52 enabled; other
// sequences repaint the screen, set the window title, forge hyperlinks, or
// leave the terminal in a broken state. LRCLIB is the sharpest edge of this:
// it is unauthenticated, so anyone can publish lyrics for a popular track.
//
// Sanitizing has to happen after JSON decoding, not on the wire: a literal
// control byte is invalid inside a JSON string, so a hostile value arrives
// escaped (\u001b) and only becomes a real control character once decoded.
package sanitize

import (
	"reflect"
	"strings"
)

// Text returns s with every character a terminal could interpret as a control
// code removed: C0 (including ESC, BEL, CR, LF and TAB), DEL, and C1 (which
// includes U+009B, the 8-bit CSI). Printable Unicode — accents, CJK, emoji —
// is left alone, so legitimate titles are unaffected.
//
// Newlines are dropped too: this is for single-line values (titles, names),
// where a newline would break the row layout as surely as an escape would.
// Use Multiline for text that is legitimately multi-line.
func Text(s string) string { return strip(s, false) }

// Multiline is Text but keeps newlines, for values where line breaks are
// meaningful (plain-text lyrics). Carriage returns are still dropped — on a
// terminal a lone CR rewrites the current line rather than starting a new one.
func Multiline(s string) string { return strip(s, true) }

func strip(s string, keepNewline bool) string {
	if !needsStrip(s, keepNewline) {
		return s // overwhelmingly the common case: no allocation
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) && (!keepNewline || r != '\n') {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func needsStrip(s string, keepNewline bool) bool {
	for _, r := range s {
		if isControl(r) && (!keepNewline || r != '\n') {
			return true
		}
	}
	return false
}

// isControl reports whether r is a C0 control, DEL, or a C1 control.
func isControl(r rune) bool {
	return r < 0x20 || r == 0x7F || (r >= 0x80 && r <= 0x9F)
}

// Strings walks v — which must be a pointer — and applies Text to every
// string it can reach through structs, slices, arrays, maps, pointers and
// interfaces. It is meant to be run on a freshly decoded API response so that
// every user-visible field is covered, including fields added later that
// nobody remembers to sanitize by hand.
//
// Unexported fields are skipped (reflect cannot set them); none of the decoded
// API types have any.
func Strings(v any) {
	if v == nil {
		return
	}
	sanitizeValue(reflect.ValueOf(v))
}

func sanitizeValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			sanitizeValue(v.Elem())
		}
	case reflect.Struct:
		for _, f := range v.Fields() {
			sanitizeValue(f)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			sanitizeValue(v.Index(i))
		}
	case reflect.Map:
		// Map values aren't addressable, so they're rewritten wholesale.
		for _, k := range v.MapKeys() {
			mv := v.MapIndex(k)
			if mv.Kind() == reflect.String {
				if cleaned := Text(mv.String()); cleaned != mv.String() {
					nv := reflect.New(mv.Type()).Elem()
					nv.SetString(cleaned)
					v.SetMapIndex(k, nv)
				}
				continue
			}
			// Non-string values need a settable copy to recurse into.
			cp := reflect.New(mv.Type()).Elem()
			cp.Set(mv)
			sanitizeValue(cp)
			v.SetMapIndex(k, cp)
		}
	case reflect.String:
		if v.CanSet() {
			if cleaned := Text(v.String()); cleaned != v.String() {
				v.SetString(cleaned)
			}
		}
	default:
	}
}
