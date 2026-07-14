// Package ocr implements image → Tamil text (plan §15).
//
// ROUTING (§15.1). Two paths, and the choice is about privacy and cost as much as
// accuracy:
//
//	client (tesseract.js, in the browser) — printed text, small images, and anything
//	  the user would rather not upload. The image never leaves the device and it costs
//	  us nothing. It is the DEFAULT for print.
//
//	server (this package, a vision model) — handwriting, low-quality photos, and
//	  anything the client path could not read. Tesseract is poor at cursive Tamil; a
//	  vision model is dramatically better, which is the whole reason the server path
//	  exists.
//
// THE SYNERGY THAT MAKES THIS WORTH BUILDING (§15.2): OCR output goes straight through
// the proofreading cascade. OCR's characteristic errors ARE the errors we already fix —
// a misread ழ/ள/ல, a broken sandhi — so the deterministic Tier 1 layer cleans them up
// for free. OCR and proofreading reinforce each other; neither product would be as good
// alone.
//
// PRIVACY (§15.3): the image is processed transiently and never persisted. Not to R2,
// not to disk, not to a log. Someone photographing a handwritten letter is handing us
// the most private thing they own.
package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxImageBytes bounds an upload. Large enough for a phone photo of a page, small
// enough that it cannot be used to exhaust memory.
const MaxImageBytes = 10 << 20 // 10 MB

// Result is a transcription.
type Result struct {
	Text string `json:"text"`

	// Confidence is the model's own estimate. Surfaced so the UI can flag a low-
	// confidence page for review rather than presenting a guess as a transcript
	// (§15.2).
	Confidence float64 `json:"confidence"`

	Model  string `json:"model"`
	TookMS int64  `json:"took_ms"`
}

// VisionOCR reads Tamil text out of an image using a vision model.
type VisionOCR struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewVisionOCR(apiKey, baseURL, model string, client *http.Client) *VisionOCR {
	if client == nil {
		// Generous: a full page of handwriting takes real time to read.
		client = &http.Client{Timeout: 90 * time.Second}
	}
	return &VisionOCR{apiKey: apiKey, baseURL: baseURL, model: model, http: client}
}

const systemPrompt = `You are a Tamil (தமிழ்) OCR engine. Transcribe the text in this image EXACTLY as written.

Rules:
- Transcribe ONLY what is actually there. Do not correct spelling, do not fix grammar,
  do not complete half-written words, do not add punctuation that is not present.
  A transcription that "improves" the text is a transcription that lies about what the
  page says — and a downstream proofreader will handle the errors properly.
- Preserve line breaks and paragraph structure.
- If a character is genuinely illegible, write [?] rather than guessing at it.
- If the image contains no Tamil text at all, return an empty string for "text".
- Do not describe the image. Do not comment. Do not translate.

Return STRICT JSON only:
{"text": "<the transcription>", "confidence": <0.0-1.0 — how legible the page was>}`

type visionReq struct {
	SystemInstruction *content  `json:"systemInstruction,omitempty"`
	Contents          []content `json:"contents"`
	GenerationConfig  *genCfg   `json:"generationConfig"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inlineData,omitempty"`
}

type inlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

type genCfg struct {
	Temperature      float64   `json:"temperature"`
	ResponseMIMEType string    `json:"responseMimeType"`
	ThinkingConfig   *thinking `json:"thinkingConfig,omitempty"`
}

type thinking struct {
	ThinkingBudget int `json:"thinkingBudget"`
}

type visionResp struct {
	Candidates []struct {
		Content content `json:"content"`
	} `json:"candidates"`
}

type transcript struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// Read transcribes an image.
func (v *VisionOCR) Read(ctx context.Context, image []byte, mimeType string) (*Result, error) {
	if len(image) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	if len(image) > MaxImageBytes {
		return nil, fmt.Errorf("image is %d bytes; the limit is %d", len(image), MaxImageBytes)
	}

	start := time.Now()

	body, err := json.Marshal(visionReq{
		SystemInstruction: &content{Parts: []part{{Text: systemPrompt}}},
		Contents: []content{{
			Role: "user",
			Parts: []part{
				{InlineData: &inlineData{
					MimeType: mimeType,
					Data:     base64.StdEncoding.EncodeToString(image),
				}},
				{Text: "Transcribe the Tamil text in this image."},
			},
		}},
		GenerationConfig: &genCfg{
			// Temperature 0: transcription is not a creative act. Any randomness here
			// is the model inventing characters that are not on the page.
			Temperature:      0,
			ResponseMIMEType: "application/json",
			// Thinking off — reading a page is perception, not reasoning, and the
			// latency would be paid on every upload.
			ThinkingConfig: &thinking{ThinkingBudget: 0},
		},
	})
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", v.baseURL, v.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", v.apiKey)

	resp, err := v.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ocr: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("ocr: HTTP %d: %s", resp.StatusCode, snippet)
	}

	var vr visionResp
	if err := json.NewDecoder(resp.Body).Decode(&vr); err != nil {
		return nil, fmt.Errorf("ocr: decode: %w", err)
	}
	if len(vr.Candidates) == 0 || len(vr.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("ocr: no candidates (possible safety block)")
	}

	raw := vr.Candidates[0].Content.Parts[0].Text
	if i, j := strings.Index(raw, "{"), strings.LastIndex(raw, "}"); i >= 0 && j > i {
		raw = raw[i : j+1]
	}

	var t transcript
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, fmt.Errorf("ocr: malformed JSON: %w", err)
	}

	return &Result{
		Text:       strings.TrimSpace(t.Text),
		Confidence: t.Confidence,
		Model:      v.model,
		TookMS:     time.Since(start).Milliseconds(),
	}, nil
}
