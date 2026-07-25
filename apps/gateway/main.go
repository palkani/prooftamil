// Command gateway is the single API host that fronts every client and routes each
// request to the right backend — the v2 Cloud Run api, or the existing v1 backend.
//
// WHY A PROXY AND NOT A LOAD BALANCER. A plain LB only routes; it cannot reshape a
// payload. The v1 UI expects v1-shaped correction JSON while the v2 api speaks a
// different shape, so migrating a v1 path to v2 needs TRANSLATION at the edge. This
// proxy does both: pure passthrough for most paths, and a translating handler for
// the v1 `/api/corrections` contract — behind a flag, with automatic fallback to v1.
//
// Routing is config-driven (V2_ROUTES), so moving an endpoint from v1 to v2 is an
// env-var change, not a redeploy — which is exactly the "adopt one endpoint at a
// time" rollout.
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"time"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ── contract shapes for the /api/corrections translation ─────────────────────

// v2's proofread suggestion (subset we map from).
type suggestion struct {
	Original    string `json:"original"`
	Suggestion  string `json:"suggestion"`
	Type        string `json:"type"`
	Explanation string `json:"explanation"`
}

type v2Response struct {
	Suggestions []suggestion `json:"suggestions"`
}

// v1's correction shape the existing client already renders.
type correction struct {
	BlockID      string `json:"blockId"`
	OriginalText string `json:"originalText"`
	Correction   string `json:"correction"`
	Reason       string `json:"reason"`
	Type         string `json:"type"`
}

type v1Response struct {
	Success     bool         `json:"success"`
	Corrections []correction `json:"corrections"`
}

// v1's client understands spelling|grammar|punctuation. v2's richer taxonomy
// (sandhi/agreement/style) collapses to grammar; spelling maps straight through.
func mapType(v2 string) string {
	if v2 == "spelling" {
		return "spelling"
	}
	return "grammar"
}

// newProxy builds a reverse proxy to a single upstream. FlushInterval -1 streams
// SSE responses (proofread/stream, write/*) through immediately instead of buffering.
func newProxy(target string) *httputil.ReverseProxy {
	u, err := url.Parse(target)
	if err != nil {
		log.Fatalf("gateway: bad upstream URL %q: %v", target, err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	director := p.Director
	p.Director = func(r *http.Request) {
		director(r)
		// Cloud Run routes on the Host header — it must be the upstream's host,
		// not the gateway's, or every request 404s at the target.
		r.Host = u.Host
	}
	p.FlushInterval = -1
	return p
}

func main() {
	port := env("PORT", "8080")
	v1URL := strings.TrimRight(env("V1_BACKEND_URL", ""), "/")
	v2URL := strings.TrimRight(env("V2_API_URL", ""), "/")
	v2Routes := splitCSV(env("V2_ROUTES", "/api/v1/proofread,/api/v1/suggest"))
	translateCorrections := env("TRANSLATE_CORRECTIONS", "false") == "true"

	if v1URL == "" && v2URL == "" {
		log.Fatal("gateway: set V1_BACKEND_URL and/or V2_API_URL")
	}

	var v1Proxy, v2Proxy *httputil.ReverseProxy
	if v1URL != "" {
		v1Proxy = newProxy(v1URL)
	}
	if v2URL != "" {
		v2Proxy = newProxy(v2URL)
	}

	// Separate client for the translation call — the reverse proxies handle their
	// own transport. 30s covers a Gemini-tier correction.
	client := &http.Client{Timeout: 30 * time.Second}

	mux := http.NewServeMux()

	mux.HandleFunc("/gateway/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"ok"}`)
	})

	toV2 := func(path string) bool {
		for _, p := range v2Routes {
			if strings.HasPrefix(path, p) {
				return true
			}
		}
		return false
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Translation route: serve the v1 `/api/corrections` contract from v2's
		// proofread. If it can't (bad input, v2 down/slow, non-text docJson), it
		// returns false and we fall through to the real v1 backend — so turning
		// this on can never make corrections worse than they are today.
		if translateCorrections && v2Proxy != nil &&
			r.Method == http.MethodPost && r.URL.Path == "/api/corrections" {
			if serveCorrectionsFromV2(w, r, v2URL, client) {
				return
			}
		}

		if v2Proxy != nil && toV2(r.URL.Path) {
			v2Proxy.ServeHTTP(w, r)
			return
		}
		if v1Proxy != nil {
			v1Proxy.ServeHTTP(w, r)
			return
		}
		http.Error(w, "no upstream configured for "+r.URL.Path, http.StatusBadGateway)
	})

	log.Printf("gateway listening on :%s  v1=%q v2=%q v2Routes=%v translateCorrections=%v",
		port, v1URL, v2URL, v2Routes, translateCorrections)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

// serveCorrectionsFromV2 answers a v1 `/api/corrections` request using v2's
// `/api/v1/proofread`, translating the response. Returns true if it produced a
// response; false means the caller should fall back to the v1 backend (the request
// body is left re-armed for that).
func serveCorrectionsFromV2(w http.ResponseWriter, r *http.Request, v2URL string, client *http.Client) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	r.Body.Close()
	if err != nil {
		return false
	}
	// Re-arm the body so a fallback to v1 can still read it.
	r.Body = io.NopCloser(bytes.NewReader(body))

	var in struct {
		Text    string          `json:"text"`
		DocJSON json.RawMessage `json:"docJson"`
	}
	_ = json.Unmarshal(body, &in)
	// Only the plain-text path is translated here; docJson (TipTap) flattening is a
	// v1-backend concern, so hand those to v1 untouched.
	if strings.TrimSpace(in.Text) == "" {
		return false
	}

	reqBody, _ := json.Marshal(map[string]string{"text": in.Text})
	resp, err := client.Post(v2URL+"/api/v1/proofread", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}

	var v2 v2Response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v2); err != nil {
		return false
	}

	out := v1Response{Success: true, Corrections: make([]correction, 0, len(v2.Suggestions))}
	for _, s := range v2.Suggestions {
		out.Corrections = append(out.Corrections, correction{
			OriginalText: s.Original,
			Correction:   s.Suggestion,
			Reason:       s.Explanation,
			Type:         mapType(s.Type),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
	return true
}
