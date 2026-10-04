-- dependency on users table (0002); added after it.
ALTER TABLE articles ADD COLUMN IF NOT EXISTS created_by BIGINT REFERENCES users(id);