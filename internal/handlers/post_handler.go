package handlers

import (
	"database/sql"
	"log"
	"markethouse/internal/services"
	"mime/multipart"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

type PostHandler struct {
	Service *services.PostService
	Hub     *services.Hub
	DB      *sql.DB
}

// CREATE POST
func (h *PostHandler) CreatePost(c *gin.Context) {
	userID := c.GetInt64("user_id")

	caption := c.PostForm("caption")
	postType := c.DefaultPostForm("post_type", "social")
	category := c.DefaultPostForm("category", "Other")
	price, _ := strconv.ParseFloat(c.DefaultPostForm("price", "0"), 64)
	isLocked, _ := strconv.ParseBool(c.DefaultPostForm("is_locked", "false"))
	taggedUsers := c.PostForm("tagged_users") // comma-separated user IDs
	location := c.PostForm("location")
	audience := c.DefaultPostForm("audience", "public")
	audienceUserIDs := c.PostForm("audience_user_ids")

	var lat, lng *float64
	if l := c.PostForm("latitude"); l != "" {
		if v, err := strconv.ParseFloat(l, 64); err == nil {
			lat = &v
		}
	}
	if lo := c.PostForm("longitude"); lo != "" {
		if v, err := strconv.ParseFloat(lo, 64); err == nil {
			lng = &v
		}
	}

	var files []*multipart.FileHeader
	if form, err := c.MultipartForm(); err == nil {
		files = form.File["files"]
	}
	if len(files) == 0 {
		// Backward compatibility: older app builds send a single "file" field.
		if f, err := c.FormFile("file"); err == nil {
			files = []*multipart.FileHeader{f}
		}
	}
	if len(files) == 0 {
		c.JSON(400, gin.H{"error": "at least one file is required"})
		return
	}

	post, err := h.Service.CreatePost(userID, caption, postType, category, price, isLocked, taggedUsers, location, audience, audienceUserIDs, lat, lng, files)
	if err != nil {
		log.Printf("[POST] create error user=%d: %v", userID, err)
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	if h.Hub != nil {
		h.Hub.Broadcast(map[string]interface{}{
			"type": "post_created", "user_id": userID, "post_id": post.ID,
		})
	}

	// Notify anyone tagged (by user ID) in the post.
	if taggedUsers != "" {
		var uname string
		h.DB.QueryRow(`SELECT COALESCE(NULLIF(username,''), NULLIF(full_name,'')) FROM users WHERE id=$1`, userID).Scan(&uname)
		for _, s := range strings.Split(taggedUsers, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			if tid, e := strconv.ParseInt(s, 10, 64); e == nil && tid != userID {
				NotifyWithWS(h.DB, h.Hub, tid, userID, "tag",
					uname+" tagged you in a post", caption, "post", post.ID)
			}
		}
	}

	c.JSON(200, post)
}

// EDIT POST (caption + tags only — media is immutable)
func (h *PostHandler) EditPost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}

	var req struct {
		Caption     string `json:"caption"`
		TaggedUsers string `json:"tagged_users"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := h.Service.EditPost(userID, postID, req.Caption, req.TaggedUsers); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "post updated"})
}

// DELETE POST
func (h *PostHandler) DeletePost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}

	if err := h.Service.DeletePost(userID, postID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "post deleted"})
}

// POST /posts/:post_id/pin  {"pin": true|false} — pin/unpin on owner's profile
func (h *PostHandler) PinPost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	var req struct {
		Pin bool `json:"pin"`
	}
	_ = c.ShouldBindJSON(&req)
	if err := h.Service.SetPin(userID, postID, req.Pin); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "ok"})
}

// GET USER POSTS
func (h *PostHandler) UserPosts(c *gin.Context) {
	targetUserID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid user ID"})
		return
	}
	viewerID := c.GetInt64("user_id")

	posts, err := h.Service.GetUserPosts(targetUserID, viewerID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

// GET SINGLE POST DETAIL
func (h *PostHandler) PostDetail(c *gin.Context) {
	postID, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid post ID"})
		return
	}
	viewerID := c.GetInt64("user_id")

	post, err := h.Service.GetPostByID(postID, viewerID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, post)
}

// GET LIKED POSTS (for Loved tab)
func (h *PostHandler) LikedPosts(c *gin.Context) {
	userID := c.GetInt64("user_id")
	posts, err := h.Service.GetLikedPosts(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

// GET RESHARED POSTS FOR A SPECIFIC USER (for public profile Reshared tab)
func (h *PostHandler) UserResharedPosts(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid user ID"})
		return
	}
	viewerID := c.GetInt64("user_id")
	posts, err := h.Service.GetResharedPostsForUser(userID, viewerID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

// GET RESHARED POSTS (for profile Reshared tab - viewer's own reshared posts)
func (h *PostHandler) ResharedPosts(c *gin.Context) {
	userID := c.GetInt64("user_id")
	posts, err := h.Service.GetResharedPosts(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

// GET FEED
func (h *PostHandler) PublicFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	posts, err := h.Service.GetPublicFeed(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

func (h *PostHandler) BusinessFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	posts, err := h.Service.GetBusinessFeed(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

func (h *PostHandler) FollowingFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	posts, err := h.Service.GetFollowingFeed(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"posts": posts})
}

// GET /hashtags/trending
func (h *PostHandler) TrendingHashtags(c *gin.Context) {
	tags, err := h.Service.GetTrendingHashtags()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"hashtags": tags})
}

// GET /hashtags/:tag/posts
func (h *PostHandler) HashtagPosts(c *gin.Context) {
	userID := c.GetInt64("user_id")
	tag := c.Param("tag")
	posts, err := h.Service.GetPostsByHashtag(userID, tag)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"tag": tag, "posts": posts})
}
