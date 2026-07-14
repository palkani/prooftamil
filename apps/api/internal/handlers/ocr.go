package handlers

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/cascade"
	"github.com/prooftamil/api/internal/ocr"
)

// OCR is POST /api/v1/ocr (§15.3) — image in, proofread Tamil out.
type OCR struct {
	reader  *ocr.VisionOCR
	cascade *cascade.Orchestrator
	log     *slog.Logger
}

func NewOCR(reader *ocr.VisionOCR, orch *cascade.Orchestrator, log *slog.Logger) *OCR {
	if log == nil {
		log = slog.Default()
	}
	return &OCR{reader: reader, cascade: orch, log: log}
}

func (o *OCR) Handle(c *gin.Context) {
	file, header, err := c.Request.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an image file is required"})
		return
	}
	defer file.Close()

	if header.Size > ocr.MaxImageBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": "image is too large (max 10 MB)",
		})
		return
	}

	// LimitReader, not a trust in header.Size — Content-Length is client-supplied and
	// a lying client could otherwise stream us an unbounded body.
	data, err := io.ReadAll(io.LimitReader(file, ocr.MaxImageBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the image"})
		return
	}
	if len(data) > ocr.MaxImageBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "image is too large"})
		return
	}

	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/jpeg"
	}

	// PRIVACY (§15.3): `data` is a photograph of someone's private writing. It lives in
	// this function and nowhere else — never written to disk, never uploaded to R2,
	// never logged. Do not add a debug dump here.
	result, err := o.reader.Read(c.Request.Context(), data, mime)
	if err != nil {
		o.log.WarnContext(c.Request.Context(), "ocr failed", "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not read the image"})
		return
	}

	if result.Text == "" {
		c.JSON(http.StatusOK, gin.H{
			"text":        "",
			"confidence":  result.Confidence,
			"suggestions": []any{},
			"warning":     "No Tamil text found in this image.",
		})
		return
	}

	// --- §15.2: the synergy ---------------------------------------------
	//
	// Run the transcription straight through the proofreading cascade. OCR's
	// characteristic mistakes ARE the mistakes we already fix — a misread ழ/ள/ல, a
	// broken sandhi — so Tier 1 cleans them up deterministically and for free. This is
	// why the OCR feature is worth more inside this product than as a standalone tool.
	//
	// The OCR prompt is deliberately told NOT to self-correct: a transcriber that
	// "improves" the page lies about what the page says. Correcting is this layer's
	// job, and here the user can see and accept each fix.
	var suggestions []cascade.Suggestion
	if o.cascade != nil {
		res, err := o.cascade.Proofread(c.Request.Context(), result.Text)
		if err != nil {
			o.log.WarnContext(c.Request.Context(), "could not proofread OCR output", "err", err)
		} else {
			suggestions = res.Suggestions
		}
	}

	o.log.InfoContext(c.Request.Context(), "ocr complete",
		"chars", len([]rune(result.Text)), "confidence", result.Confidence,
		"suggestions", len(suggestions), "took_ms", result.TookMS)

	if suggestions == nil {
		suggestions = []cascade.Suggestion{}
	}

	c.JSON(http.StatusOK, gin.H{
		"text":        result.Text,
		"confidence":  result.Confidence,
		"suggestions": suggestions,
		"took_ms":     result.TookMS,
	})
}
