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

// Sarvam is the primary corrector (Tier 3). It speaks the OpenAI-compatible
// chat-completions API.
type Sarvam struct {
	apiKey  string
	baseURL string
	model   string
	prompt  *Prompt
	http    *http.Client
}

func NewSarvam(apiKey, baseURL, model string, prompt *Prompt, client *http.Client) *Sarvam {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	if model == "" {
		model = prompt.Model
	}
	return &Sarvam{apiKey: apiKey, baseURL: baseURL, model: model, prompt: prompt, http: client}
}

func (s *Sarvam) Name() string { return "sarvam:" + s.model }

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
	// Ask for JSON at the API level as well as in the prompt. Belt and braces:
	// the prompt is advisory, this is enforced by the provider.
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (s *Sarvam) Correct(ctx context.Context, req Request) (*Response, error) {
	start := time.Now()

	body, err := json.Marshal(chatRequest{
		Model: s.model,
		Messages: []chatMessage{
			{Role: "system", Content: s.prompt.Body},
			{Role: "user", Content: userMessage(req)},
		},
		Temperature:    0.1,
		ResponseFormat: &responseFormat{Type: "json_object"},
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sarvam: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Cap the error body: a provider returning an HTML error page should not
		// end up in our logs in full.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("sarvam: HTTP %d: %s", resp.StatusCode, snippet)
	}

	var cr chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("sarvam: decode: %w", err)
	}
	if len(cr.Choices) == 0 {
		return nil, fmt.Errorf("sarvam: empty response")
	}

	jsonText, err := extractJSON(cr.Choices[0].Message.Content)
	if err != nil {
		return nil, fmt.Errorf("sarvam: %w", err)
	}

	var raw rawResponse
	if err := json.Unmarshal([]byte(jsonText), &raw); err != nil {
		return nil, fmt.Errorf("sarvam: malformed JSON: %w", err)
	}

	suggestions, err := validate(raw, req.Target, cascade.TierPrimary)
	if err != nil {
		return nil, err
	}

	return &Response{
		Suggestions:   suggestions,
		Model:         s.Name(),
		PromptVersion: s.prompt.Version,
		TokensIn:      cr.Usage.PromptTokens,
		TokensOut:     cr.Usage.CompletionTokens,
		LatencyMS:     time.Since(start).Milliseconds(),
	}, nil
}
