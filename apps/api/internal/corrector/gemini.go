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
	ResponseMIMEType string `json:"responseMimeType,omitempty"`
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

// generate is the shared call path for both roles.
func (g *Gemini) generate(ctx context.Context, system, user string, temp float64) (string, int, int, error) {
	body, err := json.Marshal(geminiRequest{
		SystemInstruction: &geminiContent{Parts: []geminiPart{{Text: system}}},
		Contents:          []geminiContent{{Role: "user", Parts: []geminiPart{{Text: user}}}},
		GenerationConfig: &geminiGenCfg{
			Temperature:      temp,
			ResponseMIMEType: "application/json",
		},
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

	text, in, out, err := g.generate(ctx, g.correctorPrompt.Body, userMessage(req), 0.1)
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

	text, _, _, err := g.generate(ctx, g.verifierPrompt.Body, user, 0.0)
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
