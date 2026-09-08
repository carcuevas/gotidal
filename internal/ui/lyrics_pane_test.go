package ui

import (
	"reflect"
	"testing"
)

// TestWrapText verifies word-boundary wrapping (not truncation) and the
// single-overlong-word fallback.
func TestWrapText(t *testing.T) {
	cases := []struct {
		name string
		in   string
		w    int
		want []string
	}{
		{
			name: "fits on one line",
			in:   "a short line",
			w:    20,
			want: []string{"a short line"},
		},
		{
			name: "wraps at a word boundary",
			in:   "one two three four five",
			w:    11,
			want: []string{"one two", "three four", "five"},
		},
		{
			name: "single word wider than w is hard-truncated",
			in:   "supercalifragilisticexpialidocious",
			w:    10,
			want: []string{"supercali…"},
		},
		{
			name: "empty string",
			in:   "",
			w:    10,
			want: []string{""},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := wrapText(c.in, c.w)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("wrapText(%q, %d) = %#v, want %#v", c.in, c.w, got, c.want)
			}
		})
	}
}
