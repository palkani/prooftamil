package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/prooftamil/api/internal/config"
)

// Clients holds the long-lived connections the service shares. Any of them may
// be nil in dev, where the corresponding backing service is not configured;
// BuildDependencies only registers probes for the ones that exist.
type Clients struct {
	DB     *pgxpool.Pool // primary (writes)
	ReadDB *pgxpool.Pool // regional read replica
	Redis  *redis.Client
	HTTP   *http.Client
}

// BuildDependencies returns the readiness probe set for the configured
// backing services. A service that is not configured is not probed — that keeps
// `make dev` green before the §1 cloud accounts exist, while prod (where
// config.Load() requires the secrets) always has the full set.
func BuildDependencies(cfg *config.Config, c *Clients) []Dependency {
	var deps []Dependency

	if c.DB != nil {
		deps = append(deps, Dependency{
			Name:     "postgres_primary",
			Required: true,
			Check:    func(ctx context.Context) error { return c.DB.Ping(ctx) },
		})
	}
	if c.ReadDB != nil {
		deps = append(deps, Dependency{
			Name:     "postgres_replica",
			Required: false, // reads fall back to primary
			Check:    func(ctx context.Context) error { return c.ReadDB.Ping(ctx) },
		})
	}
	if c.Redis != nil {
		deps = append(deps, Dependency{
			Name:     "redis",
			Required: true, // exact cache + rate limiting both depend on it
			Check:    func(ctx context.Context) error { return c.Redis.Ping(ctx).Err() },
		})
	}
	if cfg.MLServiceURL != "" {
		deps = append(deps, Dependency{
			Name:     "ml_service",
			Required: true, // Tier 1 of the cascade
			Check:    httpProbe(c.HTTP, cfg.MLServiceURL+"/health"),
		})
	}
	if cfg.QdrantURL != "" {
		deps = append(deps, Dependency{
			Name:     "qdrant",
			Required: false, // semantic-cache miss degrades gracefully
			Check:    httpProbe(c.HTTP, cfg.QdrantURL+"/healthz"),
		})
	}

	return deps
}

func httpProbe(client *http.Client, url string) func(context.Context) error {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("unhealthy: HTTP %d", resp.StatusCode)
		}
		return nil
	}
}
