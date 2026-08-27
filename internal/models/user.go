package models

type User struct {
	ID              int64   `json:"id"`
	FullName        string  `json:"full_name"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	Mobile          string  `json:"mobile"`
	Password        string  `json:"password"`
	DOB             string  `json:"dob"`
	Gender          string  `json:"gender"`
	ProfilePhoto    string  `json:"profile_photo"`
	HeaderPhoto     string  `json:"header_photo"`
	Bio             string  `json:"bio"`
	AccountType     string  `json:"account_type"` // personal, creator, business, service
	Rating          float64 `json:"rating"`
	SalesScore      int     `json:"sales_score"`
	Posts           int     `json:"posts"`
	Following       int     `json:"following"`
	Followers       int     `json:"followers"`
	IsVerified      bool    `json:"is_verified"`
	IsPhoneVerified bool    `json:"is_phone_verified"`
	IsFollowing     bool    `json:"is_following"`
	Reputation      int     `json:"reputation"`

	// Approximate location (LGA/State) used for "nearby" discovery
	LGA          string `json:"lga"`
	State        string `json:"state"`
	LocationText string `json:"location_text,omitempty"`
	Latitude     string `json:"latitude,omitempty"`
	Longitude    string `json:"longitude,omitempty"`

	// Business profile details (only meaningful when AccountType == "business")
	BusinessName     string `json:"business_name,omitempty"`
	BusinessCategory string `json:"business_category,omitempty"`
	BusinessDesc     string `json:"business_desc,omitempty"`
	BusinessPhone    string `json:"business_phone,omitempty"`
	BusinessEmail    string `json:"business_email,omitempty"`
	BusinessWebsite  string `json:"business_website,omitempty"`
	BusinessAddress  string `json:"business_address,omitempty"`
	BusinessCountry  string `json:"business_country,omitempty"`
	BusinessState    string `json:"business_state,omitempty"`
	BusinessCity     string `json:"business_city,omitempty"`
}

// BusinessUpdate carries the optional business-upgrade fields sent alongside
// a normal profile update. An empty AccountType means "leave it as is".
type BusinessUpdate struct {
	AccountType      string
	BusinessCategory string
	BusinessName     string
	BusinessDesc     string
	BusinessPhone    string
	BusinessEmail    string
	BusinessWebsite  string
	BusinessAddress  string
	BusinessCountry  string
	BusinessState    string
	BusinessCity     string
	SellingTypes     []string
}
