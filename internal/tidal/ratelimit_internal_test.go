package tidal

import (
	"testing"
	"time"
)

func TestBackoffAndRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	l := &rateLimiter{now: func() time.Time { return now }}
	for i, want := range []time.Duration{
		5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second,
		80 * time.Second, 2 * time.Minute, 2 * time.Minute,
	} {
		if got := l.hit(0); got != want {
			t.Errorf("hit %d: %v, want %v", i, got, want)
		}
		now = now.Add(l.wait()) // let it expire
	}
	l.ok()
	if got := l.hit(0); got != 5*time.Second {
		t.Errorf("backoff not reset after success: %v", got)
	}
	if d := parseRetryAfter("12", now); d != 12*time.Second {
		t.Errorf("seconds: %v", d)
	}
	if d := parseRetryAfter(now.Add(90*time.Second).Format(http1123), now); d != 90*time.Second {
		t.Errorf("HTTP date: %v", d)
	}
	if d := parseRetryAfter("soon", now); d != 0 {
		t.Errorf("garbage: %v", d)
	}
}

const http1123 = "Mon, 02 Jan 2006 15:04:05 GMT"
