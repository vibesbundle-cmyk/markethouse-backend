package services

import (
	"errors"
	"markethouse/internal/repository"
)

type FollowService struct {
	Repo *repository.FollowRepo
}

// FOLLOW
func (s *FollowService) FollowUser(followerID, followingID int64) error {
	if followerID == followingID {
		return errors.New("you cannot follow yourself")
	}
	return s.Repo.Follow(followerID, followingID)
}

// UNFOLLOW
func (s *FollowService) UnfollowUser(followerID, followingID int64) error {
	return s.Repo.Unfollow(followerID, followingID)
}

// CHECK FOLLOW
func (s *FollowService) IsFollowing(followerID, followingID int64) bool {
	return s.Repo.IsFollowing(followerID, followingID)
}

// COUNTS
func (s *FollowService) GetFollowStats(userID int64) (int64, int64) {
	followers := s.Repo.CountFollowers(userID)
	following := s.Repo.CountFollowing(userID)
	return followers, following
}

// LIST FOLLOWERS
func (s *FollowService) GetFollowers(userID int64, viewerID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetFollowers(userID, viewerID)
}

// LIST FOLLOWING
func (s *FollowService) GetFollowing(userID int64, viewerID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetFollowing(userID, viewerID)
}
