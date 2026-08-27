package database

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/lib/pq"
)

func ConnectPostgres() (*sql.DB, error) {
	// Hosted providers (Render, etc.) pass a full connection URL.
	if url := os.Getenv("DATABASE_URL"); url != "" {
		if !strings.Contains(url, "sslmode=") {
			sep := "?"
			if strings.Contains(url, "?") {
				sep = "&"
			}
			url += sep + "sslmode=require"
		}
		db, err := sql.Open("postgres", url)
		if err != nil {
			return nil, err
		}
		return postgresWithMigrations(db)
	}

	host := getEnv("DB_HOST", "localhost")
	port := getEnv("DB_PORT", "5432")
	user := getEnv("DB_USER", "postgres")
	password := getEnv("DB_PASSWORD", "")
	dbname := getEnv("DB_NAME", "markethouse")
	sslmode := getEnv("DB_SSLMODE", "disable")

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, port, user, password, dbname, sslmode,
	)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	return postgresWithMigrations(db)
}

func postgresWithMigrations(db *sql.DB) (*sql.DB, error) {
	err := db.Ping()
	if err != nil {
		return nil, err
	}

	// ensureBaseSchema runs vinci.sql (embedded) on a brand-new database so
	// the base tables (users, posts, comments, ...) exist before the other
	// migrations touch them. On databases that already have the schema it's
	// a no-op. Without this, a fresh Render/CI database has no base tables
	// and every migration fails with "relation does not exist".
	if err := ensureBaseSchema(db); err != nil {
		return nil, fmt.Errorf("base schema (vinci.sql) failed: %w", err)
	}

	// runNewFeatureMigrations MUST run first: it CREATEs the communities/
	// community_posts/commerce_listings/... tables. runSelfHealingMigrations
	// only ever ALTERs/adds columns to tables that already exist (community
	// posts' is_locked, poll_ends_at, etc., commerce_listings.quality_score),
	// so on a fresh database it would fail with "relation does not exist"
	// if it ran before the tables it's altering were created.
	if err := runNewFeatureMigrations(db); err != nil {
		log.Printf("warning: new-feature migration: %v", err)
	}
	if err := runSelfHealingMigrations(db); err != nil {
		return nil, fmt.Errorf("schema migration failed: %w", err)
	}

	return db, nil
}

// runSelfHealingMigrations applies small, additive, idempotent schema
// changes on every boot (IF NOT EXISTS everywhere) so the running server
// never 500s because someone forgot to run vinci.sql by hand after a
// feature was added. It only ever adds columns/tables — never drops or
// rewrites existing data.
// ensureBaseSchema applies the embedded vinci.sql on every boot, one statement
// at a time. vinci.sql is the project's idempotent schema script ("run this
// whole file at any time") — every CREATE TABLE is IF NOT EXISTS and every
// ALTER is ADD COLUMN IF NOT EXISTS, so re-running it is safe. It must be
// executed statement-by-statement rather than as one script: the file is
// versioned and contains multiple conflicting legacy definitions of the same
// table (three `orders`, five `wallet_transactions`, ...). Whichever one
// executes first wins, so later statements that reference columns that only
// exist in the later definition fail with "column does not exist". Those are
// expected on a fresh database; we log them as warnings and continue, then the
// app's canonical migrations (runNewFeatureMigrations + runSelfHealingMigrations)
// converge the schema. Statements are split with splitSQLStatements, which is
// quote/dollar-quote aware so DO $$...$$ blocks and string literals survive.
func ensureBaseSchema(db *sql.DB) error {
	for _, stmt := range splitSQLStatements(vinciSchema) {
		if _, err := db.Exec(stmt); err != nil {
			log.Printf("warning: vinci.sql statement skipped: %v — %.120s", err, stmt)
		}
	}
	return nil
}

// splitSQLStatements splits a SQL script into individual statements at
// top-level semicolons, ignoring semicolons inside single/double-quoted
// strings, dollar-quoted blocks ($$...$$, $tag$...$tag$), and comments.
func splitSQLStatements(src string) []string {
	var out []string
	var cur strings.Builder
	i, n := 0, len(src)
	// state: normal | single | double | linecomment | blockcomment | dollar
	state := "normal"
	var tag string

	for i < n {
		c := src[i]
		var next byte
		if i+1 < n {
			next = src[i+1]
		}

		switch state {
		case "normal":
			switch {
			case c == '-' && next == '-':
				state = "linecomment"
				cur.WriteString("--")
				i += 2
			case c == '/' && next == '*':
				state = "blockcomment"
				cur.WriteString("/*")
				i += 2
			case c == '\'':
				state = "single"
				cur.WriteByte(c)
				i++
			case c == '"':
				state = "double"
				cur.WriteByte(c)
				i++
			case c == '$':
				if t, ok := readDollarTag(src[i:]); ok {
					state = "dollar"
					tag = t
					cur.WriteString(t)
					i += len(t)
				} else {
					cur.WriteByte(c)
					i++
				}
			case c == ';':
				out = append(out, strings.TrimSpace(cur.String()))
				cur.Reset()
				i++
			default:
				cur.WriteByte(c)
				i++
			}

		case "single":
			cur.WriteByte(c)
			if c == '\'' {
				if next == '\'' {
					cur.WriteByte('\'')
					i += 2
				} else {
					state = "normal"
					i++
				}
			} else {
				i++
			}

		case "double":
			cur.WriteByte(c)
			if c == '"' {
				if next == '"' {
					cur.WriteByte('"')
					i += 2
				} else {
					state = "normal"
					i++
				}
			} else {
				i++
			}

		case "linecomment":
			cur.WriteByte(c)
			if c == '\n' {
				state = "normal"
			}
			i++

		case "blockcomment":
			cur.WriteByte(c)
			if c == '*' && next == '/' {
				cur.WriteByte('/')
				i += 2
				state = "normal"
			} else {
				i++
			}

		case "dollar":
			if strings.HasPrefix(src[i:], tag) {
				cur.WriteString(tag)
				i += len(tag)
				state = "normal"
			} else {
				cur.WriteByte(c)
				i++
			}
		}
	}

	if trimmed := strings.TrimSpace(cur.String()); trimmed != "" {
		out = append(out, trimmed)
	}
	return out
}

// readDollarTag returns the full dollar-quote delimiter at the start of s
// ($, $body$, ...) and whether one was found. `$1`-style parameter markers are
// NOT treated as dollar quotes.
func readDollarTag(s string) (string, bool) {
	if len(s) < 2 || s[0] != '$' {
		return "", false
	}
	for j := 1; j < len(s); j++ {
		c := s[j]
		if c == '$' {
			return s[:j+1], true
		}
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return "", false
		}
	}
	return "", false
}

func runSelfHealingMigrations(db *sql.DB) error {
	stmts := []string{
		// comments thread + likes
		`ALTER TABLE comments ADD COLUMN IF NOT EXISTS parent_comment_id INTEGER NULL REFERENCES comments(id) ON DELETE CASCADE`,
		`CREATE INDEX IF NOT EXISTS idx_comments_parent_id ON comments(parent_comment_id)`,
		`CREATE TABLE IF NOT EXISTS comment_likes (
			id         SERIAL PRIMARY KEY,
			comment_id INTEGER NOT NULL,
			user_id    INTEGER NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (comment_id) REFERENCES comments(id) ON DELETE CASCADE,
			FOREIGN KEY (user_id)    REFERENCES users(id)    ON DELETE CASCADE,
			UNIQUE (comment_id, user_id)
		)`,
		// community extras
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS category TEXT`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS post_count INTEGER DEFAULT 0`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS username TEXT`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS marketplace_enabled BOOLEAN DEFAULT false`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_communities_username ON communities(username) WHERE username IS NOT NULL AND username <> ''`,
		`ALTER TABLE community_members ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active'`,
		`ALTER TABLE community_members ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'member'`,
		`ALTER TABLE community_posts ADD COLUMN IF NOT EXISTS is_locked BOOLEAN DEFAULT false`,
		`ALTER TABLE community_posts ADD COLUMN IF NOT EXISTS views INTEGER DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS community_post_views (
			id SERIAL PRIMARY KEY,
			post_id INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
			viewer_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			viewed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(post_id, viewer_id)
		)`,

		// polls
		`ALTER TABLE community_posts ADD COLUMN IF NOT EXISTS poll_ends_at TIMESTAMP`,
		`ALTER TABLE community_posts ADD COLUMN IF NOT EXISTS poll_multiple BOOLEAN DEFAULT false`,
		`ALTER TABLE community_posts ADD COLUMN IF NOT EXISTS poll_anonymous BOOLEAN DEFAULT false`,
		`CREATE TABLE IF NOT EXISTS community_poll_options (
			id          SERIAL PRIMARY KEY,
			post_id     INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
			option_text TEXT NOT NULL,
			position    INTEGER NOT NULL DEFAULT 0,
			vote_count  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS community_poll_votes (
			id        SERIAL PRIMARY KEY,
			post_id   INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
			option_id INTEGER NOT NULL REFERENCES community_poll_options(id) ON DELETE CASCADE,
			user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE(option_id, user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_poll_options_post ON community_poll_options(post_id)`,

		// questions / best answer
		`ALTER TABLE community_posts ADD COLUMN IF NOT EXISTS best_answer_id INTEGER`,
		`ALTER TABLE community_comments ADD COLUMN IF NOT EXISTS is_best_answer BOOLEAN DEFAULT false`,

		// reputation
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS reputation INTEGER NOT NULL DEFAULT 0`,

		`CREATE TABLE IF NOT EXISTS community_comment_likes (
			id SERIAL PRIMARY KEY,
			comment_id INTEGER NOT NULL REFERENCES community_comments(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			UNIQUE(comment_id, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS community_votes (
			id SERIAL PRIMARY KEY,
			post_id INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			vote SMALLINT NOT NULL,
			UNIQUE(post_id,user_id)
		)`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS message_type TEXT NOT NULL DEFAULT 'text'`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS media_url    TEXT`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS media_type   TEXT`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS reply_to_id  INTEGER REFERENCES messages(id) ON DELETE SET NULL`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_starred   BOOLEAN DEFAULT false`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_pinned    BOOLEAN DEFAULT false`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS reaction     TEXT`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS is_edited    BOOLEAN DEFAULT false`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS edited_at    TIMESTAMP`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS expires_at   TIMESTAMP`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS receiver_id  INTEGER NOT NULL DEFAULT 0`,
		// location-share messages (message_type='location') carry a point
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS latitude  DOUBLE PRECISION`,
		`ALTER TABLE messages ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION`,
		// conversation settings
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_pinned            BOOLEAN DEFAULT false`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_archived          BOOLEAN DEFAULT false`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS custom_category      TEXT`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS wallpaper            TEXT`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS wallpaper_color      TEXT DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS wallpaper_dim        REAL DEFAULT 0.3`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS bubble_color         TEXT DEFAULT ''`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS bubble_opacity       REAL DEFAULT 1`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS disappearing_seconds INTEGER DEFAULT 0`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_muted             BOOLEAN DEFAULT false`,
		// per-user "clear chat" markers — history/unread counts hide everything
		// older than the caller's own marker
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS cleared_at_one TIMESTAMPTZ`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS cleared_at_two TIMESTAMPTZ`,
		// per-user "hide/delete chat" markers — hidden conversations are
		// excluded from the conversation list for that user only
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS hidden_at_one TIMESTAMPTZ`,
		`ALTER TABLE conversations ADD COLUMN IF NOT EXISTS hidden_at_two TIMESTAMPTZ`,
		// status custom audience: CSV of user ids for privacy='custom'
		`ALTER TABLE statuses ADD COLUMN IF NOT EXISTS custom_ids TEXT NOT NULL DEFAULT ''`,
		// status reshare attribution: which status this was reshared from,
		// plus a snapshot of the original author (survives original expiry).
		`ALTER TABLE statuses ADD COLUMN IF NOT EXISTS reshared_from_id      INTEGER`,
		`ALTER TABLE statuses ADD COLUMN IF NOT EXISTS reshared_from_user_id INTEGER`,
		`ALTER TABLE statuses ADD COLUMN IF NOT EXISTS reshared_from_username TEXT NOT NULL DEFAULT ''`,
		// user can hide their name on other people's reshares of their
		// statuses — feed then serves the origin as anonymous (default on)
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS hide_status_credit BOOLEAN NOT NULL DEFAULT false`,

		// communities created before "created_by"/"visibility" existed (old
		// schema used "owner_id"/"type") — add the columns the handlers use
		// and backfill them from whichever old columns are actually present.
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'public'`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS created_by INTEGER REFERENCES users(id) ON DELETE CASCADE`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS tags TEXT[] DEFAULT '{}'`,
		`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='communities' AND column_name='owner_id') THEN
				UPDATE communities SET created_by = owner_id WHERE created_by IS NULL;
				-- vinci.sql's legacy definition made owner_id NOT NULL with no
				-- default, but the create handler only writes created_by — so
				-- INSERTs would fail on fresh databases. Make it nullable.
				ALTER TABLE communities ALTER COLUMN owner_id DROP NOT NULL;
			END IF;
			IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='communities' AND column_name='type') THEN
				UPDATE communities SET visibility = type WHERE type IS NOT NULL;
			END IF;
		END $$;`,

		// posts: a post can now hold several mixed photos/videos. posts.media_url
		// / media_type still mirror the first item for backward compatibility.
		`CREATE TABLE IF NOT EXISTS post_media (
			id         SERIAL PRIMARY KEY,
			post_id    INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			media_url  TEXT NOT NULL,
			media_type TEXT NOT NULL,
			position   INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_post_media_post_id ON post_media(post_id)`,
		`INSERT INTO post_media (post_id, media_url, media_type, position)
			SELECT p.id, p.media_url, p.media_type, 0 FROM posts p
			WHERE p.media_url <> ''
			AND NOT EXISTS (SELECT 1 FROM post_media pm WHERE pm.post_id = p.id)`,

		// notifications created before actor_id/entity_type/entity_id existed
		`ALTER TABLE notifications ADD COLUMN IF NOT EXISTS actor_id    INTEGER REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE notifications ADD COLUMN IF NOT EXISTS entity_type TEXT`,
		`ALTER TABLE notifications ADD COLUMN IF NOT EXISTS entity_id   INTEGER`,

		// ── Recommendation Engine ─────────────────────────────────────────────────
		// Post category (required for recommendation routing)
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS category TEXT DEFAULT 'Other'`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS quality_score NUMERIC(10,2) DEFAULT 0`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS distribution_stage INTEGER DEFAULT 1`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS views INTEGER NOT NULL DEFAULT 0`,
		// posts: post_repo.go and interaction_repo.go SELECT/INSERT p.tagged_users,
		// but no migration ever added the column — so any DB bootstrapped from
		// vinci.sql alone (like the production Render DB) 500s on every feed and
		// post create. This column already exists on long-lived local DBs.
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS tagged_users TEXT NOT NULL DEFAULT ''`,

		// User location (for Nearby feed)
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS latitude  DOUBLE PRECISION`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS lga      TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS state    TEXT DEFAULT ''`,

		// Post signals table (permanent store of every user-interaction event)
		`CREATE TABLE IF NOT EXISTS post_signals (
			id          BIGSERIAL PRIMARY KEY,
			user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			post_id     INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			signal      TEXT NOT NULL,
			weight      NUMERIC(8,2) NOT NULL DEFAULT 0,
			category    TEXT,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, post_id, signal)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_post ON post_signals(post_id)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_user ON post_signals(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_created ON post_signals(created_at)`,

		// Commerce signals table
		`CREATE TABLE IF NOT EXISTS commerce_signals (
			id          BIGSERIAL PRIMARY KEY,
			user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			listing_id  INTEGER NOT NULL REFERENCES commerce_listings(id) ON DELETE CASCADE,
			signal      TEXT NOT NULL,
			weight      NUMERIC(8,2) NOT NULL DEFAULT 0,
			category    TEXT,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, listing_id, signal)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commerce_signals_listing ON commerce_signals(listing_id)`,

		// User interest profiles (persisted copy of the Redis interest maps)
		`CREATE TABLE IF NOT EXISTS user_interest_profiles (
			id           SERIAL PRIMARY KEY,
			user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			category     TEXT NOT NULL,
			weight       NUMERIC(5,2) NOT NULL DEFAULT 0,
			profile_type TEXT NOT NULL DEFAULT 'content',
			updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, category)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_uip_user ON user_interest_profiles(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_uip_category ON user_interest_profiles(category, weight DESC)`,

		// Commerce listings quality score column
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS quality_score NUMERIC(10,2) DEFAULT 0`,

		// ── Recommendation engine ──────────────────────────────────────────────

		// Post-level signals (permanent analytics store)
		`CREATE TABLE IF NOT EXISTS post_signals (
			id          BIGSERIAL PRIMARY KEY,
			user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			post_id     INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			signal      TEXT NOT NULL,
			weight      FLOAT NOT NULL DEFAULT 0,
			category    TEXT,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_post ON post_signals(post_id)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_user ON post_signals(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_created ON post_signals(created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_post_signals_cat ON post_signals(category)`,

		// Commerce signals (purchase-intent analytics)
		`CREATE TABLE IF NOT EXISTS commerce_signals (
			id          BIGSERIAL PRIMARY KEY,
			user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			listing_id  INTEGER NOT NULL REFERENCES commerce_listings(id) ON DELETE CASCADE,
			signal      TEXT NOT NULL,
			weight      FLOAT NOT NULL DEFAULT 0,
			category    TEXT,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commerce_signals_listing ON commerce_signals(listing_id)`,
		`CREATE INDEX IF NOT EXISTS idx_commerce_signals_user ON commerce_signals(user_id)`,

		// User interest profiles (content + commerce, 0-100 per category)
		`CREATE TABLE IF NOT EXISTS user_interest_profiles (
			id           SERIAL PRIMARY KEY,
			user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			category     TEXT NOT NULL,
			weight       FLOAT NOT NULL DEFAULT 0,
			profile_type TEXT NOT NULL DEFAULT 'content',
			updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, category)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_interests_user ON user_interest_profiles(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_interests_cat ON user_interest_profiles(category, weight)`,

		// Post-level computed quality + distribution stage
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS quality_score FLOAT DEFAULT 0`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS distribution_stage INTEGER DEFAULT 1`,
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS category TEXT DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_posts_quality ON posts(quality_score DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_posts_category ON posts(category)`,

		// Location on users for Nearby feed
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS latitude  FLOAT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS longitude FLOAT`,

		// Commerce listing quality score
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS quality_score FLOAT DEFAULT 0`,

		// commerce_listings: unified table backing every /commerce listing type
		// (product, service, job, hotel, property, vehicle, event)
		`CREATE TABLE IF NOT EXISTS commerce_listings (
			id             SERIAL PRIMARY KEY,
			user_id        INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			listing_type   TEXT NOT NULL,
			title          TEXT NOT NULL,
			description    TEXT,
			price          NUMERIC(15,2),
			discount_price NUMERIC(15,2),
			currency       TEXT DEFAULT 'NGN',
			category       TEXT,
			brand          TEXT,
			condition      TEXT,
			stock          INTEGER,
			sku            TEXT,
			delivery_available BOOLEAN DEFAULT false,
			location       TEXT,
			images         TEXT[],
			video_url      TEXT,
			metadata       JSONB DEFAULT '{}',
			status         TEXT DEFAULT 'active',
			views          INTEGER DEFAULT 0,
			created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commerce_listings_type ON commerce_listings(listing_type, status)`,
		// Location, for radius-based "near me" browsing/matching on listings.
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS latitude  DOUBLE PRECISION`,
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS longitude DOUBLE PRECISION`,
		// Thumbs up/down (replacing the old heart-icon "like" that was never
		// wired to the backend at all — it was just local UI state). Counts
		// are denormalized onto the listing row so listing/feed reads don't
		// need a join+count on every request; the vote table exists so we
		// know who voted which way (toggle/change vote, one per user).
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS upvotes INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS downvotes INTEGER NOT NULL DEFAULT 0`,
		`CREATE TABLE IF NOT EXISTS commerce_listing_votes (
			listing_id INTEGER NOT NULL REFERENCES commerce_listings(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			vote SMALLINT NOT NULL, -- 1 = up, -1 = down
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (listing_id, user_id)
		)`,
		// Reports — the reason is a fixed pick-list (see kListingReportReasons
		// in commerce.dart) rather than free text, so it's actually skimmable
		// by whoever reviews the queue instead of an open text box nobody
		// reads carefully. Status starts 'pending'; an admin resolves it.
		`CREATE TABLE IF NOT EXISTS commerce_listing_reports (
			id SERIAL PRIMARY KEY,
			listing_id INTEGER NOT NULL REFERENCES commerce_listings(id) ON DELETE CASCADE,
			reporter_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			reason TEXT NOT NULL,
			details TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending', -- pending | reviewed | dismissed
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commerce_reports_status ON commerce_listing_reports(status, created_at)`,

		// ── Contact Syncing (Phase 1 roadmap) ───────────────────────────────────
		// users: master toggle + timestamp of the last successful sync
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS contact_sync_enabled BOOLEAN NOT NULL DEFAULT false`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS contact_sync_at TIMESTAMP`,
		// The user's imported address book. phone_hash is a sha256 of the
		// E.164-normalized number so a server-side join with users.mobile
		// never has to worry about formatting differences (0803... vs +234803...).
		`CREATE TABLE IF NOT EXISTS user_contacts (
			id            SERIAL PRIMARY KEY,
			user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			contact_name  TEXT NOT NULL,
			contact_phone TEXT NOT NULL,
			phone_hash    TEXT NOT NULL,
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (user_id, contact_phone)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_user_contacts_user ON user_contacts(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_user_contacts_hash ON user_contacts(phone_hash)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func runNewFeatureMigrations(db *sql.DB) error {
	stmts := []string{
		// ── Baseline tables that only ever lived in vinci.sql (a script meant
		// to be run BY HAND against a fresh database) — if that was never run,
		// or was run before these were added to it, these tables plain don't
		// exist and every endpoint touching them 500s, which api.dart quietly
		// turns into "shows nothing" on screens like Shop's Supplies/Demands
		// tabs. Mirrored here (idempotent, matches vinci.sql's final columns)
		// so the app is self-sufficient regardless of manual setup steps.
		`CREATE TABLE IF NOT EXISTS demands (
			id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			category TEXT, looking_for TEXT, condition_pref TEXT[] DEFAULT '{}',
			min_price NUMERIC DEFAULT 0, max_price NUMERIC DEFAULT 0, location TEXT,
			latitude DOUBLE PRECISION DEFAULT 0, longitude DOUBLE PRECISION DEFAULT 0,
			search_radius INTEGER DEFAULT 10, description TEXT DEFAULT '',
			urgency TEXT DEFAULT 'Flexible', contact_number TEXT,
			preferred_contact TEXT DEFAULT 'Both', is_active BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_demands_user_id ON demands(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_demands_is_active ON demands(is_active)`,
		`CREATE TABLE IF NOT EXISTS supplies (
			id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			goods_name TEXT, category TEXT, condition TEXT DEFAULT 'Used',
			age_value INTEGER DEFAULT 0, age_unit TEXT DEFAULT 'years', brand TEXT DEFAULT '',
			price NUMERIC DEFAULT 0, negotiable BOOLEAN DEFAULT false, description TEXT DEFAULT '',
			location TEXT, latitude DOUBLE PRECISION DEFAULT 0, longitude DOUBLE PRECISION DEFAULT 0,
			delivery_radius INTEGER DEFAULT 0, delivery_available BOOLEAN DEFAULT false,
			photos TEXT[] DEFAULT '{}', contact_number TEXT, whatsapp_number TEXT DEFAULT '',
			is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_supplies_user_id ON supplies(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_supplies_is_active ON supplies(is_active)`,
		// Shop — products/cart/orders + the wallet ledger the escrow flow
		// (Checkout/ConfirmDelivery/cancel+refund in shop_service.go) reads
		// and writes via user_id. Deposit/withdraw/send/Transfer were
		// reconciled onto this same table (ledger-driven); the separate
		// wallets table is legacy and no longer read or written.
		`CREATE TABLE IF NOT EXISTS products (
			id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			name TEXT NOT NULL, description TEXT DEFAULT '', category TEXT DEFAULT '',
			price NUMERIC NOT NULL DEFAULT 0, stock_count INTEGER NOT NULL DEFAULT 0,
			is_unlimited_stock BOOLEAN DEFAULT false, images TEXT[] DEFAULT '{}',
			is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_products_user_id ON products(user_id)`,
		`CREATE TABLE IF NOT EXISTS cart_items (
			id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			quantity INTEGER NOT NULL DEFAULT 1, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (user_id, product_id))`,
		`CREATE TABLE IF NOT EXISTS orders (
			id SERIAL PRIMARY KEY,
			buyer_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			vendor_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			product_id INTEGER NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			quantity INTEGER NOT NULL DEFAULT 1, total_price NUMERIC NOT NULL DEFAULT 0,
			escrow_amount NUMERIC NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'pending',
			delivery_date_scheduled TIMESTAMP NULL, delivery_code VARCHAR(32) NOT NULL DEFAULT '',
			cancel_requested_by TEXT DEFAULT '', cancel_buyer_pin TEXT DEFAULT '',
			vendor_cancel_approved BOOLEAN DEFAULT false, admin_approved BOOLEAN DEFAULT false,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_buyer_id ON orders(buyer_id)`,
		`CREATE INDEX IF NOT EXISTS idx_orders_vendor_id ON orders(vendor_id)`,
		`CREATE INDEX IF NOT EXISTS idx_wallet_user_id ON wallet_transactions(user_id)`,
		// Communities
		`CREATE TABLE IF NOT EXISTS communities (id SERIAL PRIMARY KEY, name TEXT NOT NULL UNIQUE, slug TEXT NOT NULL UNIQUE, description TEXT, rules TEXT, cover_photo TEXT, icon TEXT, created_by INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, member_count INTEGER DEFAULT 0, post_count INTEGER DEFAULT 0, visibility TEXT NOT NULL DEFAULT 'public', is_nsfw BOOLEAN DEFAULT false, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS community_members (id SERIAL PRIMARY KEY, community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, role TEXT NOT NULL DEFAULT 'member', status TEXT NOT NULL DEFAULT 'active', joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, UNIQUE (community_id, user_id))`,
		`CREATE TABLE IF NOT EXISTS community_posts (id SERIAL PRIMARY KEY, community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, post_type TEXT NOT NULL DEFAULT 'discussion', title TEXT NOT NULL, body TEXT, media_url TEXT, link_url TEXT, upvotes INTEGER DEFAULT 0, downvotes INTEGER DEFAULT 0, comment_count INTEGER DEFAULT 0, is_pinned BOOLEAN DEFAULT false, is_locked BOOLEAN DEFAULT false, is_approved BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS community_post_votes (id SERIAL PRIMARY KEY, post_id INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, vote SMALLINT NOT NULL, UNIQUE (post_id, user_id))`,
		`CREATE TABLE IF NOT EXISTS community_comments (id SERIAL PRIMARY KEY, post_id INTEGER NOT NULL REFERENCES community_posts(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, parent_id INTEGER REFERENCES community_comments(id) ON DELETE CASCADE, body TEXT NOT NULL, upvotes INTEGER DEFAULT 0, downvotes INTEGER DEFAULT 0, is_deleted BOOLEAN DEFAULT false, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		// Community group chat ("General Chat") — one shared room per community
		`CREATE TABLE IF NOT EXISTS community_messages (id SERIAL PRIMARY KEY, community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, body TEXT, media_url TEXT, media_type TEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_community_messages_comm ON community_messages(community_id, created_at)`,
		`ALTER TABLE community_messages ADD COLUMN IF NOT EXISTS edited_at TIMESTAMP`,
		`ALTER TABLE community_messages ADD COLUMN IF NOT EXISTS reply_to_id INTEGER REFERENCES community_messages(id) ON DELETE SET NULL`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS slowmode_seconds INTEGER NOT NULL DEFAULT 0`,
		// Message reactions (community chat) — one row per user per emoji
		`CREATE TABLE IF NOT EXISTS community_message_reactions (
			id SERIAL PRIMARY KEY,
			message_id INTEGER NOT NULL REFERENCES community_messages(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			emoji TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (message_id, user_id, emoji))`,
		`CREATE INDEX IF NOT EXISTS idx_cmr_message ON community_message_reactions(message_id)`,
		// Auto-mod rules — admin-configured, enforced server-side on send
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS automod_block_links BOOLEAN NOT NULL DEFAULT FALSE`,
		`ALTER TABLE communities ADD COLUMN IF NOT EXISTS automod_words TEXT NOT NULL DEFAULT ''`,
		// Custom member title set by owner/admin (shows next to the username)
		`ALTER TABLE community_members ADD COLUMN IF NOT EXISTS custom_title TEXT NOT NULL DEFAULT ''`,
		// Referrals — code is generated lazily; reward lands when the invitee verifies
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_code TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_by INTEGER REFERENCES users(id) ON DELETE SET NULL`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS ref_rewarded BOOLEAN NOT NULL DEFAULT FALSE`,
		// Optional bcrypt hash — when set, /wallet/send requires it
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS transfer_pin_hash TEXT`,
		// Backfill: pair legacy transfer legs (same note, amount, ±5min) so
		// counterparty usernames resolve for transactions created before
		// counterparty_id existed.
		`UPDATE wallet_transactions r SET counterparty_id = s.user_id
		 FROM wallet_transactions s
		 WHERE r.type='credit' AND r.counterparty_id IS NULL
		   AND r.description LIKE 'Received: %'
		   AND s.type='transfer'
		   AND regexp_replace(r.description, '^Received: ', '') = regexp_replace(s.description, '^Sent: ', '')
		   AND r.amount = s.amount
		   AND s.user_id <> r.user_id
		   AND ABS(EXTRACT(EPOCH FROM (r.created_at - s.created_at))) < 300`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_referral_code ON users(referral_code) WHERE referral_code IS NOT NULL AND referral_code <> ''`,
		// Community marketplace — buy/sell listings scoped to a single community
		`CREATE TABLE IF NOT EXISTS community_listings (
			id SERIAL PRIMARY KEY,
			community_id INTEGER NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			description TEXT,
			price NUMERIC(15,2) NOT NULL DEFAULT 0,
			category TEXT,
			images TEXT[] DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'active',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_community_listings_comm ON community_listings(community_id, status, created_at)`,
		// Admin flag — gates fee configuration and other admin-only settings
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_admin BOOLEAN DEFAULT false`,
		// Key/value config the app reads at runtime (app fee, radius pricing)
		// so it can be changed from the backend without an app release.
		`CREATE TABLE IF NOT EXISTS app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO app_settings(key, value) VALUES ('app_fee_percent','0') ON CONFLICT (key) DO NOTHING`,
		`INSERT INTO app_settings(key, value) VALUES ('app_fee_flat','0') ON CONFLICT (key) DO NOTHING`,
		`INSERT INTO app_settings(key, value) VALUES ('radius_fee_per_km','0') ON CONFLICT (key) DO NOTHING`,
		`INSERT INTO app_settings(key, value) VALUES ('max_supply_posts_per_day','3') ON CONFLICT (key) DO NOTHING`,
		// Supply & Demand — thrift/used-item marketplace, distinct from Commerce.
		// "supply" = person selling a used/thrift item, "demand" = person
		// looking to buy one. Both feeds show every active listing to everyone.
		`CREATE TABLE IF NOT EXISTS supply_demand_listings (
			id SERIAL PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			kind TEXT NOT NULL, -- 'supply' | 'demand' | 'ask_around'
			title TEXT NOT NULL,
			description TEXT,
			price NUMERIC(14,2) DEFAULT 0,
			min_price NUMERIC(14,2) DEFAULT 0,
			max_price NUMERIC(14,2) DEFAULT 0,
			negotiable BOOLEAN DEFAULT false,
			category TEXT,
			condition TEXT DEFAULT '',
			quantity INTEGER DEFAULT 1,
			images TEXT[] DEFAULT '{}',
			location_text TEXT DEFAULT '',
			location_lat DOUBLE PRECISION,
			location_lng DOUBLE PRECISION,
			radius_km INTEGER NOT NULL DEFAULT 5,
			boosted_until TIMESTAMP,
			status TEXT NOT NULL DEFAULT 'active', -- active|sold|expired|removed
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sd_listings_kind ON supply_demand_listings(kind, status, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_sd_listings_user_day ON supply_demand_listings(user_id, created_at)`,
		// Self-heal columns added after initial schema
		`ALTER TABLE supply_demand_listings ADD COLUMN IF NOT EXISTS location_text TEXT DEFAULT ''`,
		`ALTER TABLE supply_demand_listings ADD COLUMN IF NOT EXISTS min_price NUMERIC(14,2) DEFAULT 0`,
		`ALTER TABLE supply_demand_listings ADD COLUMN IF NOT EXISTS max_price NUMERIC(14,2) DEFAULT 0`,
		`ALTER TABLE supply_demand_listings ADD COLUMN IF NOT EXISTS negotiable BOOLEAN DEFAULT false`,
		`ALTER TABLE supply_demand_listings ADD COLUMN IF NOT EXISTS condition TEXT DEFAULT ''`,
		`ALTER TABLE supply_demand_listings ADD COLUMN IF NOT EXISTS quantity INTEGER DEFAULT 1`,
		// Supplier preferences — who wants to receive Ask Around notifications
		`CREATE TABLE IF NOT EXISTS supplier_preferences (
			id SERIAL PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			categories TEXT[] DEFAULT '{}',
			supply_radius_km INTEGER NOT NULL DEFAULT 10,
			is_active BOOLEAN DEFAULT true,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE (user_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_supplier_prefs_user ON supplier_preferences(user_id)`,
		// Status
		`CREATE TABLE IF NOT EXISTS statuses (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, status_type TEXT NOT NULL DEFAULT 'image', media_url TEXT, text_content TEXT, bg_color TEXT DEFAULT '#1DB954', text_color TEXT DEFAULT '#FFFFFF', font_style TEXT DEFAULT 'normal', privacy TEXT NOT NULL DEFAULT 'followers', view_count INTEGER DEFAULT 0, expires_at TIMESTAMP NOT NULL DEFAULT (CURRENT_TIMESTAMP + INTERVAL '24 hours'), created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS status_views (id SERIAL PRIMARY KEY, status_id INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE, viewer_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, viewed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, UNIQUE (status_id, viewer_id))`,
		`CREATE TABLE IF NOT EXISTS status_reactions (id SERIAL PRIMARY KEY, status_id INTEGER NOT NULL REFERENCES statuses(id) ON DELETE CASCADE, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, reaction TEXT NOT NULL, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, UNIQUE (status_id, user_id))`,
		// Blocks — used by the recommendation engine to filter blocked creators out of feeds
		`CREATE TABLE IF NOT EXISTS blocks (id SERIAL PRIMARY KEY, blocker_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, blocked_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, UNIQUE (blocker_id, blocked_id))`,
		`CREATE INDEX IF NOT EXISTS idx_blocks_blocker ON blocks(blocker_id)`,
		// Wallet
		`CREATE TABLE IF NOT EXISTS wallets (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE, balance NUMERIC(15,2) DEFAULT 0.00, currency TEXT NOT NULL DEFAULT 'NGN', is_frozen BOOLEAN DEFAULT false, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		// Unified wallet_transactions schema — supports BOTH the escrow ledger
		// (user_id/order_id) and the fintech wallet (wallet_id) systems.
		`CREATE TABLE IF NOT EXISTS wallet_transactions (
			id SERIAL PRIMARY KEY,
			user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
			wallet_id INTEGER REFERENCES wallets(id) ON DELETE CASCADE,
			order_id INTEGER REFERENCES orders(id) ON DELETE SET NULL,
			type TEXT NOT NULL, amount NUMERIC(15,2) NOT NULL DEFAULT 0,
			balance_after NUMERIC(15,2), reference TEXT, description TEXT DEFAULT '',
			status TEXT NOT NULL DEFAULT 'completed',
			counterparty_id INTEGER REFERENCES users(id), related_order_id INTEGER,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_wallet_user_id ON wallet_transactions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_wallet_wallet_id ON wallet_transactions(wallet_id)`,
		// Notifications
		`CREATE TABLE IF NOT EXISTS notifications (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, actor_id INTEGER REFERENCES users(id) ON DELETE SET NULL, type TEXT NOT NULL, title TEXT NOT NULL, body TEXT, ref_type TEXT, ref_id INTEGER, is_read BOOLEAN DEFAULT false, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		// Per-user notification preferences (toggles in Settings)
		`CREATE TABLE IF NOT EXISTS notification_preferences (
			user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			master BOOLEAN NOT NULL DEFAULT true,
			community_messages BOOLEAN NOT NULL DEFAULT true,
			wallet BOOLEAN NOT NULL DEFAULT true,
			likes BOOLEAN NOT NULL DEFAULT true,
			comments BOOLEAN NOT NULL DEFAULT true,
			reshares BOOLEAN NOT NULL DEFAULT true,
			views BOOLEAN NOT NULL DEFAULT true
		)`,
		// Registered device push tokens (FCM / APNs) for real OS push.
		`CREATE TABLE IF NOT EXISTS device_tokens (
			id SERIAL PRIMARY KEY,
			user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			token TEXT NOT NULL,
			platform TEXT,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, token)
		)`,
		// Commerce
		`CREATE TABLE IF NOT EXISTS commerce_products (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, category TEXT, brand TEXT, description TEXT, price NUMERIC(15,2) NOT NULL DEFAULT 0, discount_price NUMERIC(15,2), stock INTEGER DEFAULT 0, sku TEXT, condition TEXT DEFAULT 'new', delivery BOOLEAN DEFAULT true, images TEXT[], tags TEXT[], is_active BOOLEAN DEFAULT true, view_count INTEGER DEFAULT 0, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS commerce_services (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, category TEXT, price NUMERIC(15,2), description TEXT, duration TEXT, availability TEXT, location TEXT, images TEXT[], is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS commerce_jobs (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, title TEXT NOT NULL, company TEXT, salary TEXT, employment_type TEXT, experience TEXT, qualification TEXT, location TEXT, description TEXT, deadline DATE, apply_link TEXT, apply_in_app BOOLEAN DEFAULT false, is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS commerce_hotels (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, hotel_name TEXT NOT NULL, room_name TEXT, price_per_night NUMERIC(15,2), max_guests INTEGER, amenities TEXT[], images TEXT[], available_rooms INTEGER DEFAULT 1, checkin_time TEXT, checkout_time TEXT, description TEXT, location TEXT, is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS commerce_properties (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, property_type TEXT, listing_type TEXT, bedrooms INTEGER, bathrooms INTEGER, area TEXT, address TEXT, price NUMERIC(15,2), description TEXT, images TEXT[], is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS commerce_vehicles (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, brand TEXT, model TEXT, year INTEGER, fuel TEXT, transmission TEXT, mileage TEXT, price NUMERIC(15,2), description TEXT, images TEXT[], is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS commerce_events (id SERIAL PRIMARY KEY, user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE, name TEXT NOT NULL, event_date DATE, event_time TEXT, venue TEXT, description TEXT, ticket_price NUMERIC(15,2) DEFAULT 0, registration_link TEXT, images TEXT[], is_active BOOLEAN DEFAULT true, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS orders (id SERIAL PRIMARY KEY, buyer_id INTEGER NOT NULL REFERENCES users(id), seller_id INTEGER NOT NULL REFERENCES users(id), item_type TEXT NOT NULL, item_id INTEGER NOT NULL, quantity INTEGER DEFAULT 1, amount NUMERIC(15,2) NOT NULL, status TEXT NOT NULL DEFAULT 'pending', payment_ref TEXT, notes TEXT, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		// Business profile fields
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_category TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_name TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_desc TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_phone TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_email TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_website TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_address TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_country TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_state TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS business_city TEXT`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS selling_types TEXT[]`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_verified BOOLEAN DEFAULT false`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}

	// ── Wallet schema: always-run, error-tolerant pass ─────────────────
	// These ALTERs MUST run even if earlier statements failed, because
	// the wallet is core functionality. Failures are logged but never fatal.
	for _, s := range []string{
		`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS wallet_id INTEGER`,
		`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS user_id INTEGER`,
		`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS balance_after NUMERIC(15,2)`,
		`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'completed'`,
		`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS counterparty_id INTEGER`,
		`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS related_order_id INTEGER`,
		// Hashtags parsed from post captions (#word) — lowercase, deduped per post
		`CREATE TABLE IF NOT EXISTS post_hashtags (
			id SERIAL PRIMARY KEY,
			post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
			tag TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE INDEX IF NOT EXISTS idx_post_hashtags_tag ON post_hashtags(lower(tag))`,
		`CREATE INDEX IF NOT EXISTS idx_post_hashtags_post ON post_hashtags(post_id)`,
		// Batch checkout — one payment can cover several cart orders
		`ALTER TABLE orders ADD COLUMN IF NOT EXISTS payment_reference TEXT`,
		`CREATE INDEX IF NOT EXISTS idx_orders_payment_ref ON orders(payment_reference)`,
		`ALTER TABLE wallet_transactions ALTER COLUMN user_id DROP NOT NULL`,
		// Commerce listings mirror into the legacy products table for the
		// cart/checkout flow — this link column records the mirror row.
		`ALTER TABLE commerce_listings ADD COLUMN IF NOT EXISTS product_id BIGINT`,
		// Profile post pinning (IG/TikTok style, max 3 per user)
		`ALTER TABLE posts ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ`,
	} {
		if _, err := db.Exec(s); err != nil {
			log.Printf("warning: wallet self-heal skipped: %v — %.120s", err, s)
		}
	}

	// Diagnostic: confirm wallet_id exists
	var colExists bool
	_ = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_name='wallet_transactions' AND column_name='wallet_id')`).Scan(&colExists)
	log.Printf("[DIAG] wallet_transactions has wallet_id: %v", colExists)
	if !colExists {
		log.Printf("[DIAG] wallet_id STILL missing — forcing ALTER TABLE ADD COLUMN")
		if _, err := db.Exec(`ALTER TABLE wallet_transactions ADD COLUMN wallet_id INTEGER`); err != nil {
			log.Printf("[DIAG] FORCE ADD failed: %v", err)
		} else {
			log.Printf("[DIAG] FORCE ADD wallet_id succeeded")
		}
	}

	return nil
}
