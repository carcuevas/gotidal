package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/carcuevas/gotidal/internal/mpris"
)

// playLog returns a logger that writes to ~/.local/share/gotidal/play.log.
// Errors opening the file fall back to stderr.
func playLog() *log.Logger {
	dir := filepath.Join(func() string {
		h, err := os.UserHomeDir()
		if err != nil {
			return "."
		}
		return h
	}(), ".local", "share", "gotidal")
	_ = os.MkdirAll(dir, 0o700)
	//nolint:gosec // G304: fixed "play.log" filename under the user's own ~/.local/share/gotidal dir, not attacker-controlled
	f, err := os.OpenFile(filepath.Join(dir, "play.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return log.New(os.Stderr, "", 0)
	}
	return log.New(f, "", 0)
}

// runPlay handles the "gotidal play <url>" subcommand.
//
// If a parent gotidal instance is already running, the URL is forwarded over
// D-Bus and the process exits immediately — no terminal needed.
//
// Otherwise a terminal emulator is launched with "gotidal <url>" so the full
// TUI starts in a proper TTY with the URL queued for auto-play.
func runPlay(url string) error {
	lg := playLog()
	lg.Printf("[%s] gotidal play invoked with url=%q", time.Now().Format(time.RFC3339), url)

	if url == "" {
		lg.Printf("error: no URL provided")
		return errors.New("usage: gotidal play <tidal://... or https://tidal.com/...>")
	}
	if err := validatePlayURL(url); err != nil {
		lg.Printf("error: rejected URL: %v", err)
		return err
	}

	// If a parent is already running just push the URL and exit.
	c, err := mpris.NewClient()
	if err == nil {
		defer c.Close()
		if sendErr := c.SendURL(url); sendErr != nil {
			lg.Printf("error: failed to send URL to running instance: %v", sendErr)
			return fmt.Errorf("failed to send URL to running instance: %w", sendErr)
		}
		lg.Printf("forwarded URL to running instance")
		return nil
	}
	lg.Printf("no running instance (%v), launching terminal", err)

	// No running instance — launch a terminal with the TUI.
	self, err := os.Executable()
	if err != nil {
		lg.Printf("error: cannot determine executable path: %v", err)
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	lg.Printf("self=%q", self)

	term, args := findTerminal(self, url)
	if term == "" {
		lg.Printf("error: no terminal emulator found")
		return errors.New("no terminal emulator found; set $TERMINAL or install one of: kitty, ghostty, alacritty, foot, wezterm, konsole, xterm")
	}
	lg.Printf("launching terminal: %q args=%v", term, args)

	// context.Background: the launched terminal is detached (Release below) and
	// must outlive this process, so it is deliberately not tied to a cancellable context.
	//nolint:gosec // G204: term is a known terminal binary resolved via exec.LookPath and args are built from the user's own play URL, not untrusted remote input
	cmd := exec.CommandContext(context.Background(), term, args...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if startErr := cmd.Start(); startErr != nil {
		lg.Printf("error: failed to launch terminal %q: %v", term, startErr)
		return fmt.Errorf("failed to launch terminal %q: %w", term, startErr)
	}
	lg.Printf("terminal launched (pid %d)", cmd.Process.Pid)
	// Detach — let the terminal own the child process.
	_ = cmd.Process.Release()
	return nil
}

// allowedPlayHosts are the hosts a https:// play URL may point at.
var allowedPlayHosts = map[string]bool{
	"tidal.com":        true,
	"www.tidal.com":    true,
	"listen.tidal.com": true,
	"open.spotify.com": true,
}

// validatePlayURL checks a URL handed to `gotidal play` before it is used to
// build a command line or an API request.
//
// This is the program's one untrusted entry point: gotidal registers itself as
// the tidal:// scheme handler, so any web page can make the desktop launch
// `gotidal play <whatever>`. Restricting it to the schemes and hosts the app
// actually handles, and rejecting whitespace, quotes and control characters,
// keeps a hostile link from reaching the terminal-launch argv or the Tidal API
// URL builders as anything other than an ordinary identifier.
func validatePlayURL(raw string) error {
	if len(raw) > 2048 {
		return errors.New("play URL is too long")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7F || r == ' ' || r == '"' || r == '\'' || r == '`' || r == '\\' {
			return fmt.Errorf("play URL contains a disallowed character %q", r)
		}
	}

	u, err := neturl.Parse(raw)
	if err != nil {
		return fmt.Errorf("not a valid URL: %w", err)
	}
	switch u.Scheme {
	case "tidal":
		return nil
	case "https":
		if !allowedPlayHosts[u.Hostname()] {
			return fmt.Errorf("unsupported host %q", u.Hostname())
		}
		return nil
	default:
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
}

// findTerminal returns the terminal binary and argument list needed to run
// "gotidal <url>" in a new window. Returns ("", nil) if nothing is found.
//
// Lookup order:
//  1. $TERMINAL env var (assumed to accept "-e <cmd> [args...]")
//  2. Well-known terminals, each with their correct flag convention.
func findTerminal(self, url string) (term string, args []string) {
	for _, c := range terminalCandidates() {
		if c.bin == "" {
			continue
		}
		path, err := exec.LookPath(c.bin)
		if err != nil {
			continue
		}
		full := c.args(self, url)
		// Replace bare name with resolved path.
		full[0] = path
		return path, full[1:]
	}
	return "", nil
}

type terminalCandidate struct {
	bin  string
	args func(self, url string) []string
}

// terminalCandidates lists the terminals to try, in order, each with its own
// flag convention. Split out from findTerminal so the argument construction is
// testable without the binaries being installed — every entry must pass the
// URL as its own argv element rather than folding it into a command string.
func terminalCandidates() []terminalCandidate {
	// Standard "-e cmd args..." convention.
	withE := func(bin, self, url string) []string { return []string{bin, "-e", self, url} }

	return []terminalCandidate{
		// $TERMINAL — honour the user's explicit preference first.
		{os.Getenv("TERMINAL"), func(s, u string) []string { return withE(os.Getenv("TERMINAL"), s, u) }},
		// Terminals that use "-e":
		{"kitty", func(s, u string) []string { return []string{"kitty", s, u} }},
		{"ghostty", func(s, u string) []string { return []string{"ghostty", "-e", s, u} }},
		{"alacritty", func(s, u string) []string { return []string{"alacritty", "-e", s, u} }},
		{"foot", func(s, u string) []string { return []string{"foot", s, u} }},
		{"wezterm", func(s, u string) []string { return []string{"wezterm", "start", "--", s, u} }},
		{"konsole", func(s, u string) []string { return []string{"konsole", "-e", s, u} }},
		// -x, not -e: xfce4-terminal's -e takes a single string which it splits
		// with g_shell_parse_argv, so concatenating "self + \" \" + url" would
		// let a crafted tidal:// link inject extra argv entries. -x consumes
		// the rest of the command line as argv directly.
		{"xfce4-terminal", func(s, u string) []string { return []string{"xfce4-terminal", "-x", s, u} }},
		{"gnome-terminal", func(s, u string) []string { return []string{"gnome-terminal", "--", s, u} }},
		{"xterm", func(s, u string) []string { return []string{"xterm", "-e", s, u} }},
	}
}
