package main

import (
	"strings"
	"testing"
)

// gotidal registers itself as the tidal:// scheme handler, so any web page can
// make the desktop run `gotidal play <url>`. These cases are the ones that
// must not reach the terminal-launch argv or the API URL builders.
func TestValidatePlayURL(t *testing.T) {
	valid := []string{
		"tidal://track/12345",
		"tidal://album/67890",
		"tidal://mix/00abc123def",
		"https://tidal.com/browse/track/12345",
		"https://listen.tidal.com/album/1",
		"https://www.tidal.com/track/1?u=2",
		"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M",
	}
	for _, u := range valid {
		t.Run("accept "+u, func(t *testing.T) {
			if err := validatePlayURL(u); err != nil {
				t.Errorf("validatePlayURL(%q) = %v, want nil", u, err)
			}
		})
	}

	invalid := []struct{ name, url string }{
		// A space is what would let xfce4-terminal's argv splitting (and any
		// other "-e <string>" convention) see more than one argument.
		{"embedded space", "tidal://track/1 --flag"},
		{"shell quote", `tidal://track/1'"`},
		{"backtick", "tidal://track/1`id`"},
		{"backslash", `tidal://track/1\x`},
		{"newline", "tidal://track/1\ntidal://track/2"},
		{"terminal escape", "tidal://track/1\x1b]52;c;aGk=\x07"},
		{"file scheme", "file:///etc/passwd"},
		{"foreign http host", "https://evil.example.com/track/1"},
		{"lookalike host", "https://tidal.com.evil.example/track/1"},
		{"plain http", "http://tidal.com/track/1"},
		{"javascript scheme", "javascript:alert(1)"},
		{"no scheme", "track/1"},
	}
	for _, c := range invalid {
		t.Run("reject "+c.name, func(t *testing.T) {
			if err := validatePlayURL(c.url); err == nil {
				t.Errorf("validatePlayURL(%q) = nil, want an error", c.url)
			}
		})
	}
}

func TestValidatePlayURLLengthCap(t *testing.T) {
	long := "tidal://track/" + strings.Repeat("1", 3000)
	if err := validatePlayURL(long); err == nil {
		t.Error("an over-long URL should be rejected")
	}
}

// Every terminal must receive the URL as its own argv element. xfce4-terminal
// used to get "-e", self+" "+url — a single string it re-splits itself, which
// let the URL widen the command line.
func TestTerminalCandidatesPassURLAsSeparateArg(t *testing.T) {
	const self, target = "/usr/bin/gotidal", "tidal://track/1"

	for _, c := range terminalCandidates() {
		if c.bin == "" {
			continue // $TERMINAL is unset in the test environment
		}
		t.Run(c.bin, func(t *testing.T) {
			args := c.args(self, target)
			var found bool
			for _, a := range args {
				switch {
				case a == target:
					found = true
				case strings.Contains(a, target):
					t.Errorf("URL folded into %q instead of being its own argument", a)
				case a != self && strings.Contains(a, self):
					t.Errorf("executable path folded into %q", a)
				}
			}
			if !found {
				t.Errorf("URL never appears as its own argument in %v", args)
			}
		})
	}
}
