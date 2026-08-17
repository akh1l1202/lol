-- Habits and their per-day completion logs, scoped to a user.
CREATE TABLE IF NOT EXISTS habits (
    id          BIGSERIAL PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'daily', -- 'daily' | 'weekly'
    target      INTEGER NOT NULL DEFAULT 1,    -- weekly completions goal
    color       TEXT NOT NULL DEFAULT '',
    sort_order  INTEGER NOT NULL DEFAULT 0,
    archived    BOOLEAN NOT NULL DEFAULT FALSE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX IF NOT EXISTS idx_habits_user
    ON habits (user_id, archived, sort_order);

CREATE TABLE IF NOT EXISTS habit_logs (
    id          BIGSERIAL PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES users(id),
    habit_id    BIGINT NOT NULL REFERENCES habits(id) ON DELETE CASCADE,
    logged_on   DATE NOT NULL,
    count       INTEGER NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (habit_id, logged_on)
);

CREATE INDEX IF NOT EXISTS idx_habit_logs_user_day
    ON habit_logs (user_id, logged_on);
