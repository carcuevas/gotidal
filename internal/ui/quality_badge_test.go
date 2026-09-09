package ui

import (
	"testing"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// The badge is coloured by the rate that actually reached the device, so which
// hi-res tier you got is legible at a glance without putting the numbers back
// into the text (which was dropped deliberately).
//
// Asserted against the chosen style rather than the rendered string: lipgloss
// drops colour when there is no TTY, so both colours render identically under
// `go test` and a string comparison would pass for either.
func TestQualityBadgeColourFollowsRate(t *testing.T) {
	theme := paletteGoTidal.Theme()
	pal := paletteGoTidal

	cases := []struct {
		name       string
		quality    tidal.Quality
		rate       uint32
		bitPerfect bool
		wantFg     any
		wantText   string
	}{
		{"192k hi-res is violet", tidal.QualityHiRes, 192000, true, pal.Purple, "hi-res"},
		{"176.4k is the same family as 192k", tidal.QualityHiRes, 176400, true, pal.Purple, "hi-res"},
		{"96k hi-res is green", tidal.QualityHiRes, 96000, true, pal.Green, "hi-res"},
		{"88.2k is the same family as 96k", tidal.QualityHiRes, 88200, true, pal.Green, "hi-res"},
		// A hi-res tier delivered at a CD-family rate gets neither colour:
		// nothing hi-res is actually coming out of the device.
		{"48k under a hi-res tier is neither", tidal.QualityHiRes, 48000, true, pal.FgFaint, "hi-res"},
		{"lossless is not coloured by rate", tidal.QualityLossless, 192000, true, pal.FgFaint, "lossless"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// dacMode=true throughout: these cases are about rate/lossy-ness,
			// not the DAC-vs-PipeWire distinction covered separately below.
			text, style, ok := qualityBadge(theme, c.quality, c.rate, c.bitPerfect, true)
			if !ok {
				t.Fatal("expected a badge")
			}
			if text != c.wantText {
				t.Errorf("text = %q, want %q", text, c.wantText)
			}
			if got := style.GetForeground(); got != c.wantFg {
				t.Errorf("foreground = %v, want %v", got, c.wantFg)
			}
		})
	}
}

// A lossy tier keeps its warning regardless of rate: being delivered at a high
// sample rate does not make it lossless.
func TestQualityBadgeLossyWinsOverRate(t *testing.T) {
	theme := paletteGoTidal.Theme()
	for _, q := range []tidal.Quality{tidal.QualityHigh, tidal.QualityLow} {
		text, style, ok := qualityBadge(theme, q, 192000, true, true)
		if !ok {
			t.Fatal("expected a badge")
		}
		if text != q.Label()+" (lossy)" {
			t.Errorf("text = %q, want it marked lossy", text)
		}
		if style.GetForeground() != paletteGoTidal.Rose {
			t.Errorf("%s at 192 kHz should keep the warning colour, got %v", q, style.GetForeground())
		}
	}
}

// Output that is not reaching the DAC untouched via the hw:->plughw: fallback
// must still say so — this is a real, verified format compromise, unlike
// PipeWire mode below.
func TestQualityBadgeConvertedIsMarkedForThePlughwFallback(t *testing.T) {
	theme := paletteGoTidal.Theme()
	// dacMode=true (DAC path was requested) but bitPerfect=false (the
	// negotiation itself had to fall back), which is exactly what the
	// hw:->plughw: fallback looks like from here.
	text, _, ok := qualityBadge(theme, tidal.QualityLossless, 44100, false, true)
	if !ok {
		t.Fatal("expected a badge")
	}
	if text != "lossless (converted)" {
		t.Errorf("text = %q, want it marked converted", text)
	}
}

// Reported: a lossless track played through PipeWire mode showed
// "lossless (converted)" — every single time, regardless of the track,
// because bitPerfect is unconditionally false whenever PipeWire is the
// chosen output path. That claimed a downgrade PipeWire may or may not have
// actually made (we cannot tell), which read as broken. PipeWire being the
// user's own choice must not itself trigger the "(converted)" label — only a
// real fallback (asserted above) does.
func TestQualityBadgePipeWireIsNotMarkedConverted(t *testing.T) {
	theme := paletteGoTidal.Theme()
	// dacMode=false: PipeWire mode. bitPerfect is always false here too (see
	// AudioPath), which is exactly the state that used to produce
	// "(converted)" unconditionally.
	text, _, ok := qualityBadge(theme, tidal.QualityLossless, 44100, false, false)
	if !ok {
		t.Fatal("expected a badge")
	}
	if text != "lossless" {
		t.Errorf("text = %q, want the plain tier with no conversion claim", text)
	}
}

// Hi-res colouring must still apply through PipeWire — the stream itself is
// genuinely 192kHz regardless of what PipeWire's graph might do downstream,
// and that shouldn't be hidden behind the output-path distinction.
func TestQualityBadgeHiResColourSurvivesPipeWireMode(t *testing.T) {
	theme := paletteGoTidal.Theme()
	text, style, ok := qualityBadge(theme, tidal.QualityHiRes, 192000, false, false)
	if !ok {
		t.Fatal("expected a badge")
	}
	if text != "hi-res" {
		t.Errorf("text = %q, want the plain hi-res label", text)
	}
	if style.GetForeground() != paletteGoTidal.Purple {
		t.Errorf("192kHz hi-res through PipeWire should still be violet, got %v", style.GetForeground())
	}
}

// With nothing playing there is no tier to report.
func TestQualityBadgeEmptyWhenNoQuality(t *testing.T) {
	if _, _, ok := qualityBadge(paletteGoTidal.Theme(), "", 0, true, true); ok {
		t.Error("an empty quality should produce no badge")
	}
}

// Every theme has to distinguish the two hi-res colours, or switching theme
// would silently collapse them into one.
func TestAllPalettesDistinguishHiResColours(t *testing.T) {
	for _, name := range paletteOrder {
		t.Run(name, func(t *testing.T) {
			p := resolvePalette(name)
			if p.Purple == p.Green {
				t.Errorf("theme %q uses the same colour for Purple and Green, so 192 kHz and 96 kHz would look identical", name)
			}
			theme := p.Theme()
			if theme.QualityHiRes192.GetForeground() != p.Purple {
				t.Errorf("theme %q: the 192 kHz badge is not violet", name)
			}
			if theme.QualityHiRes96.GetForeground() != p.Green {
				t.Errorf("theme %q: the 96 kHz badge is not green", name)
			}
		})
	}
}
