// Package events is the event backbone (plan §9, Phase 3).
//
// Every model call and every user action on a suggestion is published to a durable log
// (NATS JetStream). Consumers fan out from it: the ClickHouse writer (analytics), the
// semantic-cache builder, and the training-dataset builder.
//
// THE ONE RULE: PUBLISHING MUST NEVER SLOW OR BREAK A USER REQUEST.
//
// Telemetry is worth exactly nothing to the person typing. If the event bus is slow, the
// user must not wait for it. If it is down, the user must not see an error. So Publish()
// is non-blocking and lossy by design: it drops onto a buffered channel and returns
// immediately, and if that channel is full it DISCARDS the event and counts the drop.
//
// Dropping telemetry under load is the correct trade. The alternative — blocking the
// request until the analytics write succeeds — means an analytics outage becomes a
// product outage, which is precisely the failure this whole phase exists to prevent.
// (Before Phase 3 these rows went to the primary Postgres, so every user request
// competed with telemetry for the same connection pool. Under load, telemetry wins,
// because there is always more of it.)
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	// StreamName is the durable log. One stream, subjects underneath it, so a consumer
	// can replay everything or filter to what it cares about.
	StreamName = "PROOFTAMIL"

	SubjectAIRequest = "pt.ai_request"
	SubjectActivity  = "pt.activity"
)

// Event types on SubjectActivity.
const (
	CorrectionShown    = "correction_shown"
	CorrectionAccepted = "correction_accepted"
	CorrectionRejected = "correction_rejected"
)

// AIRequest is one model call — the cost ledger (§6.2).
type AIRequest struct {
	TS            time.Time `json:"ts"`
	UserID        string    `json:"user_id"`
	Region        string    `json:"region"`
	TierResolved  int       `json:"tier_resolved"`
	Model         string    `json:"model"`
	PromptVersion int       `json:"prompt_version"`
	Thinking      bool      `json:"thinking"`
	TokensIn      int       `json:"tokens_in"`
	TokensOut     int       `json:"tokens_out"`
	LatencyMS     int64     `json:"latency_ms"`
	CacheHit      bool      `json:"cache_hit"`
	CostMicros    uint64    `json:"cost_micros"`
	FellBack      bool      `json:"fell_back"`
	Error         string    `json:"error,omitempty"`
}

// Activity is one user action on a suggestion.
//
// The correction is denormalised into the event on purpose. Asking "which suggestions do
// users reject most?" must not require a join back to Postgres — that would defeat the
// entire reason these events left Postgres.
type Activity struct {
	TS             time.Time `json:"ts"`
	UserID         string    `json:"user_id"`
	EventType      string    `json:"event_type"`
	SuggestionType string    `json:"suggestion_type,omitempty"`
	SourceTier     int       `json:"source_tier,omitempty"`
	Confidence     float64   `json:"confidence,omitempty"`
	Original       string    `json:"original,omitempty"`
	Suggestion     string    `json:"suggestion,omitempty"`
	Props          string    `json:"props,omitempty"`
}

type message struct {
	subject string
	data    []byte
}

// Publisher is a non-blocking, fire-and-forget event publisher.
type Publisher struct {
	ch     chan message
	js     jetstream.JetStream
	nc     *nats.Conn
	log    *slog.Logger
	wg     sync.WaitGroup
	closed atomic.Bool

	// dropped counts events discarded because the buffer was full. It is a metric, not
	// an error: a rising drop count means the bus cannot keep up, which is worth an
	// alert, but never worth failing a user's request over.
	dropped atomic.Uint64
	sent    atomic.Uint64
}

// New connects to NATS and starts the background publisher.
//
// A nil Publisher is a valid, supported value — see Publish. That is what dev without a
// running NATS looks like, and the cascade must not care.
func New(ctx context.Context, url string, log *slog.Logger) (*Publisher, error) {
	if url == "" {
		log.Info("no NATS_URL; event publishing is disabled")
		return nil, nil
	}
	if log == nil {
		log = slog.Default()
	}

	nc, err := nats.Connect(url,
		nats.MaxReconnects(-1), // reconnect forever; a bus blip must not be permanent
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, err
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}

	// Create the stream if it does not exist. Idempotent, so every replica can call it.
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{"pt.>"},
		// File storage: the log must survive a restart, or "replay the stream to rebuild
		// the cache" is a lie.
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
	})
	if err != nil {
		nc.Close()
		return nil, err
	}

	p := &Publisher{
		// 4096 deep. Big enough to ride out a slow-consumer blip, small enough that a
		// genuinely broken bus is noticed (as drops) rather than eating unbounded memory.
		ch:  make(chan message, 4096),
		js:  js,
		nc:  nc,
		log: log,
	}

	p.wg.Add(1)
	go p.run()

	log.Info("event backbone connected", "url", url, "stream", StreamName)
	return p, nil
}

// run drains the buffer in the background. This is the ONLY place that talks to NATS, so
// a slow broker backs up here and nowhere near a request goroutine.
func (p *Publisher) run() {
	defer p.wg.Done()

	for msg := range p.ch {
		// A short timeout: if the broker cannot take an event in two seconds it is not
		// going to, and holding the drain goroutine hostage just backs up the buffer and
		// causes drops.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, err := p.js.Publish(ctx, msg.subject, msg.data)
		cancel()

		if err != nil {
			p.log.Warn("event publish failed", "subject", msg.subject, "err", err)
			continue
		}
		p.sent.Add(1)
	}
}

// AIRequest publishes a model-call record. Never blocks.
func (p *Publisher) AIRequest(e AIRequest) {
	p.publish(SubjectAIRequest, e)
}

// Activity publishes a user action. Never blocks.
func (p *Publisher) Activity(e Activity) {
	p.publish(SubjectActivity, e)
}

func (p *Publisher) publish(subject string, payload any) {
	// A nil publisher is a no-op. This is what lets every call site say
	// `events.Publish(...)` without a nil check, and what makes dev-without-NATS work.
	if p == nil || p.closed.Load() {
		return
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	select {
	case p.ch <- message{subject: subject, data: data}:
	default:
		// THE BUFFER IS FULL. Drop it.
		//
		// This is the load-bearing line of the package. Blocking here would push
		// back-pressure from the analytics pipeline all the way into the user's HTTP
		// request — a slow ClickHouse would become a slow editor. Telemetry is not worth
		// that. Count the loss and move on.
		p.dropped.Add(1)
	}
}

// Stats reports what got through and what did not.
func (p *Publisher) Stats() (sent, dropped uint64) {
	if p == nil {
		return 0, 0
	}
	return p.sent.Load(), p.dropped.Load()
}

// Close drains what is buffered, within a bound, and disconnects.
func (p *Publisher) Close() {
	if p == nil || !p.closed.CompareAndSwap(false, true) {
		return
	}
	close(p.ch)

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	// Wait for the drain, but not forever. Cloud Run gives ~10s after SIGTERM before
	// SIGKILL, and losing the last few analytics events is a far better outcome than
	// being killed mid-shutdown with connections still open.
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		p.log.Warn("event publisher did not drain in time; some events were lost")
	}

	sent, dropped := p.Stats()
	p.log.Info("event publisher closed", "sent", sent, "dropped", dropped)
	p.nc.Close()
}
