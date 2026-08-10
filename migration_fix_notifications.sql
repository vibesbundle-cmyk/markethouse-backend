-- ============================================================
-- MARKETHOUSE — NOTIFICATIONS TABLE FIX
-- Same problem as communities: notification_handler.go queries
-- n.actor_id, n.entity_type, n.entity_id, but the notifications table
-- that actually got created has none of those columns. Run ONCE.
-- ============================================================

SET client_min_messages = WARNING;

ALTER TABLE notifications ADD COLUMN IF NOT EXISTS actor_id    INTEGER REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS entity_type TEXT;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS entity_id   INTEGER;
