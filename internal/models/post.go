package models

// PostMediaItem is one photo or video attached to a post. A post can now
// carry several of these, mixed (some images, some videos) in any order.
type PostMediaItem struct {
	URL  string `json:"url"`
	Type string `json:"type"` // image | video
}

type Post struct {
	ID          int64           `json:"id"`
	UserID      int64           `json:"user_id"`
	Caption     string          `json:"caption"`
	MediaURL    string          `json:"media_url"`  // first item — kept for backward compatibility
	MediaType   string          `json:"media_type"` // first item — kept for backward compatibility
	Media       []PostMediaItem `json:"media"`       // full ordered list of photos/videos on this post
	PostType    string          `json:"post_type"`  // social, product, service, subscriber_only
	Category    string          `json:"category"`   // required — feeds the recommendation engine
	Price       float64         `json:"price,omitempty"`
	IsLocked    bool            `json:"is_locked"`
	TaggedUsers string          `json:"tagged_users,omitempty"` // comma-separated user IDs
	Location    string          `json:"location,omitempty"`     // place name for the post
	Latitude    *float64        `json:"latitude,omitempty"`
	Longitude   *float64        `json:"longitude,omitempty"`
	Audience    string          `json:"audience,omitempty"`             // public | followers | private
	AudienceIDs string          `json:"audience_user_ids,omitempty"`    // comma-separated user IDs (private/custom)
	CreatedAt   string          `json:"created_at"`
}
