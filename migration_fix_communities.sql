-- ============================================================
-- MARKETHOUSE — COMMUNITY TABLE FIX
-- The "communities" table (as actually created by vinci.sql) has
-- columns: type, owner_id  — but community_handler.go queries/inserts
-- columns: visibility, category, created_by. That mismatch is the
-- exact cause of the 500 errors on POST /community and GET /communities
-- ("column does not exist"). Run this once against your database.
-- ============================================================

SET client_min_messages = WARNING;

-- Add the columns the handler code actually expects
ALTER TABLE communities ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public';
ALTER TABLE communities ADD COLUMN IF NOT EXISTS category   TEXT DEFAULT '';
ALTER TABLE communities ADD COLUMN IF NOT EXISTS created_by INTEGER REFERENCES users(id) ON DELETE CASCADE;

-- Backfill from the old columns so existing communities keep working
UPDATE communities SET visibility = type WHERE type IS NOT NULL;
UPDATE communities SET created_by = owner_id WHERE created_by IS NULL;
