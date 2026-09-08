package ui

import "github.com/charmbracelet/lipgloss"

// The built-in color schemes. Hex values are transcribed verbatim from the
// design handoff's SCHEMES block (TIDALT Redesign.html, the --token: #hex map
// per scheme). OnAccent is the scheme's modal/bg-ish dark used for text on a
// cyan selection band.

func c(hex string) lipgloss.TerminalColor { return lipgloss.Color(hex) }

var paletteGoTidal = Palette{
	Bg: c("#262b33"), Bg2: c("#2c323b"), BgSel: c("#323a45"), BgModal: c("#20252c"),
	Fg: c("#c5cad3"), FgDim: c("#6c7682"), FgFaint: c("#4b545f"),
	Cyan: c("#38b6f0"), CyanSoft: c("#5bc0f0"), Teal: c("#5fd0c5"), Green: c("#7fd6a0"),
	Purple: c("#a679e8"), Indigo: c("#6a74e8"), Amber: c("#e0b057"), Rose: c("#e87a7a"),
	Border: c("#3a414b"), BorderSoft: c("#313842"), BorderAccent: c("#2f6f8f"),
	ProgA: c("#5b6ee1"), ProgB: c("#a05be1"),
	Grad1: c("#6fe0c9"), Grad2: c("#4db6e6"), Grad3: c("#6a74e8"), Grad4: c("#8f7fe8"),
	OnAccent: c("#0f2733"),
}

var paletteCatppuccin = Palette{
	Bg: c("#1e1e2e"), Bg2: c("#313244"), BgSel: c("#45475a"), BgModal: c("#181825"),
	Fg: c("#cdd6f4"), FgDim: c("#a6adc8"), FgFaint: c("#6c7086"),
	Cyan: c("#89dceb"), CyanSoft: c("#74c7ec"), Teal: c("#94e2d5"), Green: c("#a6e3a1"),
	Purple: c("#cba6f7"), Indigo: c("#b4befe"), Amber: c("#f9e2af"), Rose: c("#f38ba8"),
	Border: c("#45475a"), BorderSoft: c("#313244"), BorderAccent: c("#74c7ec"),
	ProgA: c("#89b4fa"), ProgB: c("#cba6f7"),
	Grad1: c("#94e2d5"), Grad2: c("#89dceb"), Grad3: c("#89b4fa"), Grad4: c("#cba6f7"),
	OnAccent: c("#181825"),
}

var paletteTokyoNight = Palette{
	Bg: c("#1a1b26"), Bg2: c("#24283b"), BgSel: c("#2f334d"), BgModal: c("#16161e"),
	Fg: c("#c0caf5"), FgDim: c("#565f89"), FgFaint: c("#414868"),
	Cyan: c("#7dcfff"), CyanSoft: c("#2ac3de"), Teal: c("#73daca"), Green: c("#9ece6a"),
	Purple: c("#bb9af7"), Indigo: c("#7aa2f7"), Amber: c("#e0af68"), Rose: c("#f7768e"),
	Border: c("#292e42"), BorderSoft: c("#232433"), BorderAccent: c("#3d59a1"),
	ProgA: c("#7aa2f7"), ProgB: c("#bb9af7"),
	Grad1: c("#73daca"), Grad2: c("#7dcfff"), Grad3: c("#7aa2f7"), Grad4: c("#bb9af7"),
	OnAccent: c("#16161e"),
}

var paletteGruvbox = Palette{
	Bg: c("#282828"), Bg2: c("#32302f"), BgSel: c("#3c3836"), BgModal: c("#1d2021"),
	Fg: c("#ebdbb2"), FgDim: c("#a89984"), FgFaint: c("#665c54"),
	Cyan: c("#83a598"), CyanSoft: c("#8ec07c"), Teal: c("#8ec07c"), Green: c("#b8bb26"),
	Purple: c("#d3869b"), Indigo: c("#83a598"), Amber: c("#fabd2f"), Rose: c("#fb4934"),
	Border: c("#504945"), BorderSoft: c("#3c3836"), BorderAccent: c("#458588"),
	ProgA: c("#fe8019"), ProgB: c("#fabd2f"),
	Grad1: c("#8ec07c"), Grad2: c("#fabd2f"), Grad3: c("#fe8019"), Grad4: c("#b8bb26"),
	OnAccent: c("#1d2021"),
}

var paletteNord = Palette{
	Bg: c("#2e3440"), Bg2: c("#3b4252"), BgSel: c("#434c5e"), BgModal: c("#272c36"),
	Fg: c("#d8dee9"), FgDim: c("#88909e"), FgFaint: c("#4c566a"),
	Cyan: c("#88c0d0"), CyanSoft: c("#8fbcbb"), Teal: c("#8fbcbb"), Green: c("#a3be8c"),
	Purple: c("#b48ead"), Indigo: c("#81a1c1"), Amber: c("#ebcb8b"), Rose: c("#bf616a"),
	Border: c("#434c5e"), BorderSoft: c("#3b4252"), BorderAccent: c("#5e81ac"),
	ProgA: c("#5e81ac"), ProgB: c("#88c0d0"),
	Grad1: c("#8fbcbb"), Grad2: c("#88c0d0"), Grad3: c("#81a1c1"), Grad4: c("#b48ead"),
	OnAccent: c("#272c36"),
}

var paletteRosePine = Palette{
	Bg: c("#191724"), Bg2: c("#1f1d2e"), BgSel: c("#26233a"), BgModal: c("#16141f"),
	Fg: c("#e0def4"), FgDim: c("#908caa"), FgFaint: c("#6e6a86"),
	Cyan: c("#9ccfd8"), CyanSoft: c("#9ccfd8"), Teal: c("#3e8fb0"), Green: c("#9ccfd8"),
	Purple: c("#c4a7e7"), Indigo: c("#3e8fb0"), Amber: c("#f6c177"), Rose: c("#eb6f92"),
	Border: c("#403d52"), BorderSoft: c("#26233a"), BorderAccent: c("#3e8fb0"),
	ProgA: c("#c4a7e7"), ProgB: c("#eb6f92"),
	Grad1: c("#9ccfd8"), Grad2: c("#c4a7e7"), Grad3: c("#eb6f92"), Grad4: c("#f6c177"),
	OnAccent: c("#16141f"),
}

var paletteDracula = Palette{
	Bg: c("#282a36"), Bg2: c("#343746"), BgSel: c("#44475a"), BgModal: c("#21222c"),
	Fg: c("#f8f8f2"), FgDim: c("#6272a4"), FgFaint: c("#4d5478"),
	Cyan: c("#8be9fd"), CyanSoft: c("#8be9fd"), Teal: c("#8be9fd"), Green: c("#50fa7b"),
	Purple: c("#bd93f9"), Indigo: c("#bd93f9"), Amber: c("#f1fa8c"), Rose: c("#ff79c6"),
	Border: c("#44475a"), BorderSoft: c("#343746"), BorderAccent: c("#6272a4"),
	ProgA: c("#bd93f9"), ProgB: c("#ff79c6"),
	Grad1: c("#8be9fd"), Grad2: c("#50fa7b"), Grad3: c("#bd93f9"), Grad4: c("#ff79c6"),
	OnAccent: c("#21222c"),
}

var paletteAmber = Palette{
	Bg: c("#1a1305"), Bg2: c("#241a08"), BgSel: c("#2e2410"), BgModal: c("#140e03"),
	Fg: c("#ffb000"), FgDim: c("#b37c00"), FgFaint: c("#6b4a00"),
	Cyan: c("#ffd060"), CyanSoft: c("#ffe080"), Teal: c("#ffc040"), Green: c("#ffc040"),
	Purple: c("#ffb000"), Indigo: c("#ffb000"), Amber: c("#ffd060"), Rose: c("#ff8c1a"),
	Border: c("#4a3500"), BorderSoft: c("#2e2108"), BorderAccent: c("#b37c00"),
	ProgA: c("#b37c00"), ProgB: c("#ffd060"),
	Grad1: c("#ffe080"), Grad2: c("#ffd060"), Grad3: c("#ffb000"), Grad4: c("#cc8c00"),
	OnAccent: c("#140e03"),
}

// paletteGoldenHour is a warm, multi-tonal gold/champagne scheme — distinct
// from Amber CRT's monochrome single-hue phosphor look: a warm brown (not
// near-black) ground, champagne text, and gold/bronze/copper/rose-gold
// accents spread across the usual accent roles rather than one repeated hue.
var paletteGoldenHour = Palette{
	Bg: c("#1c1610"), Bg2: c("#241d14"), BgSel: c("#33280f"), BgModal: c("#16110b"),
	Fg: c("#f0dcae"), FgDim: c("#b89860"), FgFaint: c("#7a6440"),
	Cyan: c("#e8b64f"), CyanSoft: c("#f4d78a"), Teal: c("#d4af37"), Green: c("#b5a642"),
	Purple: c("#b5654b"), Indigo: c("#c98a4b"), Amber: c("#ffb703"), Rose: c("#d98a6b"),
	Border: c("#4a3a20"), BorderSoft: c("#362a16"), BorderAccent: c("#b8860b"),
	ProgA: c("#d4af37"), ProgB: c("#ffd76a"),
	Grad1: c("#f4d78a"), Grad2: c("#e8b64f"), Grad3: c("#d4af37"), Grad4: c("#b8860b"),
	OnAccent: c("#201804"),
}

// paletteMistyForest is a soft, nature-inspired scheme meant to read as calm
// rather than vibrant: a muted dark sage/forest ground (not a stark black),
// warm cream text, and gentle sage-green/teal accents instead of a punchy
// saturated hue — deliberately lower-contrast than the rest of the lineup.
var paletteMistyForest = Palette{
	Bg: c("#232a2b"), Bg2: c("#2a3335"), BgSel: c("#3a4547"), BgModal: c("#1c2223"),
	Fg: c("#d3c6aa"), FgDim: c("#8a9a8b"), FgFaint: c("#5a6b5c"),
	Cyan: c("#83c092"), CyanSoft: c("#a7c080"), Teal: c("#7fbbb3"), Green: c("#a7c080"),
	Purple: c("#a68fb0"), Indigo: c("#7fbbb3"), Amber: c("#dbbc7f"), Rose: c("#e69875"),
	Border: c("#3d484a"), BorderSoft: c("#333d3f"), BorderAccent: c("#83c092"),
	ProgA: c("#83c092"), ProgB: c("#a7c080"),
	Grad1: c("#a7c080"), Grad2: c("#83c092"), Grad3: c("#7fbbb3"), Grad4: c("#a68fb0"),
	OnAccent: c("#1c2223"),
}

// palettes maps a scheme key to its Palette. "auto" is resolved lazily via
// autoPalette() in resolvePalette so it can sample the terminal at call time.
var palettes = map[string]Palette{
	"gotidal":     paletteGoTidal,
	"catppuccin":  paletteCatppuccin,
	"tokyonight":  paletteTokyoNight,
	"gruvbox":     paletteGruvbox,
	"nord":        paletteNord,
	"rosepine":    paletteRosePine,
	"dracula":     paletteDracula,
	"amber":       paletteAmber,
	"goldenhour":  paletteGoldenHour,
	"mistyforest": paletteMistyForest,
}

// paletteOrder is the picker's display order (and the cycle order for `t`).
var paletteOrder = []string{
	"auto", "gotidal", "catppuccin", "tokyonight",
	"gruvbox", "nord", "rosepine", "dracula", "amber", "goldenhour", "mistyforest",
}

// paletteNames maps a scheme key to its human-readable label (theme picker).
var paletteNames = map[string]string{
	"auto":        "Auto — match terminal",
	"gotidal":     "goTidal",
	"catppuccin":  "Catppuccin Mocha",
	"tokyonight":  "Tokyo Night",
	"gruvbox":     "Gruvbox Dark",
	"nord":        "Nord",
	"rosepine":    "Rosé Pine",
	"dracula":     "Dracula",
	"amber":       "Amber CRT",
	"goldenhour":  "Golden Hour",
	"mistyforest": "Misty Forest",
}

const defaultThemeName = "gotidal"

// resolvePalette returns the Palette for a scheme name, falling back to the
// default if the name is unknown or empty. "tidalt" is accepted as an alias
// for "gotidal" so a theme choice saved before the rename still resolves
// instead of silently reverting to the default.
func resolvePalette(name string) Palette {
	if name == "auto" {
		return autoPalette()
	}
	if name == "tidalt" {
		return paletteGoTidal
	}
	if p, ok := palettes[name]; ok {
		return p
	}
	return paletteGoTidal
}

// clientTint overrides a palette's focus-accent tokens with a steel-blue family
// so a client-mode instance reads as visually distinct from the server, while
// still honoring the user's chosen background/text/scheme colors.
func clientTint(p Palette) Palette {
	steel := c("#5f87af")
	steelSoft := c("#87afd7")
	p.Cyan = steel
	p.CyanSoft = steelSoft
	p.BorderAccent = steel
	p.Grad1 = c("#87afd7")
	p.Grad2 = c("#5f87af")
	p.Grad3 = c("#5f87d7")
	p.Grad4 = c("#8787d7")
	return p
}
