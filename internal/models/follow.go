package models

type Follow struct {
	ID          int64 `json:"id"`
	FollowerID  int64 `json:"follower_id"`
	FollowingID int64 `json:"following_id"`
}