package handlers

import (
	"database/sql"
	"markethouse/internal/services"
	"strconv"

	"github.com/gin-gonic/gin"
)

type FollowHandler struct {
	Service *services.FollowService
	Hub     *services.Hub
	DB      *sql.DB
}

// notifyFollow pushes a realtime follow/unfollow event to both the actor
// (their "following" count changed) and the target (their "followers" count
// changed), so open profile pages update without a refresh or re-login.
func (h *FollowHandler) notifyFollow(actorID, targetID int64, following bool) {
	if h.Hub == nil {
		return
	}
	payload := map[string]interface{}{
		"type":       "follow",
		"user_id":    targetID,
		"actor_id":   actorID,
		"following":  following,
		"delta":      1,
	}
	if !following {
		payload["delta"] = -1
	}
	h.Hub.SendToUser(targetID, payload)
	h.Hub.SendToUser(actorID, payload)
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
	h.notifyFollow(userID, req.UserID, true)

	// Persistent "started following you" notification for the target, plus
	// friend-of-friend suggestions: people the new follower may know (the
	// followed account's other followers, excluding ones already followed).
	if h.DB != nil {
		actor := userName(h.DB, userID)
		NotifyWithWS(h.DB, h.Hub, req.UserID, userID, "follow",
			"New follower", actor+" started following you", "user", userID)

		rows, err := h.DB.Query(`
			SELECT u.id, COALESCE(u.username,'')
			FROM follows f JOIN users u ON u.id = f.follower_id
			WHERE f.following_id = $1 AND f.follower_id <> $2
			  AND NOT EXISTS (SELECT 1 FROM follows mine
			                 WHERE mine.follower_id = $2 AND mine.following_id = u.id)
			LIMIT 5`, req.UserID, userID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var fid int64
				var fn string
				if rows.Scan(&fid, &fn) == nil {
					NotifyWithWS(h.DB, h.Hub, userID, fid, "suggested_friend",
						"People you may know", fn+" is followed by someone you follow", "user", fid)
				}
			}
		}
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
	h.notifyFollow(userID, req.UserID, false)
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
