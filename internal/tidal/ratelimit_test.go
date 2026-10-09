package tidal_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// One 429 must stop the quality ladder (no lower tiers, no fallback
// endpoint), and further requests must fail locally during the cooldown.
func TestRateLimitStopsLadderAndCoolsDown(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	c := newTestClient(srv)

	_, err := c.GetStreamURL(context.Background(), 1, tidal.ModeBest)
	rl, ok := tidal.IsRateLimited(err)
	if !ok {
		t.Fatalf("want a rate-limit error, got %v", err)
	}
	if rl.RetryAfter < 29*time.Second || rl.RetryAfter > 31*time.Second {
		t.Errorf("Retry-After not honoured: %v", rl.RetryAfter)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("ladder kept going after a 429: %d requests", n)
	}

	if _, err := c.GetStreamURL(context.Background(), 2, tidal.ModeBest); err == nil {
		t.Fatal("expected the cooldown to refuse the request")
	} else if _, ok := tidal.IsRateLimited(err); !ok {
		t.Fatalf("want a rate-limit error during cooldown, got %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("a request reached the network during the cooldown: %d", n)
	}
}
