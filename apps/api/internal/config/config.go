// Package config loads all runtime configuration for the serving plane.
//
// Every field maps to a variable in the env inventory (plan §3.1). Secrets are
// injected by Cloud Run from Secret Manager; non-secret config comes from
// Terraform-managed env vars. Nothing here is ever hand-entered in a console.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Service identity
	Port          string
	AppEnv        string
	PrimaryRegion string
	ServiceRegion string
	LogLevel      string
	AppBaseURL    string

	// Auth (RS256 — the plan replaces the old HS256 scheme)
	JWTPrivateKey string
	JWTPublicKey  string
	JWTAccessTTL  time.Duration
	JWTRefreshTTL time.Duration

	// Data plane
	DatabaseURL     string
	DatabaseReadURL string
	RedisURL        string
	QdrantURL       string
	QdrantAPIKey    string
	MLServiceURL    string

	// Models
	SarvamAPIKey  string
	SarvamBaseURL string
	GeminiAPIKey  string
	GeminiBaseURL string

	// Cascade tuning
	ModelRouterConfig           string
	SemanticSimilarityThreshold float64
	CacheTTL                    time.Duration
	HedgeDelay                  time.Duration
	ConfidenceGate              float64

	// Ops
	AdminEmails  []string
	SentryDSN    string
	OTELEndpoint string
}

// Load reads config from the environment, applying defaults for non-secret
// values. It returns an error listing every missing required var at once, so a
// misconfigured deploy fails fast with one actionable message instead of
// crashing on first use.
func Load() (*Config, error) {
	c := &Config{
		Port:          env("PORT", "8080"),
		AppEnv:        env("APP_ENV", "dev"),
		PrimaryRegion: env("PRIMARY_REGION", "asia-south1"),
		ServiceRegion: env("SERVICE_REGION", "local"),
		LogLevel:      env("LOG_LEVEL", "info"),
		AppBaseURL:    env("APP_BASE_URL", "http://localhost:3000"),

		JWTPrivateKey: os.Getenv("JWT_PRIVATE_KEY"),
		JWTPublicKey:  os.Getenv("JWT_PUBLIC_KEY"),

		DatabaseURL:     os.Getenv("DATABASE_URL"),
		DatabaseReadURL: os.Getenv("DATABASE_READ_URL"),
		RedisURL:        os.Getenv("REDIS_URL"),
		QdrantURL:       os.Getenv("QDRANT_URL"),
		QdrantAPIKey:    os.Getenv("QDRANT_API_KEY"),
		MLServiceURL:    env("ML_SERVICE_URL", "http://localhost:8081"),

		SarvamAPIKey:  os.Getenv("SARVAM_API_KEY"),
		SarvamBaseURL: env("SARVAM_BASE_URL", "https://api.sarvam.ai"),
		GeminiAPIKey:  os.Getenv("GEMINI_API_KEY"),
		GeminiBaseURL: env("GEMINI_BASE_URL", "https://generativelanguage.googleapis.com"),

		ModelRouterConfig: env("MODEL_ROUTER_CONFIG", "{}"),

		SentryDSN:    os.Getenv("SENTRY_DSN"),
		OTELEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	}

	var errs []string
	var err error

	if c.JWTAccessTTL, err = envDuration("JWT_ACCESS_TTL", "15m"); err != nil {
		errs = append(errs, err.Error())
	}
	if c.JWTRefreshTTL, err = envDuration("JWT_REFRESH_TTL", "168h"); err != nil {
		errs = append(errs, err.Error())
	}
	if c.HedgeDelay, err = envDurationMS("HEDGE_DELAY_MS", 800); err != nil {
		errs = append(errs, err.Error())
	}
	if c.CacheTTL, err = envDurationSec("CACHE_TTL_SECONDS", 604800); err != nil {
		errs = append(errs, err.Error())
	}
	if c.SemanticSimilarityThreshold, err = envFloat("SEMANTIC_SIMILARITY_THRESHOLD", 0.92); err != nil {
		errs = append(errs, err.Error())
	}
	if c.ConfidenceGate, err = envFloat("CONFIDENCE_GATE", 0.85); err != nil {
		errs = append(errs, err.Error())
	}

	if admins := os.Getenv("ADMIN_EMAILS"); admins != "" {
		for _, a := range strings.Split(admins, ",") {
			if a = strings.TrimSpace(a); a != "" {
				c.AdminEmails = append(c.AdminEmails, strings.ToLower(a))
			}
		}
	}

	// Secrets are required everywhere except dev, where the local stack runs
	// without cloud credentials.
	if c.AppEnv != "dev" {
		for _, r := range []struct{ name, val string }{
			{"JWT_PRIVATE_KEY", c.JWTPrivateKey},
			{"JWT_PUBLIC_KEY", c.JWTPublicKey},
			{"DATABASE_URL", c.DatabaseURL},
			{"REDIS_URL", c.RedisURL},
			{"SARVAM_API_KEY", c.SarvamAPIKey},
			{"GEMINI_API_KEY", c.GeminiAPIKey},
		} {
			if r.val == "" {
				errs = append(errs, fmt.Sprintf("%s is required when APP_ENV=%s", r.name, c.AppEnv))
			}
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return c, nil
}

func (c *Config) IsAdmin(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, a := range c.AdminEmails {
		if a == email {
			return true
		}
	}
	return false
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key, def string) (time.Duration, error) {
	d, err := time.ParseDuration(env(key, def))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return d, nil
}

func envDurationMS(key string, def int) (time.Duration, error) {
	v, err := envInt(key, def)
	return time.Duration(v) * time.Millisecond, err
}

func envDurationSec(key string, def int) (time.Duration, error) {
	v, err := envInt(key, def)
	return time.Duration(v) * time.Second, err
}

func envInt(key string, def int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return def, fmt.Errorf("%s: must be an integer, got %q", key, raw)
	}
	return v, nil
}

func envFloat(key string, def float64) (float64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return def, fmt.Errorf("%s: must be a float, got %q", key, raw)
	}
	return v, nil
}
