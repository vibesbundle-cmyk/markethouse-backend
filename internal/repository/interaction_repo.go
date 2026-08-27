package repository

import (
	"database/sql"
	"markethouse/internal/models"
)

type InteractionRepo struct {
	DB *sql.DB
}

// ================= LIKE =================
func (r *InteractionRepo) Like(userID, postID int64) error {
	_, err := r.DB.Exec(`
		INSERT INTO likes (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, postID)
	return err
}
func (r *InteractionRepo) Unlike(userID, postID int64) error {
	_, err := r.DB.Exec(`DELETE FROM likes WHERE user_id=$1 AND post_id=$2`, userID, postID)
	return err
}

// ================= COMMENT =================
func (r *InteractionRepo) AddComment(userID, postID int64, content string, parentCommentID *int64) (int64, error) {
	var id int64
	err := r.DB.QueryRow(
		`INSERT INTO comments (user_id, post_id, content, parent_comment_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, postID, content, parentCommentID).Scan(&id)
	return id, err
}

// GetComments returns every comment for a post (top-level and replies — the
// client groups replies under their parent using parent_comment_id), each
// annotated with the commenter's identity, like count, whether the viewer
// has liked it, and how many direct replies it has.
func (r *InteractionRepo) GetComments(postID, viewerID int64) ([]models.Comment, error) {
	rows, err := r.DB.Query(`
		SELECT
			c.id, c.user_id, c.post_id, c.content, c.parent_comment_id, c.created_at,
			u.username, u.full_name, COALESCE(u.profile_photo, ''),
			(SELECT COUNT(*) FROM comment_likes cl WHERE cl.comment_id = c.id) AS like_count,
			EXISTS(SELECT 1 FROM comment_likes cl WHERE cl.comment_id = c.id AND cl.user_id = $2) AS is_liked,
			(SELECT COUNT(*) FROM comments r WHERE r.parent_comment_id = c.id) AS reply_count
		FROM comments c
		JOIN users u ON u.id = c.user_id
		WHERE c.post_id = $1
		ORDER BY c.created_at ASC`, postID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.Comment
	for rows.Next() {
		var c models.Comment
		var parentID sql.NullInt64
		if err := rows.Scan(&c.ID, &c.UserID, &c.PostID, &c.Content, &parentID, &c.CreatedAt,
			&c.Username, &c.FullName, &c.ProfilePhoto, &c.LikeCount, &c.IsLiked, &c.ReplyCount); err != nil {
			return nil, err
		}
		if parentID.Valid {
			c.ParentCommentID = &parentID.Int64
		}
		list = append(list, c)
	}
	return list, nil
}

func (r *InteractionRepo) LikeComment(userID, commentID int64) error {
	_, err := r.DB.Exec(`INSERT INTO comment_likes (user_id, comment_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, userID, commentID)
	return err
}
func (r *InteractionRepo) UnlikeComment(userID, commentID int64) error {
	_, err := r.DB.Exec(`DELETE FROM comment_likes WHERE user_id=$1 AND comment_id=$2`, userID, commentID)
	return err
}
func (r *InteractionRepo) DeleteComment(userID, commentID int64) error {
	_, err := r.DB.Exec(`DELETE FROM comments WHERE id=$1 AND user_id=$2`, commentID, userID)
	return err
}

// ================= SAVE =================
func (r *InteractionRepo) Save(userID, postID int64) error {
	_, err := r.DB.Exec(`
		INSERT INTO saves (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, postID)
	return err
}
func (r *InteractionRepo) Unsave(userID, postID int64) error {
	_, err := r.DB.Exec(`DELETE FROM saves WHERE user_id=$1 AND post_id=$2`, userID, postID)
	return err
}

// GetSavedPosts — returns posts saved by the user, with scanPostRows format
func (r *InteractionRepo) GetSavedPosts(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		p.id, p.caption, p.media_url, p.media_type, p.post_type, p.price, p.is_locked,
		p.tagged_users, p.location, p.latitude, p.longitude, p.audience, p.audience_user_ids, p.created_at,
		u.id, u.username, u.profile_photo,
		(SELECT COUNT(*) FROM likes WHERE post_id = p.id) as like_count,
		(SELECT COUNT(*) FROM comments WHERE post_id = p.id) as comment_count,
		EXISTS(SELECT 1 FROM likes WHERE post_id=p.id AND user_id=$1) as is_liked,
		EXISTS(SELECT 1 FROM saves WHERE post_id=p.id AND user_id=$1) as is_saved
	FROM saves s
	JOIN posts p ON p.id = s.post_id
	JOIN users u ON p.user_id = u.id
	WHERE s.user_id = $1`+audienceFilter("$1")+`
	ORDER BY s.created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPostRows(r.DB, rows)
}

// ================= RESHARE =================
func (r *InteractionRepo) Reshare(userID, postID int64) error {
	_, err := r.DB.Exec(`
		INSERT INTO post_reshare (user_id, post_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, postID)
	return err
}
func (r *InteractionRepo) Unreshare(userID, postID int64) error {
	_, err := r.DB.Exec(
		`DELETE FROM post_reshare WHERE user_id=$1 AND post_id=$2`, userID, postID)
	return err
}
func (r *InteractionRepo) ReshareCount(postID int64) (int, error) {
	var count int
	err := r.DB.QueryRow(
		`SELECT COUNT(*) FROM post_reshare WHERE post_id=$1`, postID).Scan(&count)
	return count, err
}
