package handlers

import (
	"database/sql"
	"math"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"markethouse/internal/services"
)

// SignalHandler receives all user-interaction events and routes them to
// the recommendation engine. A single endpoint keeps the Flutter client simple.
type SignalHandler struct {
	DB    *sql.DB
	Redis *redis.Client
	Rec   *services.RecommendationService
}

// POST /signal
// Body: { "post_id": 12, "signal": "watch_100", "category": "Sports", "lat": 6.5, "lng": 3.3 }
func (h *SignalHandler) Record(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		PostID   *int64   `json:"post_id"`
		Signal   string   `json:"signal"`
		Category string   `json:"category"`
		Lat      *float64 `json:"lat"`
		Lng      *float64 `json:"lng"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Signal == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "signal required"})
		return
	}
	if req.PostID == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "post_id required"})
		return
	}
	h.Rec.RecordSignal(userID, *req.PostID, req.Signal, req.Category, req.Lat, req.Lng)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// POST /signal/commerce
func (h *SignalHandler) RecordCommerce(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		ListingID int64  `json:"listing_id"`
		Signal    string `json:"signal"`
		Category  string `json:"category"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Signal == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "signal + listing_id required"})
		return
	}
	h.Rec.RecordCommerceSignal(userID, req.ListingID, req.Signal, req.Category)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// GET /feed/for-you?lat=&lng=&page=
func (h *SignalHandler) ForYouFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var lat, lng *float64
	if v, err := strconv.ParseFloat(c.Query("lat"), 64); err == nil {
		lat = &v
	}
	if v, err := strconv.ParseFloat(c.Query("lng"), 64); err == nil {
		lng = &v
	}
	ids := h.Rec.BuildForYouFeed(userID, lat, lng, 30)
	if len(ids) == 0 {
		c.JSON(http.StatusOK, gin.H{"posts": []gin.H{}})
		return
	}
	posts := h.fetchPostsByIDs(c, userID, ids)
	c.JSON(http.StatusOK, gin.H{"posts": posts})
}

// GET /feed/trending?category=
func (h *SignalHandler) TrendingFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	category := c.Query("category")
	ids := h.Rec.GetTrending(category, 50)
	if len(ids) == 0 {
		// fallback: recent high-score posts from DB
		rows, _ := h.DB.Query(`
			SELECT id FROM posts WHERE is_locked=false
			ORDER BY COALESCE(quality_score,0) DESC, created_at DESC LIMIT 50`)
		if rows != nil {
			defer rows.Close()
			for rows.Next() {
				var id int64
				rows.Scan(&id)
				ids = append(ids, id)
			}
		}
	}
	posts := h.fetchPostsByIDs(c, userID, ids)
	c.JSON(http.StatusOK, gin.H{"posts": posts})
}

// GET /feed/nearby?lat=&lng=&radius_km=50
func (h *SignalHandler) NearbyFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	lat, _ := strconv.ParseFloat(c.DefaultQuery("lat", "0"), 64)
	lng, _ := strconv.ParseFloat(c.DefaultQuery("lng", "0"), 64)
	radiusKm, _ := strconv.ParseFloat(c.DefaultQuery("radius_km", "50"), 64)
	if lat == 0 && lng == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "lat and lng required for nearby feed"})
		return
	}
	// Haversine filter — pulls users within radius_km, then ranks by quality score
	rows, err := h.DB.Query(`
		SELECT p.id FROM posts p
		JOIN users u ON u.id = p.user_id
		WHERE p.is_locked = false
		  AND u.latitude IS NOT NULL AND u.longitude IS NOT NULL
		  AND (
		    6371 * acos(
		      cos(radians($1)) * cos(radians(u.latitude)) *
		      cos(radians(u.longitude) - radians($2)) +
		      sin(radians($1)) * sin(radians(u.latitude))
		    )
		  ) <= $3
		  AND p.user_id != $4
		  AND (
		    p.audience = 'public'
		    OR (p.audience = 'followers' AND (
		      p.user_id = $4
		      OR EXISTS(SELECT 1 FROM follows f WHERE f.following_id = p.user_id AND f.follower_id = $4)
		    ))
		    OR (p.audience = 'private' AND (
		      p.user_id = $4
		      OR ',' || p.audience_user_ids || ',' LIKE '%,' || $4 || ',%'
		    ))
		  )
		ORDER BY COALESCE(p.quality_score,0) DESC, p.created_at DESC
		LIMIT 50`, lat, lng, radiusKm, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	posts := h.fetchPostsByIDs(c, userID, ids)
	c.JSON(http.StatusOK, gin.H{"posts": posts})
}

// GET /nearby/users?lat=&lng=&radius_km=50 — discover nearby people.
// If lat/lng are omitted the caller's own saved location is used.
// Returns users ordered by distance with their LGA/state + distance in km.
func (h *SignalHandler) NearbyUsers(c *gin.Context) {
	userID := c.GetInt64("user_id")
	lat, _ := strconv.ParseFloat(c.DefaultQuery("lat", ""), 64)
	lng, _ := strconv.ParseFloat(c.DefaultQuery("lng", ""), 64)
	radiusKm, _ := strconv.ParseFloat(c.DefaultQuery("radius_km", "50"), 64)

	// Fall back to the viewer's last-saved coordinates.
	if lat == 0 && lng == 0 {
		var savedLat, savedLng *float64
		err := h.DB.QueryRow(
			`SELECT latitude, longitude FROM users WHERE id=$1`, userID,
		).Scan(&savedLat, &savedLng)
		if err != nil || savedLat == nil || savedLng == nil {
			c.JSON(400, gin.H{"error": "no location — pass lat/lng or save your location first"})
			return
		}
		lat, lng = *savedLat, *savedLng
	}

	rows, err := h.DB.Query(`
		SELECT u.id, COALESCE(u.username,''), COALESCE(u.full_name,''),
		       COALESCE(u.profile_photo,''), COALESCE(u.account_type,'personal'),
		       COALESCE(u.is_verified,false), COALESCE(u.lga,''), COALESCE(u.state,''),
		       u.latitude, u.longitude,
		       (6371 * acos(
		         LEAST(1, GREATEST(-1,
		           cos(radians($1)) * cos(radians(u.latitude)) *
		           cos(radians(u.longitude) - radians($2)) +
		           sin(radians($1)) * sin(radians(u.latitude))
		         ))
		       )) AS distance_km
		FROM users u
		WHERE u.latitude IS NOT NULL AND u.longitude IS NOT NULL
		  AND u.id != $3
		  AND (6371 * acos(
		         LEAST(1, GREATEST(-1,
		           cos(radians($1)) * cos(radians(u.latitude)) *
		           cos(radians(u.longitude) - radians($2)) +
		           sin(radians($1)) * sin(radians(u.latitude))
		         ))
		       )) <= $4
		ORDER BY distance_km ASC
		LIMIT 100`, lat, lng, userID, radiusKm)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	people := []gin.H{}
	for rows.Next() {
		var id int64
		var uname, fullName, photo, accountType, lga, state string
		var isVerified bool
		var uLat, uLng, distance float64
		if rows.Scan(&id, &uname, &fullName, &photo, &accountType, &isVerified,
			&lga, &state, &uLat, &uLng, &distance) != nil {
			continue
		}
		people = append(people, gin.H{
			"id":            id,
			"username":      uname,
			"full_name":     fullName,
			"profile_photo": photo,
			"account_type":  accountType,
			"is_verified":   isVerified,
			"lga":           lga,
			"state":         state,
			"latitude":      uLat,
			"longitude":     uLng,
			"distance_km":   math.Round(distance*100) / 100,
		})
	}
	c.JSON(http.StatusOK, gin.H{"people": people})
}

// GET /analytics/post/:post_id — creator analytics for one post
func (h *SignalHandler) PostAnalytics(c *gin.Context) {
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	callerID := c.GetInt64("user_id")
	var authorID int64
	if err := h.DB.QueryRow(`SELECT user_id FROM posts WHERE id=$1`, postID).Scan(&authorID); err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if authorID != callerID {
		c.JSON(403, gin.H{"error": "not your post"})
		return
	}
	rows, err := h.DB.Query(`
		SELECT signal, COUNT(*) as cnt, SUM(weight) as total_weight
		FROM post_signals WHERE post_id=$1
		GROUP BY signal ORDER BY cnt DESC`, postID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	signals := []gin.H{}
	for rows.Next() {
		var signal string
		var cnt int
		var totalWeight float64
		rows.Scan(&signal, &cnt, &totalWeight)
		signals = append(signals, gin.H{"signal": signal, "count": cnt, "weight": totalWeight})
	}
	var qualityScore float64
	var distributionStage int
	h.DB.QueryRow(`SELECT COALESCE(quality_score,0), COALESCE(distribution_stage,1) FROM posts WHERE id=$1`, postID).
		Scan(&qualityScore, &distributionStage)
	c.JSON(200, gin.H{
		"post_id":            postID,
		"quality_score":      qualityScore,
		"distribution_stage": distributionStage,
		"signals":            signals,
	})
}

// GET /analytics/profile — overall creator dashboard
func (h *SignalHandler) CreatorAnalytics(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var totalViews, totalLikes, totalComments, totalShares, totalSaves int64
	h.DB.QueryRow(`
		SELECT
		  COUNT(CASE WHEN signal='view' THEN 1 END),
		  COUNT(CASE WHEN signal='like' THEN 1 END),
		  COUNT(CASE WHEN signal='comment' THEN 1 END),
		  COUNT(CASE WHEN signal='share' THEN 1 END),
		  COUNT(CASE WHEN signal='save' THEN 1 END)
		FROM post_signals ps
		JOIN posts p ON p.id = ps.post_id
		WHERE p.user_id = $1`, userID).
		Scan(&totalViews, &totalLikes, &totalComments, &totalShares, &totalSaves)
	var followers int64
	h.DB.QueryRow(`SELECT COUNT(*) FROM follows WHERE following_id=$1`, userID).Scan(&followers)
	interestRows, _ := h.DB.Query(`
		SELECT category, weight FROM user_interest_profiles
		WHERE user_id=$1 AND profile_type='content'
		ORDER BY weight DESC LIMIT 10`, userID)
	interests := []gin.H{}
	if interestRows != nil {
		defer interestRows.Close()
		for interestRows.Next() {
			var cat string
			var w float64
			interestRows.Scan(&cat, &w)
			interests = append(interests, gin.H{"category": cat, "weight": w})
		}
	}
	c.JSON(200, gin.H{
		"views": totalViews, "likes": totalLikes, "comments": totalComments,
		"shares": totalShares, "saves": totalSaves, "followers": followers,
		"top_interests": interests,
	})
}

// GET /interests — current user's interest profile (for debugging/profile display)
func (h *SignalHandler) GetInterests(c *gin.Context) {
	userID := c.GetInt64("user_id")
	interests := h.Rec.GetUserInterests(userID)
	c.JSON(200, gin.H{"interests": interests})
}

// GET /analytics/business — business dashboard (views, chats, cart, purchases, revenue)
func (h *SignalHandler) BusinessAnalytics(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var views, chats, carts, purchases int
	var revenue float64
	h.DB.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN cs.signal='view' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN cs.signal='chat_seller' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN cs.signal='add_to_cart' THEN 1 ELSE 0 END),0),
		  COALESCE(SUM(CASE WHEN cs.signal='purchase' THEN 1 ELSE 0 END),0)
		FROM commerce_signals cs JOIN commerce_listings cl ON cl.id=cs.listing_id
		WHERE cl.user_id=$1`, userID).Scan(&views, &chats, &carts, &purchases)
	h.DB.QueryRow(`
		SELECT COALESCE(SUM(o.total_price),0) FROM orders o
		JOIN products p ON p.id=o.product_id
		WHERE p.user_id=$1 AND o.status='delivered'`, userID).Scan(&revenue)
	var followers, products int
	h.DB.QueryRow(`SELECT COUNT(*) FROM follows WHERE following_id=$1`, userID).Scan(&followers)
	h.DB.QueryRow(`SELECT COUNT(*) FROM commerce_listings WHERE user_id=$1 AND status='active'`, userID).Scan(&products)
	c.JSON(200, gin.H{
		"views": views, "chats": chats, "carts": carts, "purchases": purchases,
		"revenue": revenue, "followers": followers, "products": products,
	})
}

// ─── private helpers ──────────────────────────────────────────────────────────

func (h *SignalHandler) fetchPostsByIDs(c *gin.Context, viewerID int64, ids []int64) []gin.H {
	if len(ids) == 0 {
		return []gin.H{}
	}
	// build a VALUES(...) clause for ordering
	params := []interface{}{viewerID}
	inClause := "VALUES"
	for i, id := range ids {
		if i > 0 {
			inClause += ","
		}
		params = append(params, id)
		inClause += "($" + strconv.Itoa(len(params)) + ")"
	}
	rows, err := h.DB.Query(`
		SELECT p.id, p.user_id, p.caption, COALESCE(p.media_url,''), COALESCE(p.media_type,'image'),
		       COALESCE(p.category,''), p.post_type, p.created_at,
		       u.username, COALESCE(u.profile_photo,''),
		       COALESCE(p.quality_score,0),
		       (SELECT COUNT(*) FROM likes WHERE post_id=p.id) as likes,
		       (SELECT COUNT(*) FROM comments WHERE post_id=p.id) as comments,
		       EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		       EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
		FROM posts p
		JOIN users u ON u.id=p.user_id
		JOIN (`+inClause+`) AS ord(id) ON ord.id=p.id
		ORDER BY array_position(ARRAY[`+buildIDArray(ids)+`], p.id)`,
		params...)
	if err != nil {
		return []gin.H{}
	}
	defer rows.Close()
	var posts []gin.H
	for rows.Next() {
		var id, authorID int64
		var caption, mediaURL, mediaType, category, postType, ca, uname, photo string
		var qualityScore float64
		var likes, comments int64
		var isLiked, isSaved bool
		if err := rows.Scan(&id, &authorID, &caption, &mediaURL, &mediaType, &category, &postType, &ca,
			&uname, &photo, &qualityScore, &likes, &comments, &isLiked, &isSaved); err != nil {
			continue
		}
		posts = append(posts, gin.H{
			"id": id, "user_id": authorID, "caption": caption,
			"media_url": mediaURL, "media_type": mediaType, "category": category, "post_type": postType,
			"created_at": ca, "username": uname, "profile_photo": photo,
			"quality_score": qualityScore, "like_count": likes, "comment_count": comments,
			"is_liked": isLiked, "is_saved": isSaved,
		})
	}
	return posts
}

func buildIDArray(ids []int64) string {
	s := ""
	for i, id := range ids {
		if i > 0 {
			s += ","
		}
		s += strconv.FormatInt(id, 10)
	}
	return s
}
