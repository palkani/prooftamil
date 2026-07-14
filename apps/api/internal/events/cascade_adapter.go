package events

import (
	"time"

	"github.com/prooftamil/api/internal/cascade"
)

// TelemetryAdapter lets a Publisher satisfy cascade.Telemetry.
//
// The cascade declares its own Telemetry interface in its own types, so it never imports
// this package. The adapter lives HERE, on the dependency's side of the boundary — which
// is what keeps the cascade testable with a fake and free of a message-broker dependency.
type TelemetryAdapter struct{ P *Publisher }

func (a TelemetryAdapter) AIRequest(e cascade.TelemetryAIRequest) {
	a.P.AIRequest(AIRequest{
		TS:           time.Now(),
		Region:       e.Region,
		TierResolved: e.TierResolved,
		CacheHit:     e.CacheHit,
		LatencyMS:    e.LatencyMS,
		Error:        e.Error,
	})
}

var _ cascade.Telemetry = TelemetryAdapter{}
