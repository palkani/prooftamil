package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// maxQueryRunes bounds an IME query. Nobody types a 100-character word; anything
// longer is abuse or a bug, and it would make the prefix scan do real work.
const maxQueryRunes = 64

// Suggest is the Tamil IME endpoint (RFC-001): romanized input -> Tamil script.
//
//	GET /api/v1/suggest?q=vanakkam  ->  வணக்கம், வானகம், ...
//
// PRIVACY — read before changing this.
//
// `q` is a raw keystroke prefix from someone's private writing. It is the most
// sensitive data this product handles, and this endpoint fires on EVERY KEYSTROKE.
// Three properties keep it from being a keylogger, and all three are load-bearing:
//
//  1. It takes no user identity. Not a header, not a cookie, not a param.
//  2. `q` is never logged. A query in an access log, correlated with an IP, is a
//     transcript of what someone was writing.
//  3. Because the response depends only on `q`, it is identical for every user and
//     can be cached at the Cloudflare edge — so most requests never reach an origin
//     that could log them at all.
//
// Do NOT add a user_id to personalise ranking. Personal history belongs on the
// CLIENT, where the user's own picks never have to leave the device (RFC-001 §5).
type Suggest struct {
	ml  *MLSuggestClient
	ttl time.Duration
}

func NewSuggest(mlBaseURL string, client *http.Client) *Suggest {
	return &Suggest{
		ml: &MLSuggestClient{baseURL: mlBaseURL, http: client},
		// The Tamil language does not change minute to minute. A long TTL is what
		// turns this from an origin call per keystroke into an edge hit.
		ttl: 24 * time.Hour,
	}
}

func (s *Suggest) Handle(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"query": "", "suggestions": []any{}})
		return
	}
	if len([]rune(q)) > maxQueryRunes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query too long"})
		return
	}

	limit := 8
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			limit = n
		}
	}

	body, err := s.ml.Suggest(c.Request.Context(), q, limit)
	if err != nil {
		// The IME failing must not break typing. Return an empty list: the user keeps
		// their Latin text and can carry on, rather than seeing an error toast on
		// every keypress.
		c.JSON(http.StatusOK, gin.H{"query": q, "suggestions": []any{}})
		return
	}

	// Cacheable at the edge: the answer depends only on `q`, never on who asked.
	c.Header("Cache-Control", fmt.Sprintf("public, max-age=%d", int(s.ttl.Seconds())))
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// MLSuggestClient talks to the Python IME.
type MLSuggestClient struct {
	baseURL string
	http    *http.Client
}

func (m *MLSuggestClient) Suggest(ctx context.Context, q string, limit int) ([]byte, error) {
	u := fmt.Sprintf("%s/suggest?q=%s&limit=%d", m.baseURL, url.QueryEscape(q), limit)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ml suggest: HTTP %d", resp.StatusCode)
	}

	// Pass the ML service's JSON straight through rather than decode/re-encode it:
	// this is on the keystroke path, and the shapes are identical.
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}
