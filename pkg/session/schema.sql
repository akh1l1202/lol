-- Focus / Pomodoro sessions logged from the app, scoped to a user.
CREATE TABLE IF NOT EXISTS focus_sessions (
    id             BIGSERIAL PRIMARY KEY,
    user_id        TEXT NOT NULL REFERENCES users(id),
    task           TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ NOT NULL,
    ended_at       TIMESTAMPTZ,
    status         TEXT NOT NULL DEFAULT 'completed',
    pomodoro_index INTEGER NOT NULL DEFAULT 0,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sessions_user_started
    ON focus_sessions (user_id, started_at);
