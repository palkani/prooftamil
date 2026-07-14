package corrector

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prooftamil/api/internal/cascade"
)

// These exercise the HTTP clients against servers speaking the providers' real
// response shapes. The router tests use fakes and so never touch the wire format —
// which is precisely what would break on the first live API call, when there are
// no keys in this environment to catch it.

func testPrompt() *Prompt {
	return &Prompt{ID: "corrector", Version: 1, Model: "test-model", Body: "system prompt"}
}

func TestSarvamParsesAChatCompletion(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		// The OpenAI-compatible shape Sarvam returns, with the corrections nested
		// as a JSON string inside the assistant message.
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
          "choices": [{"message": {"content": "{\"suggestions\":[{\"original\":\"அந்த\",\"suggestion\":\"அந்தப்\",\"type\":\"sandhi\",\"explanation\":\"விதி\",\"confidence\":0.95}]}"}}],
          "usage": {"prompt_tokens": 120, "completion_tokens": 40}
        }`)
	}))
	defer srv.Close()

	s := NewSarvam("sk-test", srv.URL, "sarvam-30b", 4096, testPrompt(), srv.Client())

	resp, err := s.Correct(context.Background(), Request{Target: target, ContextBefore: "முன்"})
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	// max_tokens must ALWAYS be sent. Sarvam's models are reasoning models and the
	// chain-of-thought counts against the budget; at the provider default the
	// reasoning routinely eats it all and the answer never gets written.
	if gotBody.MaxTokens <= 0 {
		t.Error("max_tokens must be set explicitly — the reasoning trace consumes the budget")
	}
	// The context sentence must reach the model, clearly marked do-not-correct.
	if len(gotBody.Messages) != 2 || !strings.Contains(gotBody.Messages[1].Content, "do not correct") {
		t.Error("the user message must label the context as do-not-correct")
	}

	if len(resp.Suggestions) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(resp.Suggestions))
	}
	if resp.Suggestions[0].Suggestion != "அந்தப்" {
		t.Errorf("suggestion = %q", resp.Suggestions[0].Suggestion)
	}
	if resp.Suggestions[0].SourceTier != cascade.TierPrimary {
		t.Error("a Sarvam correction must be tagged Tier 3")
	}
	// Token counts feed the cost row in ai_requests (§6.2).
	if resp.TokensIn != 120 || resp.TokensOut != 40 {
		t.Errorf("tokens = (%d, %d), want (120, 40)", resp.TokensIn, resp.TokensOut)
	}
}

func TestSarvamSurfacesAnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":"rate limit exceeded"}`)
	}))
	defer srv.Close()

	s := NewSarvam("sk-test", srv.URL, "sarvam-30b", 4096, testPrompt(), srv.Client())

	_, err := s.Correct(context.Background(), Request{Target: target})
	if err == nil {
		t.Fatal("a 429 must be surfaced as an error so the router can fail over")
	}
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("the error should name the status: %v", err)
	}
}

func TestSarvamRejectsHallucinatedTextOverTheWire(t *testing.T) {
	// End to end: a model claiming a span that does not contain what it says must
	// not reach the writer's document, even when the HTTP call itself succeeds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
          "choices": [{"message": {"content": "{\"suggestions\":[{\"original\":\"NOT_IN_TEXT\",\"suggestion\":\"x\",\"type\":\"spelling\",\"confidence\":0.99}]}"}}],
          "usage": {}
        }`)
	}))
	defer srv.Close()

	s := NewSarvam("sk-test", srv.URL, "sarvam-30b", 4096, testPrompt(), srv.Client())

	resp, err := s.Correct(context.Background(), Request{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Suggestions) != 0 {
		t.Error("text absent from the target must be dropped even at 0.99 confidence")
	}
}

// REGRESSION — found only by calling the live API.
//
// Sarvam's models are reasoning models. The chain-of-thought counts against
// max_tokens, and on a long trace the model runs out of budget before writing the
// answer: finish_reason="length", "content": null.
//
// If that decoded to an empty string it would sail through as ZERO SUGGESTIONS,
// and the cascade would tell the writer their document is clean — a silent,
// confident lie that would then be cached for a week. It has to be an error so the
// router fails over to Gemini.
func TestSarvamTruncatedReasoningIsAnErrorNotACleanDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The exact shape observed from the live API.
		_, _ = io.WriteString(w, `{
          "choices": [{"finish_reason": "length", "message": {"content": null, "reasoning_content": "...long chain of thought..."}}],
          "usage": {"completion_tokens": 4096}
        }`)
	}))
	defer srv.Close()

	s := NewSarvam("sk-test", srv.URL, "sarvam-30b", 4096, testPrompt(), srv.Client())

	resp, err := s.Correct(context.Background(), Request{Target: target})
	if err == nil {
		t.Fatalf("a null content / finish_reason=length MUST be an error, "+
			"got a usable response with %d suggestions — this would report the "+
			"document as clean", len(resp.Suggestions))
	}
	if !strings.Contains(err.Error(), "4096") {
		t.Errorf("the error should name the token budget it blew: %v", err)
	}
}

// A reply that was cut off MID-ANSWER is also refused: the suggestion list is
// necessarily incomplete, and showing a partial set of corrections as if it were
// the whole set is its own kind of lie.
func TestSarvamTruncatedAnswerIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
          "choices": [{"finish_reason": "length", "message": {"content": "{\"suggestions\":[{\"original\":\"அந்த\",\"suggestion\":\"அந்தப்\",\"type\":\"sandhi\",\"confidence\":0.9}]}"}}],
          "usage": {"completion_tokens": 4096}
        }`)
	}))
	defer srv.Close()

	s := NewSarvam("sk-test", srv.URL, "sarvam-30b", 4096, testPrompt(), srv.Client())

	if _, err := s.Correct(context.Background(), Request{Target: target}); err == nil {
		t.Fatal("a truncated answer must be refused, not served as a complete set")
	}
}

func TestGeminiParsesGenerateContent(t *testing.T) {
	var gotKey, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-goog-api-key")
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{
          "candidates": [{"content": {"parts": [{"text": "{\"suggestions\":[{\"original\":\"அந்த\",\"suggestion\":\"அந்தப்\",\"type\":\"sandhi\",\"confidence\":0.91}]}"}]}}],
          "usageMetadata": {"promptTokenCount": 90, "candidatesTokenCount": 30}
        }`)
	}))
	defer srv.Close()

	g := NewGemini("gk-test", srv.URL, "gemini-2.5-flash", testPrompt(), testPrompt(), srv.Client())

	resp, err := g.Correct(context.Background(), Request{Target: target})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(gotPath, "/v1beta/models/gemini-2.5-flash:generateContent") {
		t.Errorf("path = %q", gotPath)
	}
	// The key must travel in a header. In the query string it would be copied into
	// every access log and proxy log the request passes through.
	if gotKey != "gk-test" {
		t.Errorf("API key must be sent as the x-goog-api-key header, got %q", gotKey)
	}
	if len(resp.Suggestions) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(resp.Suggestions))
	}
}

func TestGeminiVerifierParsesAVerdict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{
          "candidates": [{"content": {"parts": [{"text": "{\"approve\":false,\"confidence\":0.93,\"reason\":\"'புலி' சரியான சொல்.\"}"}]}}],
          "usageMetadata": {}
        }`)
	}))
	defer srv.Close()

	g := NewGemini("gk", srv.URL, "gemini-2.5-flash", testPrompt(), testPrompt(), srv.Client())

	v, err := g.Verify(context.Background(), "அது ஒரு புலி",
		cascade.Suggestion{Original: "புலி", Suggestion: "புளி"})
	if err != nil {
		t.Fatal(err)
	}
	if v.Approve {
		t.Error("the verifier rejected this; Approve must be false")
	}
	if v.Confidence != 0.93 {
		t.Errorf("confidence = %v", v.Confidence)
	}
}

func TestGeminiSafetyBlockIsAnError(t *testing.T) {
	// An empty candidate list means the reply was filtered. It must read as an
	// error so the router fails over, not as "no corrections found" — which would
	// silently tell the writer their text was clean.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"candidates": []}`)
	}))
	defer srv.Close()

	g := NewGemini("gk", srv.URL, "gemini-2.5-flash", testPrompt(), testPrompt(), srv.Client())

	if _, err := g.Correct(context.Background(), Request{Target: target}); err == nil {
		t.Fatal("a blocked/empty candidate list must be an error, not an empty result")
	}
}

// --- prompts ---------------------------------------------------------------

func TestLoadPromptReadsTheVersionedFile(t *testing.T) {
	// Reads the real packages/tamil-rules/prompts/corrector.v1.md, so a broken or
	// missing prompt file fails here rather than on the first paid API call.
	p, err := LoadPrompt("corrector", 1)
	if err != nil {
		t.Fatalf("the shipped corrector prompt must load: %v", err)
	}

	if p.Version != 1 {
		t.Errorf("version = %d, want 1", p.Version)
	}
	if p.Model == "" {
		t.Error("the prompt front matter must name a model — it is logged with every correction")
	}
	// Front matter must be stripped; the body is what the model sees.
	if strings.HasPrefix(p.Body, "---") {
		t.Error("front matter leaked into the prompt body")
	}
	if !strings.Contains(p.Body, "Tamil") {
		t.Error("the prompt body looks wrong")
	}

	if _, err := LoadPrompt("corrector", 99); err == nil {
		t.Error("a missing prompt version must error, not fall back silently")
	}
}
