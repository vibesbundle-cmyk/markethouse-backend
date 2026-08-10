package services

import (
	"fmt"
	"mime/multipart"
	"markethouse/internal/models"
	"markethouse/internal/repository"
	"markethouse/internal/storage"
)

type MarketplaceService struct {
	Repo    *repository.MarketplaceRepo
	Storage storage.Storage
}

func (s *MarketplaceService) CreateDemand(userID int64, d models.Demand) (int64, error) {
	d.UserID = userID
	if d.LookingFor == "" {
		return 0, fmt.Errorf("looking_for is required")
	}
	if d.Category == "" {
		return 0, fmt.Errorf("category is required")
	}
	if d.Location == "" {
		return 0, fmt.Errorf("location is required")
	}
	if d.ContactNumber == "" {
		return 0, fmt.Errorf("contact_number is required")
	}
	return s.Repo.CreateDemand(d)
}

func (s *MarketplaceService) CreateSupply(userID int64, sup models.Supply, files []*multipart.FileHeader) (int64, error) {
	sup.UserID = userID
	if sup.GoodsName == "" {
		return 0, fmt.Errorf("goods_name is required")
	}
	if sup.Category == "" {
		return 0, fmt.Errorf("category is required")
	}
	if sup.Description == "" {
		return 0, fmt.Errorf("description is required")
	}
	if sup.Location == "" {
		return 0, fmt.Errorf("location is required")
	}
	if sup.ContactNumber == "" {
		return 0, fmt.Errorf("contact_number is required")
	}

	// Upload each image
	for _, fh := range files {
		url, err := s.Storage.Upload(fh, "supply", fh.Filename)
		if err != nil {
			return 0, fmt.Errorf("image upload failed: %w", err)
		}
		sup.Photos = append(sup.Photos, url)
	}

	return s.Repo.CreateSupply(sup)
}

func (s *MarketplaceService) GetMyDemands(userID int64) ([]models.Demand, error) {
	return s.Repo.GetDemandsByUser(userID)
}

func (s *MarketplaceService) GetMySupplies(userID int64) ([]models.Supply, error) {
	return s.Repo.GetSuppliesByUser(userID)
}

func (s *MarketplaceService) GetPublicSupplies(category string) ([]models.Supply, error) {
	return s.Repo.GetPublicSupplies(category)
}

func (s *MarketplaceService) GetPublicDemands() ([]models.Demand, error) {
	return s.Repo.GetPublicDemands()
}
