package logger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

// newTestLogger returns a logger writing into buf through the same redacting
// wrapper the package installs at init.
func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(&redactHandler{h})
}

// A tokenised stream URL reaching the log inside an error was the real leak:
// redactAttr used to return early on anything that was not a plain string, so
// `logger.L.Error("...", "err", err)` wrote the token to disk verbatim.
func TestErrorValuesAreRedacted(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.SUPERSECRET" //nolint:gosec // G101: a fake token, which is the point of the test
	streamURL := "https://sp-ad-fa.audio.tidal.com/mediatracks/abc/0.flac?token=" + token + "&Expires=123"

	cases := []struct {
		name string
		log  func(l *slog.Logger)
	}{
		{
			// The exact shape http.Client produces.
			name: "url.Error attribute",
			log: func(l *slog.Logger) {
				err := &url.Error{Op: "Get", URL: streamURL, Err: errors.New("dial tcp: i/o timeout")}
				l.Error("stream open failed", "err", err)
			},
		},
		{
			name: "wrapped error attribute",
			log: func(l *slog.Logger) {
				l.Error("stream", "err", fmt.Errorf("open %s: %w", streamURL, errors.New("refused")))
			},
		},
		{
			name: "plain string attribute",
			log:  func(l *slog.Logger) { l.Info("resolved", "url", streamURL) },
		},
		{
			name: "url embedded in the message",
			log:  func(l *slog.Logger) { l.Info("fetching " + streamURL) },
		},
		{
			name: "inside a group",
			log:  func(l *slog.Logger) { l.Info("x", slog.Group("req", "url", streamURL)) },
		},
		{
			name: "attached via With",
			log:  func(l *slog.Logger) { l.With("url", streamURL).Info("x") },
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			c.log(newTestLogger(&buf))
			out := buf.String()
			if strings.Contains(out, token) {
				t.Errorf("token leaked into the log: %s", out)
			}
			// The URL should still be identifiable for debugging.
			if !strings.Contains(out, "sp-ad-fa.audio.tidal.com") {
				t.Errorf("redaction removed the whole URL, leaving nothing to debug with: %s", out)
			}
		})
	}
}

func TestRedactString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no secret is untouched", "opened hw:1,0", "opened hw:1,0"},
		{
			"query stripped, path kept",
			"https://api.tidal.com/v1/tracks/1?token=abc&countryCode=ES",
			"https://api.tidal.com/v1/tracks/1",
		},
		{"query-less url untouched", "https://lrclib.net/api/get", "https://lrclib.net/api/get"},
		{
			"url mid-sentence, trailing punctuation preserved",
			`Get "https://x.com/a?token=s": timeout`,
			`Get "https://x.com/a": timeout`,
		},
		{
			"credentials in userinfo removed",
			"https://user:pw@example.com/x",
			"https://example.com/x",
		},
		{"fragment removed", "https://x.com/a#token=s", "https://x.com/a"},
		{
			"bare parameter outside a url",
			"auth failed: refresh_token=abc123 rejected",
			"auth failed: refresh_token=<redacted> rejected",
		},
		{
			"client secret in a form body",
			"client_id=x&client_secret=shhh",
			"client_id=x&client_secret=<redacted>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactString(c.in); got != c.want {
				t.Errorf("redactString(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Non-secret values must keep their original type and formatting — redaction
// should be invisible unless it actually removed something.
func TestNonSecretValuesArePreserved(t *testing.T) {
	var buf bytes.Buffer
	l := newTestLogger(&buf)
	l.Info("stats", "count", 42, "ratio", 0.5, "ok", true, "dev", "hw:1,0")
	out := buf.String()
	for _, want := range []string{"count=42", "ratio=0.5", "ok=true", "dev=hw:1,0"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

// A LogValuer must be resolved before inspection, or it would smuggle a secret
// past the wrapper and only produce it when the handler formats the record.
func TestLogValuerIsResolved(t *testing.T) {
	var buf bytes.Buffer
	newTestLogger(&buf).Info("x", "cred", lazySecret{})
	if strings.Contains(buf.String(), "SECRET") {
		t.Errorf("LogValuer bypassed redaction: %s", buf.String())
	}
}

type lazySecret struct{}

func (lazySecret) LogValue() slog.Value {
	return slog.StringValue("https://api.tidal.com/x?token=SECRET")
}

func TestHandlerEnabledDelegates(t *testing.T) {
	h := &redactHandler{slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelWarn})}
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("Enabled should defer to the wrapped handler's level")
	}
	if !h.Enabled(context.Background(), slog.LevelError) {
		t.Error("Error should be enabled at LevelWarn")
	}
}
