// Package writer implements the AI Content Writer (plan §16).
//
// Three modes: rewrite an existing passage, draft from a template, continue from the
// cursor. All Pro-gated, all streamed.
//
// TWO PROPERTIES MAKE THIS SAFE TO SHIP:
//
//  1. GENERATION IS THE PRICIEST CALL IN THE PRODUCT. Proofreading sends one sentence
//     and gets a few tokens back. Generation sends a document and streams back
//     hundreds. Without a cap, one user with a script can run up a bill that dwarfs
//     every other cost in the system — so there is a hard per-user daily quota, and it
//     is checked BEFORE the model is called, not after.
//
//  2. AI-WRITTEN TAMIL MUST STILL BE CORRECT TAMIL. Every generated passage is routed
//     back through the proofreading cascade before it reaches the user (§16.2). A
//     generic LLM writer produces plausible Tamil with sandhi and agreement errors in
//     it; running our own cascade over the output is the moat. If we shipped
//     ungrammatical AI text, the proofreader — the entire product — would be
//     implicitly calling itself a liar.
package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type Mode string

const (
	ModeRewrite  Mode = "rewrite"
	ModeTemplate Mode = "template"
	ModeContinue Mode = "continue"
)

// Errors the handler maps to HTTP status codes.
var (
	// ErrNotPro — the caller is on the free plan. A product decision, not a failure:
	// generation is the expensive feature, so it is what justifies the subscription.
	ErrNotPro = errors.New("this feature requires Pro")

	// ErrQuotaExceeded — the caller has burned their daily generation budget.
	ErrQuotaExceeded = errors.New("daily generation limit reached")
)

// Request is one generation.
type Request struct {
	Mode Mode

	// rewrite
	Text   string
	Axis   string // tone | clarity | formality | length
	Target string // e.g. "formal", "shorter"

	// template
	TemplateID string
	Fields     map[string]string

	// continue
	Context string
}

// Validate rejects a malformed request before it can reach a paid model.
func (r Request) Validate() error {
	switch r.Mode {
	case ModeRewrite:
		if strings.TrimSpace(r.Text) == "" {
			return errors.New("text is required")
		}
		if !validAxis[r.Axis] {
			return fmt.Errorf("axis must be one of tone, clarity, formality, length")
		}
		if strings.TrimSpace(r.Target) == "" {
			return errors.New("target is required")
		}
	case ModeTemplate:
		if _, ok := Templates[r.TemplateID]; !ok {
			return fmt.Errorf("unknown template %q", r.TemplateID)
		}
	case ModeContinue:
		if strings.TrimSpace(r.Context) == "" {
			return errors.New("context is required")
		}
	default:
		return fmt.Errorf("unknown mode %q", r.Mode)
	}
	return nil
}

var validAxis = map[string]bool{
	"tone": true, "clarity": true, "formality": true, "length": true,
}

// Template is a guided document type (§16.1 mode 2).
type Template struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`    // English
	NameTa string   `json:"name_ta"` // Tamil — this is a Tamil product
	Fields []string `json:"fields"`
}

// Templates is the seeded library (§16.6).
var Templates = map[string]Template{
	"letter": {
		ID: "letter", Name: "Letter", NameTa: "கடிதம்",
		Fields: []string{"recipient", "purpose", "tone"},
	},
	"application": {
		ID: "application", Name: "Application", NameTa: "விண்ணப்பம்",
		Fields: []string{"recipient", "subject", "reason"},
	},
	"essay": {
		ID: "essay", Name: "Essay", NameTa: "கட்டுரை",
		Fields: []string{"topic", "audience", "length"},
	},
	"social": {
		ID: "social", Name: "Social post", NameTa: "சமூக ஊடகப் பதிவு",
		Fields: []string{"topic", "tone", "platform"},
	},
	"notice": {
		ID: "notice", Name: "Formal notice", NameTa: "அறிவிப்பு",
		Fields: []string{"subject", "audience", "date"},
	},
}

func TemplateList() []Template {
	out := make([]Template, 0, len(Templates))
	// Deterministic order — a map iterates randomly, and a UI whose template list
	// reshuffles on every page load looks broken.
	for _, id := range []string{"letter", "application", "essay", "social", "notice"} {
		out = append(out, Templates[id])
	}
	return out
}

// Quota bounds per-user generation spend (§16.5). Backed by Redis in deployed
// environments; a nil implementation allows everything, which is correct for dev.
type Quota interface {
	// Take reserves one generation for the user. It returns ErrQuotaExceeded if the
	// daily budget is spent.
	Take(ctx context.Context, userID string) error
}

// ProChecker is the single source of truth for entitlement (§16.2).
//
// An interface, not a bool on a user struct, because Pro status is derived from the
// subscription table and MUST have exactly one implementation — if two places decide
// what "Pro" means, they will eventually disagree, and the disagreement will be a
// free user getting paid features or a paying user being denied them.
type ProChecker interface {
	IsUserPro(ctx context.Context, userID string) (bool, error)
}
