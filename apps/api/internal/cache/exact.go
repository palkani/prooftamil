// Package cache implements cascade Tier 2 — the exact cache.
//
// Tamil writing is highly repetitive across users (stock phrases, common
// sentences, boilerplate), so an exact cache in front of the model tiers is the
// single cheapest win in the whole cascade: a hit costs one Redis round trip
// instead of a model call.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// ErrMiss is returned when the key is absent. It is a normal control-flow
// signal, not a failure.
var ErrMiss = errors.New("cache miss")

// Exact is a content-addressed cache of proofreading results.
type Exact struct {
	rdb *redis.Client
	ttl time.Duration

	// version salts every key. Bump it whenever anything that changes the OUTPUT
	// for a given input changes: the rule set, the lexicon, the prompt, the model,
	// or the confidence gate.
	//
	// Without this, shipping a rules fix would leave every previously-cached
	// sentence serving the OLD, wrong correction until the TTL expired — a week,
	// at the default CACHE_TTL_SECONDS. The bug would look like "my fix didn't
	// deploy". Salting the key makes a version bump an instant, total invalidation
	// with no flush and no thundering herd.
	version string
}

func NewExact(rdb *redis.Client, ttl time.Duration, version string) *Exact {
	return &Exact{rdb: rdb, ttl: ttl, version: version}
}

// Key is content-addressed: the same sentence under the same engine version maps
// to the same slot, regardless of which user or region asked. That sharing is
// the whole point — one user's model call warms the cache for everyone else.
//
// Note there is deliberately no user ID in the key. These are corrections to
// Tamil text, not user data, so cross-user sharing is safe and is what makes the
// hit rate worth having.
func (e *Exact) Key(text string) string {
	sum := sha256.Sum256([]byte(text))
	return fmt.Sprintf("pt:v%s:%s", e.version, hex.EncodeToString(sum[:16]))
}

// Get returns the cached value, or ErrMiss.
func (e *Exact) Get(ctx context.Context, text string, out any) error {
	if e.rdb == nil {
		return ErrMiss
	}

	raw, err := e.rdb.Get(ctx, e.Key(text)).Bytes()
	if errors.Is(err, redis.Nil) {
		return ErrMiss
	}
	if err != nil {
		return fmt.Errorf("cache get: %w", err)
	}

	if err := json.Unmarshal(raw, out); err != nil {
		// A corrupt or stale-shaped entry must not break the request. Treat it as
		// a miss and let the caller recompute; the bad entry is overwritten on Set.
		return ErrMiss
	}
	return nil
}

// Set stores a value. A cache write failure is never fatal: the caller already
// has the answer, and failing the user's request because we could not memoize it
// would turn a Redis blip into an outage.
func (e *Exact) Set(ctx context.Context, text string, val any) error {
	if e.rdb == nil {
		return nil
	}

	raw, err := json.Marshal(val)
	if err != nil {
		return fmt.Errorf("cache marshal: %w", err)
	}
	return e.rdb.Set(ctx, e.Key(text), raw, e.ttl).Err()
}
