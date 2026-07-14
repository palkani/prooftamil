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
	"github.com/prooftamil/api/internal/handlers"
	"github.com/prooftamil/api/internal/router"
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
	const cacheVersion = "2" // bumped: Phase 2 adds model-tier output to cached results

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

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router.New(cfg, handlers.NewHealth(cfg, deps), handlers.NewProofread(orch)),
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

	correctorPrompt, err := corrector.LoadPrompt("corrector", 1)
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

	var primary, fallback corrector.Corrector
	var verifier corrector.Verifier

	if cfg.SarvamAPIKey != "" {
		primary = corrector.NewSarvam(
			cfg.SarvamAPIKey, cfg.SarvamBaseURL, cfg.SarvamModel, cfg.SarvamMaxTokens,
			correctorPrompt, modelHTTP)
	}
	if cfg.GeminiAPIKey != "" {
		g := corrector.NewGemini(
			cfg.GeminiAPIKey, cfg.GeminiBaseURL, cfg.GeminiModel,
			correctorPrompt, verifierPrompt, modelHTTP)
		fallback = g
		verifier = g
	}

	opts := []corrector.RouterOption{}
	if verifier != nil {
		// Suggestions below the confidence gate are the ones we would otherwise have
		// to throw away. Sending them to the verifier is what gives them a chance to
		// be shown — with a second opinion behind them.
		opts = append(opts, corrector.WithVerifier(verifier, cfg.ConfidenceGate))
	}

	log.Info("model tier configured",
		"primary", nameOf(primary), "fallback", nameOf(fallback),
		"hedge_delay", cfg.HedgeDelay, "verify_below", cfg.ConfidenceGate)

	return corrector.NewRouter(primary, fallback, cfg.HedgeDelay, log, opts...), nil
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
