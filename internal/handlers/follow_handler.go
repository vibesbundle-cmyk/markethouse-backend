package handlers

import (
	"markethouse/internal/services"
	"strconv"

	"github.com/gin-gonic/gin"
)

type FollowHandler struct {
	Service *services.FollowService
}

// FOLLOW
func (h *FollowHandler) Follow(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	err := h.Service.FollowUser(userID, req.UserID)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "followed"})
}

// UNFOLLOW
func (h *FollowHandler) Unfollow(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		UserID int64 `json:"user_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	h.Service.UnfollowUser(userID, req.UserID)
	c.JSON(200, gin.H{"message": "unfollowed"})
}

// FOLLOW STATS — counts only
func (h *FollowHandler) Stats(c *gin.Context) {
	userID := c.Param("user_id")
	id, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid user ID"})
		return
	}

	followers, following := h.Service.GetFollowStats(id)
	c.JSON(200, gin.H{
		"followers": followers,
		"following": following,
	})
}

// GET FOLLOWERS LIST  — GET /follow/followers/:user_id
func (h *FollowHandler) GetFollowers(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid user ID"})
		return
	}

	viewerID := int64(0)
	if id, exists := c.Get("user_id"); exists {
		viewerID = id.(int64)
	}

	list, err := h.Service.GetFollowers(id, viewerID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"followers": list})
}

// GET FOLLOWING LIST  — GET /follow/following/:user_id
func (h *FollowHandler) GetFollowing(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid user ID"})
		return
	}

	viewerID := int64(0)
	if id, exists := c.Get("user_id"); exists {
		viewerID = id.(int64)
	}

	list, err := h.Service.GetFollowing(id, viewerID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"following": list})
}
