package repository

import (
	"database/sql"
	"github.com/lib/pq"
	"markethouse/internal/models"
)

type MarketplaceRepo struct {
	DB *sql.DB
}

func NewMarketplaceRepo(db *sql.DB) *MarketplaceRepo {
	return &MarketplaceRepo{DB: db}
}

// ── DEMAND ────────────────────────────────────────────────────────────────────

func (r *MarketplaceRepo) CreateDemand(d models.Demand) (int64, error) {
	var id int64
	err := r.DB.QueryRow(`
		INSERT INTO demands
		  (user_id, looking_for, category, condition_pref, min_price, max_price,
		   location, latitude, longitude, search_radius, description,
		   urgency, contact_number, preferred_contact)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id`,
		d.UserID, d.LookingFor, d.Category, pq.Array(d.ConditionPref),
		d.MinPrice, d.MaxPrice, d.Location, d.Latitude, d.Longitude,
		d.SearchRadius, d.Description, d.Urgency, d.ContactNumber,
		d.PreferredContact,
	).Scan(&id)
	return id, err
}

func (r *MarketplaceRepo) GetDemandsByUser(userID int64) ([]models.Demand, error) {
	rows, err := r.DB.Query(`
		SELECT id, user_id, looking_for, category, condition_pref, min_price, max_price,
		       location, latitude, longitude, search_radius, description,
		       urgency, contact_number, preferred_contact, is_active, created_at
		FROM demands WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDemands(rows)
}

func (r *MarketplaceRepo) GetPublicDemands() ([]models.Demand, error) {
	rows, err := r.DB.Query(`
		SELECT id, user_id, looking_for, category, condition_pref, min_price, max_price,
		       location, latitude, longitude, search_radius, description,
		       urgency, contact_number, preferred_contact, is_active, created_at
		FROM demands WHERE is_active=true ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDemands(rows)
}

func scanDemands(rows *sql.Rows) ([]models.Demand, error) {
	var list []models.Demand
	for rows.Next() {
		var d models.Demand
		if err := rows.Scan(
			&d.ID, &d.UserID, &d.LookingFor, &d.Category, pq.Array(&d.ConditionPref),
			&d.MinPrice, &d.MaxPrice, &d.Location, &d.Latitude, &d.Longitude,
			&d.SearchRadius, &d.Description, &d.Urgency, &d.ContactNumber,
			&d.PreferredContact, &d.IsActive, &d.CreatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, nil
}

// ── SUPPLY ────────────────────────────────────────────────────────────────────

func (r *MarketplaceRepo) CreateSupply(s models.Supply) (int64, error) {
	var id int64
	err := r.DB.QueryRow(`
		INSERT INTO supplies
		  (user_id, goods_name, category, condition, age_value, age_unit, brand,
		   price, negotiable, description, location, latitude, longitude,
		   delivery_radius, delivery_available, photos, contact_number, whatsapp_number)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
		RETURNING id`,
		s.UserID, s.GoodsName, s.Category, s.Condition, s.AgeValue, s.AgeUnit,
		s.Brand, s.Price, s.Negotiable, s.Description, s.Location,
		s.Latitude, s.Longitude, s.DeliveryRadius, s.DeliveryAvailable,
		pq.Array(s.Photos), s.ContactNumber, s.WhatsappNumber,
	).Scan(&id)
	return id, err
}

func (r *MarketplaceRepo) GetSuppliesByUser(userID int64) ([]models.Supply, error) {
	rows, err := r.DB.Query(`
		SELECT id, user_id, goods_name, category, condition, age_value, age_unit, brand,
		       price, negotiable, description, location, latitude, longitude,
		       delivery_radius, delivery_available, photos, contact_number, whatsapp_number,
		       is_active, created_at
		FROM supplies WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSupplies(rows)
}

func (r *MarketplaceRepo) GetPublicSupplies(category string) ([]models.Supply, error) {
	query := `
		SELECT id, user_id, goods_name, category, condition, age_value, age_unit, brand,
		       price, negotiable, description, location, latitude, longitude,
		       delivery_radius, delivery_available, photos, contact_number, whatsapp_number,
		       is_active, created_at
		FROM supplies WHERE is_active=true`
	args := []any{}
	if category != "" && category != "All" {
		query += ` AND category=$1`
		args = append(args, category)
	}
	query += ` ORDER BY created_at DESC LIMIT 100`
	rows, err := r.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSupplies(rows)
}

func (r *MarketplaceRepo) GetMatchesForDemand(category string, minPrice, maxPrice float64) ([]models.Supply, error) {
	rows, err := r.DB.Query(`
		SELECT id, user_id, goods_name, category, condition, age_value, age_unit, brand,
		       price, negotiable, description, location, latitude, longitude,
		       delivery_radius, delivery_available, photos, contact_number, whatsapp_number,
		       is_active, created_at
		FROM supplies
		WHERE category=$1 AND price BETWEEN $2 AND $3 AND is_active=true
		ORDER BY created_at DESC`, category, minPrice, maxPrice)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSupplies(rows)
}

func scanSupplies(rows *sql.Rows) ([]models.Supply, error) {
	var list []models.Supply
	for rows.Next() {
		var s models.Supply
		if err := rows.Scan(
			&s.ID, &s.UserID, &s.GoodsName, &s.Category, &s.Condition,
			&s.AgeValue, &s.AgeUnit, &s.Brand, &s.Price, &s.Negotiable,
			&s.Description, &s.Location, &s.Latitude, &s.Longitude,
			&s.DeliveryRadius, &s.DeliveryAvailable, pq.Array(&s.Photos),
			&s.ContactNumber, &s.WhatsappNumber, &s.IsActive, &s.CreatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, s)
	}
	return list, nil
}
