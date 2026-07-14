-- ClickHouse analytics schema (plan §6.2).
--
-- These two tables were originally in Postgres. Moving them here is the point of
-- Phase 3: they are append-only, high-volume, and NEVER read by the serving path.
-- Keeping them on the primary DB meant every user request competed with telemetry
-- writes for the same connection pool — and under load, telemetry wins, because there
-- is always more of it.

CREATE DATABASE IF NOT EXISTS analytics;

-- Every model call. This is the cost ledger: what we spent, on which model, for whom,
-- and whether the cascade managed to avoid the call entirely.
CREATE TABLE IF NOT EXISTS analytics.ai_requests
(
    ts             DateTime64(3) DEFAULT now64(3),
    user_id        String,
    region         LowCardinality(String),

    -- Which tier actually resolved the request. The whole argument for the cascade is
    -- that this is usually 1 or 2 (free) rather than 3 or 4 (paid), so it is the first
    -- number to look at when asking whether any of this was worth building.
    tier_resolved  UInt8,
    model          LowCardinality(String),
    prompt_version UInt16,
    thinking       Bool,

    tokens_in      UInt32,
    tokens_out     UInt32,
    latency_ms     UInt32,
    cache_hit      Bool,
    cost_micros    UInt64,

    -- Set when the primary failed and the fallback served (§8.6). A rising rate here is
    -- the earliest signal that a provider is degrading.
    fell_back      Bool DEFAULT false,
    error          String DEFAULT ''
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (ts, user_id)
-- 90 days. Cost analysis needs a quarter; nobody needs a two-year-old token count, and
-- the rows are worthless the moment the models change under them.
TTL toDateTime(ts) + INTERVAL 90 DAY;

-- Every user action. Powers the correction-accept rate, which is the closest thing we
-- have to a direct measurement of whether the suggestions are any good.
CREATE TABLE IF NOT EXISTS analytics.activity_events
(
    ts          DateTime64(3) DEFAULT now64(3),
    user_id     String,
    event_type  LowCardinality(String),  -- correction_shown | correction_accepted | correction_rejected | ...

    -- The correction itself, denormalised on purpose. A join back to Postgres to answer
    -- "which suggestions get rejected most" would defeat the entire reason these events
    -- left Postgres.
    suggestion_type LowCardinality(String),
    source_tier     UInt8,
    confidence      Float32,
    original        String,
    suggestion      String,

    props       String DEFAULT ''  -- JSON, for anything not worth a column yet
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(ts)
ORDER BY (ts, user_id)
TTL toDateTime(ts) + INTERVAL 180 DAY;

-- A rejected suggestion is a human saying "you were wrong" — the highest-signal training
-- data in the product, and far rarer than an acceptance. This view is what the
-- training-dataset builder reads.
CREATE VIEW IF NOT EXISTS analytics.rejections AS
SELECT ts, suggestion_type, source_tier, confidence, original, suggestion
FROM analytics.activity_events
WHERE event_type = 'correction_rejected';
