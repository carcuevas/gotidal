package ui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/carcuevas/gotidal/internal/tidal"
)

// rewriteTransport redirects every request to a test server while keeping the
// path and query intact, so a handler can see which endpoint was called. The
// Tidal API base URLs are package constants, so intercepting at the transport
// is the only way to fake a client in tests.
type rewriteTransport struct{ base *url.URL }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = t.base.Scheme
	r.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(r)
}

func newTestTidalClient(t *testing.T, srv *httptest.Server) *tidal.Client {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	c := tidal.NewClient()
	c.Session = &tidal.Session{
		AccessToken:  "test-token",
		RefreshToken: "test-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
		CountryCode:  "US",
	}
	c.Transport = rewriteTransport{base: u}
	return c
}

// Reported: pressing "a" on a mix row did nothing (or, worse, could enqueue an
// unrelated queue track — see below). selectedTrack()'s generic fallback used
// m.cursor as an index into m.tracks (the Queue's own list, shared via the
// same handler and cursor), but on the Mixes tab the cursor indexes m.mixes
// instead — a mix is not a track.
func TestSelectedTrackExcludesMixesTab(t *testing.T) {
	m := newSmokeModel()
	m.section = SecMixes
	m.mixes = []tidal.Mix{{ID: "mix1", Title: "Daily Mix 1"}}
	m.cursor = 0
	// m.tracks (inherited from newSmokeModel) is non-empty, so before the fix
	// this index coincidentally resolved to a real (wrong) queue track rather
	// than failing loudly.
	if len(m.tracks) == 0 {
		t.Fatal("fixture must have queue tracks for this to be a meaningful test")
	}

	got := m.selectedTrack()
	if got != nil && got.ID == m.tracks[0].ID {
		t.Errorf("selectedTrack() returned queue track %+v while browsing Mixes — a mix row is not a track", got)
	}
}

// "a" on a mix row must fetch that mix's tracks and append them to the queue,
// the Mixes-tab counterpart of enqueuePlaylistCmd for playlists.
func TestEnqueueMixCmdAddsTracksToQueue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("countryCode"); got == "" {
			t.Errorf("request missing countryCode: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[
			{"type":"track","item":{"id":101,"title":"Song One"}},
			{"type":"track","item":{"id":202,"title":"Song Two"}}
		]}`))
	}))
	defer srv.Close()

	m := newSmokeModel()
	m.client = newTestTidalClient(t, srv)
	m.ctx = context.Background()
	m.section = SecMixes
	m.mixes = []tidal.Mix{{ID: "mix1", Title: "Daily Mix 1"}}
	m.cursor = 0
	existing := len(m.tracks)

	nm, cmd := m.updateListKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if cmd == nil {
		t.Fatal("expected a command to fetch the mix's tracks")
	}
	msg := cmd()
	enq, ok := msg.(enqueuePlaylistMsg)
	if !ok {
		if errMsg, isErr := msg.(errMsg); isErr {
			t.Fatalf("command returned an error: %v", error(errMsg))
		}
		t.Fatalf("expected enqueuePlaylistMsg, got %T: %+v", msg, msg)
	}
	if len(enq.tracks) != 2 || enq.tracks[0].ID != 101 || enq.tracks[1].ID != 202 {
		t.Errorf("unexpected tracks: %+v", enq.tracks)
	}

	got := asModel(t, nm)
	updated, _ := got.Update(enq)
	final := asModel(t, updated)
	if len(final.tracks) != existing+2 {
		t.Errorf("queue has %d tracks, want %d (existing %d + 2 enqueued)",
			len(final.tracks), existing+2, existing)
	}
}

// Browsing the Queue tab itself must be unaffected: "a" there still uses
// selectedTrack()'s normal (Queue) behaviour via commonKeys.
func TestEnqueueMixCmdDoesNotFireOutsideMixesTab(t *testing.T) {
	m := newSmokeModel()
	m.section = SecQueue
	m.cursor = 0

	before := len(m.tracks)
	nm, _ := m.updateListKeys(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	got := asModel(t, nm)
	// Queue's "a" re-adds the selected queue track — a different, pre-existing
	// behaviour this change must not disturb.
	if len(got.tracks) != before+1 {
		t.Errorf("queue has %d tracks after \"a\" on the Queue tab, want %d", len(got.tracks), before+1)
	}
}
