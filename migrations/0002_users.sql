-- ojslab SSR: users + roles (ASVS authn/authz baseline)
CREATE TABLE IF NOT EXISTS users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL DEFAULT 'author'
                  CHECK (role IN ('author','editor','admin')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- sessions are persisted by scs/pgxstore; keep table here for clarity
CREATE TABLE IF NOT EXISTS sessions (
    token  TEXT PRIMARY KEY,
    data   BYTEA NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_expiry_idx ON sessions (expiry);