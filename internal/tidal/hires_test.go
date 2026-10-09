package tidal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// In bit-perfect mode a track without a hi-res master is refused — never
// played at a lower tier — and nothing below hi-res is even requested.
func TestHiResOnlyRefusesLowerTiers(t *testing.T) {
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		asked.Add(1)
		// Asking for hi-res on a CD-quality track is answered with LOSSLESS.
		respond(w, 200, btsPlaybackInfo(qualityLossless, 16, 44100))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetStreamURL(context.Background(), 7, tidal.ModeHiResOnly)
	var nh *tidal.NotHiResError
	if !errors.As(err, &nh) {
		t.Fatalf("want *NotHiResError, got %v", err)
	}
	if nh.Granted != tidal.QualityLossless {
		t.Errorf("Granted = %q, want LOSSLESS", nh.Granted)
	}
	if n := asked.Load(); n != 1 {
		t.Errorf("hi-res-only mode made %d requests, want 1", n)
	}

	// The same track plays (as lossless) in PipeWire mode.
	info, err := newTestClient(srv).GetStreamURL(context.Background(), 7, tidal.ModeBest)
	if err != nil || info.Quality != tidal.QualityLossless {
		t.Fatalf("ModeBest: %v, %q", err, info.Quality)
	}
}

func TestHiResOnlyAcceptsHiRes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, btsPlaybackInfo(string(tidal.QualityHiRes), 24, 96000))
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 8, tidal.ModeHiResOnly)
	if err != nil || info.Quality != tidal.QualityHiRes {
		t.Fatalf("got %v, %q", err, info.Quality)
	}
}

// btsPlaybackInfo is a playbackinfopostpaywall answer granting quality q.
func btsPlaybackInfo(q string, bitDepth, sampleRate int) map[string]any {
	return map[string]any{
		"audioQuality":     q,
		"manifestMimeType": "application/vnd.tidal.bts",
		"manifest":         btsManifest(urlLosslessFLAC),
		"bitDepth":         bitDepth,
		"sampleRate":       sampleRate,
	}
}
