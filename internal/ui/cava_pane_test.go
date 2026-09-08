package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestGradientColor verifies the gradient interpolates smoothly between
// stops rather than snapping between them — the fix for bars that repeated a
// small palette every few rows instead of reading as a continuous intensity
// ramp.
func TestGradientColor(t *testing.T) {
	// lipgloss.Color.RGBA() resolves through the active color profile, which
	// defaults to no-color/degraded outside a real terminal (e.g. under `go
	// test`) — force TrueColor so hex values round-trip exactly.
	lipgloss.SetColorProfile(termenv.TrueColor)
	stops := []lipgloss.TerminalColor{
		lipgloss.Color("#000000"),
		lipgloss.Color("#ffffff"),
	}

	cases := []struct {
		frac float64
		want string
	}{
		{0, "#000000"},
		{1, "#ffffff"},
		{0.5, "#7f7f7f"},
	}
	for _, c := range cases {
		got := gradientColor(stops, c.frac)
		if string(got) != c.want {
			t.Errorf("gradientColor(stops, %v) = %v, want %v", c.frac, got, c.want)
		}
	}
}

// TestGradientColorMultiStop verifies a mid-gradient fraction lands on the
// correct pair of adjacent stops rather than blending across the whole range.
func TestGradientColorMultiStop(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	stops := []lipgloss.TerminalColor{
		lipgloss.Color("#000000"),
		lipgloss.Color("#ff0000"),
		lipgloss.Color("#ffffff"),
	}
	// frac=0.25 is halfway between stop 0 and stop 1 (pos = 0.25*2 = 0.5).
	got := gradientColor(stops, 0.25)
	want := "#7f0000"
	if string(got) != want {
		t.Errorf("gradientColor(stops, 0.25) = %v, want %v", got, want)
	}
}
