-- Raw app-usage events ingested from the device, scoped to a user.
-- started_at is indexed for range queries; monthly partitioning can be added
-- later if volume warrants it.
CREATE TABLE IF NOT EXISTS usage_events (
    id          BIGSERIAL PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    app_package TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    duration_s  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_usage_events_user_started
    ON usage_events (user_id, started_at);
