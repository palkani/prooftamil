package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/cascade"
)

// maxTextRunes bounds a single request. Measured in runes: a byte limit would
// silently allow only a third as much Tamil as English, since Tamil is 3 bytes
// per character in UTF-8.
const maxTextRunes = 50_000

type Proofread struct {
	orch *cascade.Orchestrator
}

func NewProofread(orch *cascade.Orchestrator) *Proofread {
	return &Proofread{orch: orch}
}

type proofreadRequest struct {
	Text string `json:"text"`
}

// Sync is POST /api/v1/proofread (§7.2) — returns everything the fast tiers
// (0–2) can resolve, immediately.
func (p *Proofread) Sync(c *gin.Context) {
	var req proofreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}
	if n := len([]rune(req.Text)); n > maxTextRunes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("text is %d characters; the limit is %d", n, maxTextRunes),
		})
		return
	}

	res, err := p.orch.Proofread(c.Request.Context(), req.Text)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "proofreading failed"})
		return
	}
	c.JSON(http.StatusOK, res)
}

// Stream is GET /api/v1/proofread/stream (§7.2) — Server-Sent Events, so
// corrections appear sentence-by-sentence instead of after the whole document.
func (p *Proofread) Stream(c *gin.Context) {
	text := c.Query("text")
	if n := len([]rune(text)); n > maxTextRunes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("text is %d characters; the limit is %d", n, maxTextRunes),
		})
		return
	}

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	// Cloudflare and nginx buffer responses by default, which would hold every
	// event until the stream closed and defeat the entire point of streaming.
	// no-transform (above) stops Cloudflare rewriting the body; this stops nginx
	// buffering it.
	h.Set("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	emit := func(ev cascade.Event) error {
		payload, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		// SSE frame: `id:` lets the client resume, `data:` carries the JSON.
		if _, err := fmt.Fprintf(c.Writer, "id: %d\ndata: %s\n\n", ev.Seq, payload); err != nil {
			return err // client hung up
		}
		flusher.Flush()
		return nil
	}

	if err := p.orch.Stream(c.Request.Context(), text, emit); err != nil {
		// The client disconnecting is the normal way a stream ends (the user kept
		// typing and the editor cancelled this request), so it is not an error
		// worth reporting to a client that is no longer listening.
		return
	}
}
