// Command consumer drains the event log into ClickHouse (plan §9, Phase 3).
//
// A SEPARATE BINARY, deliberately. If the analytics writer wedges, leaks, or dies on a
// bad row, it must take nothing with it — the API keeps serving and the events keep
// piling up in the durable log until this comes back. That is the whole point of putting
// a log in the middle.
//
// It is also why the consumer is DURABLE: it resumes from where it stopped rather than
// from "now". A restart does not lose an hour of cost data, and a brand-new consumer can
// be pointed at the start of the stream to rebuild a table from scratch — which is what
// "event replay rebuilds the cache" (the Phase 3 exit check) actually means in practice.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/prooftamil/api/internal/events"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	natsURL := env("NATS_URL", "nats://localhost:4222")
	chURL := env("CLICKHOUSE_URL", "http://localhost:8123")
	chUser := env("CLICKHOUSE_USER", "prooftamil")
	chPass := env("CLICKHOUSE_PASSWORD", "localdev")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nc, err := nats.Connect(natsURL, nats.MaxReconnects(-1), nats.ReconnectWait(2*time.Second))
	if err != nil {
		return err
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}

	ch := &clickhouse{
		url:  chURL,
		user: chUser,
		pass: chPass,
		http: &http.Client{Timeout: 15 * time.Second},
		log:  log,
	}
	if err := ch.wait(ctx); err != nil {
		return err
	}

	// CREATE-OR-ATTACH, not attach.
	//
	// An earlier version called js.Stream(), which fails with "stream not found" if the
	// API has not booted yet — so the consumer crash-looped whenever it happened to start
	// first. A consumer must never depend on a producer having run: in a real deployment
	// they are separate services with no ordering guarantee between them, and the whole
	// point of a durable log is that it exists independently of both.
	//
	// The config is identical to the publisher's, and CreateOrUpdateStream is idempotent,
	// so whoever gets there first wins and the other attaches.
	stream, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:      events.StreamName,
		Subjects:  []string{"pt.>"},
		Storage:   jetstream.FileStorage,
		Retention: jetstream.LimitsPolicy,
		MaxAge:    30 * 24 * time.Hour,
	})
	if err != nil {
		return err
	}

	// A DURABLE consumer: NATS remembers our position. Restarting resumes where we left
	// off instead of silently skipping everything that happened while we were down.
	cons, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Durable:       "clickhouse-writer",
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxDeliver:    5,
		AckWait:       30 * time.Second,
	})
	if err != nil {
		return err
	}

	log.Info("consumer started", "nats", natsURL, "clickhouse", chURL)

	// Batch inserts. ClickHouse is built for large appends and is genuinely bad at
	// one-row-at-a-time writes — each becomes its own part, and the merge pressure
	// eventually stalls the table. Batching is not an optimisation here, it is the
	// supported way to use it.
	batch := newBatcher(ch, 500, 2*time.Second, log)
	defer batch.flush(context.Background())

	consume, err := cons.Consume(func(msg jetstream.Msg) {
		if err := handle(msg, batch); err != nil {
			log.Warn("bad event; discarding", "subject", msg.Subject(), "err", err)
			// Ack it anyway. A malformed row must not be redelivered forever — it will
			// never parse, and a poison message that blocks the stream would stop ALL
			// analytics, which is a far bigger problem than losing one row.
			_ = msg.Ack()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return err
	}
	defer consume.Stop()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("draining before shutdown")
			flushCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			batch.flush(flushCtx)
			cancel()
			return nil
		case <-ticker.C:
			batch.flushIfDue(ctx)
		}
	}
}

func handle(msg jetstream.Msg, b *batcher) error {
	switch msg.Subject() {
	case events.SubjectAIRequest:
		var e events.AIRequest
		if err := json.Unmarshal(msg.Data(), &e); err != nil {
			return err
		}
		b.addAIRequest(e)
	case events.SubjectActivity:
		var e events.Activity
		if err := json.Unmarshal(msg.Data(), &e); err != nil {
			return err
		}
		b.addActivity(e)
	default:
		return errors.New("unknown subject")
	}
	return nil
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

/* ---------------------------------------------------------------- clickhouse */

type clickhouse struct {
	url, user, pass string
	http            *http.Client
	log             *slog.Logger
}

// wait blocks until ClickHouse answers. The consumer starts alongside it in compose, and
// racing the database on boot would just crash-loop.
func (c *clickhouse) wait(ctx context.Context) error {
	for i := 0; i < 30; i++ {
		if err := c.exec(ctx, "SELECT 1"); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return errors.New("clickhouse did not become ready")
}

func (c *clickhouse) exec(ctx context.Context, query string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, strings.NewReader(query))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.user, c.pass)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 256)
		n, _ := resp.Body.Read(buf)
		return errors.New("clickhouse: " + string(buf[:n]))
	}
	return nil
}

// insertJSON appends rows using JSONEachRow — one JSON object per line. It keeps the
// consumer free of a ClickHouse driver and of column-order coupling: adding a nullable
// column to the schema does not require a code change here.
func (c *clickhouse) insertJSON(ctx context.Context, table string, rows []string) error {
	if len(rows) == 0 {
		return nil
	}
	body := "INSERT INTO " + table + " FORMAT JSONEachRow\n" + strings.Join(rows, "\n")
	return c.exec(ctx, body)
}
