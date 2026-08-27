package services

import (
	"errors"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"markethouse/internal/models"
	"markethouse/internal/repository"
	"markethouse/internal/storage"
)

const maxPostFileSize = 50 * 1024 * 1024 // 50MB

// randSuffix keeps filenames unique when several files from the same
// multi-select post share a similar original name (e.g. IMG_0001.jpg twice).
func randSuffix() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

type PostService struct {
	Repo     *repository.PostRepo
	AuthRepo *repository.AuthRepo
	Storage  storage.Storage
}

func (s *PostService) CreatePost(userID int64, caption, postType, category string, price float64, isLocked bool, taggedUsers, location, audience, audienceUserIDs string, lat, lng *float64, files []*multipart.FileHeader) (models.Post, error) {
	if len(caption) > 2000 {
		return models.Post{}, errors.New("caption too long (max 2000 chars)")
	}
	if len(files) == 0 {
		return models.Post{}, errors.New("at least one file is required")
	}

	user, err := s.AuthRepo.GetFullUserByID(userID)
	if err != nil {
		return models.Post{}, errors.New("user not found")
	}
	if postType == "product" && user.AccountType != "business" {
		return models.Post{}, errors.New("only business accounts can post products")
	}
	if isLocked && user.AccountType != "creator" {
		return models.Post{}, errors.New("only creators can lock content")
	}
	if price < 0 {
		return models.Post{}, errors.New("price cannot be negative")
	}
	switch audience {
	case "", "public", "followers", "private":
	default:
		return models.Post{}, errors.New("audience must be public, followers or private")
	}

	media := make([]models.PostMediaItem, 0, len(files))
	for _, file := range files {
		url, mediaType, err := s.uploadPostFile(file)
		if err != nil {
			return models.Post{}, err
		}
		media = append(media, models.PostMediaItem{URL: url, Type: mediaType})
	}

	if category == "" {
		category = "Other"
	}
	if audience == "" {
		audience = "public"
	}

	post := models.Post{
		UserID:      userID,
		Caption:     caption,
		MediaURL:    media[0].URL,
		MediaType:   media[0].Type,
		PostType:    postType,
		Category:    category,
		Price:       price,
		IsLocked:    isLocked,
		TaggedUsers: taggedUsers,
		Location:    location,
		Latitude:    lat,
		Longitude:   lng,
		Audience:    audience,
		AudienceIDs: audienceUserIDs,
	}

	if err = s.Repo.CreatePost(&post, media); err != nil {
		return models.Post{}, err
	}
	return post, nil
}

// uploadPostFile validates and stores a single photo/video for a post,
// returning its public URL and detected media type ("image" or "video").
func (s *PostService) uploadPostFile(file *multipart.FileHeader) (url, mediaType string, err error) {
	if file.Size > maxPostFileSize {
		return "", "", errors.New("file too large (max 50MB)")
	}

	ct := file.Header.Get("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		src, err := file.Open()
		if err == nil {
			buf := make([]byte, 512)
			n, _ := src.Read(buf)
			src.Close()
			ct = http.DetectContentType(buf[:n])
		}
	}

	if !strings.HasPrefix(ct, "image/") && !strings.HasPrefix(ct, "video/") {
		return "", "", errors.New("unsupported file type: " + ct)
	}

	mediaType = "image"
	if strings.HasPrefix(ct, "video") {
		mediaType = "video"
	}

	baseName := strings.TrimSuffix(file.Filename, ".jpg")
	baseName = strings.TrimSuffix(baseName, ".jpeg")
	baseName = strings.TrimSuffix(baseName, ".png")
	baseName = strings.TrimSuffix(baseName, ".webp")
	baseName = strings.TrimSuffix(baseName, ".mp4")
	baseName = strings.TrimSuffix(baseName, ".mov")
	baseName = strings.TrimSuffix(baseName, ".MOV")
	baseName = strings.TrimSuffix(baseName, ".JPG")
	baseName = strings.TrimSuffix(baseName, ".JPEG")
	baseName = strings.TrimSuffix(baseName, ".PNG")
	baseName = strings.ReplaceAll(baseName, " ", "_")

	ext := ".jpg"
	switch {
	case strings.Contains(ct, "png"):
		ext = ".png"
	case strings.Contains(ct, "webp"):
		ext = ".webp"
	case strings.Contains(ct, "gif"):
		ext = ".gif"
	case strings.Contains(ct, "mp4"):
		ext = ".mp4"
	case strings.Contains(ct, "quicktime"):
		ext = ".mov"
	}
	filename := "post_" + baseName + "_" + randSuffix() + ext

	url, err = s.Storage.Upload(file, "posts", filename)
	if err != nil {
		return "", "", err
	}
	return url, mediaType, nil
}

func (s *PostService) EditPost(userID, postID int64, caption, taggedUsers string) error {
	if len(caption) > 2000 {
		return errors.New("caption too long (max 2000 chars)")
	}
	return s.Repo.EditPost(userID, postID, caption, taggedUsers)
}

func (s *PostService) DeletePost(userID, postID int64) error {
	return s.Repo.DeletePost(userID, postID)
}

func (s *PostService) GetUserPosts(targetUserID, viewerID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetUserPosts(targetUserID, viewerID)
}

// SetPin pins/unpins a post on the owner's profile (max 3, FIFO eviction).
func (s *PostService) SetPin(userID, postID int64, pin bool) error {
	return s.Repo.SetPin(userID, postID, pin)
}

func (s *PostService) GetFollowingFeed(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetFollowingPosts(userID)
}

func (s *PostService) GetLikedPosts(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetLikedPosts(userID)
}

func (s *PostService) GetResharedPosts(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetResharedPosts(userID)
}

func (s *PostService) GetResharedPostsForUser(targetUserID, viewerID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetResharedPostsForUser(targetUserID, viewerID)
}

func (s *PostService) GetPostByID(postID, viewerID int64) (map[string]interface{}, error) {
	return s.Repo.GetPostByID(postID, viewerID)
}

func (s *PostService) GetPublicFeed(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetAllPostsWithUser(userID)
}

func (s *PostService) GetBusinessFeed(userID int64) ([]map[string]interface{}, error) {
	return s.Repo.GetBusinessPosts(userID)
}

func (s *PostService) GetPostsByHashtag(viewerID int64, tag string) ([]map[string]interface{}, error) {
	return s.Repo.GetPostsByHashtag(viewerID, tag)
}

func (s *PostService) GetTrendingHashtags() ([]map[string]interface{}, error) {
	return s.Repo.GetTrendingHashtags(7, 20)
}
