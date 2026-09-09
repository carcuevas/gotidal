package tidal_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// roundTripFunc is a convenience type that lets a plain function satisfy
// http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// newTestClient returns a *tidal.Client pre-loaded with a dummy session and a
// transport that delegates to the supplied httptest.Server.  The token is set
// far in the future so the oauth2 layer never attempts a refresh.
func newTestClient(srv *httptest.Server) *tidal.Client {
	c := tidal.NewClient()
	c.Session = &tidal.Session{
		AccessToken: "test-token",
		TokenType:   "Bearer",
		Expiry:      time.Now().Add(time.Hour),
		UserID:      42,
		CountryCode: "US",
	}
	// Rewrite every request to point at the test server instead of the real
	// Tidal API endpoints.
	c.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
		req.URL.Scheme = "http"
		return srv.Client().Transport.RoundTrip(req)
	})
	// Give the oauth2 config a token endpoint on the test server so that any
	// token refresh attempt would also stay local (won't be triggered in
	// practice because the expiry is in the future).
	c.Oauth.Endpoint = oauth2.Endpoint{
		TokenURL: srv.URL + "/token",
	}
	return c
}

// respond writes JSON to the ResponseRecorder / hijacked response.
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

// --- GetUser ---

func TestGetUser_OK(t *testing.T) {
	want := tidal.UserResponse{
		ID:          42,
		CountryCode: "US",
		Email:       "user@example.com",
		FullName:    "Test User",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/users/42") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		respond(w, 200, want)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetUser(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Email != want.Email || got.FullName != want.FullName {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetUser_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 401, map[string]string{"error": "unauthorized"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetUser(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

// --- GetTrack ---

func TestGetTrack_OK(t *testing.T) {
	want := tidal.Track{ID: 123, Title: "Song Title"}
	want.Artist.Name = "Artist Name"
	want.Album.Title = "Album Title"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tracks/123") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		respond(w, 200, want)
	}))
	defer srv.Close()

	got, err := newTestClient(srv).GetTrack(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Title != want.Title || got.Artist.Name != want.Artist.Name {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetTrack_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 404, map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetTrack(context.Background(), "999")
	if err == nil {
		t.Fatal("expected error for 404 response")
	}
}

// --- Search ---

func TestSearch_OK(t *testing.T) {
	payload := tidal.SearchResponse{}
	payload.Tracks.Items = []tidal.Track{
		{ID: 1, Title: "Alpha"},
		{ID: 2, Title: "Beta"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/search") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if q := r.URL.Query().Get("query"); q != "test query" {
			t.Errorf("unexpected query param: %q", q)
		}
		if !strings.Contains(r.URL.Query().Get("types"), "TRACKS") {
			t.Errorf("types param missing TRACKS: %q", r.URL.Query().Get("types"))
		}
		respond(w, 200, payload)
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).Search(context.Background(), "test query")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	if tracks[0].Title != "Alpha" || tracks[1].Title != "Beta" {
		t.Errorf("unexpected tracks: %+v", tracks)
	}
}

func TestSearch_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, tidal.SearchResponse{})
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).Search(context.Background(), "nothing")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 0 {
		t.Errorf("expected empty slice, got %d tracks", len(tracks))
	}
}

// --- GetStreamURL ---
//
// Resolution tries playbackinfopostpaywall first (the only endpoint that can
// express a hi-res DASH presentation) and falls back to the older
// urlpostpaywall per tier, so these handlers route on the path.

const (
	qualityLossless  = "LOSSLESS"
	qualityHigh      = "HIGH"
	urlLosslessFLAC  = "https://cdn.tidal.com/lossless.flac"
	pathPlaybackInfo = "playbackinfopostpaywall"
	pathURLPostPay   = "urlpostpaywall"
)

// dashManifest builds a base64 MPEG-DASH manifest of the shape Tidal returns
// for hi-res: an init segment plus a numbered media series.
func dashManifest(segments int) string {
	mpd := fmt.Sprintf(`<?xml version="1.0"?>
<MPD xmlns="urn:mpeg:dash:schema:mpd:2011" type="static" mediaPresentationDuration="PT3M20.0S">
  <Period id="0">
    <AdaptationSet id="0" contentType="audio" mimeType="audio/mp4">
      <Representation id="1" codecs="flac" audioSamplingRate="192000">
        <SegmentTemplate timescale="192000" startNumber="1"
            initialization="https://cdn.tidal.com/init.mp4"
            media="https://cdn.tidal.com/seg-$Number$.mp4">
          <SegmentTimeline><S d="1000" r="%d"/></SegmentTimeline>
        </SegmentTemplate>
      </Representation>
    </AdaptationSet>
  </Period>
</MPD>`, segments-1)
	return base64.StdEncoding.EncodeToString([]byte(mpd))
}

// btsManifest builds a base64 BTS manifest, what the lossy and plain lossless
// tiers return.
func btsManifest(url string) string {
	return base64.StdEncoding.EncodeToString([]byte(
		`{"mimeType":"audio/flac","codecs":"flac","encryptionType":"NONE","urls":["` + url + `"]}`))
}

// Hi-res exists only as a DASH presentation, so this is the path that makes
// 192/24 playable at all.
func TestGetStreamURL_HiResFromDASHManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, pathPlaybackInfo) {
			t.Errorf("hi-res must not be requested from %s", r.URL.Path)
			respond(w, 404, map[string]string{"error": "wrong endpoint"})
			return
		}
		respond(w, 200, map[string]any{
			"trackId":          206514049,
			"audioQuality":     "HI_RES_LOSSLESS",
			"manifestMimeType": "application/dash+xml",
			"manifest":         dashManifest(3),
			"bitDepth":         24,
			"sampleRate":       192000,
		})
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 206514049, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"https://cdn.tidal.com/init.mp4",
		"https://cdn.tidal.com/seg-1.mp4",
		"https://cdn.tidal.com/seg-2.mp4",
		"https://cdn.tidal.com/seg-3.mp4",
	}
	if !slices.Equal(info.URLs, want) {
		t.Errorf("URLs = %v, want %v", info.URLs, want)
	}
	if info.Quality != tidal.QualityHiRes {
		t.Errorf("Quality = %q, want %q", info.Quality, tidal.QualityHiRes)
	}
	// The depth and rate must come back with the manifest: ALSA is configured
	// from them before the first frame is decoded.
	if info.BitDepth != 24 {
		t.Errorf("BitDepth = %d, want 24", info.BitDepth)
	}
	if info.SampleRate != 192000 {
		t.Errorf("SampleRate = %d, want 192000", info.SampleRate)
	}
	if info.Ext != "mp4" {
		t.Errorf("Ext = %q, want mp4", info.Ext)
	}
}

func TestGetStreamURL_BTSManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audioquality") != qualityLossless {
			respond(w, 404, map[string]string{"error": "no hi-res master"})
			return
		}
		respond(w, 200, map[string]any{
			"audioQuality":     qualityLossless,
			"manifestMimeType": "application/vnd.tidal.bts",
			"manifest":         btsManifest(urlLosslessFLAC),
			"bitDepth":         16,
			"sampleRate":       44100,
		})
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 123, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.URL() != urlLosslessFLAC {
		t.Errorf("unexpected URL: %s", info.URL())
	}
	if info.Quality != tidal.QualityLossless {
		t.Errorf("Quality = %q, want LOSSLESS", info.Quality)
	}
	if info.BitDepth != 16 || info.SampleRate != 44100 {
		t.Errorf("format = %d/%d, want 44100/16", info.SampleRate, info.BitDepth)
	}
}

// When playbackinfo is unavailable the older endpoint still has to work, so a
// deployment where it fails degrades to the previously shipping behaviour
// rather than losing the track.
func TestGetStreamURL_FallsBackToURLPostPaywall(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, pathPlaybackInfo) {
			respond(w, 500, map[string]string{"userMessage": "playbackinfo is down"})
			return
		}
		seen = append(seen, r.URL.Query().Get("audioquality"))
		respond(w, 200, tidal.StreamResponse{URLs: []string{"https://cdn.tidal.com/stream.flac"}})
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 123, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.URL() != "https://cdn.tidal.com/stream.flac" {
		t.Errorf("unexpected URL: %s", info.URL())
	}
	if info.Ext != "flac" {
		t.Errorf("unexpected ext: %s", info.Ext)
	}
	// Hi-res is never requested from urlpostpaywall: that endpoint cannot
	// express a DASH presentation, so asking would either fail or be answered
	// with a lower tier we would then mislabel as hi-res.
	if slices.Contains(seen, string(tidal.QualityHiRes)) {
		t.Errorf("hi-res was requested from urlpostpaywall: %v", seen)
	}
	if len(seen) == 0 || seen[0] != string(tidal.QualityLossless) {
		t.Errorf("fallback should start at LOSSLESS, got %v", seen)
	}
}

func TestGetStreamURL_FallsBackThroughQualities(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("audioquality")
		if !strings.Contains(r.URL.Path, pathPlaybackInfo) {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		seen = append(seen, q)
		if q == qualityLossless {
			respond(w, 200, map[string]any{
				"audioQuality":     qualityLossless,
				"manifestMimeType": "application/vnd.tidal.bts",
				"manifest":         btsManifest(urlLosslessFLAC),
			})
			return
		}
		respond(w, 404, map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 123, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.URL() != urlLosslessFLAC {
		t.Errorf("unexpected URL: %s", info.URL())
	}
	if len(seen) < 2 || seen[0] != "HI_RES_LOSSLESS" || seen[1] != "LOSSLESS" {
		t.Errorf("unexpected quality ladder: %v", seen)
	}
}

func TestGetStreamURL_AllQualitiesFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 404, map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetStreamURL(context.Background(), 123, false)
	if err == nil {
		t.Fatal("expected error when all qualities fail")
	}
}

func TestGetStreamURL_EmptyURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, pathPlaybackInfo) {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		respond(w, 200, tidal.StreamResponse{URLs: []string{}})
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetStreamURL(context.Background(), 123, false)
	if err == nil {
		t.Fatal("expected error for empty URLs in response")
	}
}

func TestGetStreamURL_LowDataSkipsLosslessTiers(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("audioquality")
		if !slices.Contains(seen, q) {
			seen = append(seen, q)
		}
		if strings.Contains(r.URL.Path, pathPlaybackInfo) && q == qualityHigh {
			respond(w, 200, map[string]any{
				"audioQuality":     qualityHigh,
				"manifestMimeType": "application/vnd.tidal.bts",
				"manifest":         btsManifest("https://cdn.tidal.com/high.m4a"),
			})
			return
		}
		respond(w, 404, map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 123, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.URL() != "https://cdn.tidal.com/high.m4a" {
		t.Errorf("unexpected URL: %s", info.URL())
	}
	if len(seen) != 1 || seen[0] != "HIGH" {
		t.Errorf("expected only HIGH to be tried, got %v", seen)
	}
}

// --- GetFavorites ---

func TestGetFavorites_OK(t *testing.T) {
	payload := tidal.FavoritesResponse{
		Items: []struct {
			Item tidal.Track `json:"item"`
		}{
			{Item: tidal.Track{ID: 10, Title: "Fav One"}},
			{Item: tidal.Track{ID: 20, Title: "Fav Two"}},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/favorites/tracks") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("order") != "DATE" {
			t.Errorf("order param missing or wrong")
		}
		respond(w, 200, payload)
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).GetFavorites(context.Background(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	if tracks[0].ID != 10 || tracks[1].ID != 20 {
		t.Errorf("unexpected tracks: %+v", tracks)
	}
}

// --- GetMixes ---
//
// Tidal removed the v2 openapi.tidal.com/v2/userRecommendations resource
// GetMixes used to read (every request now 404s), which is what made Daily
// Mixes come up empty. It now reads the v1 "my_collection_my_mixes" page —
// see https://github.com/Benehiko/tidalt/pull/12, which diagnosed and fixed
// the same break in the upstream project this one is a continuation of.

// mixPage builds a v1 "pages/my_collection_my_mixes" payload from the given
// mix entries.
func mixPage(items ...map[string]any) map[string]any {
	return map[string]any{
		"rows": []map[string]any{{
			"modules": []map[string]any{{
				"type": "MIX_LIST",
				"pagedList": map[string]any{
					"totalNumberOfItems": len(items),
					"items":              items,
				},
			}},
		}},
	}
}

func TestGetMixes_OK(t *testing.T) {
	payload := mixPage(
		map[string]any{
			"id":       "mix1",
			"title":    "Daily Mix",
			"subTitle": "Your daily picks",
			"mixType":  "DAILY_MIX",
		},
		map[string]any{
			"id":       "mix2",
			"title":    "Chill Mix",
			"subTitle": "Relaxing vibes",
			"mixType":  "DISCOVERY_MIX",
		},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/pages/my_collection_my_mixes") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		respond(w, 200, payload)
	}))
	defer srv.Close()

	mixes, err := newTestClient(srv).GetMixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mixes) != 2 {
		t.Fatalf("expected 2 mixes, got %d", len(mixes))
	}
	if mixes[0].Title != "Daily Mix" || mixes[1].Title != "Chill Mix" {
		t.Errorf("unexpected mix titles: %+v", mixes)
	}
	if mixes[0].ID != "mix1" {
		t.Errorf("unexpected mix ID: %q", mixes[0].ID)
	}
	if mixes[0].SubTitle != "Your daily picks" {
		t.Errorf("unexpected subtitle: %q", mixes[0].SubTitle)
	}
}

func TestGetMixes_SkipsVideoMixes(t *testing.T) {
	// Video mixes serve video items, which the player cannot decode, so they
	// must not appear in the list.
	payload := mixPage(
		map[string]any{"id": "mix1", "title": "My Mix 1", "mixType": "DAILY_MIX"},
		map[string]any{"id": "vid1", "title": "My Video Mix 1", "mixType": "VIDEO_DAILY_MIX"},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, payload)
	}))
	defer srv.Close()

	mixes, err := newTestClient(srv).GetMixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mixes) != 1 {
		t.Fatalf("expected 1 mix (video mix skipped), got %d", len(mixes))
	}
	if mixes[0].ID != "mix1" {
		t.Errorf("unexpected mix: %+v", mixes[0])
	}
}

func TestGetMixes_SkipsDuplicatesAndBlankIDs(t *testing.T) {
	// A page can repeat the same mix across modules; entries without an ID are
	// unusable.
	payload := mixPage(
		map[string]any{"id": "mix1", "title": "My Mix 1", "mixType": "DAILY_MIX"},
		map[string]any{"id": "mix1", "title": "My Mix 1", "mixType": "DAILY_MIX"},
		map[string]any{"id": "", "title": "Nameless", "mixType": "DAILY_MIX"},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, payload)
	}))
	defer srv.Close()

	mixes, err := newTestClient(srv).GetMixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mixes) != 1 {
		t.Fatalf("expected 1 mix, got %d", len(mixes))
	}
}

func TestGetMixes_NullDescription(t *testing.T) {
	// Tidal sends "description": null for every mix; it must decode to "".
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows":[{"modules":[{"pagedList":{"items":[
			{"id":"mix1","title":"My Mix 1","subTitle":"a, b and more","description":null,"mixType":"DAILY_MIX"}
		]}}]}]}`))
	}))
	defer srv.Close()

	mixes, err := newTestClient(srv).GetMixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mixes) != 1 {
		t.Fatalf("expected 1 mix, got %d", len(mixes))
	}
	if mixes[0].Description != "" {
		t.Errorf("expected empty description, got %q", mixes[0].Description)
	}
}

// A 404 on the page itself means this account has no mix recommendations
// right now — normal, empty data, not a failure worth surfacing.
func TestGetMixes_NotFoundIsEmptyNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 404, map[string]any{"userMessage": "Resource not found"})
	}))
	defer srv.Close()

	mixes, err := newTestClient(srv).GetMixes(context.Background())
	if err != nil {
		t.Fatalf("a 404 should not be an error, got %v", err)
	}
	if mixes != nil {
		t.Errorf("expected no mixes, got %+v", mixes)
	}
}

func TestGetMixes_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 500, map[string]any{"userMessage": "internal error"})
	}))
	defer srv.Close()

	if _, err := newTestClient(srv).GetMixes(context.Background()); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// --- GetMixTracks ---

// mixItems builds a v1 /mixes/{id}/items payload from typed entries.
func mixItems(entries ...map[string]any) map[string]any {
	return map[string]any{
		"totalNumberOfItems": len(entries),
		"items":              entries,
	}
}

func TestGetMixTracks_OK(t *testing.T) {
	// The v1 mix items endpoint returns fully populated tracks (artist and
	// album included) in a single request — the old v2 playlist-relationship
	// endpoint returned bare IDs only, resolved via one concurrent
	// /v1/tracks/{id} request per track.
	track1 := tidal.Track{ID: 101, Title: "Big Song"}
	track1.Artist.Name = "The Band"
	track1.Album.Title = "The Album"

	track2 := tidal.Track{ID: 202, Title: "Small Song"}
	track2.Artist.Name = "Other Artist"
	track2.Album.Title = "Other Album"

	payload := mixItems(
		map[string]any{"item": track1, "type": "track"},
		map[string]any{"item": track2, "type": "track"},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/mixes/mix1/items") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		respond(w, 200, payload)
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).GetMixTracks(context.Background(), "mix1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 2 {
		t.Fatalf("expected 2 tracks, got %d", len(tracks))
	}
	// Mix order must be preserved: 101 first, 202 second.
	if tracks[0].ID != 101 || tracks[0].Title != "Big Song" {
		t.Errorf("unexpected track[0]: %+v", tracks[0])
	}
	if tracks[0].Artist.Name != "The Band" {
		t.Errorf("unexpected artist: %q", tracks[0].Artist.Name)
	}
	if tracks[0].Album.Title != "The Album" {
		t.Errorf("unexpected album: %q", tracks[0].Album.Title)
	}
	if tracks[1].ID != 202 || tracks[1].Title != "Small Song" {
		t.Errorf("unexpected track[1]: %+v", tracks[1])
	}
}

func TestGetMixTracks_NormalizesArtist(t *testing.T) {
	// Some payloads carry only the plural "artists" array.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"type":"track","item":{
			"id":101,"title":"Song","artists":[{"id":7,"name":"Plural Only"}]
		}}]}`))
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).GetMixTracks(context.Background(), "mix1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if tracks[0].Artist.Name != "Plural Only" || tracks[0].Artist.ID != 7 {
		t.Errorf("artist not normalized: %+v", tracks[0].Artist)
	}
}

func TestGetMixTracks_SkipsNonTrackItems(t *testing.T) {
	// Video items cannot be decoded by the player and must be dropped.
	payload := mixItems(
		map[string]any{"item": tidal.Track{ID: 101, Title: "Song"}, "type": "track"},
		map[string]any{"item": tidal.Track{ID: 555, Title: "Clip"}, "type": "video"},
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 200, payload)
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).GetMixTracks(context.Background(), "mix1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track (video skipped), got %d", len(tracks))
	}
	if tracks[0].ID != 101 {
		t.Errorf("unexpected track: %+v", tracks[0])
	}
}

func TestGetMixTracks_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		respond(w, 404, map[string]any{"userMessage": "Resource not found"})
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetMixTracks(context.Background(), "gone")
	if !errors.Is(err, tidal.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// Mix and track titles are user-set on Tidal and rendered straight into a
// terminal, so they get the same escape-stripping as every other decoded
// field (see internal/sanitize). decodeJSON's reflection walk reaches these
// automatically now that pageResponse/mixItemsResponse are typed structs
// rather than the old v2 shape's opaque json.RawMessage, which needed its own
// separate sanitize.Strings call.
func TestGetMixesSanitizesTitles(t *testing.T) {
	body := "{" + `"rows":[{"modules":[{"pagedList":{"items":[` +
		`{"id":"mix1","title":"Chill Mix\u001b]52;c;aGFjaw==\u0007","subTitle":"A\u001b[2Jand","mixType":"DAILY_MIX"}` +
		`]}}]}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	mixes, err := newTestClient(srv).GetMixes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(mixes) != 1 {
		t.Fatalf("expected 1 mix, got %d", len(mixes))
	}
	if want := "Chill Mix]52;c;aGFjaw=="; mixes[0].Title != want {
		t.Errorf("Title = %q, want %q", mixes[0].Title, want)
	}
	if want := "A[2Jand"; mixes[0].SubTitle != want {
		t.Errorf("SubTitle = %q, want %q", mixes[0].SubTitle, want)
	}
}

func TestGetMixTracksSanitizesTrackFields(t *testing.T) {
	body := "{" + `"items":[{"type":"track","item":{` +
		`"id":101,"title":"Song\u001b]0;pwned\u0007","artist":{"name":"A\u001b[2Jrtist"}` +
		`}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	tracks, err := newTestClient(srv).GetMixTracks(context.Background(), "mix1")
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 {
		t.Fatalf("expected 1 track, got %d", len(tracks))
	}
	if want := "Song]0;pwned"; tracks[0].Title != want {
		t.Errorf("Title = %q, want %q", tracks[0].Title, want)
	}
	if want := "A[2Jrtist"; tracks[0].Artist.Name != want {
		t.Errorf("Artist.Name = %q, want %q", tracks[0].Artist.Name, want)
	}
}

// Asking for hi-res on an ordinary 44.1/16 track is answered with HIGH —
// lossy AAC. Accepting that at the top rung skipped LOSSLESS entirely, so
// every normal track started playing lossy. The ladder has to carry on.
func TestGetStreamURL_LossyGrantDoesNotEndTheLadder(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("audioquality")
		if !strings.Contains(r.URL.Path, pathPlaybackInfo) {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		asked = append(asked, q)
		switch q {
		case string(tidal.QualityHiRes):
			// The server answers a hi-res request with a lossy tier.
			respond(w, 200, map[string]any{
				"audioQuality":     qualityHigh,
				"manifestMimeType": "application/vnd.tidal.bts",
				"manifest":         btsManifest("https://cdn.tidal.com/lossy.m4a"),
			})
		case qualityLossless:
			respond(w, 200, map[string]any{
				"audioQuality":     qualityLossless,
				"manifestMimeType": "application/vnd.tidal.bts",
				"manifest":         btsManifest(urlLosslessFLAC),
				"bitDepth":         16,
				"sampleRate":       44100,
			})
		default:
			respond(w, 404, map[string]string{"error": "not found"})
		}
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 69144305, false)
	if err != nil {
		t.Fatal(err)
	}
	if info.Quality != tidal.QualityLossless {
		t.Errorf("Quality = %q, want LOSSLESS — a lossy answer to a hi-res request must not end the ladder",
			info.Quality)
	}
	if info.URL() != urlLosslessFLAC {
		t.Errorf("URL = %s, want the lossless stream", info.URL())
	}
	if !slices.Contains(asked, qualityLossless) {
		t.Errorf("the LOSSLESS tier was never requested: %v", asked)
	}
}

// When the ladder itself asks for a lossy tier — Data Saver — a lossy grant is
// exactly right and must be accepted.
func TestGetStreamURL_LossyGrantIsFineWhenRequested(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, pathPlaybackInfo) {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		if r.URL.Query().Get("audioquality") != qualityHigh {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		respond(w, 200, map[string]any{
			"audioQuality":     qualityHigh,
			"manifestMimeType": "application/vnd.tidal.bts",
			"manifest":         btsManifest("https://cdn.tidal.com/high.m4a"),
		})
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if info.Quality != tidal.QualityHigh {
		t.Errorf("Quality = %q, want HIGH under Data Saver", info.Quality)
	}
}
