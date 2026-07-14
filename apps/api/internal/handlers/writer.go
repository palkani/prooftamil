package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/writer"
)

// Writer is the AI Content Writer surface (§16.3). Every mode streams.
type Writer struct {
	svc *writer.Service
}

func NewWriter(svc *writer.Service) *Writer {
	return &Writer{svc: svc}
}

// Templates is GET /api/v1/write/templates.
func (w *Writer) Templates(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"templates": writer.TemplateList()})
}

type writeRequest struct {
	Text       string            `json:"text"`
	Axis       string            `json:"axis"`
	Target     string            `json:"target"`
	TemplateID string            `json:"template_id"`
	Fields     map[string]string `json:"fields"`
	Context    string            `json:"context"`
}

func (w *Writer) Rewrite(c *gin.Context)  { w.stream(c, writer.ModeRewrite) }
func (w *Writer) Template(c *gin.Context) { w.stream(c, writer.ModeTemplate) }
func (w *Writer) Continue(c *gin.Context) { w.stream(c, writer.ModeContinue) }

func (w *Writer) stream(c *gin.Context, mode writer.Mode) {
	var body writeRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	// Until auth lands (Phase 6) there is no authenticated caller. The user id is
	// taken from a header so the quota logic is exercised end to end and swapping in
	// a real JWT subject is a one-line change — but it is trivially spoofable, so it
	// must NOT be used for entitlement in production.
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		userID = "anonymous"
	}

	req := writer.Request{
		Mode:       mode,
		Text:       body.Text,
		Axis:       body.Axis,
		Target:     body.Target,
		TemplateID: body.TemplateID,
		Fields:     body.Fields,
		Context:    body.Context,
	}

	// The gates are checked inside the service, but their errors must be mapped to
	// the right status BEFORE we commit to an SSE response — once the stream is open
	// the status code is already sent and a 402 can no longer be expressed.
	if err := req.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // nginx buffers by default, which defeats streaming

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming unsupported"})
		return
	}

	seq := 0
	emit := func(ev writer.Event) error {
		payload, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		seq++
		if _, err := fmt.Fprintf(c.Writer, "id: %d\ndata: %s\n\n", seq, payload); err != nil {
			return err // client hung up
		}
		flusher.Flush()
		return nil
	}

	if err := w.svc.Generate(c.Request.Context(), userID, req, emit); err != nil {
		// The stream is already open, so the failure has to be delivered IN it — the
		// client is reading events, not checking a status code any more.
		msg := err.Error()
		switch {
		case errors.Is(err, writer.ErrNotPro):
			msg = "pro_required"
		case errors.Is(err, writer.ErrQuotaExceeded):
			msg = "quota_exceeded"
		}
		_ = emit(writer.Event{Type: "error", Error: msg})
	}
}
