-- Schema for the Go core API. Applied on startup via auth.Migrate (idempotent).

-- Users registered through POST /auth/register. Mirrors the auth.User model
-- (username-based, matching the existing handlers).
CREATE TABLE IF NOT EXISTS users (
    id            TEXT PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    name          TEXT,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

