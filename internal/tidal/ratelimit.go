package tidal

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Tidal answers HTTP 429 when a client sends too many requests. The worst
// thing to do then is keep asking: every refused request extends the
// throttle. So the first 429 starts a client-wide cooldown, during which
// requests fail locally with *RateLimitedError instead of reaching the
// network. Without it, a single track resolve (the quality ladder tries up to
// seven endpoint/tier combinations) plus the UI's auto-skip to the next track
// turned one 429 into dozens.

const (
	minCooldown = 5 * time.Second
	maxCooldown = 2 * time.Minute
)

// RateLimitedError reports that Tidal is throttling this client.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("Tidal is rate-limiting requests (HTTP 429), retrying in %ds",
		int(e.RetryAfter.Round(time.Second)/time.Second))
}

// IsRateLimited reports whether err is (or wraps) a *RateLimitedError, and
// returns it.
func IsRateLimited(err error) (*RateLimitedError, bool) {
	return errors.AsType[*RateLimitedError](err)
}

// rateLimiter holds the shared cooldown state.
type rateLimiter struct {
	mu     sync.Mutex
	until  time.Time
	streak int // consecutive 429s, for exponential backoff when no Retry-After
	now    func() time.Time
}

func (l *rateLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// wait returns how long requests must still be held back (0 = go ahead).
func (l *rateLimiter) wait() time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if d := l.until.Sub(l.clock()); d > 0 {
		return d
	}
	return 0
}

// hit records a 429 and returns the cooldown now in force.
func (l *rateLimiter) hit(retryAfter time.Duration) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	backoff := minCooldown << min(l.streak, 5) // 5s, 10s, 20s, 40s, 80s, 160s
	l.streak++
	d := max(retryAfter, backoff)
	d = min(d, maxCooldown)
	now := l.clock()
	if until := now.Add(d); until.After(l.until) {
		l.until = until
	}
	return l.until.Sub(now)
}

// ok records a successful (non-429) answer, resetting the backoff.
func (l *rateLimiter) ok() {
	l.mu.Lock()
	l.streak = 0
	l.mu.Unlock()
}

// parseRetryAfter understands both forms of the header: delta-seconds and an
// HTTP date. Anything else yields 0 (use the backoff).
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// limitedTransport applies the limiter to every authenticated API request.
type limitedTransport struct {
	l    *rateLimiter
	base http.RoundTripper
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if d := t.l.wait(); d > 0 {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, &RateLimitedError{RetryAfter: d}
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		d := t.l.hit(parseRetryAfter(resp.Header.Get("Retry-After"), t.l.clock()))
		_ = resp.Body.Close()
		return nil, &RateLimitedError{RetryAfter: d}
	}
	t.l.ok()
	return resp, nil
}
