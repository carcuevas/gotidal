package tidal

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// rewriteTransport redirects every request to the test server while keeping
// the path and query intact, so a handler can see which quality tier was
// asked for. The API base URL is a package constant, so intercepting at the
// transport is the only way in.
type rewriteTransport struct{ base *url.URL }

func (t rewriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.URL.Scheme = t.base.Scheme
	r.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(r)
}

// newTestClient returns a Client whose requests land on srv, with a session
// whose token is valid for an hour so oauth2 never tries to refresh it.
func newTestClient(srv *httptest.Server) *Client {
	u, err := url.Parse(srv.URL)
	if err != nil {
		panic(err)
	}
	c := NewClient()
	c.Session = &Session{
		AccessToken:  "test-token",
		RefreshToken: "test-refresh",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
		CountryCode:  "ES",
	}
	c.Transport = rewriteTransport{base: u}
	return c
}

// Resource IDs come from the last path segment of a tidal:// deep link, which
// a web page can choose freely, and they are concatenated straight into
// request URLs that carry the user's bearer token.
func TestCheckID(t *testing.T) {
	valid := []string{
		"12345",
		"1",
		"00abc123def",
		"0f5a1b2c-3d4e-5f60-7182-93a4b5c6d7e8",
		"some_id-1",
	}
	for _, id := range valid {
		if err := checkID(id); err != nil {
			t.Errorf("checkID(%q) = %v, want nil", id, err)
		}
	}

	invalid := []struct{ name, id string }{
		{"empty", ""},
		{"path traversal", "../users/1"},
		{"extra path segment", "1/relationships/items"},
		{"query injection", "1?countryCode=XX&"},
		{"fragment", "1#x"},
		{"percent escape", "1%2Fx"},
		{"whole url", "https://api.tidal.com/v1/users/1"},
		{"space", "1 2"},
		{"terminal escape", "1\x1b]52;c;aGk=\x07"},
		{"newline", "1\n2"},
		{"too long", strings.Repeat("1", 65)},
	}
	for _, c := range invalid {
		t.Run(c.name, func(t *testing.T) {
			err := checkID(c.id)
			if err == nil {
				t.Fatalf("checkID(%q) = nil, want an error", c.id)
			}
			if !errors.Is(err, ErrInvalidID) {
				t.Errorf("error should wrap ErrInvalidID, got %v", err)
			}
		})
	}
}

// The rejected ID is echoed back in the error, which is rendered in the TUI —
// so it must not carry the escape sequences it was rejected for.
func TestCheckIDErrorIsSanitized(t *testing.T) {
	err := checkID("1\x1b]52;c;aGFjaw==\x07")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.ContainsAny(err.Error(), "\x1b\x07") {
		t.Errorf("error message still contains a control character: %q", err.Error())
	}
}

// A literal control byte cannot appear inside a JSON string, so a hostile value
// arrives escaped (\u001b) and only becomes a real escape sequence once
// decoded — which is why sanitizing has to happen after the decode, not on the
// wire. Removing ESC and BEL is what defuses it; the remaining characters are
// ordinary printable text.
func TestDecodeJSONSanitizes(t *testing.T) {
	body := "{" +
		`"title":"Chill Mix\u001b]52;c;aGFjaw==\u0007",` +
		`"artist":{"name":"A\u001b[2Jrtist"},` +
		`"artists":[{"name":"B\u001b[2Jand"}]` +
		"}"

	var tr Track
	if err := decodeJSON(strings.NewReader(body), &tr); err != nil {
		t.Fatalf("decodeJSON: %v", err)
	}
	if want := "Chill Mix]52;c;aGFjaw=="; tr.Title != want {
		t.Errorf("Title = %q, want %q", tr.Title, want)
	}
	if want := "A[2Jrtist"; tr.Artist.Name != want {
		t.Errorf("Artist.Name = %q, want %q", tr.Artist.Name, want)
	}
	// Nested slices of structs have to be walked too.
	if want := "B[2Jand"; tr.Artists[0].Name != want {
		t.Errorf("Artists[0].Name = %q, want %q", tr.Artists[0].Name, want)
	}
}

// apiErr renders a remote error body into the TUI's error line, so it needs the
// same treatment — both when the body parses as Tidal's JSON error shape and
// when it falls back to the raw text.
func TestAPIErrSanitizesBody(t *testing.T) {
	for _, body := range []string{
		`{"userMessage":"nope\u001b]0;pwned\u0007"}`,
		"plain body\x1b]0;pwned\x07",
	} {
		err := apiErr("op", 500, []byte(body))
		if strings.ContainsAny(err.Error(), "\x1b\x07") {
			t.Errorf("apiErr(%q) leaked a control character: %q", body, err.Error())
		}
	}
}

// The old message ended in "get stream (LOW): ..." because LOW is the bottom
// of both ladders, which read as though only the lossy tier had been tried —
// and pointed the reader at the Data Saver / bit-perfect settings, neither of
// which decides whether the asset exists.
func TestStreamLadderErrorNamesEveryTier(t *testing.T) {
	const reason = "Asset is not ready for playback"

	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Query().Get("audioquality"))
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"userMessage":"` + reason + `"}`))
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.GetStreamURL(context.Background(), 363761094, false)
	if err == nil {
		t.Fatal("expected an error when every tier is refused")
	}
	msg := err.Error()

	// Every tier really is attempted...
	if len(hits) != len(qualityLadder) {
		t.Errorf("tried %v, want all of %v", hits, qualityLadder)
	}
	// ...and the message has to say so.
	for _, q := range qualityLadder {
		if !strings.Contains(msg, q.Label()) {
			t.Errorf("message does not mention the %s tier: %s", q.Label(), msg)
		}
	}
	if !strings.Contains(msg, reason) {
		t.Errorf("message drops Tidal's own explanation: %s", msg)
	}
	if !strings.Contains(msg, "363761094") {
		t.Errorf("message does not identify the track: %s", msg)
	}
	// The shape that caused the confusion must not come back.
	if strings.Contains(msg, "get stream (LOW)") {
		t.Errorf("message still reads as though only LOW was tried: %s", msg)
	}
}

// Data Saver skips the lossless tiers, so its failure message must name only
// the two tiers it actually tried.
func TestStreamLadderErrorLowDataNamesOnlyItsTiers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"userMessage":"nope"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetStreamURL(context.Background(), 1, true)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, q := range lowDataQualityLadder {
		if !strings.Contains(msg, q.Label()) {
			t.Errorf("message does not mention the %s tier: %s", q.Label(), msg)
		}
	}
	if strings.Contains(msg, QualityHiRes.Label()) || strings.Contains(msg, QualityLossless.Label()) {
		t.Errorf("Data Saver never tries the lossless tiers, so they must not appear: %s", msg)
	}
}

// Differing reasons per tier must all survive, so a mixed failure is still
// diagnosable.
func TestStreamLadderErrorKeepsDifferingReasons(t *testing.T) {
	// Reasons are looked up from a fixed table rather than reflected out of
	// the request, so nothing attacker-shaped is echoed back.
	reasons := map[Quality]string{
		QualityHiRes:    "no hi-res master",
		QualityLossless: "not in your subscription",
		QualityHigh:     "region locked",
		QualityLow:      "asset missing",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reason, ok := reasons[Quality(r.URL.Query().Get("audioquality"))]
		if !ok {
			reason = "unknown tier"
		}
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"userMessage":"` + reason + `"}`))
	}))
	defer srv.Close()

	_, err := newTestClient(srv).GetStreamURL(context.Background(), 1, false)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, q := range qualityLadder {
		if !strings.Contains(err.Error(), reasons[q]) {
			t.Errorf("message dropped %q: %s", reasons[q], err.Error())
		}
	}
}

// A tier that succeeds still wins, and no ladder error is reported.
func TestStreamLadderStopsAtTheFirstGrantedTier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("audioquality") == string(QualityHiRes) {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = w.Write([]byte(`{"userMessage":"no hi-res master"}`))
			return
		}
		_, _ = w.Write([]byte(`{"urls":["https://example.com/a.flac?token=x"]}`))
	}))
	defer srv.Close()

	info, err := newTestClient(srv).GetStreamURL(context.Background(), 1, false)
	if err != nil {
		t.Fatalf("GetStreamURL: %v", err)
	}
	if info.Quality != QualityLossless {
		t.Errorf("granted tier = %q, want %q", info.Quality, QualityLossless)
	}
	if info.Ext != "flac" {
		t.Errorf("Ext = %q, want flac", info.Ext)
	}
}
