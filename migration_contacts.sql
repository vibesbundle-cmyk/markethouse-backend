-- ============================================================
-- MARKETHOUSE — CONTACT SYNCING (Phase 1)
-- Run this against an existing database. Fresh setups get the
-- same schema automatically via vinci.sql and the server's
-- self-healing migrations (internal/database/postgres.go), so
-- this file is only needed for databases created before this
-- feature shipped. Idempotent — safe to run at any time.
-- ============================================================

SET client_min_messages = WARNING;

-- Master toggle + last successful sync timestamp on the user
ALTER TABLE users ADD COLUMN IF NOT EXISTS contact_sync_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS contact_sync_at TIMESTAMP;

-- The user's imported address book.
-- phone_hash is a sha256 of the E.164-normalized number so a
-- server-side join with users.mobile ignores formatting differences
-- (0803... vs +234803...). UNIQUE(user_id, contact_phone) keeps a
-- re-sync idempotent; the handler replaces the whole book per sync.
CREATE TABLE IF NOT EXISTS user_contacts (
  id            SERIAL PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  contact_name  TEXT NOT NULL,
  contact_phone TEXT NOT NULL,
  phone_hash    TEXT NOT NULL,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (user_id, contact_phone)
);

CREATE INDEX IF NOT EXISTS idx_user_contacts_user ON user_contacts(user_id);
CREATE INDEX IF NOT EXISTS idx_user_contacts_hash ON user_contacts(phone_hash);
