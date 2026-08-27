package repository

import (
	"database/sql"
	"errors"
	"log"
	"markethouse/internal/models"
	"regexp"
	"strings"

	"github.com/lib/pq"
)

type PostRepo struct {
	DB *sql.DB
}

// ── HASHTAGS ─────────────────────────────────────────────────────────────────

var hashtagRe = regexp.MustCompile(`#[A-Za-z0-9_]{1,50}`)

// extractHashtags pulls lowercase, deduped #tags from a caption.
func extractHashtags(caption string) []string {
	seen := map[string]bool{}
	var tags []string
	for _, m := range hashtagRe.FindAllString(caption, -1) {
		t := strings.ToLower(strings.TrimPrefix(m, "#"))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	return tags
}

type execer interface {
	Exec(string, ...interface{}) (sql.Result, error)
}

// replaceHashtags re-indexes a post's hashtags from its caption.
func replaceHashtags(q execer, postID int64, caption string) {
	if _, err := q.Exec(`DELETE FROM post_hashtags WHERE post_id=$1`, postID); err != nil {
		log.Printf("[HASHTAG] delete error post=%d: %v", postID, err)
		return
	}
	for _, t := range extractHashtags(caption) {
		if _, err := q.Exec(`INSERT INTO post_hashtags(post_id, tag) VALUES($1,$2)`, postID, t); err != nil {
			log.Printf("[HASHTAG] insert error post=%d tag=%s: %v", postID, t, err)
		}
	}
}

// CREATE POST — post.MediaURL/MediaType must already be set to the first
// media item (for backward compatibility); `media` is the full ordered list.
func (r *PostRepo) CreatePost(post *models.Post, media []models.PostMediaItem) error {
	tx, err := r.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	err = tx.QueryRow(`
	INSERT INTO posts (user_id, caption, media_url, media_type, post_type, category, price, is_locked, tagged_users, location, latitude, longitude, audience, audience_user_ids)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
	RETURNING id, created_at
	`,
		post.UserID,
		post.Caption,
		post.MediaURL,
		post.MediaType,
		post.PostType,
		post.Category,
		post.Price,
		post.IsLocked,
		post.TaggedUsers,
		post.Location,
		post.Latitude,
		post.Longitude,
		post.Audience,
		post.AudienceIDs,
	).Scan(&post.ID, &post.CreatedAt)
	if err != nil {
		return err
	}

	for i, m := range media {
		if _, err := tx.Exec(`
			INSERT INTO post_media (post_id, media_url, media_type, position)
			VALUES ($1,$2,$3,$4)`, post.ID, m.URL, m.Type, i); err != nil {
			return err
		}
	}

	replaceHashtags(tx, post.ID, post.Caption)

	if err := tx.Commit(); err != nil {
		return err
	}
	post.Media = media
	return nil
}

// EDIT POST — only caption and tagged_users; ownership enforced
func (r *PostRepo) EditPost(userID, postID int64, caption, taggedUsers string) error {
	res, err := r.DB.Exec(`
	UPDATE posts SET caption=$1, tagged_users=$2
	WHERE id=$3 AND user_id=$4
	`, caption, taggedUsers, postID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("post not found or not yours")
	}
	replaceHashtags(r.DB, postID, caption)
	return nil
}

// GET POSTS BY HASHTAG — same shape as the public feed, filtered to posts
// whose caption carried #tag.
func (r *PostRepo) GetPostsByHashtag(viewerID int64, tag string) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved,
		p.views
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE EXISTS(SELECT 1 FROM post_hashtags h WHERE h.post_id = p.id AND h.tag = LOWER($2))` + audienceFilter("$1") + `
	ORDER BY p.created_at DESC
	`, viewerID, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// TRENDING HASHTAGS — most-used tags over the last N days.
func (r *PostRepo) GetTrendingHashtags(days, limit int) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT tag, COUNT(*) AS uses
	FROM post_hashtags
	WHERE created_at > NOW() - ($1 || ' days')::interval
	GROUP BY tag
	ORDER BY uses DESC, tag ASC
	LIMIT $2`, days, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]interface{}
	for rows.Next() {
		var tag string
		var uses int
		if err := rows.Scan(&tag, &uses); err != nil {
			return nil, err
		}
		out = append(out, map[string]interface{}{"tag": tag, "uses": uses})
	}
	return out, rows.Err()
}

// DELETE POST — ownership enforced
func (r *PostRepo) DeletePost(userID, postID int64) error {
	res, err := r.DB.Exec(`
	DELETE FROM posts WHERE id=$1 AND user_id=$2
	`, postID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("post not found or not yours")
	}
	return nil
}

// SetPin pins or unpins a post on the owner's profile (IG/TikTok style, max 3).
// Pinning a 4th post automatically unpins the oldest pinned one (FIFO).
func (r *PostRepo) SetPin(userID, postID int64, pin bool) error {
	if !pin {
		_, err := r.DB.Exec(`UPDATE posts SET pinned_at=NULL WHERE id=$1 AND user_id=$2`, postID, userID)
		return err
	}
	var cnt int
	r.DB.QueryRow(`SELECT COUNT(*) FROM posts WHERE user_id=$1 AND pinned_at IS NOT NULL`, userID).Scan(&cnt)
	if cnt >= 3 {
		if _, err := r.DB.Exec(`
			UPDATE posts SET pinned_at=NULL
			WHERE id=(SELECT id FROM posts WHERE user_id=$1 AND pinned_at IS NOT NULL
			          ORDER BY pinned_at ASC LIMIT 1)`, userID); err != nil {
			return err
		}
	}
	res, err := r.DB.Exec(`UPDATE posts SET pinned_at=NOW() WHERE id=$1 AND user_id=$2`, postID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("post not found or not yours")
	}
	return nil
}

// GET USER'S POSTS (for profile grid)
func (r *PostRepo) GetUserPosts(targetUserID, viewerID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$2) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$2) as is_saved,
		p.pinned_at,
		p.views
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.user_id = $1` + audienceFilter("$2") + `
	ORDER BY p.pinned_at DESC NULLS LAST, p.created_at DESC
	`, targetUserID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUserPosts(r.DB, rows)
}

// scanUserPosts mirrors scanPostRows but also reads the trailing pinned_at
// column so pinned posts surface a "pinned" flag for profile grids.
func scanUserPosts(db *sql.DB, rows *sql.Rows) ([]map[string]interface{}, error) {
	var posts []map[string]interface{}
	for rows.Next() {
		var (
			postID                                            int64
			caption, mediaURL, mediaType, postType, createdAt string
			username, profilePhoto                            sql.NullString
			taggedUsers, location, audience, audienceUserIDs sql.NullString
			latitude, longitude                              sql.NullFloat64
			price                                             float64
			dbUserID                                          int64
			likeCount, commentCount, views                    int
			isLiked, isSaved, isLocked                        bool
			pinnedAt                                          sql.NullTime
		)
		err := rows.Scan(
			&postID, &caption, &mediaURL, &mediaType, &postType, &price, &isLocked,
			&taggedUsers, &location, &latitude, &longitude, &audience, &audienceUserIDs, &createdAt,
			&dbUserID, &username, &profilePhoto,
			&likeCount, &commentCount,
			&isLiked, &isSaved, &pinnedAt, &views,
		)
		if err != nil {
			return nil, err
		}
		posts = append(posts, map[string]interface{}{
			"id":           postID,
			"caption":      caption,
			"media_url":    mediaURL,
			"media_type":   mediaType,
			"post_type":    postType,
			"price":        price,
			"is_locked":    isLocked,
			"tagged_users": taggedUsers.String,
			"location":     location.String,
			"latitude":     nullableFloat(latitude),
			"longitude":    nullableFloat(longitude),
			"audience":     audience.String,
			"audience_user_ids": audienceUserIDs.String,
			"created_at":   createdAt,
			"pinned":       pinnedAt.Valid,
			"user": map[string]interface{}{
				"id":            dbUserID,
				"username":      username.String,
				"profile_photo": profilePhoto.String,
			},
			"like_count":    likeCount,
			"comment_count": commentCount,
			"is_liked":      isLiked,
			"is_saved":      isSaved,
			"views":         views,
		})
	}
	if posts == nil {
		posts = []map[string]interface{}{}
	}
	if err := attachMedia(db, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

// GET POSTS LIKED BY USER (for Loved tab)
func (r *PostRepo) GetLikedPosts(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		true as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved,
		p.views
	FROM likes l
	JOIN posts p ON p.id = l.post_id
	JOIN users u ON p.user_id = u.id
	WHERE l.user_id = $1` + audienceFilter("$1") + `
	ORDER BY l.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// GET POSTS RESHARED BY A SPECIFIC USER (for public profile Reshared tab)
func (r *PostRepo) GetResharedPostsForUser(targetUserID, viewerID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$2) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$2) as is_saved,
		p.views
	FROM post_reshare pr
	JOIN posts p ON p.id = pr.post_id
	JOIN users u ON p.user_id = u.id
	WHERE pr.user_id = $1` + audienceFilter("$2") + `
	ORDER BY pr.created_at DESC
	`, targetUserID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// GET POSTS RESHARED BY VIEWER (for profile Reshared tab)
func (r *PostRepo) GetResharedPosts(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved,
		p.views
	FROM post_reshare pr
	JOIN posts p ON p.id = pr.post_id
	JOIN users u ON p.user_id = u.id
	WHERE pr.user_id = $1` + audienceFilter("$1") + `
	ORDER BY pr.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// GET ALL POSTS (public feed) — only social posts (personal accounts)
func (r *PostRepo) GetAllPostsWithUser(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved,
		p.views
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.post_type = 'social'` + audienceFilter("$1") + `
	ORDER BY p.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// GET BUSINESS POSTS (for Shop tab) — only product posts from business accounts
func (r *PostRepo) GetBusinessPosts(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved,
		p.views
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.post_type = 'product'` + audienceFilter("$1") + `
	ORDER BY p.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

func (r *PostRepo) GetFollowingPosts(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved,
		p.views
	FROM posts p
	JOIN users u ON p.user_id = u.id
	INNER JOIN follows f ON p.user_id = f.following_id
	WHERE f.follower_id = $1` + audienceFilter("$1") + `
	ORDER BY p.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// GET SINGLE POST WITH FULL DETAILS
func (r *PostRepo) GetPostByID(postID, viewerID int64) (map[string]interface{}, error) {
	row := r.DB.QueryRow(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$2) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$2) as is_saved,
		EXISTS(SELECT 1 FROM post_reshare WHERE post_id=p.id AND user_id=$2) as is_reshared,
		(SELECT COUNT(*) FROM post_reshare WHERE post_id = p.id) as reshare_count,
		p.views
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.id = $1
	`+audienceFilter("$2")+`
	`, postID, viewerID)

	var (
		postIDOut                                         int64
		caption, mediaURL, mediaType, postType, createdAt string
		username, profilePhoto                            sql.NullString
		taggedUsers, location, audience, audienceUserIDs sql.NullString
		latitude, longitude                              sql.NullFloat64
		price                                             float64
		dbUserID                                          int64
		likeCount, commentCount, reshareCount, views      int
		isLiked, isSaved, isLocked, isReshared            bool
	)
	err := row.Scan(
		&postIDOut, &caption, &mediaURL, &mediaType, &postType, &price, &isLocked,
		&taggedUsers, &location, &latitude, &longitude, &audience, &audienceUserIDs, &createdAt,
		&dbUserID, &username, &profilePhoto,
		&likeCount, &commentCount,
		&isLiked, &isSaved, &isReshared, &reshareCount, &views,
	)
	if err != nil {
		return nil, err
	}
	// Count a view only when someone other than the author opens the post.
	if dbUserID != viewerID {
		if _, e := r.DB.Exec(`UPDATE posts SET views = views + 1 WHERE id = $1`, postID); e == nil {
			views++
		}
	}
	post := map[string]interface{}{
		"id":           postIDOut,
		"caption":      caption,
		"media_url":    mediaURL,
		"media_type":   mediaType,
		"post_type":    postType,
		"price":        price,
		"is_locked":    isLocked,
		"tagged_users": taggedUsers.String,
		"location":     location.String,
		"latitude":     nullableFloat(latitude),
		"longitude":    nullableFloat(longitude),
		"audience":     audience.String,
		"audience_user_ids": audienceUserIDs.String,
		"created_at":   createdAt,
		"user": map[string]interface{}{
			"id":            dbUserID,
			"username":      username.String,
			"profile_photo": profilePhoto.String,
		},
		"like_count":    likeCount,
		"comment_count": commentCount,
		"reshare_count": reshareCount,
		"views":         views,
		"is_liked":      isLiked,
		"is_saved":      isSaved,
		"is_reshared":   isReshared,
	}
	if err := attachMedia(r.DB, []map[string]interface{}{post}); err != nil {
		return nil, err
	}
	return post, nil
}

// shared row scanner
func scanPostRows(db *sql.DB, rows *sql.Rows) ([]map[string]interface{}, error) {
	var posts []map[string]interface{}
	for rows.Next() {
		var (
			postID                                            int64
			caption, mediaURL, mediaType, postType, createdAt string
			username, profilePhoto                            sql.NullString
			taggedUsers, location, audience, audienceUserIDs sql.NullString
			latitude, longitude                              sql.NullFloat64
			price                                             float64
			dbUserID                                          int64
			likeCount, commentCount, views                    int
			isLiked, isSaved, isLocked                        bool
		)
		err := rows.Scan(
			&postID, &caption, &mediaURL, &mediaType, &postType, &price, &isLocked,
			&taggedUsers, &location, &latitude, &longitude, &audience, &audienceUserIDs, &createdAt,
			&dbUserID, &username, &profilePhoto,
			&likeCount, &commentCount,
			&isLiked, &isSaved, &views,
		)
		if err != nil {
			return nil, err
		}
		posts = append(posts, map[string]interface{}{
			"id":           postID,
			"caption":      caption,
			"media_url":    mediaURL,
			"media_type":   mediaType,
			"post_type":    postType,
			"price":        price,
			"is_locked":    isLocked,
			"tagged_users": taggedUsers.String,
			"location":     location.String,
			"latitude":     nullableFloat(latitude),
			"longitude":    nullableFloat(longitude),
			"audience":     audience.String,
			"audience_user_ids": audienceUserIDs.String,
			"created_at":   createdAt,
			"user": map[string]interface{}{
				"id":            dbUserID,
				"username":      username.String,
				"profile_photo": profilePhoto.String,
			},
			"like_count":    likeCount,
			"comment_count": commentCount,
			"is_liked":      isLiked,
			"is_saved":      isSaved,
			"views":         views,
		})
	}
	if posts == nil {
		posts = []map[string]interface{}{}
	}
	if err := attachMedia(db, posts); err != nil {
		return nil, err
	}
	return posts, nil
}

// attachMedia batch-loads post_media rows for every post in `posts` (one
// extra query, not one per post) and adds a "media" array to each. Falls
// back to a single-item array built from media_url/media_type if a post
// has no post_media rows yet (e.g. rows created before this feature).
func attachMedia(db *sql.DB, posts []map[string]interface{}) error {
	if len(posts) == 0 {
		return nil
	}
	ids := make([]int64, len(posts))
	byID := make(map[int64]map[string]interface{}, len(posts))
	for i, p := range posts {
		id := p["id"].(int64)
		ids[i] = id
		byID[id] = p
		p["media"] = []map[string]interface{}{}
	}

	mrows, err := db.Query(`
		SELECT post_id, media_url, media_type FROM post_media
		WHERE post_id = ANY($1) ORDER BY post_id, position`, pq.Array(ids))
	if err != nil {
		// post_media might not exist yet on this environment (e.g. migration
		// hasn't run). Don't take the whole feed down over it — fall back to
		// each post's single media_url/media_type instead.
		log.Printf("[attachMedia] falling back to single media (post_media unavailable): %v", err)
		for _, id := range ids {
			p := byID[id]
			url, _ := p["media_url"].(string)
			mtype, _ := p["media_type"].(string)
			if url != "" {
				p["media"] = []map[string]interface{}{{"url": url, "type": mtype}}
			}
		}
		return nil
	}
	defer mrows.Close()
	seen := map[int64]bool{}
	for mrows.Next() {
		var pid int64
		var url, mtype string
		if err := mrows.Scan(&pid, &url, &mtype); err != nil {
			return err
		}
		p := byID[pid]
		if p == nil {
			continue
		}
		seen[pid] = true
		p["media"] = append(p["media"].([]map[string]interface{}), map[string]interface{}{
			"url": url, "type": mtype,
		})
	}

	// Posts with no post_media rows yet: fall back to their single media_url
	for _, id := range ids {
		if seen[id] {
			continue
		}
		p := byID[id]
		url, _ := p["media_url"].(string)
		mtype, _ := p["media_type"].(string)
		if url != "" {
			p["media"] = []map[string]interface{}{{"url": url, "type": mtype}}
		}
	}
	return nil
}

// nullableFloat converts an sql.NullFloat64 into a *float64 (nil when NULL).
func nullableFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

// audienceFilter returns a SQL fragment that hides posts the viewer is not
// allowed to see. `viewer` is the SQL placeholder for the viewer's user id
// (e.g. "$2"). Public posts are visible to everyone; followers-level posts
// only to the author and their followers; private posts only to the author
// and the specific people in audience_user_ids.
func audienceFilter(viewer string) string {
	return ` AND (
	p.audience = 'public'
	OR (p.audience = 'followers' AND (
		p.user_id = ` + viewer + `
		OR EXISTS(SELECT 1 FROM follows f WHERE f.following_id = p.user_id AND f.follower_id = ` + viewer + `)
	))
	OR (p.audience = 'private' AND (
		p.user_id = ` + viewer + `
		OR ',' || p.audience_user_ids || ',' LIKE '%,' || ` + viewer + ` || ',%'
	))
)`
}
