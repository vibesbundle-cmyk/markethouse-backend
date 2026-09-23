package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"markethouse/internal/services"

	"github.com/gin-gonic/gin"
)

type SearchHandler struct {
	Service *services.SearchService
}

func (h *SearchHandler) Search(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := int64(0)
	if userID != nil {
		uid = userID.(int64)
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' required"})
		return
	}

	// Parse types filter
	typesParam := c.Query("types")
	var types []string
	if typesParam != "" {
		types = strings.Split(typesParam, ",")
	}

	// Parse other options
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	category := c.Query("category")
	sortBy := c.DefaultQuery("sort", "relevance")

	opts := services.SearchOptions{
		Query:    query,
		UserID:   uid,
		Types:    types,
		Limit:    limit,
		Offset:   offset,
		Category: category,
		SortBy:   sortBy,
	}

	results, err := h.Service.Search(opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"query":   query,
		"results": results,
		"count":   len(results),
		// Legacy client shape: GlobalSearchScreen / searchUsers / SearchTabbedResults
		// read people/communities/posts (and expect caption/username/profile_photo
		// flattened onto each post, name/icon on communities, profile_photo on users).
		"people":      groupSearchPeople(results),
		"communities": groupSearchCommunities(results),
		"posts":       groupSearchPosts(results),
		"marketplace": groupSearchMarketplace(results),
	})
}

// groupSearch* flatten SearchResult (+ Extra) into the maps the Flutter client
// already renders, so /search keeps one endpoint while serving both shapes.
func groupSearchPeople(results []services.SearchResult) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, r := range results {
		if r.Type != "users" {
			continue
		}
		m := map[string]interface{}{
			"id":            r.ID,
			"username":      extraStr(r, "username"),
			"full_name":     extraStr(r, "full_name"),
			"bio":           extraStr(r, "bio"),
			"profile_photo": r.Image,
			"is_verified":   extraBool(r, "is_verified"),
			"title":         r.Title,
			"subtitle":      r.Subtitle,
		}
		out = append(out, m)
	}
	return out
}

func groupSearchCommunities(results []services.SearchResult) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, r := range results {
		if r.Type != "communities" {
			continue
		}
		m := map[string]interface{}{
			"id":           r.ID,
			"name":         r.Title,
			"icon":         r.Image,
			"slug":         extraStr(r, "slug"),
			"description":  extraStr(r, "description"),
			"member_count": extraNum(r, "member_count"),
			"category":     extraStr(r, "category"),
			"type":         extraStr(r, "type"),
			"cover_photo":  extraStr(r, "cover_photo"),
			"title":        r.Title,
			"subtitle":     r.Subtitle,
		}
		out = append(out, m)
	}
	return out
}

func groupSearchPosts(results []services.SearchResult) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, r := range results {
		if r.Type != "posts" {
			continue
		}
		username := extraStr(r, "username")
		if username == "" {
			username = strings.TrimPrefix(r.Subtitle, "@")
			if i := strings.Index(username, " "); i >= 0 {
				username = username[:i]
			}
		}
		m := map[string]interface{}{
			"id":            r.ID,
			"caption":       extraStr(r, "caption_full"),
			"username":      username,
			"profile_photo": extraStr(r, "profile_photo"),
			"media_url":     r.Image,
			"media_type":    extraStr(r, "media_type"),
			"category":      extraStr(r, "category"),
			"location":      extraStr(r, "location"),
			"created_at":    extraStr(r, "created_at"),
			"user_id":       extraNum(r, "user_id"),
			"like_count":    extraNum(r, "like_count"),
			"title":         r.Title,
			"subtitle":      r.Subtitle,
		}
		out = append(out, m)
	}
	return out
}

func groupSearchMarketplace(results []services.SearchResult) []map[string]interface{} {
	out := []map[string]interface{}{}
	for _, r := range results {
		if r.Type == "users" || r.Type == "communities" || r.Type == "posts" {
			continue
		}
		m := map[string]interface{}{
			"id":       r.ID,
			"type":     r.Type,
			"title":    r.Title,
			"subtitle": r.Subtitle,
			"image":    r.Image,
		}
		for k, v := range r.Extra {
			m[k] = v
		}
		out = append(out, m)
	}
	return out
}

func extraStr(r services.SearchResult, key string) string {
	if v, ok := r.Extra[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func extraNum(r services.SearchResult, key string) float64 {
	if v, ok := r.Extra[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int64:
			return float64(n)
		case int:
			return float64(n)
		}
	}
	return 0
}

func extraBool(r services.SearchResult, key string) bool {
	if v, ok := r.Extra[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func (h *SearchHandler) Autocomplete(c *gin.Context) {
	query := c.Query("q")
	if len(query) < 2 {
		c.JSON(http.StatusOK, gin.H{"suggestions": []interface{}{}})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	suggestions, err := h.Service.Autocomplete(query, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"suggestions": suggestions})
}

// SearchPosts searches only posts (for feed integration)
func (h *SearchHandler) SearchPosts(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := int64(0)
	if userID != nil {
		uid = userID.(int64)
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' required"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	opts := services.SearchOptions{
		Query:  query,
		UserID: uid,
		Types:  []string{"posts"},
		Limit:  limit,
		Offset: offset,
		SortBy: "relevance",
	}

	results, err := h.Service.Search(opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"posts": results})
}

// SearchUsers searches only users (for mentions, follow suggestions)
func (h *SearchHandler) SearchUsers(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := int64(0)
	if userID != nil {
		uid = userID.(int64)
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' required"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	opts := services.SearchOptions{
		Query:  query,
		UserID: uid,
		Types:  []string{"users"},
		Limit:  limit,
		SortBy: "relevance",
	}

	results, err := h.Service.Search(opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Same flattened shape the Flutter client already reads from `people`
	// (Api.searchUsers / SearchTabbedResults / GlobalSearchScreen).
	c.JSON(http.StatusOK, gin.H{
		"users":  results,
		"people": groupSearchPeople(results),
		"count":  len(results),
	})
}

// SearchMarketplace searches supplies, demands, products
func (h *SearchHandler) SearchMarketplace(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := int64(0)
	if userID != nil {
		uid = userID.(int64)
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' required"})
		return
	}

	category := c.Query("category")
	sortBy := c.DefaultQuery("sort", "relevance")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	opts := services.SearchOptions{
		Query:    query,
		UserID:   uid,
		Types:    []string{"supplies", "demands", "products"},
		Category: category,
		Limit:    limit,
		Offset:   offset,
		SortBy:   sortBy,
	}

	results, err := h.Service.Search(opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"results": results})
}

// SearchCommunities searches communities
func (h *SearchHandler) SearchCommunities(c *gin.Context) {
	userID, _ := c.Get("user_id")
	uid := int64(0)
	if userID != nil {
		uid = userID.(int64)
	}

	query := c.Query("q")
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query parameter 'q' required"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	opts := services.SearchOptions{
		Query:  query,
		UserID: uid,
		Types:  []string{"communities"},
		Limit:  limit,
		Offset: offset,
		SortBy: "relevance",
	}

	results, err := h.Service.Search(opts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"communities": results})
}

// Trending searches trending hashtags/topics
func (h *SearchHandler) Trending(c *gin.Context) {
	// For now, return trending hashtags from posts
	// In production, you'd track this separately
	days := 7
	limit := 20

	rows, err := h.Service.DB.Query(`
		SELECT tag, COUNT(*) as uses
		FROM post_hashtags
		WHERE created_at > NOW() - ($1 || ' days')::interval
		GROUP BY tag
		ORDER BY uses DESC, tag ASC
		LIMIT $2`, days, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var trending []map[string]interface{}
	for rows.Next() {
		var tag string
		var uses int
		if err := rows.Scan(&tag, &uses); err != nil {
			continue
		}
		trending = append(trending, map[string]interface{}{
			"tag":  tag,
			"uses": uses,
		})
	}

	c.JSON(http.StatusOK, gin.H{"trending": trending})
}

// Popular returns the most popular search queries
func (h *SearchHandler) Popular(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))

	popular, err := h.Service.GetPopularSearches(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"popular": popular})
}