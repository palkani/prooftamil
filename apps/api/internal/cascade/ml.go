package cascade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// MLClient talks to the Python Tier 1 service.
type MLClient struct {
	baseURL string
	http    *http.Client
}

func NewMLClient(baseURL string, client *http.Client) *MLClient {
	if client == nil {
		// Tier 1 is deterministic and sub-millisecond. If it has not answered in
		// a second something is badly wrong, and waiting longer only delays the
		// model tiers that would have covered for it anyway.
		client = &http.Client{Timeout: time.Second}
	}
	return &MLClient{baseURL: baseURL, http: client}
}

type analyzeRequest struct {
	Target        string `json:"target"`
	ContextBefore string `json:"context_before,omitempty"`
	ContextAfter  string `json:"context_after,omitempty"`
}

type analyzeResponse struct {
	Suggestions []Suggestion `json:"suggestions"`
	Resolved    bool         `json:"resolved"`
	TookMS      int64        `json:"took_ms"`
}

// Analyze runs Tier 1 over a single sentence. The returned offsets are runes
// relative to `target`, matching the ML service's codepoint offsets exactly.
func (m *MLClient) Analyze(ctx context.Context, target, before, after string) (*analyzeResponse, error) {
	body, err := json.Marshal(analyzeRequest{
		Target:        target,
		ContextBefore: before,
		ContextAfter:  after,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tier1: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tier1: HTTP %d", resp.StatusCode)
	}

	var out analyzeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("tier1: decode: %w", err)
	}
	return &out, nil
}
