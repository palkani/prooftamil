package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/prooftamil/api/internal/cache"
	"github.com/prooftamil/api/internal/cascade"
	"github.com/prooftamil/api/internal/config"
	"github.com/prooftamil/api/internal/corrector"
	"github.com/prooftamil/api/internal/events"
	"github.com/prooftamil/api/internal/handlers"
	"github.com/prooftamil/api/internal/ocr"
	"github.com/prooftamil/api/internal/router"
	"github.com/prooftamil/api/internal/writer"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	slog.SetDefault(log)

	// Signal-aware root context: SIGTERM (Cloud Run's shutdown signal) cancels
	// it, which unwinds in-flight work before the container is killed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	clients := &handlers.Clients{
		HTTP: &http.Client{Timeout: 5 * time.Second},
	}

	if cfg.DatabaseURL != "" {
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		clients.DB = pool
	}
	if cfg.DatabaseReadURL != "" {
		pool, err := pgxpool.New(ctx, cfg.DatabaseReadURL)
		if err != nil {
			return err
		}
		defer pool.Close()
		clients.ReadDB = pool
	}
	if cfg.RedisURL != "" {
		opt, err := redis.ParseURL(cfg.RedisURL)
		if err != nil {
			return err
		}
		rdb := redis.NewClient(opt)
		defer rdb.Close()
		clients.Redis = rdb
	}

	// The event backbone (§9, Phase 3). A nil publisher is supported and means "no
	// telemetry" — dev without NATS must still serve traffic.
	pub, err := events.New(ctx, cfg.NATSURL, log)
	if err != nil {
		log.Warn("event backbone unavailable; telemetry is disabled", "err", err)
		pub = nil
	}
	defer pub.Close()

	deps := handlers.BuildDependencies(cfg, clients)
	names := make([]string, 0, len(deps))
	for _, d := range deps {
		names = append(names, d.Name)
	}
	log.Info("dependencies registered", "deps", names)

	// The cascade (§9, Phases 1–2). cacheVersion salts every cache key: bump it
	// whenever the rules, lexicon, prompts, models or confidence gate change, or
	// the cache will keep serving corrections produced by the OLD engine for a
	// full CACHE_TTL_SECONDS (a week by default).
	const cacheVersion = "4" // bumped: corrector prompt v3 (quote_context) changes output

	models, err := buildModelTier(cfg, clients.HTTP, log)
	if err != nil {
		return err
	}

	orch := cascade.NewOrchestrator(
		cascade.NewMLClient(cfg.MLServiceURL, clients.HTTP),
		models,
		cache.NewExact(clients.Redis, cfg.CacheTTL, cacheVersion),
		cfg.ConfidenceGate,
		log,
	)
	if pub != nil {
		orch.SetTelemetry(events.TelemetryAdapter{P: pub}, cfg.ServiceRegion)
	}

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: router.New(cfg,
			handlers.NewHealth(cfg, deps),
			handlers.NewProofread(orch),
			handlers.NewSuggest(cfg.MLServiceURL, clients.HTTP),
			buildWriter(cfg, clients, orch, log),
			buildOCR(cfg, orch, log),
			handlers.NewCorrections(pub),
		),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: the SSE streaming routes (§7.2) are long-lived and a
		// write deadline would sever them mid-stream.
		IdleTimeout: 120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "port", cfg.Port, "env", cfg.AppEnv, "region", cfg.ServiceRegion)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
	}

	// Cloud Run allows 10s after SIGTERM before SIGKILL; drain inside that.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// buildModelTier assembles Tiers 3–4: Sarvam primary, Gemini hedged fallback,
// Gemini verifier (§8, Phase 2).
//
// Returns nil — a supported configuration — when no API keys are set. Dev has no
// keys until the §1 accounts exist, and the cascade must still run on Tiers 1–2
// rather than refusing to start. config.Load() already enforces that prod cannot
// boot without them, so a nil model tier in prod is impossible.
//
// The verifier is Gemini in its second role: the same client, a different prompt.
func buildModelTier(cfg *config.Config, httpc *http.Client, log *slog.Logger) (cascade.ModelTier, error) {
	if cfg.SarvamAPIKey == "" && cfg.GeminiAPIKey == "" {
		log.Warn("no model API keys configured; cascade runs Tier 1 + 2 only " +
			"(deterministic rules and cache). Grammar and real-word errors will NOT be caught.")
		return nil, nil
	}

	// v3: the model quotes the text AND supplies quote_context to disambiguate a
	// repeated quote (§8.4). v1 asked for offsets and the model echoed the ones
	// memorised from its own few-shot example; v2 fixed that but still resolved a
	// duplicate quote to the FIRST occurrence, silently correcting the wrong one.
	correctorPrompt, err := corrector.LoadPrompt("corrector", 3)
	if err != nil {
		return nil, err
	}
	verifierPrompt, err := corrector.LoadPrompt("verifier", 1)
	if err != nil {
		return nil, err
	}

	// 60s, not 20s: measured against the live Sarvam API, a single sentence takes
	// 7-18s because the model always emits an undisableable reasoning trace. A 20s
	// timeout would sever answers that were about to arrive. The hedge
	// (HEDGE_DELAY_MS) is the real latency control; this is only a backstop.
	modelHTTP := &http.Client{Timeout: 60 * time.Second}

	// PRIMARY = GEMINI, FALLBACK = SARVAM.
	//
	// This inverts the plan (§8 specified Sarvam primary, Gemini verifier). The
	// live numbers, measured over the eval sentences, left no room for debate:
	//
	//              p50      tail    correct   notes
	//   Gemini     0.9s     1.2s     5/6      0 false positives
	//   Sarvam     7.8s    17.8s     ~3/6     inconsistent between identical calls;
	//                                          1-in-6 returned no answer at all;
	//                                          once replied in English
	//
	// Sarvam is slower, less accurate and less reliable than the model it was meant
	// to lead. Its models are reasoning models whose (undisableable) chain-of-thought
	// eats the token budget — see the Sarvam client for the full autopsy.
	//
	// Sarvam is KEPT as the fallback rather than dropped: a second, independent
	// provider is what stops a Google outage taking the whole product down, and on
	// the hedged path it is only ever called when Gemini is already failing or slow.
	// Revisit if the startup credits lift the 4096-token cap.
	var primary, fallback corrector.Corrector
	var verifier corrector.Verifier

	if cfg.GeminiAPIKey != "" {
		g := corrector.NewGemini(
			cfg.GeminiAPIKey, cfg.GeminiBaseURL, cfg.GeminiModel,
			correctorPrompt, verifierPrompt, modelHTTP)
		primary = g
		verifier = g
	}
	if cfg.SarvamAPIKey != "" {
		sarvam := corrector.NewSarvam(
			cfg.SarvamAPIKey, cfg.SarvamBaseURL, cfg.SarvamModel, cfg.SarvamMaxTokens,
			correctorPrompt, modelHTTP)

		if primary == nil {
			// No Gemini key: Sarvam is all we have. Better than no model tier, but
			// the editor will feel slow and miss errors.
			primary = sarvam
			log.Warn("no Gemini key; falling back to Sarvam as PRIMARY. " +
				"Expect ~8s per sentence and inconsistent corrections.")
		} else {
			fallback = sarvam
		}
	}

	opts := []corrector.RouterOption{}
	if verifier != nil {
		// Suggestions below the confidence gate are the ones we would otherwise have
		// to throw away. Sending them to the verifier is what gives them a chance to
		// be shown — with a second opinion behind them.
		opts = append(opts, corrector.WithVerifier(verifier, cfg.ConfidenceGate))
	}

	// §8.5 — the degraded path is a supported configuration, not a failure.
	opts = append(opts, corrector.WithTier1Only(cfg.Tier1Only))
	if cfg.Tier1Only {
		log.Warn("TIER1_ONLY is set: no model will be called. " +
			"Deterministic corrections only — grammar and real-word errors will NOT be caught.")
	}

	log.Info("model tier configured",
		"primary", nameOf(primary), "fallback", nameOf(fallback),
		"fallback_trigger", cfg.FallbackTrigger, "model_timeout", cfg.ModelTimeout,
		"verify_below", cfg.ConfidenceGate, "tier1_only", cfg.Tier1Only)

	return corrector.NewRouter(primary, fallback, cfg.ModelTimeout, log, opts...), nil
}

// buildOCR assembles the server OCR path (§15.1).
//
// The vision model, not Tesseract. Tesseract is poor at cursive Tamil, which is the
// case the server path exists for at all — printed text is better served by the CLIENT
// path (tesseract.js), where the image never leaves the device and costs nothing.
func buildOCR(cfg *config.Config, orch *cascade.Orchestrator, log *slog.Logger) *handlers.OCR {
	if cfg.GeminiAPIKey == "" {
		log.Warn("no Gemini key; server-side OCR is disabled (the browser path still works)")
		return nil
	}
	reader := ocr.NewVisionOCR(cfg.GeminiAPIKey, cfg.GeminiBaseURL, cfg.GeminiModel, nil)
	log.Info("OCR enabled", "model", cfg.GeminiModel, "path", "server/vision")
	return handlers.NewOCR(reader, orch, log)
}

// buildWriter assembles the AI Content Writer (§16). Returns nil — and the routes are
// simply not registered — when there is no Gemini key, rather than exposing endpoints
// that would 500 on every call.
func buildWriter(cfg *config.Config, clients *handlers.Clients, orch *cascade.Orchestrator, log *slog.Logger) *handlers.Writer {
	if cfg.GeminiAPIKey == "" {
		log.Warn("no Gemini key; the AI Content Writer is disabled")
		return nil
	}

	prompts := map[writer.Mode]string{}
	for mode, file := range map[writer.Mode]string{
		writer.ModeRewrite:  "writer/rewrite",
		writer.ModeTemplate: "writer/template",
		writer.ModeContinue: "writer/continue",
	} {
		p, err := corrector.LoadPrompt(file, 1)
		if err != nil {
			log.Warn("writer prompt missing; feature disabled", "prompt", file, "err", err)
			return nil
		}
		prompts[mode] = p.Body
	}

	gen := writer.NewGeminiGenerator(
		cfg.GeminiAPIKey, cfg.GeminiBaseURL, cfg.GeminiModel, cfg.WriterMaxTokens, nil)

	// Entitlement: AlwaysPro until billing exists (Phase 6, blocked on §1). It is a
	// named type rather than a nil check so that when the real subscriptions table
	// lands, deleting it breaks the build at every call site — which is the reminder
	// we want.
	svc := writer.NewService(
		gen, prompts,
		writer.NewRedisQuota(clients.Redis, cfg.WriterDailyLimit),
		writer.AlwaysPro{Log: log},
		orch, log,
	)

	log.Info("AI Content Writer enabled",
		"model", cfg.GeminiModel, "daily_limit", cfg.WriterDailyLimit,
		"max_output_tokens", cfg.WriterMaxTokens, "entitlement", "ALWAYS-PRO (no billing yet)")

	return handlers.NewWriter(svc)
}

func nameOf(c corrector.Corrector) string {
	if c == nil {
		return "none"
	}
	return c.Name()
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	// JSON in the cloud so Cloud Logging parses fields; text locally for humans.
	if cfg.AppEnv == "dev" {
		return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}
