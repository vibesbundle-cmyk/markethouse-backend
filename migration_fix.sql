-- ============================================================
-- MARKETHOUSE — MIGRATION FIX
-- Aligns conversations and messages tables with the repository code.
-- Run ONCE after the original vinci.sql.
-- ============================================================

SET client_min_messages = WARNING;

-- ── CONVERSATIONS: rename user1_id→user_one_id, user2_id→user_two_id ──
DO $$ BEGIN
  ALTER TABLE conversations RENAME COLUMN user1_id TO user_one_id;
EXCEPTION WHEN undefined_column THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE conversations RENAME COLUMN user2_id TO user_two_id;
EXCEPTION WHEN undefined_column THEN NULL;
END $$;

-- Drop old unique constraint and re-create on renamed columns
DO $$ BEGIN
  ALTER TABLE conversations DROP CONSTRAINT IF EXISTS conversations_user1_id_user2_id_key;
EXCEPTION WHEN OTHERS THEN NULL;
END $$;

DO $$ BEGIN
  ALTER TABLE conversations ADD CONSTRAINT conversations_user_one_id_user_two_id_key
    UNIQUE (user_one_id, user_two_id);
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- ── CONVERSATIONS: add last_message and updated_at ──
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS last_message TEXT DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP;

-- ── MESSAGES: add receiver_id ──
ALTER TABLE messages ADD COLUMN IF NOT EXISTS receiver_id INTEGER
  REFERENCES users(id) ON DELETE CASCADE;

-- Back-fill receiver_id from the conversation table
UPDATE messages m
SET receiver_id = (
  SELECT CASE WHEN c.user_one_id = m.sender_id THEN c.user_two_id ELSE c.user_one_id END
  FROM conversations c WHERE c.id = m.conversation_id
)
WHERE m.receiver_id IS NULL;

-- Index for fast unread-count queries
CREATE INDEX IF NOT EXISTS idx_messages_receiver_id ON messages(receiver_id);

-- ── ONLINE PRESENCE (Redis-backed, but also a Postgres helper view) ──
-- Nothing needed in Postgres; online status is stored in Redis only.
