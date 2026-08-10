package models

import "time"

type Demand struct {
	ID               int64     `json:"id"`
	UserID           int64     `json:"user_id"`
	LookingFor       string    `json:"looking_for"`
	Category         string    `json:"category"`
	ConditionPref    []string  `json:"condition_pref"`
	MinPrice         float64   `json:"min_price"`
	MaxPrice         float64   `json:"max_price"`
	Location         string    `json:"location"`
	Latitude         float64   `json:"latitude"`
	Longitude        float64   `json:"longitude"`
	SearchRadius     int       `json:"search_radius"`
	Description      string    `json:"description"`
	Urgency          string    `json:"urgency"`
	ContactNumber    string    `json:"contact_number"`
	PreferredContact string    `json:"preferred_contact"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
}

type Supply struct {
	ID                int64     `json:"id"`
	UserID            int64     `json:"user_id"`
	GoodsName         string    `json:"goods_name"`
	Category          string    `json:"category"`
	Condition         string    `json:"condition"`
	AgeValue          int       `json:"age_value"`
	AgeUnit           string    `json:"age_unit"`
	Brand             string    `json:"brand"`
	Price             float64   `json:"price"`
	Negotiable        bool      `json:"negotiable"`
	Description       string    `json:"description"`
	Location          string    `json:"location"`
	Latitude          float64   `json:"latitude"`
	Longitude         float64   `json:"longitude"`
	DeliveryRadius    int       `json:"delivery_radius"`
	DeliveryAvailable bool      `json:"delivery_available"`
	Photos            []string  `json:"photos"`
	ContactNumber     string    `json:"contact_number"`
	WhatsappNumber    string    `json:"whatsapp_number"`
	IsActive          bool      `json:"is_active"`
	CreatedAt         time.Time `json:"created_at"`
}
