package handlers

import (
	"strconv"

	"markethouse/internal/services"

	"github.com/gin-gonic/gin"
)

type InteractionHandler struct {
	Service *services.InteractionService
	Hub     *services.Hub
}

// ================= LIKE =================
func (h *InteractionHandler) Like(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	if err := h.Service.Like(userID, postID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if h.Hub != nil {
		h.Hub.Broadcast(map[string]interface{}{"type": "post_like", "post_id": postID, "user_id": userID, "delta": 1})
	}
	c.JSON(200, gin.H{"message": "liked"})
}

func (h *InteractionHandler) Unlike(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	if err := h.Service.Unlike(userID, postID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if h.Hub != nil {
		h.Hub.Broadcast(map[string]interface{}{"type": "post_like", "post_id": postID, "user_id": userID, "delta": -1})
	}
	c.JSON(200, gin.H{"message": "unliked"})
}

// ================= COMMENT =================
func (h *InteractionHandler) Comment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	var req struct {
		Content         string `json:"content"`
		ParentCommentID *int64 `json:"parent_comment_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	id, err := h.Service.Comment(userID, postID, req.Content, req.ParentCommentID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	if h.Hub != nil {
		h.Hub.Broadcast(map[string]interface{}{"type": "post_comment", "post_id": postID, "comment_id": id, "delta": 1})
	}
	c.JSON(200, gin.H{"message": "comment added", "id": id})
}

func (h *InteractionHandler) GetComments(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	comments, err := h.Service.GetComments(postID, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"comments": comments})
}

func (h *InteractionHandler) LikeComment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commentID, err := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid comment ID"})
		return
	}
	if err := h.Service.LikeComment(userID, commentID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "comment liked"})
}

func (h *InteractionHandler) UnlikeComment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commentID, err := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid comment ID"})
		return
	}
	if err := h.Service.UnlikeComment(userID, commentID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "comment unliked"})
}

func (h *InteractionHandler) DeleteComment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commentID, err := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid comment ID"})
		return
	}
	if err := h.Service.DeleteComment(userID, commentID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "comment deleted"})
}

// ================= SAVE =================
func (h *InteractionHandler) SavePost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	if err := h.Service.Save(userID, postID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "saved"})
}

func (h *InteractionHandler) UnsavePost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	if err := h.Service.Unsave(userID, postID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "unsaved"})
}

// GET /posts/saved — get saved posts for the current user
func (h *InteractionHandler) SavedPosts(c *gin.Context) {
	userID := c.GetInt64("user_id")
	posts, err := h.Service.GetSavedPosts(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

// ================= RESHARE =================
func (h *InteractionHandler) Reshare(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	if err := h.Service.Reshare(userID, postID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "reshared"})
}

func (h *InteractionHandler) Unreshare(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	if err := h.Service.Unreshare(userID, postID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "unreshared"})
}

func (h *InteractionHandler) GetReshares(c *gin.Context) {
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	count, err := h.Service.ReshareCount(postID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"post_id": postID, "reshares": count})
}
