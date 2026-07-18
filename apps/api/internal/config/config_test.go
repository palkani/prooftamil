package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsInDev(t *testing.T) {
	t.Setenv("APP_ENV", "dev")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("dev must load without any secrets configured: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	// §8.6 — there is no latency hedge any more. The fallback is FAILOVER: it fires
	// only when the primary errors or blows MODEL_TIMEOUT_MS. Racing Sarvam (~8x
	// slower than Gemini) could never win, so a hedge was pure cost.
	if cfg.ModelTimeout != 6000*time.Millisecond {
		t.Errorf("ModelTimeout = %v, want 6s", cfg.ModelTimeout)
	}
	if cfg.FallbackTrigger != "on_error" {
		t.Errorf("FallbackTrigger = %q, want on_error", cfg.FallbackTrigger)
	}
	if cfg.Tier1Only {
		t.Error("Tier1Only must default to false")
	}
	if cfg.CacheTTL != 604800*time.Second {
		t.Errorf("CacheTTL = %v, want 7 days", cfg.CacheTTL)
	}
	if cfg.ConfidenceGate != 0.85 {
		t.Errorf("ConfidenceGate = %v, want 0.85", cfg.ConfidenceGate)
	}
	if cfg.SemanticSimilarityThreshold != 0.92 {
		t.Errorf("SemanticSimilarityThreshold = %v, want 0.92", cfg.SemanticSimilarityThreshold)
	}
}

// Prod must refuse to start without its secrets rather than boot and fail on the
// first user request.
func TestLoadRequiresSecretsOutsideDev(t *testing.T) {
	t.Setenv("APP_ENV", "prod")

	_, err := Load()
	if err == nil {
		t.Fatal("prod loaded with no secrets set; expected an error")
	}

	// Every missing secret should be reported at once, not just the first.
	for _, want := range []string{
		"JWT_PRIVATE_KEY", "JWT_PUBLIC_KEY", "DATABASE_URL",
		"REDIS_URL", "GEMINI_API_KEY",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the missing %s; got:\n%v", want, err)
		}
	}

	// SARVAM_API_KEY is optional (fallback corrector + server ASR only), so its
	// absence must NOT be reported as a missing required secret.
	if strings.Contains(err.Error(), "SARVAM_API_KEY") {
		t.Errorf("SARVAM_API_KEY is optional and must not be required; got:\n%v", err)
	}
}

func TestLoadSucceedsInProdWhenSecretsPresent(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("JWT_PRIVATE_KEY", "priv")
	t.Setenv("JWT_PUBLIC_KEY", "pub")
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	// No SARVAM_API_KEY on purpose: prod must load without it (it is optional).
	t.Setenv("GEMINI_API_KEY", "sk-gemini")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v, want success", err)
	}
	if cfg.AppEnv != "prod" {
		t.Errorf("AppEnv = %q, want prod", cfg.AppEnv)
	}
}

// A malformed numeric knob must fail loudly at boot. Silently falling back to a
// default would mean a deploy that "worked" but ran with the wrong cascade
// tuning — the worst kind of config bug.
func TestLoadRejectsMalformedNumbers(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("CONFIDENCE_GATE", "very-high")

	_, err := Load()
	if err == nil {
		t.Fatal("expected an error for a non-numeric CONFIDENCE_GATE")
	}
	if !strings.Contains(err.Error(), "CONFIDENCE_GATE") {
		t.Errorf("error should name the offending var; got: %v", err)
	}
}

func TestLoadRejectsMalformedDuration(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("JWT_ACCESS_TTL", "fifteen minutes")

	if _, err := Load(); err == nil {
		t.Fatal("expected an error for an unparseable JWT_ACCESS_TTL")
	}
}

func TestIsAdminIsCaseAndSpaceInsensitive(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("ADMIN_EMAILS", "Admin@ProofTamil.com, ops@prooftamil.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() = %v", err)
	}

	for _, email := range []string{"admin@prooftamil.com", "ADMIN@PROOFTAMIL.COM", " ops@prooftamil.com "} {
		if !cfg.IsAdmin(email) {
			t.Errorf("IsAdmin(%q) = false, want true", email)
		}
	}
	if cfg.IsAdmin("attacker@evil.com") {
		t.Error("IsAdmin(attacker@evil.com) = true, want false")
	}
}
