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

	"github.com/prooftamil/api/internal/config"
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

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router.New(cfg, handlers.NewHealth(cfg, deps)),
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
