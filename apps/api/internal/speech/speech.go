// Package speech implements Tamil speech-to-text (voice typing).
//
// WHY A SERVER MODEL AND NOT THE BROWSER.
//
// The browser has the Web Speech API, and it is tempting because it is free. For a Tamil
// product it is the wrong tool:
//   - Chrome and Safari only. Firefox has no support at all, so a Firefox user gets no
//     voice typing whatsoever.
//   - It ships the audio to Google's servers regardless — "in the browser" is a fiction.
//   - Its Tamil accuracy is mediocre, because Tamil is a rounding error in Google's
//     general-purpose recogniser.
//
// Sarvam's Saarika model is built FOR Indian languages. Measured against a real Tamil
// audio sample it transcribed வணக்கம், நான் தமிழில் பேசுகிறேன் exactly. It works from any
// browser (the client just records audio and uploads it) and does not depend on the
// user's browser having a Tamil language pack.
//
// The client keeps Web Speech as an optional INSTANT preview where it exists; this is the
// accurate path and the universal fallback.
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

// MaxAudioBytes bounds an upload. A minute of speech is well under this; anything larger
// is a mistake or abuse, and it must not be able to exhaust memory.
const MaxAudioBytes = 15 << 20 // 15 MB

// DefaultModel is Saarika. A default, not a constant, and SARVAM_ASR_MODEL overrides it —
// because model IDs get deprecated: saarika:v2 and saarika:flash were BOTH already dead by
// the first live call, exactly like the corrector's sarvam-m. A provider retiring a model
// must be a config change, never a code change.
const DefaultModel = "saarika:v2.5"

// Result is a transcription.
type Result struct {
	Text      string `json:"text"`
	Model     string `json:"model"`
	TookMS    int64  `json:"took_ms"`
	RequestID string `json:"request_id,omitempty"`
}

// Sarvam transcribes audio to Tamil via the Saarika ASR API.
type Sarvam struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewSarvam(apiKey, baseURL, model string, client *http.Client) *Sarvam {
	if client == nil {
		// A minute of audio takes real time to upload and transcribe.
		client = &http.Client{Timeout: 60 * time.Second}
	}
	if model == "" {
		model = DefaultModel
	}
	if baseURL == "" {
		baseURL = "https://api.sarvam.ai"
	}
	return &Sarvam{apiKey: apiKey, baseURL: baseURL, model: model, http: client}
}

type asrResponse struct {
	Transcript   string `json:"transcript"`
	RequestID    string `json:"request_id"`
	LanguageCode string `json:"language_code"`
	// Deprecation and error messages arrive under `detail`.
	Detail string `json:"detail"`
}

// Transcribe sends audio to Saarika and returns the Tamil text.
//
// PRIVACY: `audio` is a recording of the user's voice — among the most sensitive things
// this product handles. It lives in this call and is never persisted: not to disk, not to
// R2, not to a log. (Sarvam processes it transiently to transcribe; that is disclosed in
// the privacy policy alongside OCR.)
func (s *Sarvam) Transcribe(ctx context.Context, audio []byte, mimeType, filename string) (*Result, error) {
	if len(audio) == 0 {
		return nil, fmt.Errorf("empty audio")
	}
	if len(audio) > MaxAudioBytes {
		return nil, fmt.Errorf("audio is %d bytes; the limit is %d", len(audio), MaxAudioBytes)
	}

	start := time.Now()

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	// The audio part. Saarika accepts wav/webm/mp3; the browser records webm/opus, which
	// it handles.
	part, err := w.CreateFormFile("file", orDefault(filename, "audio.webm"))
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(audio); err != nil {
		return nil, err
	}

	_ = w.WriteField("model", s.model)
	// ta-IN, not auto-detect. This is a Tamil product; a user speaking Tamil into it should
	// never have their words guessed as Hindi because a sentence was ambiguous.
	_ = w.WriteField("language_code", "ta-IN")

	if err := w.Close(); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/speech-to-text", &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("api-subscription-key", s.apiKey)

	resp, err := s.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("asr: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))

	var ar asrResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("asr: HTTP %d: %s", resp.StatusCode, truncate(raw, 200))
	}

	if resp.StatusCode != http.StatusOK {
		// Surface the provider's own message — it is how "model deprecated" reached us,
		// and it is far more useful than a bare status code.
		if ar.Detail != "" {
			return nil, fmt.Errorf("asr: HTTP %d: %s", resp.StatusCode, ar.Detail)
		}
		return nil, fmt.Errorf("asr: HTTP %d", resp.StatusCode)
	}

	return &Result{
		Text:      ar.Transcript,
		Model:     s.model,
		TookMS:    time.Since(start).Milliseconds(),
		RequestID: ar.RequestID,
	}, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n])
	}
	return string(b)
}
