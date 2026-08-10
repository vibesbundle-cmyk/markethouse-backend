-- ============================================================
-- MARKETHOUSE — VINCI.SQL  (safe migration — idempotent)
-- Run this whole file at any time. Existing tables/columns
-- are preserved. Only new things are added.
-- ============================================================

SET client_min_messages = WARNING;

-- ── USERS ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
  id            SERIAL PRIMARY KEY,
  full_name     TEXT NOT NULL,
  email         TEXT NOT NULL UNIQUE,
  mobile        TEXT UNIQUE,
  password      TEXT NOT NULL,
  dob           DATE NULL,
  gender        TEXT NULL,
  profile_photo TEXT,
  header_photo  TEXT DEFAULT '',
  username      TEXT UNIQUE,
  bio           TEXT,
  account_type  TEXT DEFAULT 'personal',
  rating        NUMERIC DEFAULT 0,
  sales_score   INTEGER DEFAULT 0,
  is_verified   BOOLEAN DEFAULT false,
  is_phone_verified BOOLEAN DEFAULT false,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Add new columns to users if they don't exist
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_type TEXT DEFAULT '';

-- ── VERIFICATION ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS verification_codes (
  id         SERIAL PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  code       VARCHAR(6) NOT NULL,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS signup_attempts (
  id         SERIAL PRIMARY KEY,
  ip_hash    VARCHAR(64) NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_signup_ip_hash ON signup_attempts(ip_hash);

-- ── USER PROFILE ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS user_profile (
  id          SERIAL PRIMARY KEY,
  user_id     INT NOT NULL,
  profile_pic VARCHAR(255) NULL,
  header_pic  VARCHAR(255) NULL,
  username    VARCHAR(50) NULL,
  bio         VARCHAR(255) NULL,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (user_id)
);

-- ── POSTS ────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS posts (
  id          SERIAL PRIMARY KEY,
  user_id     INTEGER NOT NULL,
  caption     TEXT,
  media_url   TEXT,
  media_type  TEXT DEFAULT 'image',
  post_type   TEXT DEFAULT 'public',
  price       NUMERIC DEFAULT 0,
  is_locked   BOOLEAN DEFAULT false,
  tagged_users TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_posts_user_id    ON posts(user_id);
CREATE INDEX IF NOT EXISTS idx_posts_created_at ON posts(created_at);

-- ── LIKES / SAVES / COMMENTS ─────────────────────────────────
CREATE TABLE IF NOT EXISTS likes (
  id         SERIAL PRIMARY KEY,
  post_id    INTEGER NOT NULL,
  user_id    INTEGER NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS comments (
  id                 SERIAL PRIMARY KEY,
  post_id            INTEGER NOT NULL,
  user_id            INTEGER NOT NULL,
  content            TEXT NOT NULL,
  parent_comment_id  INTEGER NULL,
  created_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (parent_comment_id) REFERENCES comments(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_comments_post_id ON comments(post_id);
CREATE INDEX IF NOT EXISTS idx_comments_parent_id ON comments(parent_comment_id);

CREATE TABLE IF NOT EXISTS comment_likes (
  id         SERIAL PRIMARY KEY,
  comment_id INTEGER NOT NULL,
  user_id    INTEGER NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (comment_id) REFERENCES comments(id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (comment_id, user_id)
);

-- If `comments` already existed before this column was added, this
-- backfills it safely without touching any existing rows/data.
ALTER TABLE comments ADD COLUMN IF NOT EXISTS parent_comment_id INTEGER NULL REFERENCES comments(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_comments_parent_id ON comments(parent_comment_id);

CREATE TABLE IF NOT EXISTS saves (
  id         SERIAL PRIMARY KEY,
  post_id    INTEGER NOT NULL,
  user_id    INTEGER NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (post_id, user_id)
);

-- ── FOLLOWS ───────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS follows (
  id           SERIAL PRIMARY KEY,
  follower_id  INTEGER NOT NULL,
  following_id INTEGER NOT NULL,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (follower_id)  REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (following_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (follower_id, following_id)
);

-- ── BLOCKS ────────────────────────────────────────────────────
-- Used by the recommendation engine to filter blocked creators out of feeds.
CREATE TABLE IF NOT EXISTS blocks (
  id         SERIAL PRIMARY KEY,
  blocker_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (blocker_id, blocked_id)
);
CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON blocks(blocker_id);

CREATE TABLE IF NOT EXISTS post_reshare (
  id         SERIAL PRIMARY KEY,
  post_id    INTEGER NOT NULL,
  user_id    INTEGER NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS post_views (
  id        SERIAL PRIMARY KEY,
  post_id   INTEGER NOT NULL,
  user_id   INTEGER NULL,
  viewed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (post_id) REFERENCES posts(id) ON DELETE CASCADE,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_post_views_post_id   ON post_views(post_id);
CREATE INDEX IF NOT EXISTS idx_post_views_viewed_at ON post_views(viewed_at);

-- ── MESSAGING ─────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS conversations (
  id           SERIAL PRIMARY KEY,
  user_one_id  INTEGER NOT NULL,
  user_two_id  INTEGER NOT NULL,
  last_message TEXT DEFAULT '',
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_one_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (user_two_id) REFERENCES users(id) ON DELETE CASCADE,
  UNIQUE (user_one_id, user_two_id)
);

CREATE TABLE IF NOT EXISTS messages (
  id              SERIAL PRIMARY KEY,
  conversation_id INTEGER NOT NULL,
  sender_id       INTEGER NOT NULL,
  receiver_id     INTEGER NOT NULL DEFAULT 0,
  content         TEXT NOT NULL,
  is_read         BOOLEAN DEFAULT false,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
  FOREIGN KEY (sender_id)       REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_messages_conv_id    ON messages(conversation_id);
CREATE INDEX IF NOT EXISTS idx_messages_sender_id  ON messages(sender_id);
CREATE INDEX IF NOT EXISTS idx_messages_receiver_id ON messages(receiver_id);
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(created_at);

-- ═══════════════════════════════════════════════════════════════
-- MARKETPLACE — DEMAND (buy requests)
-- ═══════════════════════════════════════════════════════════════

-- Create MINIMAL table
CREATE TABLE IF NOT EXISTS demands (
  id                SERIAL PRIMARY KEY,
  user_id           INTEGER NOT NULL,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Add ALL columns with safe approach (nullable first, then set defaults)
DO $$ 
BEGIN
  -- Add columns as nullable first
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='category') THEN
    ALTER TABLE demands ADD COLUMN category TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='looking_for') THEN
    ALTER TABLE demands ADD COLUMN looking_for TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='condition_pref') THEN
    ALTER TABLE demands ADD COLUMN condition_pref TEXT[] DEFAULT '{}';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='min_price') THEN
    ALTER TABLE demands ADD COLUMN min_price NUMERIC DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='max_price') THEN
    ALTER TABLE demands ADD COLUMN max_price NUMERIC DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='location') THEN
    ALTER TABLE demands ADD COLUMN location TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='latitude') THEN
    ALTER TABLE demands ADD COLUMN latitude DOUBLE PRECISION DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='longitude') THEN
    ALTER TABLE demands ADD COLUMN longitude DOUBLE PRECISION DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='search_radius') THEN
    ALTER TABLE demands ADD COLUMN search_radius INTEGER DEFAULT 10;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='description') THEN
    ALTER TABLE demands ADD COLUMN description TEXT DEFAULT '';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='urgency') THEN
    ALTER TABLE demands ADD COLUMN urgency TEXT DEFAULT 'Flexible';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='contact_number') THEN
    ALTER TABLE demands ADD COLUMN contact_number TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='preferred_contact') THEN
    ALTER TABLE demands ADD COLUMN preferred_contact TEXT DEFAULT 'Both';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='is_active') THEN
    ALTER TABLE demands ADD COLUMN is_active BOOLEAN DEFAULT true;
  END IF;

  -- Now set NOT NULL constraints (only if column exists and has no nulls)
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='category' AND is_nullable='YES') THEN
    UPDATE demands SET category = '' WHERE category IS NULL;
    ALTER TABLE demands ALTER COLUMN category SET NOT NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='looking_for' AND is_nullable='YES') THEN
    UPDATE demands SET looking_for = '' WHERE looking_for IS NULL;
    ALTER TABLE demands ALTER COLUMN looking_for SET NOT NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='location' AND is_nullable='YES') THEN
    UPDATE demands SET location = '' WHERE location IS NULL;
    ALTER TABLE demands ALTER COLUMN location SET NOT NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='contact_number' AND is_nullable='YES') THEN
    UPDATE demands SET contact_number = '' WHERE contact_number IS NULL;
    ALTER TABLE demands ALTER COLUMN contact_number SET NOT NULL;
  END IF;
END $$;

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_demands_user_id    ON demands(user_id);
CREATE INDEX IF NOT EXISTS idx_demands_category   ON demands(category);
CREATE INDEX IF NOT EXISTS idx_demands_created_at ON demands(created_at);
CREATE INDEX IF NOT EXISTS idx_demands_is_active  ON demands(is_active);

-- ═══════════════════════════════════════════════════════════════
-- MARKETPLACE — SUPPLY (sell listings)
-- ═══════════════════════════════════════════════════════════════

-- Create MINIMAL table
CREATE TABLE IF NOT EXISTS supplies (
  id                SERIAL PRIMARY KEY,
  user_id           INTEGER NOT NULL,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- Add ALL columns with safe approach (nullable first)
DO $$ 
BEGIN
  -- Add columns as nullable first
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='goods_name') THEN
    ALTER TABLE supplies ADD COLUMN goods_name TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='category') THEN
    ALTER TABLE supplies ADD COLUMN category TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='condition') THEN
    ALTER TABLE supplies ADD COLUMN condition TEXT DEFAULT 'Used';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='age_value') THEN
    ALTER TABLE supplies ADD COLUMN age_value INTEGER DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='age_unit') THEN
    ALTER TABLE supplies ADD COLUMN age_unit TEXT DEFAULT 'years';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='brand') THEN
    ALTER TABLE supplies ADD COLUMN brand TEXT DEFAULT '';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='price') THEN
    ALTER TABLE supplies ADD COLUMN price NUMERIC DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='negotiable') THEN
    ALTER TABLE supplies ADD COLUMN negotiable BOOLEAN DEFAULT false;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='description') THEN
    ALTER TABLE supplies ADD COLUMN description TEXT DEFAULT '';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='location') THEN
    ALTER TABLE supplies ADD COLUMN location TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='latitude') THEN
    ALTER TABLE supplies ADD COLUMN latitude DOUBLE PRECISION DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='longitude') THEN
    ALTER TABLE supplies ADD COLUMN longitude DOUBLE PRECISION DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='delivery_radius') THEN
    ALTER TABLE supplies ADD COLUMN delivery_radius INTEGER DEFAULT 0;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='delivery_available') THEN
    ALTER TABLE supplies ADD COLUMN delivery_available BOOLEAN DEFAULT false;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='photos') THEN
    ALTER TABLE supplies ADD COLUMN photos TEXT[] DEFAULT '{}';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='contact_number') THEN
    ALTER TABLE supplies ADD COLUMN contact_number TEXT;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='whatsapp_number') THEN
    ALTER TABLE supplies ADD COLUMN whatsapp_number TEXT DEFAULT '';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='is_active') THEN
    ALTER TABLE supplies ADD COLUMN is_active BOOLEAN DEFAULT true;
  END IF;

  -- Now set NOT NULL constraints (only if column exists and has no nulls)
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='goods_name' AND is_nullable='YES') THEN
    UPDATE supplies SET goods_name = '' WHERE goods_name IS NULL;
    ALTER TABLE supplies ALTER COLUMN goods_name SET NOT NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='category' AND is_nullable='YES') THEN
    UPDATE supplies SET category = '' WHERE category IS NULL;
    ALTER TABLE supplies ALTER COLUMN category SET NOT NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='location' AND is_nullable='YES') THEN
    UPDATE supplies SET location = '' WHERE location IS NULL;
    ALTER TABLE supplies ALTER COLUMN location SET NOT NULL;
  END IF;
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='contact_number' AND is_nullable='YES') THEN
    UPDATE supplies SET contact_number = '' WHERE contact_number IS NULL;
    ALTER TABLE supplies ALTER COLUMN contact_number SET NOT NULL;
  END IF;
END $$;

-- Create indexes
CREATE INDEX IF NOT EXISTS idx_supplies_user_id    ON supplies(user_id);
CREATE INDEX IF NOT EXISTS idx_supplies_category   ON supplies(category);
CREATE INDEX IF NOT EXISTS idx_supplies_price      ON supplies(price);
CREATE INDEX IF NOT EXISTS idx_supplies_created_at ON supplies(created_at);
CREATE INDEX IF NOT EXISTS idx_supplies_is_active  ON supplies(is_active);

-- ═══════════════════════════════════════════════════════════════
-- SHOP — PRODUCTS
-- ═══════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS products (
  id                 SERIAL PRIMARY KEY,
  user_id            INTEGER NOT NULL,
  name               TEXT NOT NULL,
  description        TEXT DEFAULT '',
  category           TEXT DEFAULT '',
  price              NUMERIC NOT NULL DEFAULT 0,
  stock_count        INTEGER NOT NULL DEFAULT 0,
  is_unlimited_stock BOOLEAN DEFAULT false,
  images             TEXT[] DEFAULT '{}',
  is_active          BOOLEAN DEFAULT true,
  created_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_products_user_id    ON products(user_id);
CREATE INDEX IF NOT EXISTS idx_products_category   ON products(category);
CREATE INDEX IF NOT EXISTS idx_products_is_active  ON products(is_active);

-- ═══════════════════════════════════════════════════════════════
-- SHOP — CART
-- ═══════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS cart_items (
  id         SERIAL PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  product_id INTEGER NOT NULL,
  quantity   INTEGER NOT NULL DEFAULT 1,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id)    REFERENCES users(id)    ON DELETE CASCADE,
  FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE,
  UNIQUE (user_id, product_id)
);
CREATE INDEX IF NOT EXISTS idx_cart_user_id ON cart_items(user_id);

-- ═══════════════════════════════════════════════════════════════
-- SHOP — ORDERS
-- ═══════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS orders (
  id                      SERIAL PRIMARY KEY,
  buyer_id                INTEGER NOT NULL,
  vendor_id               INTEGER NOT NULL,
  product_id              INTEGER NOT NULL,
  quantity                INTEGER NOT NULL DEFAULT 1,
  total_price             NUMERIC NOT NULL DEFAULT 0,
  escrow_amount           NUMERIC NOT NULL DEFAULT 0,
  status                  TEXT NOT NULL DEFAULT 'pending',
  delivery_date_scheduled TIMESTAMP NULL,
  delivery_code           VARCHAR(32) NOT NULL,
  cancel_requested_by     TEXT DEFAULT '',
  cancel_buyer_pin        TEXT DEFAULT '',
  vendor_cancel_approved  BOOLEAN DEFAULT false,
  admin_approved          BOOLEAN DEFAULT false,
  created_at              TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at              TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (buyer_id)   REFERENCES users(id)    ON DELETE CASCADE,
  FOREIGN KEY (vendor_id)  REFERENCES users(id)    ON DELETE CASCADE,
  FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_orders_buyer_id  ON orders(buyer_id);
CREATE INDEX IF NOT EXISTS idx_orders_vendor_id ON orders(vendor_id);
CREATE INDEX IF NOT EXISTS idx_orders_status    ON orders(status);
CREATE INDEX IF NOT EXISTS idx_orders_delivery_date ON orders(delivery_date_scheduled)
  WHERE status = 'paid';

-- ═══════════════════════════════════════════════════════════════
-- WALLET TRANSACTIONS (unified — escrow ledger + fintech wallet)
-- ═══════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS wallet_transactions (
  id              SERIAL PRIMARY KEY,
  user_id         INTEGER REFERENCES users(id)  ON DELETE CASCADE,
  wallet_id       INTEGER,
  order_id        INTEGER REFERENCES orders(id)  ON DELETE SET NULL,
  type            TEXT NOT NULL,
  amount          NUMERIC(15,2) NOT NULL DEFAULT 0,
  balance_after   NUMERIC(15,2),
  reference       TEXT DEFAULT '',
  description     TEXT DEFAULT '',
  status          TEXT NOT NULL DEFAULT 'completed',
  counterparty_id INTEGER REFERENCES users(id),
  related_order_id INTEGER,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_wallet_user_id    ON wallet_transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_wallet_wallet_id  ON wallet_transactions(wallet_id);
CREATE INDEX IF NOT EXISTS idx_wallet_order_id   ON wallet_transactions(order_id);
CREATE INDEX IF NOT EXISTS idx_wallet_created_at ON wallet_transactions(created_at);

-- ═══════════════════════════════════════════════════════════════
-- LEGACY COLUMN MIGRATION
-- Your DB may have older column names from previous versions.
-- These renames safely migrate old data to new column names.
-- If both old and new columns exist, the old duplicate is dropped.
-- ═══════════════════════════════════════════════════════════════

-- demands: some older DBs have "item_name" instead of "looking_for"
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='item_name') THEN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='demands' AND column_name='looking_for') THEN
      ALTER TABLE demands RENAME COLUMN item_name TO looking_for;
      RAISE NOTICE 'demands: renamed item_name → looking_for';
    ELSE
      ALTER TABLE demands DROP COLUMN item_name;
      RAISE NOTICE 'demands: dropped duplicate item_name (looking_for already exists)';
    END IF;
  END IF;
END $$;

-- supplies: some older DBs have "item_name" instead of "goods_name"
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='item_name') THEN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='supplies' AND column_name='goods_name') THEN
      ALTER TABLE supplies RENAME COLUMN item_name TO goods_name;
      RAISE NOTICE 'supplies: renamed item_name → goods_name';
    ELSE
      ALTER TABLE supplies DROP COLUMN item_name;
      RAISE NOTICE 'supplies: dropped duplicate item_name (goods_name already exists)';
    END IF;
  END IF;
END $$;

-- conversations: some older DBs have "user1_id"/"user2_id" instead of "user_one_id"/"user_two_id"
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='conversations' AND column_name='user1_id') THEN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='conversations' AND column_name='user_one_id') THEN
      ALTER TABLE conversations RENAME COLUMN user1_id TO user_one_id;
    ELSE
      ALTER TABLE conversations DROP COLUMN user1_id;
    END IF;
  END IF;
END $$;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='conversations' AND column_name='user2_id') THEN
    IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='conversations' AND column_name='user_two_id') THEN
      ALTER TABLE conversations RENAME COLUMN user2_id TO user_two_id;
    ELSE
      ALTER TABLE conversations DROP COLUMN user2_id;
    END IF;
  END IF;
END $$;
-- Add columns that may be missing from older conversations tables
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS last_message TEXT DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP;
-- ── Chat message migrations (safe, additive) ──────────────────────────────
ALTER TABLE messages ADD COLUMN IF NOT EXISTS message_type   TEXT    NOT NULL DEFAULT 'text';
ALTER TABLE messages ADD COLUMN IF NOT EXISTS media_url      TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS media_type     TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to_id    INTEGER REFERENCES messages(id) ON DELETE SET NULL;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_starred     BOOLEAN DEFAULT false;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_pinned      BOOLEAN DEFAULT false;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS reaction       TEXT;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_edited      BOOLEAN DEFAULT false;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at      TIMESTAMP;
ALTER TABLE messages ADD COLUMN IF NOT EXISTS expires_at     TIMESTAMP;

-- ── Conversation-level settings ────────────────────────────────────────────
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_pinned              BOOLEAN DEFAULT false;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_archived            BOOLEAN DEFAULT false;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS custom_category        TEXT;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS wallpaper              TEXT;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS disappearing_seconds   INTEGER DEFAULT 0;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_muted               BOOLEAN DEFAULT false;



-- ══════════════════════════════════════════════════════════════════════════════
-- COMMUNITY (Reddit-style)
-- ══════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS communities (
  id           SERIAL PRIMARY KEY,
  name         VARCHAR(100) NOT NULL UNIQUE,
  slug         VARCHAR(100) NOT NULL UNIQUE,
  description  TEXT,
  rules        TEXT,
  cover_photo  TEXT,
  icon         TEXT,
  tags         TEXT[],
  member_count INTEGER DEFAULT 0,
  post_count   INTEGER DEFAULT 0,
  type         TEXT NOT NULL DEFAULT 'public', -- public | private | restricted
  owner_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS community_members (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role         TEXT NOT NULL DEFAULT 'member', -- owner | admin | moderator | member
  status       TEXT NOT NULL DEFAULT 'active', -- active | banned | muted | pending
  joined_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (community_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_posts (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  post_type    TEXT NOT NULL DEFAULT 'discussion', -- question | discussion | image | video | poll | link
  title        TEXT NOT NULL,
  body         TEXT,
  media_url    TEXT,
  link_url     TEXT,
  upvotes      INTEGER DEFAULT 0,
  downvotes    INTEGER DEFAULT 0,
  comment_count INTEGER DEFAULT 0,
  is_pinned    BOOLEAN DEFAULT false,
  is_locked    BOOLEAN DEFAULT false,
  flair        TEXT,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS community_votes (
  id           SERIAL PRIMARY KEY,
  post_id      INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  vote         SMALLINT NOT NULL, -- 1 = upvote, -1 = downvote
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_comments (
  id                SERIAL PRIMARY KEY,
  post_id           INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_comment_id INTEGER REFERENCES community_comments(id) ON DELETE CASCADE,
  body              TEXT NOT NULL,
  upvotes           INTEGER DEFAULT 0,
  downvotes         INTEGER DEFAULT 0,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Community-scoped marketplace (buy/sell within a community)
CREATE TABLE IF NOT EXISTS community_listings (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title        TEXT NOT NULL,
  description  TEXT,
  price        NUMERIC(15,2) NOT NULL DEFAULT 0,
  category     TEXT,
  images       TEXT[] DEFAULT '{}',
  status       TEXT NOT NULL DEFAULT 'active', -- active | sold | removed
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_community_listings_comm ON community_listings(community_id, status, created_at);

CREATE INDEX IF NOT EXISTS idx_comm_posts_community ON community_posts(community_id);
CREATE INDEX IF NOT EXISTS idx_comm_posts_user      ON community_posts(user_id);
CREATE INDEX IF NOT EXISTS idx_comm_members_user    ON community_members(user_id);

-- ══════════════════════════════════════════════════════════════════════════════
-- STATUS (WhatsApp-style, 24h)
-- ══════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS statuses (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type         TEXT NOT NULL DEFAULT 'image', -- image | video | text
  media_url    TEXT,
  caption      TEXT,
  bg_color     TEXT,
  privacy      TEXT NOT NULL DEFAULT 'followers', -- public | followers | contacts
  view_count   INTEGER DEFAULT 0,
  expires_at   TIMESTAMP NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS status_views (
  id         SERIAL PRIMARY KEY,
  status_id  INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  viewer_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (status_id, viewer_id)
);

-- ══════════════════════════════════════════════════════════════════════════════
-- WALLET
-- ══════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS wallets (
  id         SERIAL PRIMARY KEY,
  user_id    INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  balance    NUMERIC(18,2) DEFAULT 0.00,
  currency   TEXT NOT NULL DEFAULT 'NGN',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id           SERIAL PRIMARY KEY,
  wallet_id    INTEGER NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
  type         TEXT NOT NULL, -- deposit | withdraw | send | receive | payment | refund
  amount       NUMERIC(18,2) NOT NULL,
  balance_after NUMERIC(18,2) NOT NULL,
  ref          TEXT UNIQUE,
  description  TEXT,
  status       TEXT NOT NULL DEFAULT 'completed', -- pending | completed | failed
  meta         JSONB,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ══════════════════════════════════════════════════════════════════════════════
-- BUSINESS UPGRADE
-- ══════════════════════════════════════════════════════════════════════════════
ALTER TABLE users ADD COLUMN IF NOT EXISTS account_type      TEXT NOT NULL DEFAULT 'personal';
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_name     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_category TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_desc     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_phone    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_email    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_website  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_address  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_country  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_state    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_city     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS selling_types     TEXT[];

-- ══════════════════════════════════════════════════════════════════════════════
-- COMMERCE EXTENSION (services, jobs, hotels, property, vehicles, events)
-- ══════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS services (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  category     TEXT,
  price        NUMERIC(18,2),
  description  TEXT,
  duration     TEXT,
  availability TEXT,
  location     TEXT,
  images       TEXT[],
  is_active    BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS jobs (
  id              SERIAL PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  company         TEXT,
  salary          TEXT,
  employment_type TEXT,
  experience      TEXT,
  qualification   TEXT,
  location        TEXT,
  description     TEXT,
  deadline        DATE,
  apply_link      TEXT,
  apply_in_app    BOOLEAN DEFAULT false,
  is_active       BOOLEAN DEFAULT true,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS hotels (
  id             SERIAL PRIMARY KEY,
  user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  hotel_name     TEXT NOT NULL,
  room_name      TEXT,
  price_per_night NUMERIC(18,2),
  max_guests     INTEGER,
  amenities      TEXT[],
  images         TEXT[],
  available_rooms INTEGER,
  description    TEXT,
  is_active      BOOLEAN DEFAULT true,
  created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS properties (
  id            SERIAL PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  property_type TEXT,
  listing_type  TEXT, -- sale | rent
  bedrooms      INTEGER,
  bathrooms     INTEGER,
  area          TEXT,
  address       TEXT,
  price         NUMERIC(18,2),
  description   TEXT,
  images        TEXT[],
  is_active     BOOLEAN DEFAULT true,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS vehicles (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  brand        TEXT,
  model        TEXT,
  year         INTEGER,
  fuel         TEXT,
  transmission TEXT,
  mileage      TEXT,
  price        NUMERIC(18,2),
  description  TEXT,
  images       TEXT[],
  is_active    BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS events (
  id                 SERIAL PRIMARY KEY,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT NOT NULL,
  event_date         DATE,
  event_time         TEXT,
  venue              TEXT,
  description        TEXT,
  ticket_price       NUMERIC(18,2),
  registration_link  TEXT,
  is_active          BOOLEAN DEFAULT true,
  created_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Notifications
CREATE TABLE IF NOT EXISTS notifications (
  id          SERIAL PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type        TEXT NOT NULL, -- like | comment | follow | message | order | mention | community
  title       TEXT NOT NULL,
  body        TEXT,
  data        JSONB,
  is_read     BOOLEAN DEFAULT false,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id, is_read);

-- ═══════════════════════════════════════════════════════════════════════════
-- COMMUNITIES (Reddit-style)
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS communities (
  id           SERIAL PRIMARY KEY,
  name         VARCHAR(100) NOT NULL UNIQUE,
  slug         VARCHAR(100) NOT NULL UNIQUE,
  description  TEXT,
  rules        TEXT,
  cover_photo  TEXT,
  icon         TEXT,
  tags         TEXT,
  visibility   TEXT NOT NULL DEFAULT 'public', -- public | private | restricted
  owner_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  member_count INTEGER DEFAULT 0,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS community_members (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role         TEXT NOT NULL DEFAULT 'member', -- owner | admin | moderator | member
  joined_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(community_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_posts (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  post_type    TEXT NOT NULL DEFAULT 'discussion', -- question|discussion|image|video|poll|link
  title        TEXT NOT NULL,
  body         TEXT,
  media_url    TEXT,
  link_url     TEXT,
  upvotes      INTEGER DEFAULT 0,
  downvotes    INTEGER DEFAULT 0,
  comment_count INTEGER DEFAULT 0,
  is_pinned    BOOLEAN DEFAULT false,
  is_locked    BOOLEAN DEFAULT false,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS community_votes (
  id           SERIAL PRIMARY KEY,
  post_id      INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  vote         SMALLINT NOT NULL, -- 1 = up, -1 = down
  UNIQUE(post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_comments (
  id                SERIAL PRIMARY KEY,
  post_id           INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_comment_id INTEGER REFERENCES community_comments(id) ON DELETE CASCADE,
  content           TEXT NOT NULL,
  upvotes           INTEGER DEFAULT 0,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_community_posts_community ON community_posts(community_id);
CREATE INDEX IF NOT EXISTS idx_community_members_user    ON community_members(user_id);

-- ═══════════════════════════════════════════════════════════════════════════
-- STATUS (WhatsApp-style, 24h)
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS statuses (
  id          SERIAL PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  media_url   TEXT,
  media_type  TEXT DEFAULT 'image', -- image | video | text
  caption     TEXT,
  bg_color    TEXT,
  privacy     TEXT DEFAULT 'followers', -- public | followers | close_friends
  view_count  INTEGER DEFAULT 0,
  expires_at  TIMESTAMP NOT NULL DEFAULT (NOW() + INTERVAL '24 hours'),
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS status_views (
  id         SERIAL PRIMARY KEY,
  status_id  INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  viewer_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(status_id, viewer_id)
);

CREATE TABLE IF NOT EXISTS status_reactions (
  id         SERIAL PRIMARY KEY,
  status_id  INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reaction   TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(status_id, user_id)
);

-- ═══════════════════════════════════════════════════════════════════════════
-- WALLET
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS wallets (
  id         SERIAL PRIMARY KEY,
  user_id    INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  balance    NUMERIC(15,2) DEFAULT 0.00,
  currency   TEXT DEFAULT 'NGN',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id           SERIAL PRIMARY KEY,
  wallet_id    INTEGER NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
  type         TEXT NOT NULL, -- deposit|withdrawal|transfer_in|transfer_out|payment|refund
  amount       NUMERIC(15,2) NOT NULL,
  balance_after NUMERIC(15,2),
  reference    TEXT UNIQUE,
  description  TEXT,
  status       TEXT DEFAULT 'completed', -- pending|completed|failed
  counterparty_id INTEGER REFERENCES users(id),
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_wallet_txn_wallet ON wallet_transactions(wallet_id);

-- ═══════════════════════════════════════════════════════════════════════════
-- BUSINESS UPGRADE
-- ═══════════════════════════════════════════════════════════════════════════
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_name        TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_category    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_description TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_phone       TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_email       TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_website     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_address     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_country     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_state       TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_city        TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS selling_types        TEXT; -- comma-separated
ALTER TABLE users ADD COLUMN IF NOT EXISTS logo_url             TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS cover_url            TEXT;

-- ═══════════════════════════════════════════════════════════════════════════
-- COMMERCE: extended product types
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS services (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  category     TEXT,
  price        NUMERIC(15,2),
  description  TEXT,
  duration     TEXT,
  availability TEXT,
  location     TEXT,
  media_url    TEXT,
  is_active    BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS jobs (
  id              SERIAL PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  company         TEXT,
  salary          TEXT,
  employment_type TEXT, -- full-time|part-time|contract|freelance|remote
  experience      TEXT,
  qualification   TEXT,
  location        TEXT,
  description     TEXT,
  deadline        DATE,
  apply_link      TEXT,
  is_active       BOOLEAN DEFAULT true,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS hotels (
  id              SERIAL PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  hotel_name      TEXT NOT NULL,
  room_name       TEXT,
  price_per_night NUMERIC(15,2),
  max_guests      INTEGER,
  amenities       TEXT,
  available_rooms INTEGER,
  checkin_time    TEXT,
  checkout_time   TEXT,
  description     TEXT,
  media_url       TEXT,
  is_active       BOOLEAN DEFAULT true,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS properties (
  id            SERIAL PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  property_type TEXT, -- apartment|house|land|commercial
  listing_type  TEXT, -- sale|rent
  bedrooms      INTEGER,
  bathrooms     INTEGER,
  area          TEXT,
  address       TEXT,
  price         NUMERIC(15,2),
  description   TEXT,
  media_url     TEXT,
  is_active     BOOLEAN DEFAULT true,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS vehicles (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  brand        TEXT,
  model        TEXT,
  year         INTEGER,
  fuel         TEXT,
  transmission TEXT,
  mileage      TEXT,
  price        NUMERIC(15,2),
  description  TEXT,
  media_url    TEXT,
  is_active    BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS events (
  id                SERIAL PRIMARY KEY,
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  event_date        DATE,
  event_time        TEXT,
  venue             TEXT,
  description       TEXT,
  ticket_price      NUMERIC(15,2),
  registration_link TEXT,
  media_url         TEXT,
  is_active         BOOLEAN DEFAULT true,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ═══════════════════════════════════════════════════════════════════════════
-- SAFE MIGRATIONS for self-healing boot
-- ═══════════════════════════════════════════════════════════════════════════
ALTER TABLE users ADD COLUMN IF NOT EXISTS account_type TEXT DEFAULT 'personal';

-- ════════════════════════════════════════════════════════════════════════════
-- COMMUNITIES (Reddit-style)
-- ════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS communities (
  id           SERIAL PRIMARY KEY,
  name         TEXT NOT NULL UNIQUE,
  slug         TEXT NOT NULL UNIQUE,
  description  TEXT,
  rules        TEXT,
  cover_photo  TEXT,
  icon         TEXT,
  tags         TEXT[],
  created_by   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  member_count INTEGER DEFAULT 0,
  post_count   INTEGER DEFAULT 0,
  visibility   TEXT NOT NULL DEFAULT 'public', -- public | private | restricted
  is_nsfw      BOOLEAN DEFAULT false,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_communities_slug ON communities(slug);

CREATE TABLE IF NOT EXISTS community_members (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role         TEXT NOT NULL DEFAULT 'member', -- owner | admin | moderator | member
  status       TEXT NOT NULL DEFAULT 'active', -- active | banned | pending
  joined_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (community_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_comm_members_comm ON community_members(community_id);
CREATE INDEX IF NOT EXISTS idx_comm_members_user ON community_members(user_id);

CREATE TABLE IF NOT EXISTS community_posts (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  post_type    TEXT NOT NULL DEFAULT 'discussion', -- discussion | question | image | video | poll | link
  title        TEXT NOT NULL,
  body         TEXT,
  media_url    TEXT,
  link_url     TEXT,
  upvotes      INTEGER DEFAULT 0,
  downvotes    INTEGER DEFAULT 0,
  comment_count INTEGER DEFAULT 0,
  is_pinned    BOOLEAN DEFAULT false,
  is_locked    BOOLEAN DEFAULT false,
  is_approved  BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_comm_posts_comm ON community_posts(community_id);
CREATE INDEX IF NOT EXISTS idx_comm_posts_user ON community_posts(user_id);

CREATE TABLE IF NOT EXISTS community_post_votes (
  id           SERIAL PRIMARY KEY,
  post_id      INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  vote         SMALLINT NOT NULL, -- 1 or -1
  UNIQUE (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_comments (
  id           SERIAL PRIMARY KEY,
  post_id      INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_id    INTEGER REFERENCES community_comments(id) ON DELETE CASCADE,
  body         TEXT NOT NULL,
  upvotes      INTEGER DEFAULT 0,
  downvotes    INTEGER DEFAULT 0,
  is_deleted   BOOLEAN DEFAULT false,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_comm_comments_post ON community_comments(post_id);

-- ════════════════════════════════════════════════════════════════════════════
-- STATUS (WhatsApp/Instagram Stories style)
-- ════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS statuses (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status_type  TEXT NOT NULL DEFAULT 'image', -- image | video | text
  media_url    TEXT,
  text_content TEXT,
  bg_color     TEXT DEFAULT '#1DB954',
  text_color   TEXT DEFAULT '#FFFFFF',
  font_style   TEXT DEFAULT 'normal',
  privacy      TEXT NOT NULL DEFAULT 'followers', -- public | followers | close_friends
  view_count   INTEGER DEFAULT 0,
  expires_at   TIMESTAMP NOT NULL DEFAULT (CURRENT_TIMESTAMP + INTERVAL '24 hours'),
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_statuses_user   ON statuses(user_id);
CREATE INDEX IF NOT EXISTS idx_statuses_expiry ON statuses(expires_at);

CREATE TABLE IF NOT EXISTS status_views (
  id          SERIAL PRIMARY KEY,
  status_id   INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  viewer_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (status_id, viewer_id)
);

CREATE TABLE IF NOT EXISTS status_reactions (
  id          SERIAL PRIMARY KEY,
  status_id   INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reaction    TEXT NOT NULL,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (status_id, user_id)
);

-- ════════════════════════════════════════════════════════════════════════════
-- WALLET
-- ════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS wallets (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  balance      NUMERIC(15,2) DEFAULT 0.00,
  currency     TEXT NOT NULL DEFAULT 'NGN',
  is_frozen    BOOLEAN DEFAULT false,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id             SERIAL PRIMARY KEY,
  wallet_id      INTEGER NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
  type           TEXT NOT NULL, -- deposit | withdrawal | send | receive | payment | refund | escrow
  amount         NUMERIC(15,2) NOT NULL,
  balance_after  NUMERIC(15,2) NOT NULL,
  reference      TEXT UNIQUE,
  description    TEXT,
  status         TEXT NOT NULL DEFAULT 'pending', -- pending | completed | failed | reversed
  counterparty_id INTEGER REFERENCES users(id),
  related_order_id INTEGER,
  created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_wallet_txn_wallet ON wallet_transactions(wallet_id);
CREATE INDEX IF NOT EXISTS idx_wallet_txn_ref    ON wallet_transactions(reference);

-- ════════════════════════════════════════════════════════════════════════════
-- COMMERCE — extended post types for Business accounts
-- ════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS commerce_products (
  id              SERIAL PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  category        TEXT,
  brand           TEXT,
  description     TEXT,
  price           NUMERIC(15,2) NOT NULL DEFAULT 0,
  discount_price  NUMERIC(15,2),
  stock           INTEGER DEFAULT 0,
  sku             TEXT,
  condition       TEXT DEFAULT 'new', -- new | used | refurbished
  delivery        BOOLEAN DEFAULT true,
  images          TEXT[],
  videos          TEXT[],
  tags            TEXT[],
  is_active       BOOLEAN DEFAULT true,
  view_count      INTEGER DEFAULT 0,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_products_user ON commerce_products(user_id);

CREATE TABLE IF NOT EXISTS commerce_services (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         TEXT NOT NULL,
  category     TEXT,
  price        NUMERIC(15,2),
  description  TEXT,
  duration     TEXT,
  availability TEXT,
  location     TEXT,
  images       TEXT[],
  is_active    BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS commerce_jobs (
  id               SERIAL PRIMARY KEY,
  user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title            TEXT NOT NULL,
  company          TEXT,
  salary           TEXT,
  employment_type  TEXT, -- full-time | part-time | contract | freelance | internship
  experience       TEXT,
  qualification    TEXT,
  location         TEXT,
  description      TEXT,
  deadline         DATE,
  apply_link       TEXT,
  apply_in_app     BOOLEAN DEFAULT false,
  is_active        BOOLEAN DEFAULT true,
  created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS commerce_hotels (
  id              SERIAL PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  hotel_name      TEXT NOT NULL,
  room_name       TEXT,
  price_per_night NUMERIC(15,2),
  max_guests      INTEGER,
  amenities       TEXT[],
  images          TEXT[],
  available_rooms INTEGER DEFAULT 1,
  checkin_time    TEXT,
  checkout_time   TEXT,
  description     TEXT,
  location        TEXT,
  is_active       BOOLEAN DEFAULT true,
  created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS commerce_properties (
  id            SERIAL PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  property_type TEXT, -- house | apartment | land | commercial | office
  listing_type  TEXT, -- sale | rent
  bedrooms      INTEGER,
  bathrooms     INTEGER,
  area          TEXT,
  address       TEXT,
  price         NUMERIC(15,2),
  description   TEXT,
  images        TEXT[],
  is_active     BOOLEAN DEFAULT true,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS commerce_vehicles (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  brand        TEXT,
  model        TEXT,
  year         INTEGER,
  fuel         TEXT, -- petrol | diesel | electric | hybrid
  transmission TEXT, -- manual | automatic
  mileage      TEXT,
  price        NUMERIC(15,2),
  description  TEXT,
  images       TEXT[],
  is_active    BOOLEAN DEFAULT true,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS commerce_events (
  id                SERIAL PRIMARY KEY,
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name              TEXT NOT NULL,
  event_date        DATE,
  event_time        TEXT,
  venue             TEXT,
  description       TEXT,
  ticket_price      NUMERIC(15,2) DEFAULT 0,
  registration_link TEXT,
  images            TEXT[],
  is_active         BOOLEAN DEFAULT true,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ════════════════════════════════════════════════════════════════════════════
-- ORDERS (buying products/services/tickets)
-- ════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS orders (
  id           SERIAL PRIMARY KEY,
  buyer_id     INTEGER NOT NULL REFERENCES users(id),
  seller_id    INTEGER NOT NULL REFERENCES users(id),
  item_type    TEXT NOT NULL, -- product | service | hotel | event | vehicle | property
  item_id      INTEGER NOT NULL,
  quantity     INTEGER DEFAULT 1,
  amount       NUMERIC(15,2) NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending', -- pending | confirmed | shipped | delivered | cancelled | refunded
  payment_ref  TEXT,
  notes        TEXT,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_orders_buyer  ON orders(buyer_id);
CREATE INDEX IF NOT EXISTS idx_orders_seller ON orders(seller_id);

-- ════════════════════════════════════════════════════════════════════════════
-- NOTIFICATIONS
-- ════════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS notifications (
  id           SERIAL PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  actor_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
  type         TEXT NOT NULL, -- like | comment | follow | message | order | mention | community_post | status_reply
  title        TEXT NOT NULL,
  body         TEXT,
  ref_type     TEXT, -- post | comment | order | community_post | status
  ref_id       INTEGER,
  is_read      BOOLEAN DEFAULT false,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_notifs_user    ON notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_notifs_unread  ON notifications(user_id, is_read);

-- ════════════════════════════════════════════════════════════════════════════
-- BUSINESS PROFILE EXTENSION
-- ════════════════════════════════════════════════════════════════════════════
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_category TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_name     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_desc     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_phone    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_email    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_website  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_address  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_country  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_state    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_city     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS selling_types     TEXT[]; -- products | services | jobs | events | hotel | property | vehicles
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_verified       BOOLEAN DEFAULT false;

-- ═══════════════════════════════════════════════════════════════════════════
-- COMMUNITY (Reddit-style)
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS communities (
  id           SERIAL PRIMARY KEY,
  name         VARCHAR(80) UNIQUE NOT NULL,
  slug         VARCHAR(80) UNIQUE NOT NULL,
  description  TEXT,
  rules        TEXT,
  cover_photo  TEXT,
  icon         TEXT,
  tags         TEXT[],
  visibility   TEXT NOT NULL DEFAULT 'public', -- public | private | restricted
  member_count INTEGER DEFAULT 0,
  owner_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS community_members (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role         TEXT NOT NULL DEFAULT 'member', -- owner | admin | moderator | member
  joined_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (community_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_posts (
  id           SERIAL PRIMARY KEY,
  community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  post_type    TEXT NOT NULL DEFAULT 'discussion', -- question|discussion|image|video|poll|link
  title        TEXT NOT NULL,
  body         TEXT,
  media_url    TEXT,
  link_url     TEXT,
  upvotes      INTEGER DEFAULT 0,
  downvotes    INTEGER DEFAULT 0,
  comment_count INTEGER DEFAULT 0,
  is_pinned    BOOLEAN DEFAULT false,
  is_locked    BOOLEAN DEFAULT false,
  created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS community_votes (
  id           SERIAL PRIMARY KEY,
  post_id      INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  vote         SMALLINT NOT NULL, -- 1 or -1
  UNIQUE (post_id, user_id)
);

CREATE TABLE IF NOT EXISTS community_comments (
  id                SERIAL PRIMARY KEY,
  post_id           INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
  user_id           INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  parent_comment_id INTEGER REFERENCES community_comments(id) ON DELETE CASCADE,
  body              TEXT NOT NULL,
  upvotes           INTEGER DEFAULT 0,
  created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_community_posts_comm ON community_posts(community_id);
CREATE INDEX IF NOT EXISTS idx_community_members_uid ON community_members(user_id);

-- ═══════════════════════════════════════════════════════════════════════════
-- STATUS (WhatsApp-style, 24h)
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS statuses (
  id         SERIAL PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  type       TEXT NOT NULL DEFAULT 'image', -- image | video | text
  media_url  TEXT,
  text       TEXT,
  bg_color   TEXT,
  privacy    TEXT NOT NULL DEFAULT 'followers', -- all | followers | contacts
  view_count INTEGER DEFAULT 0,
  expires_at TIMESTAMP NOT NULL DEFAULT (CURRENT_TIMESTAMP + INTERVAL '24 hours'),
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS status_views (
  id         SERIAL PRIMARY KEY,
  status_id  INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  viewer_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  viewed_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (status_id, viewer_id)
);

CREATE TABLE IF NOT EXISTS status_reactions (
  id         SERIAL PRIMARY KEY,
  status_id  INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  reaction   TEXT NOT NULL,
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (status_id, user_id)
);

-- ═══════════════════════════════════════════════════════════════════════════
-- WALLET
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS wallets (
  id         SERIAL PRIMARY KEY,
  user_id    INTEGER UNIQUE NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  balance    NUMERIC(15,2) DEFAULT 0,
  currency   TEXT DEFAULT 'NGN',
  created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS wallet_transactions (
  id          SERIAL PRIMARY KEY,
  wallet_id   INTEGER NOT NULL REFERENCES wallets(id) ON DELETE CASCADE,
  type        TEXT NOT NULL, -- credit | debit | transfer | refund | escrow
  amount      NUMERIC(15,2) NOT NULL,
  description TEXT,
  reference   TEXT UNIQUE,
  status      TEXT DEFAULT 'completed', -- pending | completed | failed
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ═══════════════════════════════════════════════════════════════════════════
-- BUSINESS UPGRADE
-- ═══════════════════════════════════════════════════════════════════════════
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_category TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_name     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_desc     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_phone    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_email    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_website  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_address  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_country  TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_state    TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS business_city     TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS selling_types     TEXT[];

-- ═══════════════════════════════════════════════════════════════════════════
-- COMMERCE: extended product types
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS commerce_listings (
  id            SERIAL PRIMARY KEY,
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  listing_type  TEXT NOT NULL, -- product|service|job|hotel|property|vehicle|event
  title         TEXT NOT NULL,
  description   TEXT,
  price         NUMERIC(15,2),
  discount_price NUMERIC(15,2),
  currency      TEXT DEFAULT 'NGN',
  category      TEXT,
  brand         TEXT,
  condition     TEXT,
  stock         INTEGER,
  sku           TEXT,
  delivery_available BOOLEAN DEFAULT false,
  location      TEXT,
  images        TEXT[],
  video_url     TEXT,
  metadata      JSONB DEFAULT '{}',
  status        TEXT DEFAULT 'active', -- active | sold | closed | draft
  views         INTEGER DEFAULT 0,
  created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_commerce_type    ON commerce_listings(listing_type);
CREATE INDEX IF NOT EXISTS idx_commerce_user    ON commerce_listings(user_id);
CREATE INDEX IF NOT EXISTS idx_commerce_status  ON commerce_listings(status);

-- ═══════════════════════════════════════════════════════════════════════════
-- NOTIFICATIONS
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS notifications (
  id          SERIAL PRIMARY KEY,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  actor_id    INTEGER REFERENCES users(id) ON DELETE SET NULL,
  type        TEXT NOT NULL, -- like|comment|follow|message|mention|order|wallet|community
  title       TEXT NOT NULL,
  body        TEXT,
  entity_type TEXT,
  entity_id   INTEGER,
  is_read     BOOLEAN DEFAULT false,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_notif_user ON notifications(user_id, is_read);

-- ═══════════════════════════════════════════════════════════════════════════
-- ORDERS
-- ═══════════════════════════════════════════════════════════════════════════
CREATE TABLE IF NOT EXISTS orders (
  id          SERIAL PRIMARY KEY,
  buyer_id    INTEGER NOT NULL REFERENCES users(id),
  seller_id   INTEGER NOT NULL REFERENCES users(id),
  listing_id  INTEGER REFERENCES commerce_listings(id),
  quantity    INTEGER DEFAULT 1,
  amount      NUMERIC(15,2) NOT NULL,
  status      TEXT DEFAULT 'pending', -- pending|paid|shipped|delivered|cancelled|refunded
  address     TEXT,
  notes       TEXT,
  created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- ═══════════════════════════════════════════════════════════════════════════
-- CONTACT SYNCING (Phase 1)
-- ═══════════════════════════════════════════════════════════════════════════
ALTER TABLE users ADD COLUMN IF NOT EXISTS contact_sync_enabled BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE users ADD COLUMN IF NOT EXISTS contact_sync_at TIMESTAMP;

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
