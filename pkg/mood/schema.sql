-- Mood check-ins submitted from the app, scoped to a user.
CREATE TABLE IF NOT EXISTS mood_checkins (
    id           BIGSERIAL PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id),
    energy       INTEGER NOT NULL,
    stress       INTEGER NOT NULL,
    burnout_risk INTEGER NOT NULL DEFAULT 0,
    reflection   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mood_user_created
    ON mood_checkins (user_id, created_at);
