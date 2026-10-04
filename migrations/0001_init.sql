-- ojslab MVP schema
CREATE TABLE IF NOT EXISTS articles (
    id            BIGSERIAL PRIMARY KEY,
    title         TEXT NOT NULL,
    abstract      TEXT NOT NULL,
    author        TEXT NOT NULL,
    email         TEXT NOT NULL,
    manuscript_path TEXT,
    status        TEXT NOT NULL DEFAULT 'submitted'
                  CHECK (status IN ('submitted','in_review','accepted','rejected','published')),
    volume        TEXT,
    issue         TEXT,
    published_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS review_events (
    id          BIGSERIAL PRIMARY KEY,
    article_id  BIGINT NOT NULL REFERENCES articles(id) ON DELETE CASCADE,
    from_status TEXT,
    to_status   TEXT NOT NULL,
    note        TEXT,
    by_email    TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_articles_status ON articles(status);