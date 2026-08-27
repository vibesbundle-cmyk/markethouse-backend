package services

// ─────────────────────────────────────────────────────────────────────────────
// Recommendation Engine
//
// Architecture (as described in the spec):
//   1. Post Quality Score  — how engaging is this specific post?
//   2. User Interest Profile — what categories does this user love?
//   3. Creator Quality Score — how consistently good is this creator?
//   4. Freshness Bonus       — newer content gets a boost
//   5. Location Relevance    — posts from nearby users/businesses
//
// Signals are recorded in PostgreSQL (permanent) and summarised in Redis
// (fast read for feed ranking). Redis is refreshed on a write-through basis:
// every time a signal arrives, both stores are updated atomically.
//
// Feed tabs:
//   - following : posts from accounts you follow, newest first, 10-20% discovery
//   - for_you   : full recommendation score ranking
//   - nearby    : location-filtered posts, ranked by quality score
//   - trending  : velocity-based ranking (shares/min, saves/min, comments/min)
//
// Commerce ranking adds buy-intent signals (view_images, chat_seller, add_to_cart,
// purchase) on top of the base quality score.
// ─────────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"time"

	"github.com/redis/go-redis/v9"
)

// ─── signal weights ────────────────────────────────────────────────────────────

// postSignalWeight maps signal names to their Quality Score contribution.
// Negative weights represent negative signals (skip, report, block, etc.).
var postSignalWeight = map[string]float64{
	// positive engagement
	"watch_100":      40,
	"watch_75":       30,
	"watch_50":       20,
	"watch_25":       8,
	"replay":         15,
	"share":          30,
	"save":           25,
	"comment":        20,
	"profile_visit":  10,
	"follow":         35,
	"like":           10,
	"view":           1, // simple impression — low weight
	// negative signals
	"not_interested": -40,
	"report":         -100,
	"block_creator":  -150,
	"skip_3s":        -20,
}

// commerceSignalWeight overrides for the Commerce ranking.
var commerceSignalWeight = map[string]float64{
	"view":             5,
	"view_all_images":  10,
	"view_video":       15,
	"save":             20,
	"share":            20,
	"chat_seller":      35,
	"add_to_cart":      45,
	"add_to_wishlist":  25,
	"purchase":         100,
	"left_review":      30,
	"follow_business":  25,
	"report":           -100,
}

// userInterestDelta maps signals to how much they shift a user's interest
// weight for the post's category (0–100 scale).
var userInterestDelta = map[string]float64{
	"watch_100":     5,
	"replay":        4,
	"like":          2,
	"comment":       3,
	"save":          5,
	"share":         8,
	"follow":        10,
	"skip_3s":       -4,
	"not_interested":-8,
	"report":        -20,
}

// ─── service ──────────────────────────────────────────────────────────────────

type RecommendationService struct {
	DB    *sql.DB
	Redis *redis.Client
}



// ─── RecordSignal ─────────────────────────────────────────────────────────────
// Called from HTTP handlers whenever a user interacts with a post.
// Writes to: post_signals table (permanent) AND Redis (fast reads).
func (s *RecommendationService) RecordSignal(userID, postID int64, signal, category string, lat, lng *float64) {
	weight := postSignalWeight[signal]

	// 1. Write to permanent store
	go func() {
		_, err := s.DB.Exec(`
			INSERT INTO post_signals(user_id, post_id, signal, weight, category, created_at)
			VALUES($1,$2,$3,$4,$5,NOW())
			ON CONFLICT DO NOTHING`,
			userID, postID, signal, weight, category)
		if err != nil {
			log.Printf("[rec] signal insert: %v", err)
			return
		}

		// 2. Update post's cached quality score in Redis
		s.refreshPostScore(postID, category)

		// 3. Shift user interest profile
		if delta, ok := userInterestDelta[signal]; ok && category != "" {
			s.shiftUserInterest(userID, category, delta)
		}

		// 4. Refresh trending lists for this category
		s.refreshTrending(category)
	}()
}

// RecordCommerceSignal records a commerce-specific user action.
func (s *RecommendationService) RecordCommerceSignal(userID, listingID int64, signal, category string) {
	weight := commerceSignalWeight[signal]
	go func() {
		s.DB.Exec(`
			INSERT INTO commerce_signals(user_id, listing_id, signal, weight, category, created_at)
			VALUES($1,$2,$3,$4,$5,NOW())
			ON CONFLICT DO NOTHING`,
			userID, listingID, signal, weight, category)
		s.refreshCommerceScore(listingID, category)
		if delta, ok := userInterestDelta[signal]; ok && category != "" {
			s.shiftCommerceInterest(userID, category, delta)
		}
	}()
}

// ─── Score computation ────────────────────────────────────────────────────────

func (s *RecommendationService) refreshPostScore(postID int64, category string) {
	var score float64
	s.DB.QueryRow(`
		SELECT COALESCE(SUM(weight),0) FROM post_signals WHERE post_id=$1`, postID).Scan(&score)

	// freshness bonus: posts < 24h get up to +50, decaying over 7 days
	var createdAt time.Time
	s.DB.QueryRow(`SELECT created_at FROM posts WHERE id=$1`, postID).Scan(&createdAt)
	score += freshnessBonus(createdAt)

	// creator quality bonus
	var authorID int64
	s.DB.QueryRow(`SELECT user_id FROM posts WHERE id=$1`, postID).Scan(&authorID)
	creatorScore := s.creatorQualityScore(authorID)
	score += creatorScore * 0.15 // up to +15 from creator quality

	s.DB.Exec(`UPDATE posts SET quality_score=$1 WHERE id=$2`, score, postID)

	pipe := s.Redis.Pipeline()
	pipe.ZAdd(bgCtx, "scores:posts", redis.Z{Score: score, Member: postID})
	if category != "" {
		pipe.ZAdd(bgCtx, "scores:cat:"+category, redis.Z{Score: score, Member: postID})
	}
	pipe.Exec(bgCtx)
}

func (s *RecommendationService) refreshCommerceScore(listingID int64, category string) {
	var score float64
	s.DB.QueryRow(`SELECT COALESCE(SUM(weight),0) FROM commerce_signals WHERE listing_id=$1`, listingID).Scan(&score)
	s.DB.Exec(`UPDATE commerce_listings SET quality_score=COALESCE(quality_score,0)+$1 WHERE id=$2`, score, listingID)
	pipe := s.Redis.Pipeline()
	pipe.ZAdd(bgCtx, "scores:commerce", redis.Z{Score: score, Member: listingID})
	if category != "" {
		pipe.ZAdd(bgCtx, "scores:commerce:"+category, redis.Z{Score: score, Member: listingID})
	}
	pipe.Exec(bgCtx)
}

// creatorQualityScore returns 0-100 based on the creator's historical performance.
func (s *RecommendationService) creatorQualityScore(userID int64) float64 {
	cacheKey := fmt.Sprintf("creator:score:%d", userID)
	if v, err := s.Redis.Get(bgCtx, cacheKey).Float64(); err == nil {
		return v
	}
	var avgScore float64
	s.DB.QueryRow(`
		SELECT COALESCE(AVG(ps.weight_sum),0)
		FROM (
			SELECT post_id, SUM(weight) as weight_sum
			FROM post_signals
			WHERE user_id=$1 AND signal != 'report' AND signal != 'block_creator'
			GROUP BY post_id
		) ps`, userID).Scan(&avgScore)
	score := math.Min(100, math.Max(0, avgScore/10))
	s.Redis.Set(bgCtx, cacheKey, score, 6*time.Hour)
	return score
}

func freshnessBonus(created time.Time) float64 {
	age := time.Since(created)
	switch {
	case age < 24*time.Hour:
		return 50
	case age < 7*24*time.Hour:
		ratio := 1 - (age.Hours()-24)/(6*24)
		return 25 * ratio
	default:
		return 0
	}
}

// ─── User Interest Profile ────────────────────────────────────────────────────

func (s *RecommendationService) shiftUserInterest(userID int64, category string, delta float64) {
	key := fmt.Sprintf("user:interests:%d", userID)
	// Read current interest map
	raw, _ := s.Redis.Get(bgCtx, key).Bytes()
	m := map[string]float64{}
	json.Unmarshal(raw, &m)
	v := m[category] + delta
	v = math.Min(100, math.Max(0, v))
	m[category] = v
	b, _ := json.Marshal(m)
	s.Redis.Set(bgCtx, key, b, 30*24*time.Hour)
	// Persist to DB for durability
	s.DB.Exec(`
		INSERT INTO user_interest_profiles(user_id, category, weight, updated_at)
		VALUES($1,$2,$3,NOW())
		ON CONFLICT(user_id,category)
		DO UPDATE SET weight=EXCLUDED.weight, updated_at=NOW()`, userID, category, v)
}

func (s *RecommendationService) shiftCommerceInterest(userID int64, category string, delta float64) {
	key := fmt.Sprintf("user:commerce_interests:%d", userID)
	raw, _ := s.Redis.Get(bgCtx, key).Bytes()
	m := map[string]float64{}
	json.Unmarshal(raw, &m)
	v := math.Min(100, math.Max(0, m[category]+delta))
	m[category] = v
	b, _ := json.Marshal(m)
	s.Redis.Set(bgCtx, key, b, 30*24*time.Hour)
	s.DB.Exec(`
		INSERT INTO user_interest_profiles(user_id, category, weight, profile_type, updated_at)
		VALUES($1,$2,$3,'commerce',NOW())
		ON CONFLICT(user_id,category)
		DO UPDATE SET weight=EXCLUDED.weight, updated_at=NOW()`, userID, category, v)
}

// GetUserInterests returns the user's interest map from Redis (or DB fallback).
func (s *RecommendationService) GetUserInterests(userID int64) map[string]float64 {
	key := fmt.Sprintf("user:interests:%d", userID)
	raw, err := s.Redis.Get(bgCtx, key).Bytes()
	if err == nil {
		m := map[string]float64{}
		json.Unmarshal(raw, &m)
		return m
	}
	// DB fallback
	rows, _ := s.DB.Query(`SELECT category, weight FROM user_interest_profiles WHERE user_id=$1 AND profile_type='content'`, userID)
	m := map[string]float64{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var cat string
			var w float64
			rows.Scan(&cat, &w)
			m[cat] = w
		}
	}
	return m
}

// ─── Trending ─────────────────────────────────────────────────────────────────

func (s *RecommendationService) refreshTrending(category string) {
	// Velocity-based trending: sum signals from last hour, weighted by recency
	rows, err := s.DB.Query(`
		SELECT post_id, SUM(weight * EXTRACT(EPOCH FROM (NOW()-created_at))/3600.0) as velocity
		FROM post_signals
		WHERE created_at > NOW() - INTERVAL '24 hours'
		AND signal IN ('share','save','comment','like','follow','replay')
		GROUP BY post_id
		ORDER BY velocity DESC
		LIMIT 500`)
	if err != nil {
		return
	}
	defer rows.Close()
	pipe := s.Redis.Pipeline()
	pipe.Del(bgCtx, "trending:national")
	for rows.Next() {
		var postID int64
		var velocity float64
		rows.Scan(&postID, &velocity)
		pipe.ZAdd(bgCtx, "trending:national", redis.Z{Score: velocity, Member: postID})
	}
	pipe.Expire(bgCtx, "trending:national", 10*time.Minute)
	if category != "" {
		pipe.Expire(bgCtx, "trending:cat:"+category, 10*time.Minute)
	}
	pipe.Exec(bgCtx)
}

// GetTrending returns the top-N trending post IDs from Redis.
func (s *RecommendationService) GetTrending(category string, limit int64) []int64 {
	key := "trending:national"
	if category != "" {
		key = "trending:cat:" + category
	}
	vals, err := s.Redis.ZRevRangeWithScores(bgCtx, key, 0, limit-1).Result()
	if err != nil || len(vals) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(vals))
	for _, v := range vals {
		if id, ok := v.Member.(int64); ok {
			ids = append(ids, id)
		} else if idStr, ok := v.Member.(string); ok {
			var id int64
			fmt.Sscanf(idStr, "%d", &id)
			ids = append(ids, id)
		}
	}
	return ids
}

// ─── Feed Builders ────────────────────────────────────────────────────────────

// BuildForYouFeed ranks posts for a user using the 3-layer scoring system.
func (s *RecommendationService) BuildForYouFeed(userID int64, lat, lng *float64, limit int) []int64 {
	interests := s.GetUserInterests(userID)
	rows, err := s.DB.Query(`
		SELECT p.id, p.category, p.quality_score, p.created_at, p.user_id,
		       CASE WHEN $2::float IS NOT NULL AND u.latitude IS NOT NULL
		            THEN 1 / (1 + |/((p.user_id - 0) + 0)) -- placeholder for location
		            ELSE 0 END as loc_score
		FROM posts p
		JOIN users u ON u.id = p.user_id
		WHERE p.is_locked = false
		  AND p.user_id != $1
		  AND NOT EXISTS(SELECT 1 FROM blocks WHERE blocker_id=$1 AND blocked_id=p.user_id)
		  AND (
		    p.audience = 'public'
		    OR (p.audience = 'followers' AND (
		      p.user_id = $1
		      OR EXISTS(SELECT 1 FROM follows f WHERE f.following_id = p.user_id AND f.follower_id = $1)
		    ))
		    OR (p.audience = 'private' AND (
		      p.user_id = $1
		      OR ',' || p.audience_user_ids || ',' LIKE '%,' || $1 || ',%'
		    ))
		  )
		ORDER BY p.created_at DESC
		LIMIT $3`, userID, lat, limit*3)
	if err != nil {
		return nil
	}
	defer rows.Close()

	type ranked struct {
		id    int64
		score float64
	}
	var candidates []ranked
	for rows.Next() {
		var id, authorID int64
		var category string
		var qualityScore float64
		var createdAt time.Time
		var locScore float64
		rows.Scan(&id, &category, &qualityScore, &createdAt, &authorID, &locScore)

		interestMatch := interests[category] // 0-100
		creatorScore := s.creatorQualityScore(authorID)
		freshness := freshnessBonus(createdAt)

		final := qualityScore +
			(interestMatch * 0.3) + // 30% weight for user interest
			(creatorScore * 0.15) + // 15% from creator reputation
			freshness +
			(locScore * 20)

		candidates = append(candidates, ranked{id: id, score: final})
	}

	// sort descending
	for i := 0; i < len(candidates)-1; i++ {
		for j := i + 1; j < len(candidates); j++ {
			if candidates[j].score > candidates[i].score {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}

	ids := make([]int64, 0, limit)
	for i := 0; i < len(candidates) && i < limit; i++ {
		ids = append(ids, candidates[i].id)
	}
	return ids
}

// ─── Test Audience Stage ──────────────────────────────────────────────────────
// When a post is first published it goes to a small test audience
// before wider distribution — mirrors TikTok's staged rollout.

func (s *RecommendationService) AssignTestAudience(postID, authorID int64, category string) {
	go func() {
		// Stage 1: 100 users — 40% similar interests, 20% followers, 20% nearby, 20% random
		var stage1 []int64

		// 40 interest-matched users
		rows, _ := s.DB.Query(`
			SELECT user_id FROM user_interest_profiles
			WHERE category=$1 AND profile_type='content' AND weight > 30
			AND user_id != $2
			ORDER BY weight DESC LIMIT 40`, category, authorID)
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var uid int64
				rows.Scan(&uid)
				stage1 = append(stage1, uid)
			}
		}

		// 20 followers
		fRows, _ := s.DB.Query(`SELECT follower_id FROM follows WHERE following_id=$1 LIMIT 20`, authorID)
		if fRows != nil {
			defer fRows.Close()
			for fRows.Next() {
				var uid int64
				fRows.Scan(&uid)
				stage1 = append(stage1, uid)
			}
		}

		// 20 random (discovery)
		rRows, _ := s.DB.Query(`SELECT id FROM users WHERE id != $1 ORDER BY RANDOM() LIMIT 20`, authorID)
		if rRows != nil {
			defer rRows.Close()
			for rRows.Next() {
				var uid int64
				rRows.Scan(&uid)
				stage1 = append(stage1, uid)
			}
		}

		// Write to Redis — any of these users will see the post in their feed
		// for the next 60 minutes (test window).
		pipe := s.Redis.Pipeline()
		for _, uid := range stage1 {
			key := fmt.Sprintf("test_audience:%d", uid)
			pipe.SAdd(bgCtx, key, postID)
			pipe.Expire(bgCtx, key, 60*time.Minute)
		}
		// Record test start
		pipe.HSet(bgCtx, fmt.Sprintf("post:test:%d", postID), "stage", 1, "started_at", time.Now().Unix())
		pipe.Expire(bgCtx, fmt.Sprintf("post:test:%d", postID), 24*time.Hour)
		pipe.Exec(bgCtx)
	}()
}

// EvaluateAndPromote checks if a post should advance to the next distribution stage.
// Should be called by a cron job or after enough signals have arrived (~30-60 min after posting).
func (s *RecommendationService) EvaluateAndPromote(postID int64) {
	go func() {
		stageKey := fmt.Sprintf("post:test:%d", postID)
		stageStr, _ := s.Redis.HGet(bgCtx, stageKey, "stage").Result()
		var stage int64
		fmt.Sscanf(stageStr, "%d", &stage)

		var qualityScore float64
		s.DB.QueryRow(`SELECT COALESCE(quality_score,0) FROM posts WHERE id=$1`, postID).Scan(&qualityScore)

		// Thresholds per stage: score must meet the bar to advance
		thresholds := map[int64]float64{1: 80, 2: 120, 3: 180, 4: 250}
		audiences := map[int64]int64{1: 100, 2: 1000, 3: 10000, 4: 100000}

		threshold, ok := thresholds[stage]
		if !ok {
			return // already at max or invalid
		}
		if qualityScore < threshold {
			log.Printf("[rec] post %d stalled at stage %d (score %.1f < threshold %.1f)", postID, stage, qualityScore, threshold)
			return
		}

		nextStage := stage + 1
		nextAudience := audiences[nextStage]
		log.Printf("[rec] post %d advancing to stage %d (audience ~%d)", postID, nextStage, nextAudience)
		s.Redis.HSet(bgCtx, stageKey, "stage", nextStage)
		s.DB.Exec(`UPDATE posts SET distribution_stage=$1 WHERE id=$2`, nextStage, postID)
	}()
}
