package sanitize

import "testing"

func TestTextStripsControlSequences(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"clean text is untouched", "Kishi Bashi — 151a", "Kishi Bashi — 151a"},
		{"emoji and accents survive", "Café ☕ 東京", "Café ☕ 東京"},
		// Removing ESC (and BEL) is what defuses these — the remaining bytes
		// are ordinary printable characters and render as inert text.
		{"OSC 52 clipboard write", "Chill Mix\x1b]52;c;aGFjaw==\x07", "Chill Mix]52;c;aGFjaw=="},
		{"CSI screen clear", "Album\x1b[2J\x1b[H", "Album[2J[H"},
		{"OSC 8 hyperlink", "T\x1b]8;;http://evil\x1b\\itle", `T]8;;http://evil\itle`},
		{"window title set", "x\x1b]0;pwned\x07", "x]0;pwned"},
		{"bare BEL and backspace", "a\x07b\x08c", "abc"},
		{"newline and CR dropped", "line1\r\nline2", "line1line2"},
		{"tab dropped", "a\tb", "ab"},
		{"DEL dropped", "a\x7fb", "ab"},
		{"8-bit CSI (U+009B) dropped", "a\u009b2Jb", "a2Jb"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Text(c.in); got != c.want {
				t.Errorf("Text(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMultilineKeepsNewlinesOnly(t *testing.T) {
	in := "verse one\nverse two\r\x1b[31mred\x1b[0m\ttab"
	want := "verse one\nverse two[31mred[0mtab"
	if got := Multiline(in); got != want {
		t.Errorf("Multiline(%q) = %q, want %q", in, got, want)
	}
}

func TestStringsWalksNestedStructures(t *testing.T) {
	type inner struct {
		Name string
	}
	type outer struct {
		Title   string
		Nested  inner
		Ptr     *inner
		List    []inner
		Strs    []string
		Lookup  map[string]string
		Ignored int
	}

	bad := "x\x1b]52;c;h\x07"
	v := &outer{
		Title:   bad,
		Nested:  inner{Name: bad},
		Ptr:     &inner{Name: bad},
		List:    []inner{{Name: bad}},
		Strs:    []string{bad},
		Lookup:  map[string]string{"k": bad},
		Ignored: 42,
	}
	Strings(v)

	const want = "x]52;c;h"
	if v.Title != want {
		t.Errorf("Title = %q, want %q", v.Title, want)
	}
	if v.Nested.Name != want {
		t.Errorf("Nested.Name = %q, want %q", v.Nested.Name, want)
	}
	if v.Ptr.Name != want {
		t.Errorf("Ptr.Name = %q, want %q", v.Ptr.Name, want)
	}
	if v.List[0].Name != want {
		t.Errorf("List[0].Name = %q, want %q", v.List[0].Name, want)
	}
	if v.Strs[0] != want {
		t.Errorf("Strs[0] = %q, want %q", v.Strs[0], want)
	}
	if v.Lookup["k"] != want {
		t.Errorf("Lookup[k] = %q, want %q", v.Lookup["k"], want)
	}
	if v.Ignored != 42 {
		t.Errorf("non-string field was disturbed: %d", v.Ignored)
	}
}

func TestStringsHandlesNilAndNonPointer(t *testing.T) {
	Strings(nil)              // must not panic
	Strings("not a pointer")  // unaddressable; must not panic
	var p *struct{ S string } // deliberately nil
	Strings(p)                // nil pointer; must not panic
	Strings(&struct{}{})      // no string fields
}
