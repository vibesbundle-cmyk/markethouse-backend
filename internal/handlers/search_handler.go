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
	})
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

	c.JSON(http.StatusOK, gin.H{"users": results})
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