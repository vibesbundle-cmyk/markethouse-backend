-- ============================================================
-- MARKETHOUSE — FULL-TEXT SEARCH MIGRATION
-- Adds tsvector columns and GIN indexes for PostgreSQL FTS
-- Run this whole file at any time. Existing columns/indexes are preserved.
-- ============================================================

SET client_min_messages = WARNING;

-- ── Enable pg_trgm for fuzzy/trigram search ──────────────────
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS unaccent;

-- ── Helper: safe column add ───────────────────────────────────
DO $$
BEGIN
    -- POSTS: add search_vector if not exists
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name='posts' AND column_name='search_vector'
    ) THEN
        ALTER TABLE posts ADD COLUMN search_vector tsvector;
        RAISE NOTICE 'Added search_vector to posts';
    END IF;
END $$;

-- ── POSTS: tsvector from caption + location + category + hashtags ─────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes 
        WHERE tablename='posts' AND indexname='idx_posts_search_vector'
    ) THEN
        CREATE INDEX idx_posts_search_vector ON posts USING GIN (search_vector);
        RAISE NOTICE 'Created idx_posts_search_vector';
    END IF;
END $$;

-- ── USERS: add search_vector ────────────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name='users' AND column_name='search_vector'
    ) THEN
        ALTER TABLE users ADD COLUMN search_vector tsvector;
        RAISE NOTICE 'Added search_vector to users';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes 
        WHERE tablename='users' AND indexname='idx_users_search_vector'
    ) THEN
        CREATE INDEX idx_users_search_vector ON users USING GIN (search_vector);
        RAISE NOTICE 'Created idx_users_search_vector';
    END IF;
END $$;

-- ── SUPPLIES: add search_vector ────────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name='supplies' AND column_name='search_vector'
    ) THEN
        ALTER TABLE supplies ADD COLUMN search_vector tsvector;
        RAISE NOTICE 'Added search_vector to supplies';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes 
        WHERE tablename='supplies' AND indexname='idx_supplies_search_vector'
    ) THEN
        CREATE INDEX idx_supplies_search_vector ON supplies USING GIN (search_vector);
        RAISE NOTICE 'Created idx_supplies_search_vector';
    END IF;
END $$;

-- ── DEMANDS: add search_vector ─────────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name='demands' AND column_name='search_vector'
    ) THEN
        ALTER TABLE demands ADD COLUMN search_vector tsvector;
        RAISE NOTICE 'Added search_vector to demands';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes 
        WHERE tablename='demands' AND indexname='idx_demands_search_vector'
    ) THEN
        CREATE INDEX idx_demands_search_vector ON demands USING GIN (search_vector);
        RAISE NOTICE 'Created idx_demands_search_vector';
    END IF;
END $$;

-- ── PRODUCTS: add search_vector ────────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name='products' AND column_name='search_vector'
    ) THEN
        ALTER TABLE products ADD COLUMN search_vector tsvector;
        RAISE NOTICE 'Added search_vector to products';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes 
        WHERE tablename='products' AND indexname='idx_products_search_vector'
    ) THEN
        CREATE INDEX idx_products_search_vector ON products USING GIN (search_vector);
        RAISE NOTICE 'Created idx_products_search_vector';
    END IF;
END $$;

-- ── COMMUNITIES: add search_vector ─────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name='communities' AND column_name='search_vector'
    ) THEN
        ALTER TABLE communities ADD COLUMN search_vector tsvector;
        RAISE NOTICE 'Added search_vector to communities';
    END IF;
END $$;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_indexes 
        WHERE tablename='communities' AND indexname='idx_communities_search_vector'
    ) THEN
        CREATE INDEX idx_communities_search_vector ON communities USING GIN (search_vector);
        RAISE NOTICE 'Created idx_communities_search_vector';
    END IF;
END $$;

-- ── Triggers to keep search_vector updated ─────────────────────
-- POSTS
CREATE OR REPLACE FUNCTION posts_search_vector_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', COALESCE(NEW.caption, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.location, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.category, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.audience, '')), 'C');
    RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS posts_search_vector_trigger ON posts;
CREATE TRIGGER posts_search_vector_trigger
BEFORE INSERT OR UPDATE ON posts
FOR EACH ROW EXECUTE FUNCTION posts_search_vector_update();

-- Backfill existing posts
UPDATE posts SET search_vector = 
    setweight(to_tsvector('english', COALESCE(caption, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(location, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(category, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(audience, '')), 'C')
WHERE search_vector IS NULL;

-- USERS
CREATE OR REPLACE FUNCTION users_search_vector_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', COALESCE(NEW.username, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.full_name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.bio, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.business_name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.business_category, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.business_desc, '')), 'B');
    RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS users_search_vector_trigger ON users;
CREATE TRIGGER users_search_vector_trigger
BEFORE INSERT OR UPDATE ON users
FOR EACH ROW EXECUTE FUNCTION users_search_vector_update();

UPDATE users SET search_vector = 
    setweight(to_tsvector('english', COALESCE(username, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(full_name, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(bio, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(business_name, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(business_category, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(business_desc, '')), 'B')
WHERE search_vector IS NULL;

-- SUPPLIES
CREATE OR REPLACE FUNCTION supplies_search_vector_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', COALESCE(NEW.goods_name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.description, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.category, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.brand, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.condition, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(NEW.location, '')), 'C');
    RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS supplies_search_vector_trigger ON supplies;
CREATE TRIGGER supplies_search_vector_trigger
BEFORE INSERT OR UPDATE ON supplies
FOR EACH ROW EXECUTE FUNCTION supplies_search_vector_update();

UPDATE supplies SET search_vector = 
    setweight(to_tsvector('english', COALESCE(goods_name, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(description, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(category, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(brand, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(condition, '')), 'C') ||
    setweight(to_tsvector('english', COALESCE(location, '')), 'C')
WHERE search_vector IS NULL;

-- DEMANDS
CREATE OR REPLACE FUNCTION demands_search_vector_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', COALESCE(NEW.looking_for, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.description, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.category, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.location, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(NEW.condition_pref[1], '')), 'C');
    RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS demands_search_vector_trigger ON demands;
CREATE TRIGGER demands_search_vector_trigger
BEFORE INSERT OR UPDATE ON demands
FOR EACH ROW EXECUTE FUNCTION demands_search_vector_update();

UPDATE demands SET search_vector = 
    setweight(to_tsvector('english', COALESCE(looking_for, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(description, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(category, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(location, '')), 'C')
WHERE search_vector IS NULL;

-- PRODUCTS
CREATE OR REPLACE FUNCTION products_search_vector_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', COALESCE(NEW.name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.description, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.category, '')), 'B');
    RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS products_search_vector_trigger ON products;
CREATE TRIGGER products_search_vector_trigger
BEFORE INSERT OR UPDATE ON products
FOR EACH ROW EXECUTE FUNCTION products_search_vector_update();

UPDATE products SET search_vector = 
    setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(description, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(category, '')), 'B')
WHERE search_vector IS NULL;

-- COMMUNITIES
CREATE OR REPLACE FUNCTION communities_search_vector_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.search_vector := 
        setweight(to_tsvector('english', COALESCE(NEW.name, '')), 'A') ||
        setweight(to_tsvector('english', COALESCE(NEW.description, '')), 'B') ||
        setweight(to_tsvector('english', COALESCE(NEW.rules, '')), 'C') ||
        setweight(to_tsvector('english', COALESCE(array_to_string(NEW.tags, ' '), '')), 'B');
    RETURN NEW;
END$$;

DROP TRIGGER IF EXISTS communities_search_vector_trigger ON communities;
CREATE TRIGGER communities_search_vector_trigger
BEFORE INSERT OR UPDATE ON communities
FOR EACH ROW EXECUTE FUNCTION communities_search_vector_update();

UPDATE communities SET search_vector = 
    setweight(to_tsvector('english', COALESCE(name, '')), 'A') ||
    setweight(to_tsvector('english', COALESCE(description, '')), 'B') ||
    setweight(to_tsvector('english', COALESCE(rules, '')), 'C')
WHERE search_vector IS NULL;

-- ── Trigram indexes for fuzzy prefix search (autocomplete) ─────
CREATE INDEX IF NOT EXISTS idx_users_username_trgm ON users USING GIN (username gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_users_fullname_trgm ON users USING GIN (full_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_posts_caption_trgm ON posts USING GIN (caption gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_supplies_goods_name_trgm ON supplies USING GIN (goods_name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_products_name_trgm ON products USING GIN (name gin_trgm_ops);
CREATE INDEX IF NOT EXISTS idx_communities_name_trgm ON communities USING GIN (name gin_trgm_ops);

-- ── View for unified search results (optional, for admin/debug) ─────
CREATE OR REPLACE VIEW search_stats AS
SELECT 'posts' AS table_name, COUNT(*) AS total, COUNT(search_vector) AS indexed
FROM posts
UNION ALL
SELECT 'users', COUNT(*), COUNT(search_vector) FROM users
UNION ALL
SELECT 'supplies', COUNT(*), COUNT(search_vector) FROM supplies
UNION ALL
SELECT 'demands', COUNT(*), COUNT(search_vector) FROM demands
UNION ALL
SELECT 'products', COUNT(*), COUNT(search_vector) FROM products
UNION ALL
SELECT 'communities', COUNT(*), COUNT(search_vector) FROM communities;