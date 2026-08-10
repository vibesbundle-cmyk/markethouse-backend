package storage

import "mime/multipart"

type Storage interface {
	Upload(file *multipart.FileHeader, folder string, filename string) (string, error)
	Delete(fileURL string) error
}