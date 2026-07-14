package writer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiGenerator streams generated Tamil from Gemini (§16.2 — the writer reuses the
// same serving plane as the corrector).
type GeminiGenerator struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client

	// maxOutputTokens caps a single generation. This is the cost ceiling per call
	// (§16.5): without it, "continue writing" on a long document could stream back
	// thousands of tokens the user never asked for and would not read.
	maxOutputTokens int
}

func NewGeminiGenerator(apiKey, baseURL, model string, maxOutputTokens int, client *http.Client) *GeminiGenerator {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if maxOutputTokens <= 0 {
		maxOutputTokens = 1024
	}
	return &GeminiGenerator{
		apiKey: apiKey, baseURL: baseURL, model: model,
		maxOutputTokens: maxOutputTokens, http: client,
	}
}

type genReq struct {
	SystemInstruction *content  `json:"systemInstruction,omitempty"`
	Contents          []content `json:"contents"`
	GenerationConfig  *genCfg   `json:"generationConfig"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type genCfg struct {
	Temperature     float64   `json:"temperature"`
	MaxOutputTokens int       `json:"maxOutputTokens"`
	ThinkingConfig  *thinking `json:"thinkingConfig,omitempty"`
}

type thinking struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type streamChunk struct {
	Candidates []struct {
		Content content `json:"content"`
	} `json:"candidates"`
}

// Generate streams the model's output, calling emit for each fragment.
//
// STREAMING IS NOT COSMETIC HERE. A rewrite of a long paragraph takes several seconds;
// showing a spinner for that long makes the feature feel broken, while watching Tamil
// appear word by word makes the wait legible. It also means the user can hit Stop once
// they see it going the wrong way, instead of paying for the whole generation.
func (g *GeminiGenerator) Generate(
	ctx context.Context,
	system, user string,
	temperature float64,
	emit func(string) error,
) error {
	body, err := json.Marshal(genReq{
		SystemInstruction: &content{Parts: []part{{Text: system}}},
		Contents:          []content{{Role: "user", Parts: []part{{Text: user}}}},
		GenerationConfig: &genCfg{
			Temperature:     temperature,
			MaxOutputTokens: g.maxOutputTokens,
			// Thinking OFF: this is writing, not reasoning, and the latency of a
			// chain-of-thought would be paid on every generation.
			ThinkingConfig: &thinking{ThinkingBudget: 0},
		},
	})
	if err != nil {
		return err
	}

	// streamGenerateContent with alt=sse gives us Server-Sent Events rather than one
	// buffered blob.
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse", g.baseURL, g.model)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	resp, err := g.http.Do(req)
	if err != nil {
		return fmt.Errorf("writer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("writer: HTTP %d: %s", resp.StatusCode, snippet)
	}

	sc := bufio.NewScanner(resp.Body)
	// Model chunks can exceed bufio's default 64KB line limit.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // a malformed frame must not abort a good generation
		}
		for _, c := range chunk.Candidates {
			for _, p := range c.Content.Parts {
				if p.Text == "" {
					continue
				}
				if err := emit(p.Text); err != nil {
					return err // the client hung up
				}
			}
		}
	}
	return sc.Err()
}
