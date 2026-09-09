package tidal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/carcuevas/gotidal/internal/logger"
	"github.com/carcuevas/gotidal/internal/sanitize"
)

// ErrNotFound is returned by GetTrack when the API responds with 404.
var ErrNotFound = errors.New("not found")

// ErrInvalidID is returned when a resource ID is not in the shape Tidal uses.
var ErrInvalidID = errors.New("invalid Tidal resource ID")

// resourceIDRE describes every ID shape Tidal actually issues: decimal for
// tracks and albums, a UUID or hex string for playlists and mixes.
var resourceIDRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// checkID rejects an ID before it is concatenated into a request URL.
//
// These IDs come from the last path segment of a tidal:// deep link, which the
// browser hands to `gotidal play` when a page navigates to that scheme — so
// they are remote input. Without this, an ID of
// "1/relationships/items?countryCode=XX&" splices extra path segments and
// query parameters into a request that carries the user's bearer token.
// Excluding "/", "?", "&", "#", "%" and "." is what makes the concatenation
// below safe.
func checkID(id string) error {
	if !resourceIDRE.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidID, sanitize.Text(id))
	}
	return nil
}

// decodeJSON decodes a JSON response body into v, then strips terminal
// control characters from every string it contains (see internal/sanitize).
//
// Every decode in this package must go through here rather than calling
// json.NewDecoder directly. These responses carry remote, attacker-influenced
// text — track, album, playlist and mix names are user-set on Tidal — that is
// rendered straight into a terminal, which executes escape sequences rather
// than displaying them. Sanitizing here rather than at the ~15 render sites
// means fields added later are covered by default.
func decodeJSON(r io.Reader, v any) error {
	if err := json.NewDecoder(r).Decode(v); err != nil {
		return err
	}
	sanitize.Strings(v)
	return nil
}

// authGet issues a context-aware GET to u through the authenticated client.
// It centralises request construction so every call carries the request
// context (satisfying noctx) and reuses the oauth2 token source.
func (c *Client) authGet(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, err
	}
	return c.GetAuthClient(ctx).Do(req)
}

// authPostForm issues a context-aware form POST to u through the authenticated
// client.
func (c *Client) authPostForm(ctx context.Context, u string, body url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.GetAuthClient(ctx).Do(req)
}

// CoverURL returns the HTTPS URL for the album cover image at the given size.
// cover is the UUID string returned in Track.Album.Cover.
// Typical sizes: "80x80", "160x160", "320x320", "640x640", "1280x1280".
// Returns "" when cover is empty.
func CoverURL(cover, size string) string {
	if cover == "" {
		return ""
	}
	// Tidal stores the UUID with dashes replaced by underscores in the JSON;
	// the image CDN path uses forward slashes between groups.
	dashed := strings.ReplaceAll(cover, "-", "/")
	return "https://resources.tidal.com/images/" + dashed + "/" + size + ".jpg"
}

// apiReason extracts the human-readable part of a non-2xx response: Tidal's
// own "userMessage" when present, otherwise the raw body text. Split out of
// apiErr so callers that retry across several attempts (GetStreamURL's
// quality ladder) can compare and group the reasons rather than keeping only
// the last error.
//
// Both branches end up rendered in the TUI's error line, so the remote text
// gets the same escape-stripping as any other decoded field.
func apiReason(status int, body []byte) string {
	var e struct {
		UserMessage string `json:"userMessage"`
	}
	if json.Unmarshal(body, &e) == nil && e.UserMessage != "" {
		return sanitize.Text(e.UserMessage)
	}
	if t := strings.TrimSpace(string(body)); t != "" {
		return fmt.Sprintf("HTTP %d: %s", status, sanitize.Text(t))
	}
	return fmt.Sprintf("HTTP %d", status)
}

// apiErr returns a formatted error from a non-2xx response.
func apiErr(op string, status int, body []byte) error {
	return fmt.Errorf("%s: %s", op, apiReason(status, body))
}

// qualityError is one quality tier's refusal, carrying the tier and the reason
// separately so GetStreamURL can say the whole ladder was tried.
type qualityError struct {
	quality Quality
	reason  string
}

func (e qualityError) Error() string { return string(e.quality) + ": " + e.reason }

type Artist struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Track struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Artist Artist `json:"artist"`
	// Artists is the plural form returned by some endpoints (notably /search),
	// where the singular "artist" field is absent. normalizeArtist backfills
	// Artist from Artists[0] so callers can always rely on Track.Artist.
	Artists []Artist `json:"artists"`
	Album   struct {
		ID    int    `json:"id"`
		Title string `json:"title"`
		Cover string `json:"cover"` // UUID, e.g. "a3f1d2e4-1234-5678-abcd-ef0123456789"
	} `json:"album"`
	Duration int    `json:"duration"`
	URL      string `json:"url"`
}

type Mix struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	SubTitle    string `json:"subTitle"`
	Description string `json:"description"`
}

// Playlist is a user-created or saved Tidal playlist. UUID is the v1 playlist
// identifier used for track and mutation endpoints.
type Playlist struct {
	UUID           string `json:"uuid"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	NumberOfTracks int    `json:"numberOfTracks"`
	Duration       int    `json:"duration"`
}

type SearchResponse struct {
	Tracks struct {
		Items []Track `json:"items"`
	} `json:"tracks"`
}

type StreamResponse struct {
	URLs []string `json:"urls"`
}

type UserResponse struct {
	ID           int    `json:"id"`
	CountryCode  string `json:"countryCode"`
	Email        string `json:"email"`
	FullName     string `json:"fullName"`
	ProfileImage string `json:"picture"`
}

type FavoritesResponse struct {
	Items []struct {
		Item Track `json:"item"`
	} `json:"items"`
}

type radioResponse struct {
	Items []Track `json:"items"`
}

type albumTracksResponse struct {
	Items []Track `json:"items"`
}

// Album is a standalone album as returned by the artist albums endpoint.
// (Track has its own inline album struct; this is the richer top-level shape.)
type Album struct {
	ID             int    `json:"id"`
	Title          string `json:"title"`
	Cover          string `json:"cover"` // UUID, same format as Track.Album.Cover
	ReleaseDate    string `json:"releaseDate"`
	NumberOfTracks int    `json:"numberOfTracks"`
}

type artistAlbumsResponse struct {
	Items              []Album `json:"items"`
	TotalNumberOfItems int     `json:"totalNumberOfItems"`
}

type artistTopTracksResponse struct {
	Items []Track `json:"items"`
}

// mixTracksLimit caps how many items a single mix request returns. Mixes are
// short (tens of tracks), so one page is always enough.
const mixTracksLimit = 100

// pageResponse is the v1 "pages/*" envelope. Only the fields the mix list
// needs are modelled; the rest of the page payload (graphics, colours, module
// metadata) is ignored.
type pageResponse struct {
	Rows []struct {
		Modules []struct {
			PagedList struct {
				Items              []pageMixItem `json:"items"`
				TotalNumberOfItems int           `json:"totalNumberOfItems"`
			} `json:"pagedList"`
		} `json:"modules"`
	} `json:"rows"`
}

// pageMixItem is one mix entry inside a MIX_LIST module.
type pageMixItem struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	SubTitle    string `json:"subTitle"`
	Description string `json:"description"`
	MixType     string `json:"mixType"`
}

// mixItemsResponse is the v1 /mixes/{id}/items payload. Each entry wraps the
// media object and tags it with its kind ("track" or "video").
type mixItemsResponse struct {
	Items []struct {
		Item Track  `json:"item"`
		Type string `json:"type"`
	} `json:"items"`
	TotalNumberOfItems int `json:"totalNumberOfItems"`
}

func (c *Client) GetUser(ctx context.Context) (*UserResponse, error) {
	resp, err := c.authGet(ctx, fmt.Sprintf("%s/users/%d", BaseURL, c.Session.UserID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get user", resp.StatusCode, body)
	}

	var u UserResponse
	if err := decodeJSON(resp.Body, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) GetTrack(ctx context.Context, trackID string) (*Track, error) {
	if err := checkID(trackID); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("countryCode", c.Session.CountryCode)
	resp, err := c.authGet(ctx, BaseURL+"/tracks/"+trackID+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get track", resp.StatusCode, body)
	}

	var t Track
	if err := decodeJSON(resp.Body, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// Search returns only the track results for a query. It is retained for
// callers that just need tracks (e.g. the tidal:// URL resolver); the grouped
// multi-category results are available via SearchAll.
func (c *Client) Search(ctx context.Context, query string) ([]Track, error) {
	res, err := c.SearchAll(ctx, query)
	if err != nil {
		return nil, err
	}
	return res.Tracks, nil
}

// normalizeArtist fills the singular Artist field from Artists[0] when the
// endpoint only returned the plural "artists" array (e.g. /search). This keeps
// Track.Artist usable everywhere — playback, favoriting, the artist view.
func (t *Track) normalizeArtist() {
	if t.Artist.ID == 0 && t.Artist.Name == "" && len(t.Artists) > 0 {
		t.Artist = t.Artists[0]
	}
}

// Quality is a Tidal audio-quality tier, as accepted by the "audioquality"
// stream parameter. This package owns the canonical ladder: callers should use
// these constants and Label rather than re-listing the tier strings.
type Quality string

const (
	QualityHiRes    Quality = "HI_RES_LOSSLESS"
	QualityLossless Quality = "LOSSLESS"
	QualityHigh     Quality = "HIGH"
	QualityLow      Quality = "LOW"
)

// qualityLadder is the descending preference order tried by GetStreamURL.
var qualityLadder = []Quality{QualityHiRes, QualityLossless, QualityHigh, QualityLow}

// lowDataQualityLadder is tried instead of qualityLadder when GetStreamURL's
// lowData argument is true — skips straight past the lossless FLAC tiers to
// lossy AAC, for a hotspot/metered-connection toggle where bandwidth matters
// more than bit-perfectness.
var lowDataQualityLadder = []Quality{QualityHigh, QualityLow}

// Lossy reports whether the tier is a lossy (AAC) one. HIGH and LOW are
// transcodes; HI_RES_LOSSLESS and LOSSLESS are not.
func (q Quality) Lossy() bool { return q == QualityHigh || q == QualityLow }

// qualityLabels maps each tier to its short display label.
var qualityLabels = map[Quality]string{
	QualityHiRes:    "hi-res",
	QualityLossless: "lossless",
	QualityHigh:     "high",
	QualityLow:      "low",
}

// Label returns a short human-readable label for the tier, suitable for a
// compact UI badge. Unknown tiers fall back to the lower-cased raw value so a
// tier added to the ladder still renders something rather than vanishing.
func (q Quality) Label() string {
	if l, ok := qualityLabels[q]; ok {
		return l
	}
	if q == "" {
		return ""
	}
	return strings.ToLower(strings.ReplaceAll(string(q), "_", " "))
}

// StreamInfo carries the resolved stream URL and its detected format extension.
type StreamInfo struct {
	// URLs is the stream in order. A plain URL is a single entry; a hi-res
	// DASH presentation is the initialization segment followed by every media
	// segment, which concatenate into one byte stream (see internal/player's
	// segmentReader). Never empty on a successful resolve.
	URLs    []string
	Ext     string  // e.g. "flac", "mp4", "m4a" — logging only
	Quality Quality // the tier that was actually granted

	// BitDepth and SampleRate are the source format as the server reports it,
	// or 0 when it does not. Only playbackinfopostpaywall supplies these;
	// urlpostpaywall leaves them zero and the depth is inferred from the
	// container instead.
	BitDepth   uint8
	SampleRate uint32

	// DurationSec is the whole presentation's length, from the manifest, or 0
	// when the container can measure itself. A fragmented stream cannot: only
	// the initialization segment is available when the decoder opens, so
	// libavformat reports the first fragment's duration as the track's.
	DurationSec float64

	// InitURLs is how many leading URLs are initialization segments (1 for a
	// DASH presentation, 0 otherwise), and SegmentSeconds the playing time of
	// one media segment. Together they let a seek reopen at the segment
	// holding the target instead of re-fetching the whole track up to it.
	InitURLs       int
	SegmentSeconds float64
}

// URL returns the first URL, for the callers and logs that only need to
// identify the stream rather than read all of it.
func (s StreamInfo) URL() string {
	if len(s.URLs) == 0 {
		return ""
	}
	return s.URLs[0]
}

// GetStreamURL resolves the stream URL for trackID, walking qualityLadder
// (HI_RES_LOSSLESS down to LOW) top to bottom and returning the first tier
// Tidal grants. lowData selects lowDataQualityLadder instead — HIGH then LOW
// only, skipping both lossless FLAC tiers — for a metered-connection toggle
// where bandwidth matters more than bit-perfectness.
func (c *Client) GetStreamURL(ctx context.Context, trackID int, lowData bool) (StreamInfo, error) {
	ladder := qualityLadder
	if lowData {
		ladder = lowDataQualityLadder
	}

	var tiers, reasons []string

	for _, q := range ladder {
		info, err := c.resolveQualityTier(ctx, trackID, q)
		if err != nil {
			// Logged rather than surfaced: a higher tier failing and falling
			// back to a lower one is normal (account entitlement, or no
			// hi-res master for this track), and these are all discarded the
			// moment any tier succeeds — so this debug line is the only way
			// to see *why* a track played below the top of the ladder.
			logger.L.Debug("stream tier unavailable, trying next", "trackID", trackID, "quality", q, "err", err)
			tiers = append(tiers, q.Label())
			var qe qualityError
			if errors.As(err, &qe) {
				reasons = append(reasons, qe.reason)
			} else {
				reasons = append(reasons, err.Error())
			}
			continue
		}
		if q != ladder[0] {
			logger.L.Debug("granted stream tier below the top of the ladder", "trackID", trackID, "quality", q, "requested", ladder[0])
		}
		return info, nil
	}

	if len(reasons) == 0 {
		return StreamInfo{}, fmt.Errorf("no stream available for track %d", trackID)
	}

	// Name every tier that was tried, not just the last rung. The previous
	// message ended in "get stream (LOW): ..." because LOW is the bottom of
	// both ladders, which read as though only the lossy tier had been
	// attempted — and sent the reader to the Data Saver and bit-perfect
	// settings, neither of which decides whether the asset exists.
	sameReason := true
	for _, r := range reasons[1:] {
		if r != reasons[0] {
			sameReason = false
			break
		}
	}
	if sameReason {
		return StreamInfo{}, fmt.Errorf("track %d is unavailable at every quality (%s): %s",
			trackID, strings.Join(tiers, ", "), reasons[0])
	}
	parts := make([]string, len(tiers))
	for i := range tiers {
		parts[i] = tiers[i] + " — " + reasons[i]
	}
	return StreamInfo{}, fmt.Errorf("track %d is unavailable at every quality: %s",
		trackID, strings.Join(parts, "; "))
}

// resolveQualityTier resolves one tier, preferring playbackinfopostpaywall —
// the only endpoint that can express a hi-res DASH presentation, and the only
// one that reports the source bit depth and sample rate — and falling back to
// the older urlpostpaywall when it refuses.
//
// The fallback matters: urlpostpaywall is what every tier used until hi-res
// support was added, so keeping it means a playbackinfo failure degrades to
// the previously working behaviour rather than losing the track outright.
func (c *Client) resolveQualityTier(ctx context.Context, trackID int, q Quality) (StreamInfo, error) {
	info, err := c.playbackInfo(ctx, trackID, q)
	if err == nil {
		return info, nil
	}
	logger.L.Debug("playbackinfo failed, falling back to urlpostpaywall",
		"trackID", trackID, "quality", q, "err", err)

	// Hi-res only exists as a DASH presentation, so urlpostpaywall cannot
	// serve it — retrying there would burn a request to be told the same
	// thing in a less specific way. Report what playbackinfo said instead.
	if q == QualityHiRes {
		return StreamInfo{}, err
	}

	info, fallbackErr := c.streamURLForQuality(ctx, trackID, q)
	if fallbackErr != nil {
		// Surface the playbackinfo reason: it is the endpoint that should
		// have worked, so its explanation is the more useful one.
		return StreamInfo{}, err
	}
	return info, nil
}

// streamURLForQuality fetches the stream URL for a single audio-quality tier.
// Pulled out of GetStreamURL so the response body is closed per attempt rather
// than via a defer accumulated inside the quality-ladder loop.
func (c *Client) streamURLForQuality(ctx context.Context, trackID int, q Quality) (StreamInfo, error) {
	endpoint := fmt.Sprintf("/tracks/%d/urlpostpaywall", trackID)
	params := url.Values{}
	params.Set("urlusagemode", "STREAM")
	params.Set("audioquality", string(q))
	params.Set("assetpresentation", "FULL")
	params.Set("countryCode", c.Session.CountryCode)

	u := BaseURL + endpoint + "?" + params.Encode()
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return StreamInfo{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return StreamInfo{}, qualityError{quality: q, reason: apiReason(resp.StatusCode, body)}
	}

	var s StreamResponse
	if err := decodeJSON(resp.Body, &s); err != nil {
		return StreamInfo{}, err
	}
	if len(s.URLs) == 0 {
		return StreamInfo{}, qualityError{quality: q, reason: "response contained no URLs"}
	}

	streamURL := s.URLs[0]
	return StreamInfo{
		URLs:    []string{streamURL},
		Ext:     streamExt("", streamURL),
		Quality: q,
	}, nil
}

func (c *Client) GetFavorites(ctx context.Context, limit int) ([]Track, error) {
	endpoint := fmt.Sprintf("/users/%d/favorites/tracks", c.Session.UserID)
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("countryCode", c.Session.CountryCode)
	params.Set("order", "DATE")
	params.Set("orderDirection", "DESC")

	u := BaseURL + endpoint + "?" + params.Encode()
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get favorites", resp.StatusCode, body)
	}

	var res FavoritesResponse
	if err := decodeJSON(resp.Body, &res); err != nil {
		return nil, err
	}

	tracks := make([]Track, len(res.Items))
	for i := range res.Items {
		tracks[i] = res.Items[i].Item
	}
	return tracks, nil
}

func (c *Client) GetTrackRadio(ctx context.Context, trackID int) ([]Track, error) {
	params := url.Values{}
	params.Set("limit", "100")
	params.Set("countryCode", c.Session.CountryCode)

	u := fmt.Sprintf("%s/tracks/%d/radio?%s", BaseURL, trackID, params.Encode())
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get track radio", resp.StatusCode, body)
	}

	var res radioResponse
	if err := decodeJSON(resp.Body, &res); err != nil {
		return nil, err
	}
	return res.Items, nil
}

func (c *Client) GetAlbumTracks(ctx context.Context, albumID string) ([]Track, error) {
	if err := checkID(albumID); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("countryCode", c.Session.CountryCode)

	u := fmt.Sprintf("%s/albums/%s/tracks?%s", BaseURL, albumID, params.Encode())
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get album tracks", resp.StatusCode, body)
	}

	var res albumTracksResponse
	if err := decodeJSON(resp.Body, &res); err != nil {
		return nil, err
	}
	return res.Items, nil
}

// artistAlbumsPageLimit is the per-page album count requested when paginating
// an artist's discography. artistAlbumsMaxPages caps the loop so a malformed
// totalNumberOfItems can never spin forever.
const (
	artistAlbumsPageLimit = 50
	artistAlbumsMaxPages  = 20 // up to 1000 albums
)

// GetArtistAlbums returns the artist's full discography, paginating through the
// v1 /artists/{id}/albums endpoint until exhausted.
func (c *Client) GetArtistAlbums(ctx context.Context, artistID int) ([]Album, error) {
	var albums []Album
	for page := range artistAlbumsMaxPages {
		params := url.Values{}
		params.Set("countryCode", c.Session.CountryCode)
		params.Set("limit", strconv.Itoa(artistAlbumsPageLimit))
		params.Set("offset", strconv.Itoa(page*artistAlbumsPageLimit))

		u := fmt.Sprintf("%s/artists/%d/albums?%s", BaseURL, artistID, params.Encode())
		resp, err := c.authGet(ctx, u)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return nil, apiErr("get artist albums", resp.StatusCode, body)
		}

		var res artistAlbumsResponse
		if err := decodeJSON(resp.Body, &res); err != nil {
			_ = resp.Body.Close()
			return nil, err
		}
		_ = resp.Body.Close()

		albums = append(albums, res.Items...)

		// Last page reached: a short page, or we've collected the reported total.
		if len(res.Items) < artistAlbumsPageLimit ||
			(res.TotalNumberOfItems > 0 && len(albums) >= res.TotalNumberOfItems) {
			break
		}
	}
	return albums, nil
}

// GetArtistTopTracks returns the artist's most popular tracks (up to limit).
func (c *Client) GetArtistTopTracks(ctx context.Context, artistID, limit int) ([]Track, error) {
	params := url.Values{}
	params.Set("countryCode", c.Session.CountryCode)
	params.Set("limit", strconv.Itoa(limit))

	u := fmt.Sprintf("%s/artists/%d/toptracks?%s", BaseURL, artistID, params.Encode())
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get artist top tracks", resp.StatusCode, body)
	}

	var res artistTopTracksResponse
	if err := decodeJSON(resp.Body, &res); err != nil {
		return nil, err
	}
	for i := range res.Items {
		res.Items[i].normalizeArtist()
	}
	return res.Items, nil
}

// artistAllTracksConcurrency bounds how many album track-list requests run at
// once in GetArtistAllTracks, so a deep discography doesn't open hundreds of
// connections at the same time.
const artistAllTracksConcurrency = 8

// GetArtistAllTracks collects every track across all of the artist's albums.
// Albums are fetched concurrently (bounded by artistAllTracksConcurrency); an
// album that fails to load (e.g. region-locked) is skipped rather than aborting
// the whole list. The result is flattened in album order and deduped by track ID
// so songs that appear on both an album and a single/compilation are not repeated.
func (c *Client) GetArtistAllTracks(ctx context.Context, artistID int) ([]Track, error) {
	albums, err := c.GetArtistAlbums(ctx, artistID)
	if err != nil {
		return nil, err
	}
	if len(albums) == 0 {
		return nil, nil
	}

	perAlbum := make([][]Track, len(albums))
	sem := make(chan struct{}, artistAllTracksConcurrency)
	var wg sync.WaitGroup
	for i, alb := range albums {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx, albumID int) {
			defer wg.Done()
			defer func() { <-sem }()
			ts, err := c.GetAlbumTracks(ctx, strconv.Itoa(albumID))
			if err != nil {
				return // skip this album
			}
			perAlbum[idx] = ts
		}(i, alb.ID)
	}
	wg.Wait()

	var all []Track
	seen := make(map[int]bool)
	for _, ts := range perAlbum {
		for i := range ts {
			if seen[ts[i].ID] {
				continue
			}
			seen[ts[i].ID] = true
			all = append(all, ts[i])
		}
	}
	return all, nil
}

func (c *Client) AddFavorite(ctx context.Context, trackID int) error {
	endpoint := fmt.Sprintf("/users/%d/favorites/tracks", c.Session.UserID)
	query := url.Values{}
	query.Set("countryCode", c.Session.CountryCode)

	body := url.Values{}
	body.Set("trackId", strconv.Itoa(trackID))

	resp, err := c.authPostForm(ctx, BaseURL+endpoint+"?"+query.Encode(), body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return apiErr("add favorite", resp.StatusCode, b)
	}
	return nil
}

func (c *Client) RemoveFavorite(ctx context.Context, trackID int) error {
	endpoint := fmt.Sprintf("/users/%d/favorites/tracks/%d", c.Session.UserID, trackID)
	params := url.Values{}
	params.Set("countryCode", c.Session.CountryCode)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		BaseURL+endpoint+"?"+params.Encode(), http.NoBody)
	if err != nil {
		return err
	}
	resp, err := c.GetAuthClient(ctx).Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return apiErr("remove favorite", resp.StatusCode, body)
	}
	return nil
}

// GetMixes returns the user's personalised mixes (My Daily Discovery, My Mix
// 1..N and friends).
//
// It reads the v1 "my_collection_my_mixes" page rather than the v2
// userRecommendations relationship this used to call: Tidal removed
// openapi.tidal.com/v2/userRecommendations entirely and it now answers 404
// for every request, which is what made the mixes view come up empty.
//
// Video mixes are skipped — their items are videos, which this player cannot
// decode.
func (c *Client) GetMixes(ctx context.Context) ([]Mix, error) {
	params := url.Values{}
	params.Set("countryCode", c.Session.CountryCode)
	params.Set("deviceType", "BROWSER")
	params.Set("locale", "en_US")

	u := BaseURL + "/pages/my_collection_my_mixes?" + params.Encode()
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	// A 404 here means Tidal has no mix recommendations for this account
	// right now (not every account/session has one) — that's normal, empty
	// data, not a failure worth surfacing as an error toast.
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get mixes", resp.StatusCode, body)
	}

	var page pageResponse
	if err := decodeJSON(resp.Body, &page); err != nil {
		return nil, err
	}

	var mixes []Mix
	seen := make(map[string]struct{})
	for _, row := range page.Rows {
		for _, mod := range row.Modules {
			for _, item := range mod.PagedList.Items {
				if item.ID == "" || isVideoMixType(item.MixType) {
					continue
				}
				if _, dup := seen[item.ID]; dup {
					continue
				}
				seen[item.ID] = struct{}{}
				mixes = append(mixes, Mix{
					ID:          item.ID,
					Title:       item.Title,
					SubTitle:    item.SubTitle,
					Description: item.Description,
				})
			}
		}
	}
	return mixes, nil
}

// isVideoMixType reports whether a mix serves video items rather than tracks.
func isVideoMixType(mixType string) bool {
	return strings.Contains(mixType, "VIDEO")
}

// GetMixTracks returns the tracks of a mix in playlist order.
//
// The v1 mix items endpoint returns fully populated tracks (artist and album
// included) in a single request, so no per-track lookups are needed — the
// old v2 playlist-relationship endpoint returned bare track IDs only, which
// this used to resolve with one concurrent /v1/tracks/{id} request per track.
func (c *Client) GetMixTracks(ctx context.Context, mixID string) ([]Track, error) {
	if err := checkID(mixID); err != nil {
		return nil, err
	}
	params := url.Values{}
	params.Set("countryCode", c.Session.CountryCode)
	params.Set("deviceType", "BROWSER")
	params.Set("locale", "en_US")
	params.Set("limit", strconv.Itoa(mixTracksLimit))

	u := BaseURL + "/mixes/" + url.PathEscape(mixID) + "/items?" + params.Encode()
	resp, err := c.authGet(ctx, u)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, apiErr("get mix tracks", resp.StatusCode, body)
	}

	var res mixItemsResponse
	if err := decodeJSON(resp.Body, &res); err != nil {
		return nil, err
	}

	tracks := make([]Track, 0, len(res.Items))
	for i := range res.Items {
		// Video mixes return "video" items, which this player cannot decode.
		if res.Items[i].Type != "track" {
			continue
		}
		t := res.Items[i].Item
		t.normalizeArtist()
		tracks = append(tracks, t)
	}
	return tracks, nil
}
