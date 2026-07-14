package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/events"
)

// Corrections is POST /api/v1/corrections/accept and /reject (§7.2).
//
// A REJECTION IS THE MOST VALUABLE EVENT IN THE PRODUCT. It is a human saying "you were
// wrong" about a specific correction, with the original text, the suggestion, the tier
// that produced it and its confidence all attached. That is exactly the labelled data the
// eval set (§11) is starved of, and it is far rarer than an acceptance — so it feeds the
// training-dataset builder directly.
//
// An acceptance is weaker evidence (people accept things they have not read), but the
// ratio between the two, sliced by tier and confidence, is the closest thing we have to a
// live measurement of whether the suggestions are any good.
type Corrections struct {
	pub *events.Publisher
}

func NewCorrections(pub *events.Publisher) *Corrections {
	return &Corrections{pub: pub}
}

type correctionFeedback struct {
	Original   string  `json:"original"`
	Suggestion string  `json:"suggestion"`
	Type       string  `json:"type"`
	SourceTier int     `json:"source_tier"`
	Confidence float64 `json:"confidence"`
}

func (c *Corrections) Accept(ctx *gin.Context) { c.record(ctx, events.CorrectionAccepted) }
func (c *Corrections) Reject(ctx *gin.Context) { c.record(ctx, events.CorrectionRejected) }

func (c *Corrections) record(ctx *gin.Context, eventType string) {
	var body correctionFeedback
	if err := ctx.ShouldBindJSON(&body); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON body"})
		return
	}

	// Until auth lands this is a stand-in. It is not trusted for anything — the event is
	// analytics, not authorisation.
	userID := ctx.GetHeader("X-User-Id")
	if userID == "" {
		userID = "anonymous"
	}

	// Fire-and-forget: a nil publisher is a no-op, and a full buffer drops the event.
	// Feedback is worth a lot to US and nothing to the user standing there — it must
	// never be able to fail their click.
	c.pub.Activity(events.Activity{
		TS:             time.Now(),
		UserID:         userID,
		EventType:      eventType,
		SuggestionType: body.Type,
		SourceTier:     body.SourceTier,
		Confidence:     body.Confidence,
		Original:       body.Original,
		Suggestion:     body.Suggestion,
	})

	// 202: we have taken it, we have not necessarily written it anywhere yet. Saying 200
	// would imply a durability we deliberately do not offer here.
	ctx.JSON(http.StatusAccepted, gin.H{"ok": true})
}
