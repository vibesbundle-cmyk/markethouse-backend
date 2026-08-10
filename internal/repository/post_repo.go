package repository

import (
	"database/sql"
	"errors"
	"log"
	"markethouse/internal/models"

	"github.com/lib/pq"
)

type PostRepo struct {
	DB *sql.DB
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
	INSERT INTO posts (user_id, caption, media_url, media_type, post_type, category, price, is_locked, tagged_users)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
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
	return nil
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

// GET USER'S POSTS (for profile grid)
func (r *PostRepo) GetUserPosts(targetUserID, viewerID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$2) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$2) as is_saved
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.user_id = $1
	ORDER BY p.created_at DESC
	`, targetUserID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// GET POSTS LIKED BY USER (for Loved tab)
func (r *PostRepo) GetLikedPosts(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		true as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
	FROM likes l
	JOIN posts p ON p.id = l.post_id
	JOIN users u ON p.user_id = u.id
	WHERE l.user_id = $1
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
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$2) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$2) as is_saved
	FROM post_reshare pr
	JOIN posts p ON p.id = pr.post_id
	JOIN users u ON p.user_id = u.id
	WHERE pr.user_id = $1
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
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
	FROM post_reshare pr
	JOIN posts p ON p.id = pr.post_id
	JOIN users u ON p.user_id = u.id
	WHERE pr.user_id = $1
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
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.post_type = 'social'
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
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.post_type = 'product'
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
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
	FROM posts p
	JOIN users u ON p.user_id = u.id
	INNER JOIN follows f ON p.user_id = f.following_id
	WHERE f.follower_id = $1
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
		p.tagged_users, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$2) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$2) as is_saved,
		EXISTS(SELECT 1 FROM post_reshare WHERE post_id=p.id AND user_id=$2) as is_reshared,
		(SELECT COUNT(*) FROM post_reshare WHERE post_id = p.id) as reshare_count
	FROM posts p
	JOIN users u ON p.user_id = u.id
	WHERE p.id = $1
	`, postID, viewerID)

	var (
		postIDOut                                         int64
		caption, mediaURL, mediaType, postType, createdAt string
		username, profilePhoto                            sql.NullString
		taggedUsers                                       sql.NullString
		price                                             float64
		dbUserID                                          int64
		likeCount, commentCount, reshareCount             int
		isLiked, isSaved, isLocked, isReshared            bool
	)
	err := row.Scan(
		&postIDOut, &caption, &mediaURL, &mediaType, &postType, &price, &isLocked,
		&taggedUsers, &createdAt,
		&dbUserID, &username, &profilePhoto,
		&likeCount, &commentCount,
		&isLiked, &isSaved, &isReshared, &reshareCount,
	)
	if err != nil {
		return nil, err
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
		"created_at":   createdAt,
		"user": map[string]interface{}{
			"id":            dbUserID,
			"username":      username.String,
			"profile_photo": profilePhoto.String,
		},
		"like_count":    likeCount,
		"comment_count": commentCount,
		"reshare_count": reshareCount,
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
			taggedUsers                                       sql.NullString
			price                                             float64
			dbUserID                                          int64
			likeCount, commentCount                           int
			isLiked, isSaved, isLocked                        bool
		)
		err := rows.Scan(
			&postID, &caption, &mediaURL, &mediaType, &postType, &price, &isLocked,
			&taggedUsers, &createdAt,
			&dbUserID, &username, &profilePhoto,
			&likeCount, &commentCount,
			&isLiked, &isSaved,
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
