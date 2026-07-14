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

// DefaultSarvamModel is the current chat model.
//
// It is a default, not a constant, and SARVAM_MODEL overrides it. Model IDs get
// deprecated: `sarvam-m` — the ID this code originally shipped with — was already
// dead by the first live call, returning "Model 'sarvam-m' has been deprecated".
// A provider retiring a model must be a config change, never a code change.
const DefaultSarvamModel = "sarvam-30b"

// DefaultSarvamMaxTokens is the ceiling on the "starter" subscription tier; the
// API rejects anything higher. It has to cover the reasoning trace AND the answer.
const DefaultSarvamMaxTokens = 4096

// Sarvam is the primary corrector (Tier 3). It speaks the OpenAI-compatible
// chat-completions API.
//
// IMPORTANT — measured against the live API, not assumed:
// Sarvam's chat models are REASONING models. Every call emits a chain-of-thought
// into `reasoning_content` before the answer, the reasoning cannot be disabled
// (neither `reasoning_effort` nor `chat_template_kwargs.enable_thinking` has any
// effect), and it counts against max_tokens. Consequences the callers must know:
//
//   - Latency is 7–18s per sentence, not sub-second. HEDGE_DELAY_MS=800 would fire
//     the fallback on literally every request.
//   - Long reasoning can exhaust the budget and leave `content` null. That MUST be
//     an error, never an empty suggestion list — see Correct().
type Sarvam struct {
	apiKey    string
	baseURL   string
	model     string
	maxTokens int
	prompt    *Prompt
	http      *http.Client
}

func NewSarvam(apiKey, baseURL, model string, maxTokens int, prompt *Prompt, client *http.Client) *Sarvam {
	if client == nil {
		// Generous: the live p50 is ~8s and the tail reaches 18s. A 20s timeout
		// would cut off answers that were about to arrive.
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if model == "" {
		model = DefaultSarvamModel
	}
	if maxTokens <= 0 {
		maxTokens = DefaultSarvamMaxTokens
	}
	return &Sarvam{
		apiKey:    apiKey,
		baseURL:   baseURL,
		model:     model,
		maxTokens: maxTokens,
		prompt:    prompt,
		http:      client,
	}
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

	// MaxTokens must be set explicitly. Sarvam's models are REASONING models: they
	// always emit a chain-of-thought into `reasoning_content` first, and that
	// counts against the completion budget. At the provider default the reasoning
	// routinely exhausts the budget and the model never gets to write the answer —
	// `content` comes back null. Measured against the live API, a one-sentence
	// correction burns 1,300–4,000 completion tokens.
	MaxTokens int `json:"max_tokens"`

	// NOTE: response_format is deliberately NOT sent.
	//
	// Setting {"type":"json_object"} against sarvam-30b measurably DESTROYS the
	// output: the same sentence that yields a correct sandhi fix without it comes
	// back as {"suggestions": []} with it — the model silently finds nothing. A
	// corrector that reports every document as clean is the worst possible failure,
	// so we rely on the prompt plus extractJSON() instead. Re-enable only with an
	// eval run to prove it.
}

type chatResponse struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			// Content is a POINTER on purpose: Sarvam returns `"content": null`
			// (not "") when the reasoning trace ate the token budget. A plain string
			// would decode that to "", which is indistinguishable from a legitimate
			// empty reply — and would be read as "no corrections found".
			Content *string `json:"content"`
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
		Temperature: 0.1,
		MaxTokens:   s.maxTokens,
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

	choice := cr.Choices[0]

	// THE DANGEROUS CASE. The model reasoned until it ran out of budget and never
	// wrote an answer: finish_reason="length", content=null.
	//
	// This MUST be an error. If it were treated as an empty reply, validate() would
	// return zero suggestions and the cascade would tell the writer their document
	// is clean — a silent, confident lie, and it would then be CACHED for a week.
	// As an error, the router fails over to Gemini instead.
	if choice.Message.Content == nil || *choice.Message.Content == "" {
		return nil, fmt.Errorf(
			"sarvam: no answer (finish_reason=%q, %d completion tokens spent). "+
				"The reasoning trace exhausted max_tokens=%d",
			choice.FinishReason, cr.Usage.CompletionTokens, s.maxTokens)
	}
	if choice.FinishReason == "length" {
		// It wrote something, but was cut off mid-answer — so the JSON is very
		// likely truncated and any suggestion list is incomplete. Refuse it rather
		// than show the writer a partial set of corrections as if it were the whole.
		return nil, fmt.Errorf("sarvam: answer truncated at max_tokens=%d", s.maxTokens)
	}

	jsonText, err := extractJSON(*choice.Message.Content)
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
