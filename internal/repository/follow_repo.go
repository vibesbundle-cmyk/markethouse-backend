package repository

import (
	"database/sql"
)

type FollowRepo struct {
	DB *sql.DB
}

// FOLLOW USER
func (r *FollowRepo) Follow(followerID, followingID int64) error {
	_, err := r.DB.Exec(`
	INSERT INTO follows (follower_id, following_id)
	VALUES ($1, $2)
	ON CONFLICT DO NOTHING
	`, followerID, followingID)
	return err
}

// UNFOLLOW USER
func (r *FollowRepo) Unfollow(followerID, followingID int64) error {
	_, err := r.DB.Exec(`
	DELETE FROM follows
	WHERE follower_id=$1 AND following_id=$2
	`, followerID, followingID)
	return err
}

// CHECK IF FOLLOWING
func (r *FollowRepo) IsFollowing(followerID, followingID int64) bool {
	var exists bool
	r.DB.QueryRow(`
	SELECT EXISTS (
		SELECT 1 FROM follows
		WHERE follower_id=$1 AND following_id=$2
	)`, followerID, followingID).Scan(&exists)
	return exists
}

// COUNT FOLLOWERS
func (r *FollowRepo) CountFollowers(userID int64) int64 {
	var count int64
	r.DB.QueryRow(`
	SELECT COUNT(*) FROM follows
	WHERE following_id=$1
	`, userID).Scan(&count)
	return count
}

// COUNT FOLLOWING
func (r *FollowRepo) CountFollowing(userID int64) int64 {
	var count int64
	r.DB.QueryRow(`
	SELECT COUNT(*) FROM follows
	WHERE follower_id=$1
	`, userID).Scan(&count)
	return count
}

// LIST FOLLOWERS — returns slim user objects with is_following for the viewer
func (r *FollowRepo) GetFollowers(userID int64, viewerID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		u.id, u.username, u.full_name, u.profile_photo,
		CASE WHEN $2 = 0 THEN false ELSE EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND following_id = u.id) END as is_following
	FROM follows f
	JOIN users u ON u.id = f.follower_id
	WHERE f.following_id = $1
	ORDER BY f.created_at DESC
	`, userID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUserList(rows)
}

// LIST FOLLOWING — returns slim user objects with is_following for the viewer
func (r *FollowRepo) GetFollowing(userID int64, viewerID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
	SELECT
		u.id, u.username, u.full_name, u.profile_photo,
		CASE WHEN $2 = 0 THEN false ELSE EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND following_id = u.id) END as is_following
	FROM follows f
	JOIN users u ON u.id = f.following_id
	WHERE f.follower_id = $1
	ORDER BY f.created_at DESC
	`, userID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUserList(rows)
}

// shared scanner for slim user rows (now includes is_following)
func scanUserList(rows *sql.Rows) ([]map[string]interface{}, error) {
	var users []map[string]interface{}
	for rows.Next() {
		var id int64
		var username, fullName sql.NullString
		var profilePhoto sql.NullString
		var isFollowing bool
		if err := rows.Scan(&id, &username, &fullName, &profilePhoto, &isFollowing); err != nil {
			return nil, err
		}
		users = append(users, map[string]interface{}{
			"id":            id,
			"username":      username.String,
			"full_name":     fullName.String,
			"profile_photo": profilePhoto.String,
			"is_following":  isFollowing,
		})
	}
	if users == nil {
		users = []map[string]interface{}{}
	}
	return users, nil
}
