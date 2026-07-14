package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/prooftamil/api/internal/events"
)

// batcher accumulates rows and flushes them to ClickHouse in bulk.
//
// ClickHouse is built for large appends and is genuinely bad at one-row-at-a-time
// inserts: each becomes its own part on disk, and the background merge eventually cannot
// keep up ("too many parts") and the table stops accepting writes. Batching is not a
// performance tweak here, it is the supported way to use the database.
//
// Flushed on whichever comes first:
//   - `size` rows buffered   — bounds memory and keeps parts a sensible size
//   - `interval` elapsed     — bounds how stale the dashboard is on a quiet system
type batcher struct {
	ch       *clickhouse
	size     int
	interval time.Duration
	log      *slog.Logger

	mu        sync.Mutex
	aiRows    []string
	actRows   []string
	lastFlush time.Time
}

func newBatcher(ch *clickhouse, size int, interval time.Duration, log *slog.Logger) *batcher {
	return &batcher{
		ch: ch, size: size, interval: interval, log: log,
		lastFlush: time.Now(),
	}
}

func (b *batcher) addAIRequest(e events.AIRequest) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	row, err := json.Marshal(chAIRequest{
		TS:            e.TS.UTC().Format("2006-01-02 15:04:05.000"),
		UserID:        e.UserID,
		Region:        e.Region,
		TierResolved:  e.TierResolved,
		Model:         e.Model,
		PromptVersion: e.PromptVersion,
		Thinking:      e.Thinking,
		TokensIn:      e.TokensIn,
		TokensOut:     e.TokensOut,
		LatencyMS:     e.LatencyMS,
		CacheHit:      e.CacheHit,
		CostMicros:    e.CostMicros,
		FellBack:      e.FellBack,
		Error:         e.Error,
	})
	if err != nil {
		return
	}

	b.mu.Lock()
	b.aiRows = append(b.aiRows, string(row))
	full := len(b.aiRows) >= b.size
	b.mu.Unlock()

	if full {
		b.flush(context.Background())
	}
}

func (b *batcher) addActivity(e events.Activity) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	row, err := json.Marshal(chActivity{
		TS:             e.TS.UTC().Format("2006-01-02 15:04:05.000"),
		UserID:         e.UserID,
		EventType:      e.EventType,
		SuggestionType: e.SuggestionType,
		SourceTier:     e.SourceTier,
		Confidence:     e.Confidence,
		Original:       e.Original,
		Suggestion:     e.Suggestion,
		Props:          e.Props,
	})
	if err != nil {
		return
	}

	b.mu.Lock()
	b.actRows = append(b.actRows, string(row))
	full := len(b.actRows) >= b.size
	b.mu.Unlock()

	if full {
		b.flush(context.Background())
	}
}

func (b *batcher) flushIfDue(ctx context.Context) {
	b.mu.Lock()
	due := time.Since(b.lastFlush) >= b.interval && (len(b.aiRows) > 0 || len(b.actRows) > 0)
	b.mu.Unlock()

	if due {
		b.flush(ctx)
	}
}

func (b *batcher) flush(ctx context.Context) {
	// Take the rows and release the lock BEFORE the network call. Holding it across the
	// insert would block every incoming event for the duration of a ClickHouse round
	// trip — the exact back-pressure this whole design exists to avoid.
	b.mu.Lock()
	ai, act := b.aiRows, b.actRows
	b.aiRows, b.actRows = nil, nil
	b.lastFlush = time.Now()
	b.mu.Unlock()

	if len(ai) == 0 && len(act) == 0 {
		return
	}

	if err := b.ch.insertJSON(ctx, "analytics.ai_requests", ai); err != nil {
		b.log.Warn("ai_requests insert failed", "rows", len(ai), "err", err)
	}
	if err := b.ch.insertJSON(ctx, "analytics.activity_events", act); err != nil {
		b.log.Warn("activity_events insert failed", "rows", len(act), "err", err)
	}

	if n := len(ai) + len(act); n > 0 {
		b.log.Info("flushed to clickhouse", "ai_requests", len(ai), "activity", len(act))
	}
}

// The ClickHouse wire shapes. Separate from the event structs so a change to the
// analytics schema does not ripple back into the API's event contract.
type chAIRequest struct {
	TS            string `json:"ts"`
	UserID        string `json:"user_id"`
	Region        string `json:"region"`
	TierResolved  int    `json:"tier_resolved"`
	Model         string `json:"model"`
	PromptVersion int    `json:"prompt_version"`
	Thinking      bool   `json:"thinking"`
	TokensIn      int    `json:"tokens_in"`
	TokensOut     int    `json:"tokens_out"`
	LatencyMS     int64  `json:"latency_ms"`
	CacheHit      bool   `json:"cache_hit"`
	CostMicros    uint64 `json:"cost_micros"`
	FellBack      bool   `json:"fell_back"`
	Error         string `json:"error"`
}

type chActivity struct {
	TS             string  `json:"ts"`
	UserID         string  `json:"user_id"`
	EventType      string  `json:"event_type"`
	SuggestionType string  `json:"suggestion_type"`
	SourceTier     int     `json:"source_tier"`
	Confidence     float64 `json:"confidence"`
	Original       string  `json:"original"`
	Suggestion     string  `json:"suggestion"`
	Props          string  `json:"props"`
}
