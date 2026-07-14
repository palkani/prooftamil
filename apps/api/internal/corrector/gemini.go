package corrector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/prooftamil/api/internal/cascade"
)

// Gemini serves two roles in the cascade:
//
//   - Corrector (Tier 3 fallback): used when Sarvam is slow (hedged after
//     HEDGE_DELAY_MS) or fails outright.
//   - Verifier (Tier 4): judges low-confidence suggestions from the primary.
//
// Both roles use the same client, different prompts.
type Gemini struct {
	apiKey          string
	baseURL         string
	model           string
	correctorPrompt *Prompt
	verifierPrompt  *Prompt
	http            *http.Client
}

func NewGemini(apiKey, baseURL, model string, correctorPrompt, verifierPrompt *Prompt, client *http.Client) *Gemini {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Gemini{
		apiKey:          apiKey,
		baseURL:         baseURL,
		model:           model,
		correctorPrompt: correctorPrompt,
		verifierPrompt:  verifierPrompt,
		http:            client,
	}
}

func (g *Gemini) Name() string { return "gemini:" + g.model }

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  *geminiGenCfg   `json:"generationConfig,omitempty"`
}

type geminiGenCfg struct {
	Temperature float64 `json:"temperature"`
	// Force JSON at the API level, not just by asking nicely in the prompt.
	//
	// Unlike Sarvam — where response_format:json_object silently emptied the
	// suggestion list — this is safe on Gemini and was verified against the live
	// API. The two providers are NOT interchangeable here.
	ResponseMIMEType string `json:"responseMimeType,omitempty"`

	// ThinkingConfig controls Gemini 2.5's reasoning. Nil = thinking ON (the
	// provider default).
	ThinkingConfig *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	// ThinkingBudget = 0 disables reasoning entirely.
	//
	// Measured on the live API over the eval sentences:
	//
	//   thinking ON  (default):  p50 3.1s, tail 10.0s, 6/6 correct
	//   thinking OFF (budget 0): p50 0.9s, tail 1.2s,  5/6 correct, 0 false positives
	//
	// The corrector runs on every unresolved sentence a user types, so 3s is the
	// difference between a live editor and a dead one. The single case fast-Gemini
	// misses (இந்த -> இந்தக்) is a sandhi error Tier 1 already catches
	// deterministically and for free — which is the entire argument for the
	// cascade. So the corrector buys an 8x speedup at no real coverage cost.
	//
	// The VERIFIER keeps thinking ON: it runs only on low-confidence suggestions
	// (rare), its whole job is careful judgement, and its verdict decides whether a
	// shaky correction is shown to the writer at all.
	ThinkingBudget int `json:"thinkingBudget"`
}

type geminiResponse struct {
	Candidates []struct {
		Content geminiContent `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

// generate is the shared call path for both roles. `think` selects Gemini 2.5's
// reasoning: off for the latency-critical corrector, on for the verifier.
func (g *Gemini) generate(ctx context.Context, system, user string, temp float64, think bool) (string, int, int, error) {
	cfg := &geminiGenCfg{
		Temperature:      temp,
		ResponseMIMEType: "application/json",
	}
	if !think {
		cfg.ThinkingConfig = &thinkingConfig{ThinkingBudget: 0}
	}

	body, err := json.Marshal(geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: system}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: user}}}},
		GenerationConfig:  cfg,
	})
	if err != nil {
		return "", 0, 0, err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", g.baseURL, g.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Header, not a query param: a key in the URL leaks into access logs and
	// proxy logs everywhere the request passes through.
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.http.Do(req)
	if err != nil {
		return "", 0, 0, fmt.Errorf("gemini: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return "", 0, 0, fmt.Errorf("gemini: HTTP %d: %s", resp.StatusCode, snippet)
	}

	var gr geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
		return "", 0, 0, fmt.Errorf("gemini: decode: %w", err)
	}
	if len(gr.Candidates) == 0 || len(gr.Candidates[0].Content.Parts) == 0 {
		// An empty candidate list usually means a safety block. Tamil prose being
		// filtered is not something we can recover from here; the caller falls back.
		return "", 0, 0, fmt.Errorf("gemini: no candidates (possible safety block)")
	}

	return gr.Candidates[0].Content.Parts[0].Text,
		gr.UsageMetadata.PromptTokenCount,
		gr.UsageMetadata.CandidatesTokenCount,
		nil
}

// Correct implements Corrector — the Tier 3 fallback role.
func (g *Gemini) Correct(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()

	// think=false: this runs on every unresolved sentence the user types, so the
	// 8x speedup (p50 3.1s -> 0.9s) is what makes the editor feel alive.
	text, in, out, err := g.generate(ctx, g.correctorPrompt.Body, userMessage(req), 0.1, false)
	if err != nil {
		return nil, err
	}

	jsonText, err := extractJSON(text)
	if err != nil {
		return nil, fmt.Errorf("gemini: %w", err)
	}

	var raw rawResponse
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		return nil, fmt.Errorf("gemini: malformed JSON: %w", err)
	}

	suggestions, err := validate(raw, req.Target, cascade.TierPrimary)
	if err != nil {
		return nil, err
	}

	return &Response{
		Suggestions:   suggestions,
		Model:         g.Name(),
		PromptVersion: g.correctorPrompt.Version,
		TokensIn:      in,
		TokensOut:     out,
		LatencyMS:     time.Since(start).Milliseconds(),
	}, nil
}

// Verify implements Verifier — the Tier 4 role (§8.2).
func (g *Gemini) Verify(ctx context.Context, sentence string, s cascade.Suggestion) (*Verdict, error) {
	user := fmt.Sprintf("Sentence: %s\nProposed: %s -> %s", sentence, s.Original, s.Suggestion)

	// think=true: the verifier runs only on low-confidence suggestions, so it is
	// rare and its extra latency is not on the typing path. Its verdict decides
	// whether a shaky correction is shown to the writer at all — exactly the place
	// to spend reasoning.
	text, _, _, err := g.generate(ctx, g.verifierPrompt.Body, user, 0.0, true)
	if err != nil {
		return nil, err
	}

	jsonText, err := extractJSON(text)
	if err != nil {
		return nil, fmt.Errorf("gemini verify: %w", err)
	}

	var v Verdict
	if err := json.Unmarshal([]byte(jsonText), &v); err != nil {
		return nil, fmt.Errorf("gemini verify: malformed JSON: %w", err)
	}
	return &v, nil
}
