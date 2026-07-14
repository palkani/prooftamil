// Package router wires the HTTP surface described in plan §7.
package router

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/prooftamil/api/internal/config"
	"github.com/prooftamil/api/internal/handlers"
)

func New(cfg *config.Config, health *handlers.Health, proofread *handlers.Proofread) *gin.Engine {
	if cfg.AppEnv != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())

	// Dev-only CORS, so a page served from a different local port (the Phase 4
	// editor, or the scratch test harness) can call the API.
	//
	// Gated on APP_ENV so this can never become a production hole: in staging/prod
	// the frontend is served from the same origin behind Cloudflare, and a wildcard
	// Allow-Origin there would let any website on the internet make authenticated
	// requests on a logged-in user's behalf.
	if cfg.AppEnv == "dev" {
		r.Use(func(c *gin.Context) {
			c.Header("Access-Control-Allow-Origin", "*")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusNoContent)
				return
			}
			c.Next()
		})
	}

	// Cloud Run and the Cloudflare load balancer terminate TLS upstream, so the
	// client IP arrives in X-Forwarded-For. Trusting only those proxies keeps
	// rate limiting keyed on the real caller rather than a spoofable header.
	_ = r.SetTrustedProxies(nil)

	// §7.6 — internal/ops surface. health is unauthenticated because Cloud Run's
	// own probes call it; everything else under /internal gets the admin guard
	// once auth lands in Phase 6.
	internal := r.Group("/internal")
	{
		internal.GET("/health", health.Live)
		internal.GET("/ready", health.Ready)
	}

	// §7 — the versioned public API. Routes are added per phase:
	//   Phase 4: /drafts/*, /export, /import
	//   Phase 6: /auth/*, /billing/*, /webhooks/dodo
	v1 := r.Group("/api/v1")
	{
		// §7.2 — the cascade.
		v1.POST("/proofread", proofread.Sync)
		v1.GET("/proofread/stream", proofread.Stream)
	}

	return r
}
