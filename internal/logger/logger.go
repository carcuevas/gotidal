package logger

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var L *slog.Logger

func init() {
	level := slog.LevelInfo
	if os.Getenv("GOTIDAL_DEBUG") == "true" {
		level = slog.LevelDebug
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	logDir := filepath.Join(home, ".local", "share", "gotidal")
	_ = os.MkdirAll(logDir, 0o700)

	// Every launch writes a new timestamped file, so without pruning they
	// accumulate forever — unbounded disk use, and a growing pile of files
	// holding request traces and device details.
	pruneOldLogs(logDir, keepLogFiles)

	logFile := filepath.Join(logDir, "gotidal-"+time.Now().Format("20060102-150405")+".log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600) //nolint:gosec // G304: log path is computed internally, not user-supplied
	if err != nil {
		// Fall back to stderr if the file can't be opened.
		L = slog.New(&redactHandler{slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})})
		return
	}

	L = slog.New(&redactHandler{slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})})
	L.Info("logging started", "file", logFile, "level", level)
}

// keepLogFiles is how many previous runs' logs are retained. The filenames
// sort chronologically, so keeping the last N by name keeps the newest N.
const keepLogFiles = 10

// pruneOldLogs deletes all but the newest keep log files in dir. Errors are
// ignored: failing to tidy up must never stop the program from starting.
func pruneOldLogs(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var logs []string
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, "gotidal-") && strings.HasSuffix(name, ".log") {
			logs = append(logs, name)
		}
	}
	if len(logs) <= keep {
		return
	}
	slices.Sort(logs)
	for _, name := range logs[:len(logs)-keep] {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// redactHandler wraps a slog.Handler and strips query strings from URL values
// to prevent tokens and other secrets from appearing in log files.
type redactHandler struct {
	inner slog.Handler
}

func (h *redactHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	// The message itself is redacted too — callers build messages with
	// fmt.Sprintf, and a URL is as likely to land there as in an attribute.
	redacted := slog.NewRecord(r.Time, r.Level, redactString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		redacted.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, redacted)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		redacted[i] = redactAttr(a)
	}
	return &redactHandler{h.inner.WithAttrs(redacted)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{h.inner.WithGroup(name)}
}

// urlRE matches a URL anywhere inside a larger string — not just one that is
// the whole attribute value. Tidal stream URLs carry the playback token in the
// query string, and they most often reach the log wrapped in an error
// ("Get \"https://…?token=…\": dial tcp: …"), never as a bare URL attribute.
var urlRE = regexp.MustCompile(`(?i)\b(?:https?|wss?)://[^\s"'` + "`" + `<>\\]+`)

// secretParamRE catches credentials in text that never parses as a URL: form
// bodies, "token=…" fragments in a provider's error message, and the like.
var secretParamRE = regexp.MustCompile(
	`(?i)\b((?:access_|refresh_|id_|device_|bearer_)?token|password|secret|client_secret|api[_-]?key|authorization|sessionid)=[^\s&"']+`)

// redactString removes query strings from any URL it finds and masks bare
// credential parameters. It returns s unchanged when there is nothing to strip.
func redactString(s string) string {
	if strings.Contains(s, "://") {
		s = urlRE.ReplaceAllStringFunc(s, func(m string) string {
			// Keep any trailing punctuation the regex swallowed (a quote or
			// comma closing an error message) out of the parse.
			trail := ""
			for m != "" && strings.ContainsRune(`.,;:)]}`, rune(m[len(m)-1])) {
				trail = string(m[len(m)-1]) + trail
				m = m[:len(m)-1]
			}
			u, err := url.Parse(m)
			if err != nil {
				return m + trail
			}
			if u.RawQuery == "" && u.Fragment == "" && u.User == nil {
				return m + trail
			}
			u.RawQuery = ""
			u.Fragment = ""
			u.User = nil
			return u.String() + trail
		})
	}
	return secretParamRE.ReplaceAllString(s, "$1=<redacted>")
}

// redactAttr strips secrets out of an attribute's value.
//
// It deliberately looks at every kind, not just strings: the leak this exists
// to prevent came through `logger.L.Error("…", "err", err)`, where the value is
// a KindAny holding an *url.Error whose message embeds the full tokenised
// stream URL. Group attributes are walked recursively, and LogValuers are
// resolved first so a lazily-produced value cannot slip past.
func redactAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	switch v.Kind() {
	case slog.KindString:
		if red := redactString(v.String()); red != v.String() {
			return slog.String(a.Key, red)
		}
		return a

	case slog.KindGroup:
		attrs := v.Group()
		redacted := make([]any, 0, len(attrs))
		for _, ga := range attrs {
			redacted = append(redacted, redactAttr(ga))
		}
		return slog.Group(a.Key, redacted...)

	case slog.KindAny:
		// Render the value the way the handler eventually will, and only
		// substitute a string when something actually had to be removed — so
		// non-secret values keep their original type and formatting.
		text := fmt.Sprint(v.Any())
		if red := redactString(text); red != text {
			return slog.String(a.Key, red)
		}
		return a

	default:
		return a
	}
}
