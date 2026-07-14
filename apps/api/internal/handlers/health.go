package handlers

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/config"
)

// probeTimeout bounds every dependency check so a hung dependency can never
// hang the readiness probe itself — Cloud Run would kill the revision.
const probeTimeout = 2 * time.Second

// Dependency is one thing readiness checks: Postgres, Redis, Qdrant, the ML
// service, the model providers.
type Dependency struct {
	Name string
	// Required marks a dependency the service cannot serve traffic without.
	// Optional ones are reported but never fail the probe (e.g. Qdrant: a
	// semantic-cache outage degrades to a cache miss, it does not break
	// proofreading).
	Required bool
	Check    func(context.Context) error
}

type Health struct {
	cfg  *config.Config
	deps []Dependency
	// started marks process start; liveness reports uptime so a crash-looping
	// revision is obvious in the probe output.
	started time.Time
}

func NewHealth(cfg *config.Config, deps []Dependency) *Health {
	return &Health{cfg: cfg, deps: deps, started: time.Now()}
}

type depStatus struct {
	Status string `json:"status"` // ok | error
	Error  string `json:"error,omitempty"`
	TookMS int64  `json:"took_ms"`
}

// Live is the liveness probe: is the process up? It touches no dependencies on
// purpose — a DB outage must not cause Cloud Run to kill healthy instances.
func (h *Health) Live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":     "ok",
		"env":        h.cfg.AppEnv,
		"region":     h.cfg.ServiceRegion,
		"uptime_sec": int64(time.Since(h.started).Seconds()),
	})
}

// Ready is the readiness probe: can this instance serve traffic? It probes
// every dependency in parallel and fails if any *required* one is down.
func (h *Health) Ready(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), probeTimeout)
	defer cancel()

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = make(map[string]depStatus, len(h.deps))
		ready   = true
	)

	for _, dep := range h.deps {
		wg.Add(1)
		go func(dep Dependency) {
			defer wg.Done()
			start := time.Now()
			err := dep.Check(ctx)
			st := depStatus{Status: "ok", TookMS: time.Since(start).Milliseconds()}
			if err != nil {
				st.Status = "error"
				st.Error = err.Error()
			}

			mu.Lock()
			defer mu.Unlock()
			results[dep.Name] = st
			if err != nil && dep.Required {
				ready = false
			}
		}(dep)
	}
	wg.Wait()

	code := http.StatusOK
	status := "ready"
	if !ready {
		code = http.StatusServiceUnavailable
		status = "not_ready"
	}

	c.JSON(code, gin.H{
		"status":       status,
		"env":          h.cfg.AppEnv,
		"region":       h.cfg.ServiceRegion,
		"dependencies": results,
	})
}
