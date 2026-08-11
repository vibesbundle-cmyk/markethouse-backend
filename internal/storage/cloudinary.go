package storage

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
)

// CloudinaryStorage stores uploads on Cloudinary, so files survive backend
// redeploys (Render's local disk is ephemeral and is wiped on every deploy).
// URLs are permanent, served from res.cloudinary.com.
type CloudinaryStorage struct {
	cld *cloudinary.Cloudinary
}

func NewCloudinaryStorage() (*CloudinaryStorage, error) {
	cloudName := os.Getenv("CLOUDINARY_CLOUD_NAME")
	apiKey := os.Getenv("CLOUDINARY_API_KEY")
	apiSecret := os.Getenv("CLOUDINARY_API_SECRET")
	if cloudName == "" || apiKey == "" || apiSecret == "" {
		return nil, errors.New("CLOUDINARY_CLOUD_NAME, CLOUDINARY_API_KEY, CLOUDINARY_API_SECRET are required")
	}
	cld, err := cloudinary.NewFromParams(cloudName, apiKey, apiSecret)
	if err != nil {
		return nil, err
	}
	return &CloudinaryStorage{cld: cld}, nil
}

func (s *CloudinaryStorage) Upload(file *multipart.FileHeader, folder string, filename string) (string, error) {
	// Validate folder (same allow-list as LocalStorage)
	if !allowedFolders[folder] {
		return "", errors.New("invalid upload folder")
	}

	// Sanitize filename — strip any path components
	filename = filepath.Base(filename)
	if filename == "." || filename == "" {
		return "", errors.New("invalid filename")
	}

	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer src.Close()

	ctx := context.Background()
	resp, err := s.cld.Upload.Upload(ctx, src, uploader.UploadParams{
		Folder:         folder,
		PublicID:       filename,
		ResourceType:   "image",
		UseFilename:    boolPtr(true),
		UniqueFilename: boolPtr(true),
		Overwrite:      boolPtr(true),
	})
	if err != nil {
		return "", fmt.Errorf("cloudinary upload failed: %w", err)
	}
	return resp.SecureURL, nil
}

func boolPtr(b bool) *bool { return &b }

func (s *CloudinaryStorage) Delete(fileURL string) error {
	if fileURL == "" {
		return nil
	}
	// Only destroy files we own on Cloudinary. Old local-disk URLs (uploaded
	// before the switch) don't exist there — ignore them.
	i := strings.Index(fileURL, "/image/upload/")
	if i < 0 {
		return nil
	}
	publicID := fileURL[i+len("/image/upload/"):]
	// strip leading version marker v1234567890/
	if strings.HasPrefix(publicID, "v") {
		if slash := strings.Index(publicID, "/"); slash > 0 {
			rest := publicID[slash+1:]
			if _, err := fmt.Sscanf(publicID[:slash], "v%*d", new(int)); err == nil {
				publicID = rest
			}
		}
	}
	// strip file extension for the public id
	if ext := filepath.Ext(publicID); ext != "" {
		publicID = strings.TrimSuffix(publicID, ext)
	}

	ctx := context.Background()
	_, err := s.cld.Upload.Destroy(ctx, uploader.DestroyParams{PublicID: publicID})
	return err
}
