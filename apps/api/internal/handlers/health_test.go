package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/config"
)

func init() { gin.SetMode(gin.TestMode) }

func ok(context.Context) error   { return nil }
func down(context.Context) error { return errors.New("connection refused") }

func probe(t *testing.T, deps []Dependency) (int, map[string]any) {
	t.Helper()
	h := NewHealth(&config.Config{AppEnv: "test", ServiceRegion: "local"}, deps)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/internal/ready", nil)
	h.Ready(c)

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("readiness returned invalid JSON: %v", err)
	}
	return w.Code, body
}

func TestReadyWhenAllDependenciesAreHealthy(t *testing.T) {
	code, body := probe(t, []Dependency{
		{Name: "postgres_primary", Required: true, Check: ok},
		{Name: "redis", Required: true, Check: ok},
	})

	if code != http.StatusOK {
		t.Errorf("status = %d, want 200", code)
	}
	if body["status"] != "ready" {
		t.Errorf("status = %v, want ready", body["status"])
	}
}

// A required dependency being down must take the instance out of rotation —
// this is what arms auto-rollback in the CD pipeline (§10).
func TestNotReadyWhenARequiredDependencyIsDown(t *testing.T) {
	code, body := probe(t, []Dependency{
		{Name: "postgres_primary", Required: true, Check: down},
		{Name: "redis", Required: true, Check: ok},
	})

	if code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", code)
	}
	if body["status"] != "not_ready" {
		t.Errorf("status = %v, want not_ready", body["status"])
	}

	deps := body["dependencies"].(map[string]any)
	pg := deps["postgres_primary"].(map[string]any)
	if pg["status"] != "error" {
		t.Errorf("postgres status = %v, want error", pg["status"])
	}
	if pg["error"] == "" {
		t.Error("a failed dependency must report why")
	}
}

// An optional dependency being down must NOT take the instance out of rotation.
// Qdrant is the real case: a semantic-cache outage degrades to a cache miss, it
// does not break proofreading. Pulling every instance offline for it would turn
// a minor degradation into a full outage.
func TestStillReadyWhenAnOptionalDependencyIsDown(t *testing.T) {
	code, body := probe(t, []Dependency{
		{Name: "postgres_primary", Required: true, Check: ok},
		{Name: "qdrant", Required: false, Check: down},
	})

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an optional dep must not fail readiness", code)
	}

	// It still has to be *reported*, so the outage is visible in the probe.
	deps := body["dependencies"].(map[string]any)
	q := deps["qdrant"].(map[string]any)
	if q["status"] != "error" {
		t.Errorf("qdrant status = %v, want the failure to be reported", q["status"])
	}
}

// A hung dependency must not hang the probe itself; Cloud Run would kill the
// revision. The probe's internal timeout has to win.
func TestProbeDoesNotHangOnASlowDependency(t *testing.T) {
	hang := func(ctx context.Context) error {
		select {
		case <-time.After(30 * time.Second):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	done := make(chan int, 1)
	go func() {
		code, _ := probe(t, []Dependency{{Name: "slow", Required: true, Check: hang}})
		done <- code
	}()

	select {
	case code := <-done:
		if code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want 503 for a timed-out dependency", code)
		}
	case <-time.After(probeTimeout + 3*time.Second):
		t.Fatal("readiness probe hung on a slow dependency")
	}
}

func TestLiveIgnoresDependencies(t *testing.T) {
	// Liveness must stay green even when everything downstream is down,
	// otherwise a DB outage would cause Cloud Run to kill healthy instances and
	// turn a recoverable incident into a crash loop.
	h := NewHealth(&config.Config{AppEnv: "test"}, []Dependency{
		{Name: "postgres_primary", Required: true, Check: down},
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/internal/health", nil)
	h.Live(c)

	if w.Code != http.StatusOK {
		t.Errorf("liveness = %d, want 200 even with a dead dependency", w.Code)
	}
}
