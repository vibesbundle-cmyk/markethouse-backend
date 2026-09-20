package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

type SearchService struct {
	DB    *sql.DB
	Redis *redis.Client
}

func NewSearchService(db *sql.DB, redisClient *redis.Client) *SearchService {
	return &SearchService{DB: db, Redis: redisClient}
}

type SearchResult struct {
	Type       string                 `json:"type"`       // posts, users, supplies, demands, products, communities
	ID         int64                  `json:"id"`
	Title      string                 `json:"title"`      // caption, name, goods_name, etc.
	Subtitle   string                 `json:"subtitle"`   // username, category, description snippet
	Image      string                 `json:"image"`      // profile_photo, media_url, first image
	Score      float64                `json:"score"`      // relevance score
	Extra      map[string]interface{} `json:"extra"`      // type-specific extra fields
}

type SearchOptions struct {
	Query      string
	UserID     int64 // for audience filtering
	Types      []string // filter by type: posts, users, supplies, demands, products, communities
	Limit      int
	Offset     int
	Category   string
	Location   string
	Lat        *float64
	Lon        *float64
	RadiusKm   int
	SortBy     string // relevance, recent, price_asc, price_desc
}

func (s *SearchService) Search(opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.Limit > 100 {
		opts.Limit = 100
	}

	// Parse query for tsquery
	tsQuery := s.toTSQuery(opts.Query)
	prefixQuery := s.toPrefixQuery(opts.Query)

	// Try cache first (skip for user-specific searches with UserID > 0 and offset > 0)
	cacheKey := s.cacheKey(opts, tsQuery)
	if s.Redis != nil && opts.UserID == 0 && opts.Offset == 0 {
		if cached, err := s.getCached(cacheKey); err == nil && cached != nil {
			return cached, nil
		}
	}

	var results []SearchResult

	// Determine which types to search
	searchTypes := opts.Types
	if len(searchTypes) == 0 {
		searchTypes = []string{"posts", "users", "supplies", "demands", "products", "communities"}
	}

	for _, t := range searchTypes {
		var typeResults []SearchResult
		var err error

		switch t {
		case "posts":
			typeResults, err = s.searchPosts(tsQuery, prefixQuery, opts)
		case "users":
			typeResults, err = s.searchUsers(tsQuery, prefixQuery, opts)
		case "supplies":
			typeResults, err = s.searchSupplies(tsQuery, prefixQuery, opts)
		case "demands":
			typeResults, err = s.searchDemands(tsQuery, prefixQuery, opts)
		case "products":
			typeResults, err = s.searchProducts(tsQuery, prefixQuery, opts)
		case "communities":
			typeResults, err = s.searchCommunities(tsQuery, prefixQuery, opts)
		}

		if err != nil {
			continue // Skip failed types, don't fail entire search
		}
		results = append(results, typeResults...)

	// Cache results (TTL: posts/users 30s, marketplace 60s, communities 5m)
	if s.Redis != nil && opts.UserID == 0 && opts.Offset == 0 {
		ttl := s.cacheTTL(opts.Types)
		_ = s.setCached(cacheKey, results, ttl)
	}
	}

	// Track popular search queries (analytics)
	if s.Redis != nil && opts.UserID == 0 && opts.Query != "" && opts.Offset == 0 {
		go func(q string) {
			ctx := context.Background()
			_ = s.Redis.ZIncrBy(ctx, "search:popular", 1, q).Err()
			_ = s.Redis.Expire(ctx, "search:popular", 7*24*time.Hour).Err()
		}(opts.Query)
	}

	// Sort combined results by score
	// Note: In production, you'd want a unified query with UNION ALL
	return results, nil
}

func (s *SearchService) searchPosts(tsQuery, prefixQuery string, opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT 
			p.id,
			p.caption,
			p.media_url,
			p.media_type,
			p.category,
			p.location,
			p.created_at,
			u.id as user_id,
			u.username,
			u.profile_photo,
			ts_rank_cd(p.search_vector, websearch_to_tsquery('english', $1)) as rank
		FROM posts p
		JOIN users u ON p.user_id = u.id
		WHERE p.search_vector @@ websearch_to_tsquery('english', $1)
	`
	args := []interface{}{tsQuery}
	argIdx := 2

	// Audience filter
	if opts.UserID > 0 {
		query += ` AND (
			p.audience = 'public'
			OR (p.audience = 'followers' AND (
				p.user_id = $` + string(rune(argIdx+'0')) + `
				OR EXISTS(SELECT 1 FROM follows f WHERE f.following_id = p.user_id AND f.follower_id = $` + string(rune(argIdx+'0')) + `)
			))
			OR (p.audience = 'private' AND (
				p.user_id = $` + string(rune(argIdx+'0')) + `
				OR ',' || p.audience_user_ids || ',' LIKE '%,' || $` + string(rune(argIdx+'0')) + ` || ',%'
			))
		)`
		args = append(args, opts.UserID)
		argIdx++
	}

	if opts.Category != "" {
		query += ` AND p.category = $` + string(rune(argIdx+'0'))
		args = append(args, opts.Category)
		argIdx++
	}

	if opts.SortBy == "recent" {
		query += ` ORDER BY p.created_at DESC`
	} else {
		query += ` ORDER BY rank DESC, p.created_at DESC`
	}

	query += ` LIMIT $` + string(rune(argIdx+'0')) + ` OFFSET $` + string(rune(argIdx+1+'0'))
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var caption, mediaURL, mediaType, category, location, createdAt sql.NullString
		var userID sql.NullInt64
		var username, profilePhoto sql.NullString
		var rank sql.NullFloat64

		err := rows.Scan(&r.ID, &caption, &mediaURL, &mediaType, &category, &location, &createdAt,
			&userID, &username, &profilePhoto, &rank)
		if err != nil {
			return nil, err
		}

		r.Type = "posts"
		r.Title = caption.String
		if len(r.Title) > 100 {
			r.Title = r.Title[:100] + "..."
		}
		r.Subtitle = "@" + username.String
		r.Image = mediaURL.String
		r.Score = rank.Float64
		r.Extra = map[string]interface{}{
			"media_type": mediaType.String,
			"category":   category.String,
			"location":   location.String,
			"created_at": createdAt.String,
			"user_id":    userID.Int64,
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SearchService) searchUsers(tsQuery, prefixQuery string, opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT 
			u.id,
			u.username,
			u.full_name,
			u.bio,
			u.profile_photo,
			u.account_type,
			u.business_name,
			u.business_category,
			u.is_verified,
			ts_rank_cd(u.search_vector, websearch_to_tsquery('english', $1)) as rank
		FROM users u
		WHERE u.search_vector @@ websearch_to_tsquery('english', $1)
		AND u.username IS NOT NULL
	`
	args := []interface{}{tsQuery}

	if opts.SortBy == "recent" {
		query += ` ORDER BY u.created_at DESC`
	} else {
		query += ` ORDER BY rank DESC, u.is_verified DESC, u.created_at DESC`
	}

	query += ` LIMIT $2 OFFSET $3`
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var username, fullName, bio, profilePhoto, accountType, businessName, businessCategory sql.NullString
		var isVerified sql.NullBool
		var rank sql.NullFloat64

		err := rows.Scan(&r.ID, &username, &fullName, &bio, &profilePhoto,
			&accountType, &businessName, &businessCategory, &isVerified, &rank)
		if err != nil {
			return nil, err
		}

		r.Type = "users"
		displayName := fullName.String
		if displayName == "" {
			displayName = username.String
		}
		r.Title = displayName
		r.Subtitle = "@" + username.String
		if bio.String != "" {
			r.Subtitle += " • " + truncate(bio.String, 60)
		}
		r.Image = profilePhoto.String
		r.Score = rank.Float64
		r.Extra = map[string]interface{}{
			"username":          username.String,
			"full_name":         fullName.String,
			"bio":               bio.String,
			"account_type":      accountType.String,
			"business_name":     businessName.String,
			"business_category": businessCategory.String,
			"is_verified":       isVerified.Bool,
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SearchService) searchSupplies(tsQuery, prefixQuery string, opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT 
			s.id,
			s.goods_name,
			s.description,
			s.category,
			s.brand,
			s.condition,
			s.price,
			s.location,
			s.photos,
			s.created_at,
			u.id as user_id,
			u.username,
			u.profile_photo,
			ts_rank_cd(s.search_vector, websearch_to_tsquery('english', $1)) as rank
		FROM supplies s
		JOIN users u ON s.user_id = u.id
		WHERE s.search_vector @@ websearch_to_tsquery('english', $1)
		AND s.is_active = true
	`
	args := []interface{}{tsQuery}
	argIdx := 2

	if opts.Category != "" {
		query += ` AND s.category = $` + string(rune(argIdx+'0'))
		args = append(args, opts.Category)
		argIdx++
	}

	if opts.SortBy == "price_asc" {
		query += ` ORDER BY s.price ASC`
	} else if opts.SortBy == "price_desc" {
		query += ` ORDER BY s.price DESC`
	} else if opts.SortBy == "recent" {
		query += ` ORDER BY s.created_at DESC`
	} else {
		query += ` ORDER BY rank DESC, s.created_at DESC`
	}

	query += ` LIMIT $` + string(rune(argIdx+'0')) + ` OFFSET $` + string(rune(argIdx+1+'0'))
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var goodsName, description, category, brand, condition, location, createdAt sql.NullString
		var price sql.NullFloat64
		var photos pq.StringArray
		var userID sql.NullInt64
		var username, profilePhoto sql.NullString
		var rank sql.NullFloat64

		err := rows.Scan(&r.ID, &goodsName, &description, &category, &brand, &condition,
			&price, &location, &photos, &createdAt, &userID, &username, &profilePhoto, &rank)
		if err != nil {
			return nil, err
		}

		r.Type = "supplies"
		r.Title = goodsName.String
		r.Subtitle = category.String
		if brand.String != "" {
			r.Subtitle += " • " + brand.String
		}
		if price.Valid {
			r.Subtitle += " • ₦" + formatPrice(price.Float64)
		}
		if len(photos) > 0 {
			r.Image = photos[0]
		}
		r.Score = rank.Float64
		r.Extra = map[string]interface{}{
			"description": description.String,
			"category":    category.String,
			"brand":       brand.String,
			"condition":   condition.String,
			"price":       price.Float64,
			"location":    location.String,
			"photos":      photos,
			"user_id":     userID.Int64,
			"username":    username.String,
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SearchService) searchDemands(tsQuery, prefixQuery string, opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT 
			d.id,
			d.looking_for,
			d.description,
			d.category,
			d.min_price,
			d.max_price,
			d.location,
			d.condition_pref,
			d.urgency,
			d.created_at,
			u.id as user_id,
			u.username,
			u.profile_photo,
			ts_rank_cd(d.search_vector, websearch_to_tsquery('english', $1)) as rank
		FROM demands d
		JOIN users u ON d.user_id = u.id
		WHERE d.search_vector @@ websearch_to_tsquery('english', $1)
		AND d.is_active = true
	`
	args := []interface{}{tsQuery}
	argIdx := 2

	if opts.Category != "" {
		query += ` AND d.category = $` + string(rune(argIdx+'0'))
		args = append(args, opts.Category)
		argIdx++
	}

	query += ` ORDER BY rank DESC, d.created_at DESC LIMIT $` + string(rune(argIdx+'0')) + ` OFFSET $` + string(rune(argIdx+1+'0'))
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var lookingFor, description, category, location, urgency, createdAt sql.NullString
		var minPrice, maxPrice sql.NullFloat64
		var conditionPref pq.StringArray
		var userID sql.NullInt64
		var username, profilePhoto sql.NullString
		var rank sql.NullFloat64

		err := rows.Scan(&r.ID, &lookingFor, &description, &category, &minPrice, &maxPrice,
			&location, &conditionPref, &urgency, &createdAt, &userID, &username, &profilePhoto, &rank)
		if err != nil {
			return nil, err
		}

		r.Type = "demands"
		r.Title = lookingFor.String
		r.Subtitle = category.String
		if minPrice.Valid || maxPrice.Valid {
			priceRange := "₦"
			if minPrice.Valid {
				priceRange += formatPrice(minPrice.Float64)
			}
			priceRange += " - "
			if maxPrice.Valid {
				priceRange += formatPrice(maxPrice.Float64)
			}
			r.Subtitle += " • " + priceRange
		}
		r.Image = profilePhoto.String
		r.Score = rank.Float64
		r.Extra = map[string]interface{}{
			"description":    description.String,
			"category":       category.String,
			"min_price":      minPrice.Float64,
			"max_price":      maxPrice.Float64,
			"location":       location.String,
			"condition_pref": conditionPref,
			"urgency":        urgency.String,
			"user_id":        userID.Int64,
			"username":       username.String,
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SearchService) searchProducts(tsQuery, prefixQuery string, opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT 
			p.id,
			p.name,
			p.description,
			p.category,
			p.price,
			p.images,
			p.stock_count,
			p.is_unlimited_stock,
			p.created_at,
			u.id as user_id,
			u.username,
			u.profile_photo,
			u.business_name,
			ts_rank_cd(p.search_vector, websearch_to_tsquery('english', $1)) as rank
		FROM products p
		JOIN users u ON p.user_id = u.id
		WHERE p.search_vector @@ websearch_to_tsquery('english', $1)
		AND p.is_active = true
	`
	args := []interface{}{tsQuery}
	argIdx := 2

	if opts.Category != "" {
		query += ` AND p.category = $` + string(rune(argIdx+'0'))
		args = append(args, opts.Category)
		argIdx++
	}

	if opts.SortBy == "price_asc" {
		query += ` ORDER BY p.price ASC`
	} else if opts.SortBy == "price_desc" {
		query += ` ORDER BY p.price DESC`
	} else if opts.SortBy == "recent" {
		query += ` ORDER BY p.created_at DESC`
	} else {
		query += ` ORDER BY rank DESC, p.created_at DESC`
	}

	query += ` LIMIT $` + string(rune(argIdx+'0')) + ` OFFSET $` + string(rune(argIdx+1+'0'))
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var name, description, category, createdAt sql.NullString
		var price sql.NullFloat64
		var images pq.StringArray
		var stockCount sql.NullInt64
		var isUnlimitedStock sql.NullBool
		var userID sql.NullInt64
		var username, profilePhoto, businessName sql.NullString
		var rank sql.NullFloat64

		err := rows.Scan(&r.ID, &name, &description, &category, &price, &images,
			&stockCount, &isUnlimitedStock, &createdAt, &userID, &username, &profilePhoto, &businessName, &rank)
		if err != nil {
			return nil, err
		}

		r.Type = "products"
		r.Title = name.String
		r.Subtitle = category.String
		if businessName.String != "" {
			r.Subtitle += " • " + businessName.String
		}
		if price.Valid {
			r.Subtitle += " • ₦" + formatPrice(price.Float64)
		}
		if len(images) > 0 {
			r.Image = images[0]
		}
		r.Score = rank.Float64
		r.Extra = map[string]interface{}{
			"description":        description.String,
			"category":           category.String,
			"price":              price.Float64,
			"stock_count":        stockCount.Int64,
			"is_unlimited_stock": isUnlimitedStock.Bool,
			"images":             images,
			"user_id":            userID.Int64,
			"username":           username.String,
			"business_name":      businessName.String,
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

func (s *SearchService) searchCommunities(tsQuery, prefixQuery string, opts SearchOptions) ([]SearchResult, error) {
	query := `
		SELECT 
			c.id,
			c.name,
			c.slug,
			c.description,
			c.icon,
			c.cover_photo,
			c.member_count,
			c.type,
			c.created_at,
			ts_rank_cd(c.search_vector, websearch_to_tsquery('english', $1)) as rank
		FROM communities c
		WHERE c.search_vector @@ websearch_to_tsquery('english', $1)
	`
	args := []interface{}{tsQuery}

	query += ` ORDER BY rank DESC, c.member_count DESC LIMIT $2 OFFSET $3`
	args = append(args, opts.Limit, opts.Offset)

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var name, slug, description, icon, coverPhoto, ctype, createdAt sql.NullString
		var memberCount sql.NullInt64
		var rank sql.NullFloat64

		err := rows.Scan(&r.ID, &name, &slug, &description, &icon, &coverPhoto,
			&memberCount, &ctype, &createdAt, &rank)
		if err != nil {
			return nil, err
		}

		r.Type = "communities"
		r.Title = name.String
		r.Subtitle = truncate(description.String, 80)
		if memberCount.Valid {
			r.Subtitle += " • " + formatNumber(memberCount.Int64) + " members"
		}
		r.Image = icon.String
		if r.Image == "" {
			r.Image = coverPhoto.String
		}
		r.Score = rank.Float64
		r.Extra = map[string]interface{}{
			"slug":          slug.String,
			"description":   description.String,
			"member_count":  memberCount.Int64,
			"type":          ctype.String,
			"cover_photo":   coverPhoto.String,
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// Autocomplete for search suggestions
func (s *SearchService) Autocomplete(query string, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 10
	}

	// Use trigram similarity for prefix matching
	q := `
		SELECT 'users' as type, id, username as title, full_name as subtitle, profile_photo as image
		FROM users
		WHERE username ILIKE $1 || '%' OR full_name ILIKE $1 || '%'
		ORDER BY is_verified DESC, 
			CASE WHEN username ILIKE $1 || '%' THEN 0 ELSE 1 END,
			similarity(username, $1) DESC
		LIMIT $2
	`
	rows, err := s.DB.Query(q, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []map[string]interface{}
	for rows.Next() {
		var r map[string]interface{}
		var id int64
		var title, subtitle, image sql.NullString
		var typ string
		if err := rows.Scan(&typ, &id, &title, &subtitle, &image); err != nil {
			return nil, err
		}
		r = map[string]interface{}{
			"type":      typ,
			"id":        id,
			"title":     title.String,
			"subtitle":  subtitle.String,
			"image":     image.String,
		}
		results = append(results, r)
	}

	// Also search supplies/products
	q2 := `
		SELECT 'supplies' as type, id, goods_name as title, category as subtitle, photos[1] as image
		FROM supplies
		WHERE goods_name ILIKE $1 || '%' AND is_active = true
		ORDER BY similarity(goods_name, $1) DESC
		LIMIT $2
	`
	rows2, err := s.DB.Query(q2, query, limit)
	if err != nil {
		return results, nil // Return what we have
	}
	defer rows2.Close()

	for rows2.Next() {
		var r map[string]interface{}
		var id int64
		var title, subtitle, image sql.NullString
		var typ string
		if err := rows2.Scan(&typ, &id, &title, &subtitle, &image); err != nil {
			continue
		}
		r = map[string]interface{}{
			"type":      typ,
			"id":        id,
			"title":     title.String,
			"subtitle":  subtitle.String,
			"image":     image.String,
		}
		results = append(results, r)
	}

	return results, nil
}

// Helper functions
func (s *SearchService) toTSQuery(query string) string {
	// Convert user query to websearch_to_tsquery format
	// Handle quoted phrases, OR, NOT
	query = strings.TrimSpace(query)
	if query == "" {
		return "''"
	}
	// Escape single quotes
	query = strings.ReplaceAll(query, "'", "''")
	return query
}

func (s *SearchService) toPrefixQuery(query string) string {
	// For prefix/autocomplete matching
	query = strings.TrimSpace(query)
	if query == "" {
		return "''"
	}
	query = strings.ReplaceAll(query, "'", "''")
	return query + ":*"
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatPrice(price float64) string {
	if price >= 1000000 {
		return string(rune(price/1000000)) + "M"
	} else if price >= 1000 {
		return string(rune(price/1000)) + "K"
	}
	return string(rune(price))
}

func formatNumber(n int64) string {
	if n >= 1000000 {
		return string(rune(n/1000000)) + "M"
	} else if n >= 1000 {
		return string(rune(n/1000)) + "K"
	}
	return string(rune(n))
}

// cacheKey generates a unique cache key for search options
func (s *SearchService) cacheKey(opts SearchOptions, tsQuery string) string {
	parts := []string{"search", tsQuery}
	if len(opts.Types) > 0 {
		parts = append(parts, strings.Join(opts.Types, ","))
	} else {
		parts = append(parts, "all")
	}
	if opts.Category != "" {
		parts = append(parts, "cat:"+opts.Category)
	}
	if opts.SortBy != "" {
		parts = append(parts, "sort:"+opts.SortBy)
	}
	parts = append(parts, "lim:"+string(rune(opts.Limit)))
	parts = append(parts, "off:"+string(rune(opts.Offset)))
	return strings.Join(parts, "|")
}

// cacheTTL returns TTL based on search types
func (s *SearchService) cacheTTL(types []string) time.Duration {
	if len(types) == 0 {
		return 30 * time.Second // default
	}
	for _, t := range types {
		if t == "posts" || t == "users" {
			return 30 * time.Second // high churn
		}
		if t == "supplies" || t == "demands" || t == "products" {
			return 60 * time.Second // medium churn
		}
	}
	return 5 * time.Minute // communities, low churn
}

func (s *SearchService) getCached(key string) ([]SearchResult, error) {
	val, err := s.Redis.Get(context.Background(), key).Bytes()
	if err != nil {
		return nil, err
	}
	var results []SearchResult
	if err := json.Unmarshal(val, &results); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *SearchService) setCached(key string, results []SearchResult, ttl time.Duration) error {
	data, err := json.Marshal(results)
	if err != nil {
		return err
	}
	return s.Redis.Set(context.Background(), key, data, ttl).Err()
}

// GetPopularSearches returns the top N most searched queries
func (s *SearchService) GetPopularSearches(limit int) ([]map[string]interface{}, error) {
	if s.Redis == nil {
		return []map[string]interface{}{}, nil
	}
	if limit <= 0 {
		limit = 10
	}
	vals, err := s.Redis.ZRevRangeWithScores(context.Background(), "search:popular", 0, int64(limit-1)).Result()
	if err != nil {
		return nil, err
	}
	var out []map[string]interface{}
	for _, v := range vals {
		out = append(out, map[string]interface{}{
			"query": v.Member.(string),
			"count": int(v.Score),
		})
	}
	return out, nil
}