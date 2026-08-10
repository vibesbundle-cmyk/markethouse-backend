package models

type Comment struct {
	ID              int64  `json:"id"`
	UserID          int64  `json:"user_id"`
	PostID          int64  `json:"post_id"`
	Content         string `json:"content"`
	ParentCommentID *int64 `json:"parent_comment_id"`
	CreatedAt       string `json:"created_at"`
	Username        string `json:"username"`
	FullName        string `json:"full_name"`
	ProfilePhoto    string `json:"profile_photo"`
	LikeCount       int    `json:"like_count"`
	IsLiked         bool   `json:"is_liked"`
	ReplyCount      int    `json:"reply_count"`
}