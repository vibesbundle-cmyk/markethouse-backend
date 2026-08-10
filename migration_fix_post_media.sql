-- ============================================================
-- MARKETHOUSE — MULTI-MEDIA POSTS
-- Lets a single post carry several photos/videos, mixed together,
-- instead of exactly one file. The original posts.media_url /
-- posts.media_type columns are kept and always mirror the FIRST
-- item, so any code that still reads those two columns keeps working.
-- Run ONCE.
-- ============================================================

SET client_min_messages = WARNING;

CREATE TABLE IF NOT EXISTS post_media (
  id         SERIAL PRIMARY KEY,
  post_id    INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
  media_url  TEXT NOT NULL,
  media_type TEXT NOT NULL, -- image | video
  position   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_post_media_post_id ON post_media(post_id);

-- Backfill: every existing post already has exactly one file in
-- posts.media_url — give it a matching post_media row (position 0)
-- so old posts show up in the new "media" array too.
INSERT INTO post_media (post_id, media_url, media_type, position)
SELECT p.id, p.media_url, p.media_type, 0
FROM posts p
WHERE p.media_url <> ''
  AND NOT EXISTS (SELECT 1 FROM post_media pm WHERE pm.post_id = p.id);
