package services

import "markethouse/internal/repository"

type InteractionService struct {
	Repo *repository.InteractionRepo
}

func (s *InteractionService) Like(userID, postID int64) error {
	return s.Repo.Like(userID, postID)
}
func (s *InteractionService) Unlike(userID, postID int64) error {
	return s.Repo.Unlike(userID, postID)
}
func (s *InteractionService) Comment(userID, postID int64, content string, parentCommentID *int64) (int64, error) {
	return s.Repo.AddComment(userID, postID, content, parentCommentID)
}
func (s *InteractionService) GetComments(postID, viewerID int64) (interface{}, error) {
	return s.Repo.GetComments(postID, viewerID)
}
func (s *InteractionService) LikeComment(userID, commentID int64) error {
	return s.Repo.LikeComment(userID, commentID)
}
func (s *InteractionService) UnlikeComment(userID, commentID int64) error {
	return s.Repo.UnlikeComment(userID, commentID)
}
func (s *InteractionService) DeleteComment(userID, commentID int64) error {
	return s.Repo.DeleteComment(userID, commentID)
}
func (s *InteractionService) Save(userID, postID int64) error {
	return s.Repo.Save(userID, postID)
}
func (s *InteractionService) Unsave(userID, postID int64) error {
	return s.Repo.Unsave(userID, postID)
}
func (s *InteractionService) Reshare(userID, postID int64) error {
	return s.Repo.Reshare(userID, postID)
}
func (s *InteractionService) Unreshare(userID, postID int64) error {
	return s.Repo.Unreshare(userID, postID)
}
func (s *InteractionService) ReshareCount(postID int64) (int, error) {
	return s.Repo.ReshareCount(postID)
}
func (s *InteractionService) GetSavedPosts(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetSavedPosts(userID)
}
