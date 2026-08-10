package storage

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
)

// allowedFolders restricts where files can be written
var allowedFolders = map[string]bool{
	"profile":   true,
	"header":    true,
	"posts":     true,
	"supply":    true,
	"products":  true,
	"commerce":  true, // commerce_handler.go: listing images
	"chat":      true, // chat message media
	"community": true, // community post/cover images
	"status":    true, // status updates
}

type LocalStorage struct {
	BaseURL string
}

func (s *LocalStorage) Upload(file *multipart.FileHeader, folder string, filename string) (string, error) {

	// Validate folder
	if !allowedFolders[folder] {
		return "", errors.New("invalid upload folder")
	}

	// Sanitize filename — strip any path components
	filename = filepath.Base(filename)
	if filename == "." || filename == "" {
		return "", errors.New("invalid filename")
	}

	// Prevent path traversal in folder name too
	if strings.Contains(folder, "..") || strings.Contains(folder, "/") {
		return "", errors.New("invalid folder")
	}

	dir := filepath.Join("uploads", folder)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	fullPath := filepath.Join(dir, filename)

	src, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("failed to open uploaded file: %w", err)
	}
	defer src.Close()

	// Fixed: Changed from 0640 to 0644 for proper web access
	// 0644 = rw-r--r-- (owner read/write, group/others read)
	dst, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer dst.Close()

	// Fixed: Use io.Copy instead of io.CopyN to copy entire file
	if _, err = io.Copy(dst, src); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	// Fixed: Add Sync to ensure data is written to disk
	if err = dst.Sync(); err != nil {
		return "", fmt.Errorf("failed to sync file to disk: %w", err)
	}

	return s.BaseURL + "/uploads/" + folder + "/" + filename, nil
}

func (s *LocalStorage) Delete(fileURL string) error {
	if fileURL == "" {
		return nil
	}

	// Strip base URL prefix and sanitize
	path := fileURL
	if s.BaseURL != "" && strings.HasPrefix(fileURL, s.BaseURL) {
		path = fileURL[len(s.BaseURL):]
	}

	// Clean and ensure path stays within uploads/
	clean := filepath.Clean(strings.TrimPrefix(path, "/"))
	if !strings.HasPrefix(clean, "uploads"+string(filepath.Separator)) {
		return errors.New("invalid file path")
	}

	err := os.Remove(clean)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
