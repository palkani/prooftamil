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
	// 2500ms, not the plan's 800ms. The primary (Gemini, thinking off) has a
	// measured p50 of 946ms and a 1210ms tail, so an 800ms hedge would fire the
	// fallback on roughly HALF of all requests and double model spend — while
	// racing in Sarvam, which is ~8x slower and could not win anyway.
	if cfg.HedgeDelay != 2500*time.Millisecond {
		t.Errorf("HedgeDelay = %v, want 2.5s", cfg.HedgeDelay)
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
		"REDIS_URL", "SARVAM_API_KEY", "GEMINI_API_KEY",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name the missing %s; got:\n%v", want, err)
		}
	}
}

func TestLoadSucceedsInProdWhenSecretsPresent(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	t.Setenv("JWT_PRIVATE_KEY", "priv")
	t.Setenv("JWT_PUBLIC_KEY", "pub")
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REDIS_URL", "redis://localhost:6379/0")
	t.Setenv("SARVAM_API_KEY", "sk-sarvam")
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
