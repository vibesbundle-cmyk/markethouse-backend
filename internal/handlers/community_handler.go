package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"markethouse/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type CommunityHandler struct {
	DB  *sql.DB
	Hub *services.Hub // optional — nil is fine, just skips the realtime push
}

// ── List all communities with is_member for the current user ─────────────────
func (h *CommunityHandler) List(c *gin.Context) {
	userID := c.GetInt64("user_id") // Will be 0 if not authenticated
	rows, err := h.DB.Query(`
		SELECT c.id, c.name, c.slug, COALESCE(c.description,''), COALESCE(c.cover_photo,''),
		       COALESCE(c.icon,''), c.member_count, COALESCE(c.visibility,'public'), COALESCE(c.category,''), c.created_at,
		       COALESCE(array_to_string(c.tags,','),''), COALESCE(c.username,''), COALESCE(c.marketplace_enabled,false),
		       CASE WHEN c.visibility='public' OR $1 > 0 THEN 
		         COALESCE(EXISTS(SELECT 1 FROM community_members cm WHERE cm.community_id=c.id AND cm.user_id=$1 AND cm.status='active'), false)
		       ELSE false END AS is_member
		FROM communities c
		ORDER BY c.member_count DESC LIMIT 100`, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []gin.H
	for rows.Next() {
		var id, mc int64
		var name, slug, desc, cover, icon, vis, cat, ca, tags, username string
		var isMember, marketplace bool
		if err := rows.Scan(&id, &name, &slug, &desc, &cover, &icon, &mc, &vis, &cat, &ca, &tags, &username, &marketplace, &isMember); err != nil {
			continue
		}
		list = append(list, gin.H{
			"id": id, "name": name, "slug": slug, "description": desc,
			"cover_photo": cover, "icon": icon, "member_count": mc,
			"visibility": vis, "category": cat, "created_at": ca, "username": username,
			"marketplace_enabled": marketplace,
			"tags":                strings.Split(tags, ","), "is_member": isMember,
		})
	}
	if list == nil {
		list = []gin.H{}
	}
	c.JSON(200, gin.H{"communities": list})
}

// ── Get single community by slug ─────────────────────────────────────────────
func (h *CommunityHandler) Get(c *gin.Context) {
	userID := c.GetInt64("user_id")
	slug := c.Param("slug")
	var id, mc int64
	var name, desc, rules, cover, icon, vis, cat, tags string
	var isMember bool
	err := h.DB.QueryRow(`
		SELECT c.id, c.name, COALESCE(c.description,''), COALESCE(c.rules,''),
		       COALESCE(c.cover_photo,''), COALESCE(c.icon,''), c.member_count,
		       COALESCE(c.visibility,'public'), COALESCE(c.category,''),
		       COALESCE(array_to_string(c.tags,','),''),
		       EXISTS(SELECT 1 FROM community_members cm WHERE cm.community_id=c.id AND cm.user_id=$2 AND cm.status='active')
		FROM communities c WHERE c.slug=$1`, slug, userID).Scan(
		&id, &name, &desc, &rules, &cover, &icon, &mc, &vis, &cat, &tags, &isMember)
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, gin.H{"community": gin.H{
		"id": id, "name": name, "description": desc, "rules": rules,
		"cover_photo": cover, "icon": icon, "member_count": mc, "visibility": vis,
		"category": cat, "tags": strings.Split(tags, ","), "is_member": isMember,
	}})
}

// ── Get community by ID ───────────────────────────────────────────────────────
func (h *CommunityHandler) GetByID(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var id, mc int64
	var name, desc, rules, cover, icon, vis, cat, tags, createdAt, username string
	var isMember, marketplace bool
	var myRole sql.NullString
	err := h.DB.QueryRow(`
		SELECT c.id, c.name, COALESCE(c.description,''), COALESCE(c.rules,''),
		       COALESCE(c.cover_photo,''), COALESCE(c.icon,''), c.member_count,
		       COALESCE(c.visibility,'public'), COALESCE(c.category,''),
		       COALESCE(array_to_string(c.tags,','),''), c.created_at, COALESCE(c.username,''), COALESCE(c.marketplace_enabled,false),
		       EXISTS(SELECT 1 FROM community_members cm WHERE cm.community_id=c.id AND cm.user_id=$2 AND cm.status='active'),
		       (SELECT cm.role FROM community_members cm WHERE cm.community_id=c.id AND cm.user_id=$2 AND cm.status='active')
		FROM communities c WHERE c.id=$1`, commID, userID).Scan(
		&id, &name, &desc, &rules, &cover, &icon, &mc, &vis, &cat, &tags, &createdAt, &username, &marketplace, &isMember, &myRole)
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}

	var owner gin.H
	var ownerUsername, ownerPhoto string
	if err := h.DB.QueryRow(`
		SELECT u.username, COALESCE(u.profile_photo,'') FROM community_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.community_id=$1 AND cm.role='owner' LIMIT 1`, commID).
		Scan(&ownerUsername, &ownerPhoto); err == nil {
		owner = gin.H{"username": ownerUsername, "profile_photo": ownerPhoto}
	}

	admins := []gin.H{}
	if rows, err := h.DB.Query(`
		SELECT u.username, COALESCE(u.profile_photo,'') FROM community_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.community_id=$1 AND cm.role='admin' AND cm.status='active'`, commID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var uname, photo string
			if rows.Scan(&uname, &photo) == nil {
				admins = append(admins, gin.H{"username": uname, "profile_photo": photo})
			}
		}
	}

	mods := []gin.H{}
	if rows, err := h.DB.Query(`
		SELECT u.username, COALESCE(u.profile_photo,'') FROM community_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.community_id=$1 AND cm.role='moderator' AND cm.status='active'`, commID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var uname, photo string
			if rows.Scan(&uname, &photo) == nil {
				mods = append(mods, gin.H{"username": uname, "profile_photo": photo})
			}
		}
	}

	c.JSON(200, gin.H{"community": gin.H{
		"id": id, "name": name, "description": desc, "rules": rules,
		"cover_photo": cover, "icon": icon, "member_count": mc, "visibility": vis,
		"category": cat, "tags": strings.Split(tags, ","), "is_member": isMember,
		"created_at": createdAt, "owner": owner, "admins": admins, "moderators": mods,
		"username": username, "marketplace_enabled": marketplace, "my_role": myRole.String,
	}})
}

// ── Create community ─────────────────────────────────────────────────────────
func (h *CommunityHandler) Create(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Name           string   `json:"name"`
		Slug           string   `json:"slug"`
		Username       string   `json:"username"`
		Description    string   `json:"description"`
		Rules          string   `json:"rules"`
		Visibility     string   `json:"visibility"`
		Category       string   `json:"category"`
		Tags           []string `json:"tags"`
		Icon           string   `json:"icon"`
		CoverPhoto     string   `json:"cover_photo"`
		Marketplace    bool     `json:"marketplace_enabled"`
		InvitedUserIDs []int64  `json:"invited_user_ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Visibility == "" {
		req.Visibility = "public"
	}
	var tagsArray string
	if len(req.Tags) > 0 {
		tagsArray = "{" + strings.Join(req.Tags, ",") + "}"
	} else {
		tagsArray = "{}"
	}
	var username interface{}
	if strings.TrimSpace(req.Username) != "" {
		username = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(req.Username), "@"))
	}
	var id int64
	err := h.DB.QueryRow(
		`INSERT INTO communities(name,slug,description,rules,visibility,category,tags,icon,cover_photo,created_by,username,marketplace_enabled)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		req.Name, req.Slug, req.Description, req.Rules, req.Visibility, req.Category, tagsArray,
		req.Icon, req.CoverPhoto, userID, username, req.Marketplace).Scan(&id)
	if err != nil {
		if strings.Contains(err.Error(), "idx_communities_username") {
			c.JSON(400, gin.H{"error": "that community username is already taken"})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	h.DB.Exec(`INSERT INTO community_members(community_id,user_id,role) VALUES($1,$2,'owner')`, id, userID)

	// Invited members: dedupe, skip self/owner, cap at 5, and only accept
	// real users (FK would reject garbage anyway — this also keeps the
	// member_count accurate in one shot).
	added := int64(0)
	seen := map[int64]bool{userID: true}
	for _, uid := range req.InvitedUserIDs {
		if added >= 5 || uid <= 0 || seen[uid] {
			continue
		}
		seen[uid] = true
		if _, err := h.DB.Exec(
			`INSERT INTO community_members(community_id,user_id,role) VALUES($1,$2,'member')
			 ON CONFLICT (community_id,user_id) DO NOTHING`, id, uid); err != nil {
			continue
		}
		added++
	}
	h.DB.Exec(`UPDATE communities SET member_count=member_count+$1 WHERE id=$2`, 1+added, id)
	c.JSON(200, gin.H{"id": id, "invited_count": added})
}

// ── Join ─────────────────────────────────────────────────────────────────────
func (h *CommunityHandler) Join(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec(`INSERT INTO community_members(community_id,user_id) VALUES($1,$2)
		ON CONFLICT(community_id,user_id) DO UPDATE SET status='active'`, commID, userID)
	h.DB.Exec(`UPDATE communities SET member_count=member_count+1 WHERE id=$1`, commID)
	c.JSON(200, gin.H{"ok": true})
}

// ── Leave ────────────────────────────────────────────────────────────────────
func (h *CommunityHandler) Leave(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec(`UPDATE community_members SET status='inactive' WHERE community_id=$1 AND user_id=$2`, commID, userID)
	h.DB.Exec(`UPDATE communities SET member_count=GREATEST(0,member_count-1) WHERE id=$1`, commID)
	c.JSON(200, gin.H{"ok": true})
}

// Delete permanently removes a community and everything under it. Only the
// owner can do this — checked via getMemberRole rather than trusting the
// client.
func (h *CommunityHandler) Delete(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	if h.getMemberRole(commID, userID) != "owner" {
		c.JSON(403, gin.H{"error": "only the owner can delete this community"})
		return
	}

	tx, err := h.DB.Begin()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer tx.Rollback()

	// community_members, community_posts (and everything hanging off posts:
	// comments, votes, poll options) all have ON DELETE CASCADE back to
	// communities(id), so this one statement cleans up everything.
	if _, err := tx.Exec(`DELETE FROM communities WHERE id=$1`, commID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	if err := tx.Commit(); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Get posts ────────────────────────────────────────────────────────────────
func (h *CommunityHandler) GetPosts(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	sort := c.DefaultQuery("sort", "hot")
	orderBy := "cp.created_at DESC"
	switch sort {
	case "top":
		orderBy = "(cp.upvotes - cp.downvotes) DESC"
	case "new":
		orderBy = "cp.created_at DESC"
	case "hot":
		orderBy = "((cp.upvotes - cp.downvotes) * 0.7 + cp.comment_count * 0.3) DESC"
	}
	rows, err := h.DB.Query(`
		SELECT cp.id, cp.user_id, cp.post_type, cp.title, COALESCE(cp.body,''), COALESCE(cp.media_url,''),
		       COALESCE(cp.link_url,''), cp.upvotes, cp.downvotes, cp.comment_count,
		       cp.is_pinned, cp.is_locked, cp.created_at,
		       u.username, COALESCE(u.profile_photo,''),
		       COALESCE((SELECT vote FROM community_votes WHERE post_id=cp.id AND user_id=$2),0),
		       COALESCE(cp.best_answer_id, 0), cp.poll_ends_at, COALESCE(cp.poll_multiple,false), COALESCE(cp.poll_anonymous,false)
		FROM community_posts cp
		JOIN users u ON u.id=cp.user_id
		WHERE cp.community_id=$1
		ORDER BY cp.is_pinned DESC, `+orderBy+` LIMIT 100`, commID, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var posts []gin.H
	var postIDs []int64
	byID := map[int64]gin.H{}
	for rows.Next() {
		var id, authorID, up, dw, cc, bestAnswerID int64
		var pt, title, body, media, link, ca, uname, uphoto string
		var pinned, locked, pollMultiple, pollAnon bool
		var myVote int
		var pollEndsAt sql.NullTime
		if err := rows.Scan(&id, &authorID, &pt, &title, &body, &media, &link, &up, &dw, &cc, &pinned, &locked, &ca,
			&uname, &uphoto, &myVote, &bestAnswerID, &pollEndsAt, &pollMultiple, &pollAnon); err != nil {
			continue
		}
		var endsAt interface{}
		if pollEndsAt.Valid {
			endsAt = pollEndsAt.Time
		}
		p := gin.H{
			"id": id, "user_id": authorID, "post_type": pt, "title": title, "body": body,
			"media_url": media, "link_url": link,
			"upvotes": up, "downvotes": dw, "comment_count": cc,
			"is_pinned": pinned, "is_locked": locked, "created_at": ca,
			"username": uname, "profile_photo": uphoto, "my_vote": myVote,
			"best_answer_id": bestAnswerID, "poll_ends_at": endsAt,
			"poll_multiple": pollMultiple, "poll_anonymous": pollAnon,
		}
		posts = append(posts, p)
		byID[id] = p
		if pt == "poll" {
			postIDs = append(postIDs, id)
		}
	}
	if posts == nil {
		posts = []gin.H{}
	}
	if len(postIDs) > 0 {
		h.attachPollOptions(postIDs, userID, byID)
	}
	c.JSON(200, gin.H{"posts": posts})
}

// attachPollOptions batch-loads poll options + vote counts + whether the
// current user voted for each poll post, adding a "poll_options" array.
func (h *CommunityHandler) attachPollOptions(postIDs []int64, userID int64, byID map[int64]gin.H) {
	for _, id := range postIDs {
		byID[id]["poll_options"] = []gin.H{}
	}
	rows, err := h.DB.Query(`
		SELECT po.id, po.post_id, po.option_text, po.vote_count,
		       EXISTS(SELECT 1 FROM community_poll_votes pv WHERE pv.option_id=po.id AND pv.user_id=$2)
		FROM community_poll_options po
		WHERE po.post_id = ANY($1) ORDER BY po.post_id, po.position`, pq.Array(postIDs), userID)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var optID, postID, voteCount int64
		var text string
		var votedByMe bool
		if rows.Scan(&optID, &postID, &text, &voteCount, &votedByMe) != nil {
			continue
		}
		p := byID[postID]
		if p == nil {
			continue
		}
		p["poll_options"] = append(p["poll_options"].([]gin.H), gin.H{
			"id": optID, "text": text, "vote_count": voteCount, "voted_by_me": votedByMe,
		})
	}
}

// ── Create post ──────────────────────────────────────────────────────────────
func (h *CommunityHandler) CreatePost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		PostType        string   `json:"post_type"`
		Title           string   `json:"title"`
		Body            string   `json:"body"`
		MediaURL        string   `json:"media_url"`
		LinkURL         string   `json:"link_url"`
		PollOptions     []string `json:"poll_options"`
		PollDurationHrs int      `json:"poll_duration_hours"`
		PollMultiple    bool     `json:"poll_multiple"`
		PollAnonymous   bool     `json:"poll_anonymous"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.PostType == "" {
		req.PostType = "discussion"
	}
	if req.PostType == "poll" && len(req.PollOptions) < 2 {
		c.JSON(400, gin.H{"error": "a poll needs at least 2 options"})
		return
	}

	var pollEndsAt interface{}
	if req.PostType == "poll" {
		hours := req.PollDurationHrs
		if hours <= 0 {
			hours = 24
		}
		pollEndsAt = time.Now().Add(time.Duration(hours) * time.Hour)
	}

	var id int64
	err := h.DB.QueryRow(
		`INSERT INTO community_posts(community_id,user_id,post_type,title,body,media_url,link_url,poll_ends_at,poll_multiple,poll_anonymous)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING id`,
		commID, userID, req.PostType, req.Title, req.Body, req.MediaURL, req.LinkURL,
		pollEndsAt, req.PollMultiple, req.PollAnonymous).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	if req.PostType == "poll" {
		for i, opt := range req.PollOptions {
			if strings.TrimSpace(opt) == "" {
				continue
			}
			h.DB.Exec(`INSERT INTO community_poll_options(post_id,option_text,position) VALUES($1,$2,$3)`, id, opt, i)
		}
	}

	h.DB.Exec(`UPDATE communities SET post_count=post_count+1 WHERE id=$1`, commID)
	c.JSON(200, gin.H{"id": id})
}

// ── Vote on a poll ────────────────────────────────────────────────────────────
func (h *CommunityHandler) VotePoll(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	var req struct {
		OptionID int64 `json:"option_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var multiple bool
	var endsAt sql.NullTime
	if err := h.DB.QueryRow(`SELECT COALESCE(poll_multiple,false), poll_ends_at FROM community_posts WHERE id=$1`, postID).
		Scan(&multiple, &endsAt); err != nil {
		c.JSON(404, gin.H{"error": "poll not found"})
		return
	}
	if endsAt.Valid && time.Now().After(endsAt.Time) {
		c.JSON(400, gin.H{"error": "this poll has ended"})
		return
	}

	if !multiple {
		// Single-choice: clear any previous vote by this user on this poll first.
		h.DB.Exec(`DELETE FROM community_poll_votes WHERE user_id=$2 AND option_id IN
			(SELECT id FROM community_poll_options WHERE post_id=$1)`, postID, userID)
	}
	if _, err := h.DB.Exec(`INSERT INTO community_poll_votes(post_id,option_id,user_id) VALUES($1,$2,$3)
		ON CONFLICT (option_id,user_id) DO NOTHING`, postID, req.OptionID, userID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	h.DB.Exec(`UPDATE community_poll_options SET vote_count=
		(SELECT COUNT(*) FROM community_poll_votes WHERE option_id=community_poll_options.id)
		WHERE post_id=$1`, postID)
	c.JSON(200, gin.H{"ok": true})
}

// ── Vote ─────────────────────────────────────────────────────────────────────
func (h *CommunityHandler) Vote(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	var req struct {
		Vote int `json:"vote"`
	}
	c.ShouldBindJSON(&req)

	var prevVote int
	h.DB.QueryRow(`SELECT COALESCE(vote,0) FROM community_votes WHERE post_id=$1 AND user_id=$2`, postID, userID).Scan(&prevVote)

	if req.Vote == 0 {
		h.DB.Exec(`DELETE FROM community_votes WHERE post_id=$1 AND user_id=$2`, postID, userID)
	} else {
		h.DB.Exec(`INSERT INTO community_votes(post_id,user_id,vote) VALUES($1,$2,$3)
			ON CONFLICT(post_id,user_id) DO UPDATE SET vote=$3`, postID, userID, req.Vote)
	}
	h.DB.Exec(`UPDATE community_posts SET
		upvotes=(SELECT COUNT(*) FROM community_votes WHERE post_id=$1 AND vote=1),
		downvotes=(SELECT COUNT(*) FROM community_votes WHERE post_id=$1 AND vote=-1)
		WHERE id=$1`, postID)

	// Small reputation reward/penalty for the post's author based on the vote change.
	repDelta := req.Vote - prevVote
	if repDelta != 0 {
		var authorID int64
		if h.DB.QueryRow(`SELECT user_id FROM community_posts WHERE id=$1`, postID).Scan(&authorID) == nil && authorID != userID {
			h.DB.Exec(`UPDATE users SET reputation=GREATEST(0, reputation+$1) WHERE id=$2`, repDelta*2, authorID)
		}
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Get comments ─────────────────────────────────────────────────────────────
func (h *CommunityHandler) GetComments(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	rows, err := h.DB.Query(`
		SELECT cc.id, cc.user_id, COALESCE(cc.parent_id, 0), cc.body, cc.upvotes, cc.created_at,
		       COALESCE(cc.is_best_answer,false), u.username, COALESCE(u.profile_photo,''),
		       EXISTS(SELECT 1 FROM community_comment_likes ccl WHERE ccl.comment_id=cc.id AND ccl.user_id=$2)
		FROM community_comments cc
		JOIN users u ON u.id=cc.user_id
		WHERE cc.post_id=$1 AND COALESCE(cc.is_deleted,false)=false
		ORDER BY cc.is_best_answer DESC, cc.created_at ASC`, postID, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var comments []gin.H
	for rows.Next() {
		var id, authorID, pid, up int64
		var body, ca, uname, photo string
		var isBest, likedByMe bool
		if err := rows.Scan(&id, &authorID, &pid, &body, &up, &ca, &isBest, &uname, &photo, &likedByMe); err != nil {
			continue
		}
		comments = append(comments, gin.H{
			"id": id, "user_id": authorID, "parent_id": pid, "body": body, "upvotes": up,
			"created_at": ca, "is_best_answer": isBest, "username": uname, "profile_photo": photo,
			"liked_by_me": likedByMe,
		})
	}
	if comments == nil {
		comments = []gin.H{}
	}
	c.JSON(200, gin.H{"comments": comments})
}

// ── Like / unlike a comment ──────────────────────────────────────────────────
func (h *CommunityHandler) LikeComment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commentID, _ := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	h.DB.Exec(`INSERT INTO community_comment_likes(comment_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, commentID, userID)
	h.DB.Exec(`UPDATE community_comments SET upvotes=(SELECT COUNT(*) FROM community_comment_likes WHERE comment_id=$1) WHERE id=$1`, commentID)
	c.JSON(200, gin.H{"ok": true})
}

func (h *CommunityHandler) UnlikeComment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	commentID, _ := strconv.ParseInt(c.Param("comment_id"), 10, 64)
	h.DB.Exec(`DELETE FROM community_comment_likes WHERE comment_id=$1 AND user_id=$2`, commentID, userID)
	h.DB.Exec(`UPDATE community_comments SET upvotes=(SELECT COUNT(*) FROM community_comment_likes WHERE comment_id=$1) WHERE id=$1`, commentID)
	c.JSON(200, gin.H{"ok": true})
}

// ── Track a post view (deduped per user, like status views) ─────────────────
func (h *CommunityHandler) ViewPost(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	res, err := h.DB.Exec(`INSERT INTO community_post_views(post_id,viewer_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, postID, userID)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			h.DB.Exec(`UPDATE community_posts SET views=views+1 WHERE id=$1`, postID)
		}
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Community analytics (owner/admin only) ───────────────────────────────────
func (h *CommunityHandler) GetAnalytics(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !canManageRoles(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only the owner or an admin can view analytics"})
		return
	}

	var memberCount, postCount, totalViews, totalComments int64
	var totalUp, totalDown int64
	h.DB.QueryRow(`SELECT member_count, post_count FROM communities WHERE id=$1`, commID).Scan(&memberCount, &postCount)
	h.DB.QueryRow(`SELECT COALESCE(SUM(views),0), COALESCE(SUM(comment_count),0), COALESCE(SUM(upvotes),0), COALESCE(SUM(downvotes),0)
		FROM community_posts WHERE community_id=$1`, commID).Scan(&totalViews, &totalComments, &totalUp, &totalDown)

	var newMembers7d, newPosts7d int64
	h.DB.QueryRow(`SELECT COUNT(*) FROM community_members WHERE community_id=$1 AND joined_at >= NOW() - INTERVAL '7 days'`, commID).Scan(&newMembers7d)
	h.DB.QueryRow(`SELECT COUNT(*) FROM community_posts WHERE community_id=$1 AND created_at >= NOW() - INTERVAL '7 days'`, commID).Scan(&newPosts7d)

	topPosts := []gin.H{}
	if rows, err := h.DB.Query(`
		SELECT id, title, views, upvotes, comment_count FROM community_posts
		WHERE community_id=$1 ORDER BY views DESC LIMIT 5`, commID); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, views, up, cc int64
			var title string
			if rows.Scan(&id, &title, &views, &up, &cc) == nil {
				topPosts = append(topPosts, gin.H{"id": id, "title": title, "views": views, "upvotes": up, "comment_count": cc})
			}
		}
	}

	c.JSON(200, gin.H{
		"member_count": memberCount, "post_count": postCount,
		"total_views": totalViews, "total_comments": totalComments,
		"total_upvotes": totalUp, "total_downvotes": totalDown,
		"new_members_7d": newMembers7d, "new_posts_7d": newPosts7d,
		"top_posts": topPosts,
	})
}
func (h *CommunityHandler) MarkBestAnswer(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	commentID, _ := strconv.ParseInt(c.Param("comment_id"), 10, 64)

	var authorID int64
	var postType string
	if err := h.DB.QueryRow(`SELECT user_id, post_type FROM community_posts WHERE id=$1`, postID).
		Scan(&authorID, &postType); err != nil {
		c.JSON(404, gin.H{"error": "post not found"})
		return
	}
	if authorID != userID {
		c.JSON(403, gin.H{"error": "only the question's author can mark a best answer"})
		return
	}
	if postType != "question" {
		c.JSON(400, gin.H{"error": "best answers only apply to questions"})
		return
	}

	var commentAuthorID int64
	h.DB.QueryRow(`SELECT user_id FROM community_comments WHERE id=$1 AND post_id=$2`, commentID, postID).Scan(&commentAuthorID)

	h.DB.Exec(`UPDATE community_comments SET is_best_answer=false WHERE post_id=$1`, postID)
	h.DB.Exec(`UPDATE community_comments SET is_best_answer=true WHERE id=$1`, commentID)
	h.DB.Exec(`UPDATE community_posts SET best_answer_id=$1 WHERE id=$2`, commentID, postID)
	if commentAuthorID > 0 {
		h.DB.Exec(`UPDATE users SET reputation=reputation+25 WHERE id=$1`, commentAuthorID)
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Add comment ──────────────────────────────────────────────────────────────
func (h *CommunityHandler) AddComment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	var req struct {
		Body     string `json:"body"`
		ParentID *int64 `json:"parent_id"`
	}
	c.ShouldBindJSON(&req)
	var id int64
	h.DB.QueryRow(
		`INSERT INTO community_comments(post_id,user_id,body,parent_id) VALUES($1,$2,$3,$4) RETURNING id`,
		postID, userID, req.Body, req.ParentID).Scan(&id)
	h.DB.Exec(`UPDATE community_posts SET comment_count=comment_count+1 WHERE id=$1`, postID)
	h.DB.Exec(`UPDATE users SET reputation=reputation+1 WHERE id=$1`, userID) // small reward for participating
	c.JSON(http.StatusOK, gin.H{"id": id})
}

// ── Pin / Lock / Delete post (moderator actions) ─────────────────────────────
func (h *CommunityHandler) PinPost(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	var commID int64
	h.DB.QueryRow(`SELECT community_id FROM community_posts WHERE id=$1`, postID).Scan(&commID)
	if !canModerate(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only moderators, admins, or the owner can pin posts"})
		return
	}
	var req struct {
		Pin bool `json:"pin"`
	}
	c.ShouldBindJSON(&req)
	h.DB.Exec(`UPDATE community_posts SET is_pinned=$1 WHERE id=$2`, req.Pin, postID)
	c.JSON(200, gin.H{"ok": true})
}

func (h *CommunityHandler) LockPost(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	var commID int64
	h.DB.QueryRow(`SELECT community_id FROM community_posts WHERE id=$1`, postID).Scan(&commID)
	if !canModerate(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only moderators, admins, or the owner can lock posts"})
		return
	}
	var req struct {
		Lock bool `json:"lock"`
	}
	c.ShouldBindJSON(&req)
	h.DB.Exec(`UPDATE community_posts SET is_locked=$1 WHERE id=$2`, req.Lock, postID)
	c.JSON(200, gin.H{"ok": true})
}

func (h *CommunityHandler) DeletePost(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	postID, _ := strconv.ParseInt(c.Param("post_id"), 10, 64)
	var commID, authorID int64
	h.DB.QueryRow(`SELECT community_id, user_id FROM community_posts WHERE id=$1`, postID).Scan(&commID, &authorID)
	if callerID != authorID && !canModerate(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only the post's author or a moderator can delete it"})
		return
	}
	h.DB.Exec(`DELETE FROM community_posts WHERE id=$1`, postID)
	c.JSON(200, gin.H{"ok": true})
}

// getMemberRole returns the caller's role in a community ("" if not a member).
func (h *CommunityHandler) getMemberRole(commID, userID int64) string {
	var role string
	h.DB.QueryRow(`SELECT role FROM community_members WHERE community_id=$1 AND user_id=$2 AND status='active'`,
		commID, userID).Scan(&role)
	return role
}

func canModerate(role string) bool    { return role == "owner" || role == "admin" || role == "moderator" }
func canManageRoles(role string) bool { return role == "owner" || role == "admin" }

// ── List members with their roles + reputation ───────────────────────────────
func (h *CommunityHandler) GetMembers(c *gin.Context) {
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	rows, err := h.DB.Query(`
		SELECT u.id, u.username, COALESCE(u.profile_photo,''), cm.role, cm.status,
		       COALESCE(u.reputation,0), cm.joined_at
		FROM community_members cm
		JOIN users u ON u.id = cm.user_id
		WHERE cm.community_id=$1 AND cm.status IN ('active','muted')
		ORDER BY CASE cm.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'moderator' THEN 2 ELSE 3 END, cm.joined_at ASC`, commID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var members []gin.H
	for rows.Next() {
		var uid, rep int64
		var uname, photo, role, status, joinedAt string
		if rows.Scan(&uid, &uname, &photo, &role, &status, &rep, &joinedAt) != nil {
			continue
		}
		members = append(members, gin.H{
			"user_id": uid, "username": uname, "profile_photo": photo,
			"role": role, "status": status, "reputation": rep, "joined_at": joinedAt,
			"badges": computeBadgesForHandler(rep),
		})
	}
	if members == nil {
		members = []gin.H{}
	}
	c.JSON(200, gin.H{"members": members})
}

// computeBadgesForHandler mirrors services.computeBadges without importing
// the services package (would create an import cycle).
func computeBadgesForHandler(reputation int64) []string {
	badges := []string{}
	if reputation >= 50 {
		badges = append(badges, "Active Member")
	}
	if reputation >= 500 {
		badges = append(badges, "Community Expert")
	}
	if reputation >= 1000 {
		badges = append(badges, "Top Contributor")
	}
	return badges
}

// ── Assign a role (owner/admin only; only owner can create/remove admins) ───
func (h *CommunityHandler) AssignRole(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		UserID int64  `json:"user_id"`
		Role   string `json:"role"` // admin | moderator | member
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Role != "admin" && req.Role != "moderator" && req.Role != "member" {
		c.JSON(400, gin.H{"error": "role must be admin, moderator, or member"})
		return
	}

	callerRole := h.getMemberRole(commID, callerID)
	if !canManageRoles(callerRole) {
		c.JSON(403, gin.H{"error": "only the owner or an admin can change member roles"})
		return
	}
	// Only the owner can hand out (or take away) admin — an admin can't make more admins.
	if req.Role == "admin" && callerRole != "owner" {
		c.JSON(403, gin.H{"error": "only the community owner can assign admins"})
		return
	}
	targetRole := h.getMemberRole(commID, req.UserID)
	if targetRole == "owner" {
		c.JSON(400, gin.H{"error": "the owner's role can't be changed"})
		return
	}

	if _, err := h.DB.Exec(`UPDATE community_members SET role=$1 WHERE community_id=$2 AND user_id=$3`,
		req.Role, commID, req.UserID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Ban / Mute member ────────────────────────────────────────────────────────
func (h *CommunityHandler) BanMember(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !canModerate(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only moderators, admins, or the owner can ban members"})
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	c.ShouldBindJSON(&req)
	if h.getMemberRole(commID, req.UserID) == "owner" {
		c.JSON(400, gin.H{"error": "the owner can't be banned"})
		return
	}
	h.DB.Exec(`UPDATE community_members SET status='banned' WHERE community_id=$1 AND user_id=$2`, commID, req.UserID)
	c.JSON(200, gin.H{"ok": true})
}

func (h *CommunityHandler) MuteMember(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !canModerate(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only moderators, admins, or the owner can mute members"})
		return
	}
	var req struct {
		UserID int64 `json:"user_id"`
	}
	c.ShouldBindJSON(&req)
	if h.getMemberRole(commID, req.UserID) == "owner" {
		c.JSON(400, gin.H{"error": "the owner can't be muted"})
		return
	}
	h.DB.Exec(`UPDATE community_members SET status='muted' WHERE community_id=$1 AND user_id=$2`, commID, req.UserID)
	c.JSON(200, gin.H{"ok": true})
}

// ── Update community settings ────────────────────────────────────────────────
func (h *CommunityHandler) UpdateSettings(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if !canManageRoles(h.getMemberRole(commID, callerID)) {
		c.JSON(403, gin.H{"error": "only the owner or an admin can change community settings"})
		return
	}
	var req struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Rules       string   `json:"rules"`
		Category    string   `json:"category"`
		Visibility  string   `json:"visibility"`
		Tags        []string `json:"tags"`
		Icon        string   `json:"icon"`        // uploaded separately via /upload/media?type=community
		CoverPhoto  string   `json:"cover_photo"` // same
	}
	c.ShouldBindJSON(&req)
	var tagsArray string
	if len(req.Tags) > 0 {
		tagsArray = "{" + strings.Join(req.Tags, ",") + "}"
	} else {
		tagsArray = "{}"
	}
	if req.Icon != "" || req.CoverPhoto != "" {
		h.DB.Exec(`UPDATE communities SET name=$1,description=$2,rules=$3,category=$4,visibility=$5,tags=$6,
			icon=COALESCE(NULLIF($7,''),icon), cover_photo=COALESCE(NULLIF($8,''),cover_photo) WHERE id=$9`,
			req.Name, req.Description, req.Rules, req.Category, req.Visibility,
			tagsArray, req.Icon, req.CoverPhoto, commID)
	} else {
		h.DB.Exec(`UPDATE communities SET name=$1,description=$2,rules=$3,category=$4,visibility=$5,tags=$6 WHERE id=$7`,
			req.Name, req.Description, req.Rules, req.Category, req.Visibility,
			tagsArray, commID)
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Community "General Chat" — one shared group-chat room per community ─────
// Any active (non-muted) member can read and post. Real-time delivery goes
// out over the same websocket hub used for post likes/comments — clients
// filter on {"type":"community_message","community_id":...}.

const communityMessagesPageSize = 50

// GetMessages returns the most recent messages, oldest first, optionally
// paginating further back with ?before=<message_id>.
func (h *CommunityHandler) GetMessages(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if h.getMemberRole(commID, callerID) == "" {
		c.JSON(403, gin.H{"error": "join the community to view its chat"})
		return
	}
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)

	query := `
		SELECT cmsg.id, cmsg.user_id, u.username, COALESCE(u.profile_photo,''),
		       COALESCE(cmsg.body,''), COALESCE(cmsg.media_url,''), COALESCE(cmsg.media_type,''),
		       cmsg.created_at
		FROM community_messages cmsg
		JOIN users u ON u.id = cmsg.user_id
		WHERE cmsg.community_id=$1`
	args := []interface{}{commID}
	if before > 0 {
		query += ` AND cmsg.id < $2`
		args = append(args, before)
	}
	query += ` ORDER BY cmsg.id DESC LIMIT ` + strconv.Itoa(communityMessagesPageSize)

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var messages []gin.H
	for rows.Next() {
		var id, uid int64
		var uname, photo, body, mediaURL, mediaType, ca string
		if rows.Scan(&id, &uid, &uname, &photo, &body, &mediaURL, &mediaType, &ca) != nil {
			continue
		}
		messages = append(messages, gin.H{
			"id": id, "user_id": uid, "username": uname, "profile_photo": photo,
			"body": body, "media_url": mediaURL, "media_type": mediaType,
			"created_at": ca, "is_mine": uid == callerID,
		})
	}
	// Reverse to chronological order (oldest first) since we queried DESC for LIMIT.
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	if messages == nil {
		messages = []gin.H{}
	}
	c.JSON(200, gin.H{"messages": messages})
}

// SendMessage posts a text and/or media message to the community's chat.
// Muted members are blocked; everyone else who's an active member can send.
func (h *CommunityHandler) SendMessage(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	status := ""
	h.DB.QueryRow(`SELECT status FROM community_members WHERE community_id=$1 AND user_id=$2`, commID, callerID).Scan(&status)
	if status == "" {
		c.JSON(403, gin.H{"error": "join the community to chat"})
		return
	}
	if status == "muted" {
		c.JSON(403, gin.H{"error": "you've been muted in this community"})
		return
	}
	var req struct {
		Body      string `json:"body"`
		MediaURL  string `json:"media_url"`
		MediaType string `json:"media_type"`
	}
	c.ShouldBindJSON(&req)
	if strings.TrimSpace(req.Body) == "" && req.MediaURL == "" {
		c.JSON(400, gin.H{"error": "message is empty"})
		return
	}
	var id int64
	var createdAt string
	err := h.DB.QueryRow(`INSERT INTO community_messages(community_id,user_id,body,media_url,media_type)
		VALUES($1,$2,$3,$4,$5) RETURNING id, created_at`,
		commID, callerID, req.Body, req.MediaURL, req.MediaType).Scan(&id, &createdAt)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var username, photo string
	h.DB.QueryRow(`SELECT username, COALESCE(profile_photo,'') FROM users WHERE id=$1`, callerID).Scan(&username, &photo)

	payload := gin.H{
		"type": "community_message", "community_id": commID,
		"id": id, "user_id": callerID, "username": username, "profile_photo": photo,
		"body": req.Body, "media_url": req.MediaURL, "media_type": req.MediaType,
		"created_at": createdAt,
	}
	if h.Hub != nil {
		h.Hub.Broadcast(payload)
	}
	c.JSON(200, gin.H{"id": id, "created_at": createdAt})
}

// CanCall reports whether the caller meets the reputation bar this
// community requires to start a voice/video call in General Chat — kept
// as its own endpoint so the client can grey out the call button without
// having to fetch full member list.
const highReputationCallThreshold = 500 // matches the "Community Expert" badge cutoff

func (h *CommunityHandler) CanCall(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	role := h.getMemberRole(commID, callerID)
	if role == "" {
		c.JSON(200, gin.H{"can_call": false})
		return
	}
	var rep int64
	h.DB.QueryRow(`SELECT COALESCE(reputation,0) FROM users WHERE id=$1`, callerID).Scan(&rep)
	canCall := rep >= highReputationCallThreshold || role == "owner" || role == "admin"
	c.JSON(200, gin.H{"can_call": canCall, "reputation": rep, "threshold": highReputationCallThreshold})
}

// ── Community marketplace ─────────────────────────────────────────────────────

// GetListings lists active buy/sell listings scoped to one community.
// Only members of the community (or any user for public communities) can view.
func (h *CommunityHandler) GetListings(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var visibility string
	var marketplace bool
	err := h.DB.QueryRow(`SELECT COALESCE(visibility,'public'), COALESCE(marketplace_enabled,false) FROM communities WHERE id=$1`, commID).
		Scan(&visibility, &marketplace)
	if err != nil {
		c.JSON(404, gin.H{"error": "community not found"})
		return
	}
	if !marketplace {
		c.JSON(200, gin.H{"listings": []gin.H{}})
		return
	}
	if visibility != "public" && h.getMemberRole(commID, callerID) == "" {
		c.JSON(403, gin.H{"error": "join the community to view its marketplace"})
		return
	}

	rows, err := h.DB.Query(`
		SELECT cl.id, cl.user_id, u.username, COALESCE(u.profile_photo,''),
		       cl.title, COALESCE(cl.description,''), cl.price, COALESCE(cl.category,''),
		       COALESCE(array_to_string(cl.images,','),''), cl.status, cl.created_at
		FROM community_listings cl
		JOIN users u ON u.id = cl.user_id
		WHERE cl.community_id=$1 AND cl.status='active'
		ORDER BY cl.created_at DESC LIMIT 100`, commID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	listings := []gin.H{}
	for rows.Next() {
		var id, uid int64
		var uname, photo, title, desc, category, images, status, ca string
		var price float64
		if rows.Scan(&id, &uid, &uname, &photo, &title, &desc, &price, &category, &images, &status, &ca) != nil {
			continue
		}
		imgList := []string{}
		if images != "" {
			imgList = strings.Split(images, ",")
		}
		listings = append(listings, gin.H{
			"id": id, "user_id": uid, "username": uname, "profile_photo": photo,
			"title": title, "description": desc, "price": price, "category": category,
			"images": imgList, "status": status, "created_at": ca, "is_mine": uid == callerID,
		})
	}
	if listings == nil {
		listings = []gin.H{}
	}
	c.JSON(200, gin.H{"listings": listings})
}

// CreateListing adds a listing to a community's marketplace. The seller must
// be an active member and the community must have marketplace_enabled.
func (h *CommunityHandler) CreateListing(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	commID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var marketplace bool
	h.DB.QueryRow(`SELECT COALESCE(marketplace_enabled,false) FROM communities WHERE id=$1`, commID).Scan(&marketplace)
	if !marketplace {
		c.JSON(400, gin.H{"error": "marketplace is not enabled for this community"})
		return
	}
	if h.getMemberRole(commID, callerID) == "" {
		c.JSON(403, gin.H{"error": "join the community to list items for sale"})
		return
	}

	var req struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Price       float64  `json:"price"`
		Category    string   `json:"category"`
		Images      []string `json:"images"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
	}
	if req.Price < 0 {
		c.JSON(400, gin.H{"error": "price cannot be negative"})
		return
	}
	var imagesArray string
	if len(req.Images) > 0 {
		imagesArray = "{" + strings.Join(req.Images, ",") + "}"
	} else {
		imagesArray = "{}"
	}

	var id int64
	var createdAt string
	err := h.DB.QueryRow(`
		INSERT INTO community_listings(community_id,user_id,title,description,price,category,images)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id, created_at`,
		commID, callerID, req.Title, req.Description, req.Price, req.Category, imagesArray).
		Scan(&id, &createdAt)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	var username, photo string
	h.DB.QueryRow(`SELECT username, COALESCE(profile_photo,'') FROM users WHERE id=$1`, callerID).Scan(&username, &photo)
	c.JSON(200, gin.H{
		"id": id, "user_id": callerID, "username": username, "profile_photo": photo,
		"title": req.Title, "description": req.Description, "price": req.Price,
		"category": req.Category, "images": req.Images, "status": "active", "created_at": createdAt,
		"is_mine": true,
	})
}

// DeleteListing removes a listing. Allowed for the seller, or any owner/admin/
// moderator of the community.
func (h *CommunityHandler) DeleteListing(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var commID, sellerID int64
	err := h.DB.QueryRow(`SELECT community_id, user_id FROM community_listings WHERE id=$1`, listingID).Scan(&commID, &sellerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "listing not found"})
		return
	}
	role := h.getMemberRole(commID, callerID)
	if sellerID != callerID && !canModerate(role) {
		c.JSON(403, gin.H{"error": "not your listing"})
		return
	}
	if _, err := h.DB.Exec(`DELETE FROM community_listings WHERE id=$1`, listingID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// MarkListingSold lets the seller (or a moderator) flip a listing to sold.
func (h *CommunityHandler) MarkListingSold(c *gin.Context) {
	callerID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var commID, sellerID int64
	err := h.DB.QueryRow(`SELECT community_id, user_id FROM community_listings WHERE id=$1`, listingID).Scan(&commID, &sellerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "listing not found"})
		return
	}
	role := h.getMemberRole(commID, callerID)
	if sellerID != callerID && !canModerate(role) {
		c.JSON(403, gin.H{"error": "not your listing"})
		return
	}
	if _, err := h.DB.Exec(`UPDATE community_listings SET status='sold' WHERE id=$1`, listingID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
