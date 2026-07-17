package handlers

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/speech"
)

// Transcribe is POST /api/v1/transcribe — voice typing (audio → Tamil text).
type Transcribe struct {
	asr *speech.Sarvam
	log *slog.Logger
}

func NewTranscribe(asr *speech.Sarvam, log *slog.Logger) *Transcribe {
	if log == nil {
		log = slog.Default()
	}
	return &Transcribe{asr: asr, log: log}
}

func (t *Transcribe) Handle(c *gin.Context) {
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "an audio file is required"})
		return
	}
	defer file.Close()

	if header.Size > speech.MaxAudioBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "recording too long (max 15 MB)"})
		return
	}

	// LimitReader, not a trust in header.Size — Content-Length is client-supplied and a
	// lying client could otherwise stream an unbounded body.
	data, err := io.ReadAll(io.LimitReader(file, speech.MaxAudioBytes+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not read the audio"})
		return
	}
	if len(data) > speech.MaxAudioBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "recording too long"})
		return
	}

	mime := header.Header.Get("Content-Type")

	// PRIVACY: `data` is a recording of the user's voice. It lives in this function and
	// nowhere else — never written to disk, never logged. Do not add a debug dump here.
	result, err := t.asr.Transcribe(c.Request.Context(), data, mime, header.Filename)
	if err != nil {
		t.log.WarnContext(c.Request.Context(), "transcription failed", "err", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "could not transcribe the audio"})
		return
	}

	t.log.InfoContext(c.Request.Context(), "transcribed",
		"chars", len([]rune(result.Text)), "took_ms", result.TookMS)

	c.JSON(http.StatusOK, gin.H{
		"text":    result.Text,
		"took_ms": result.TookMS,
	})
}
