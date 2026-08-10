package models

import "time"

type Message struct {
	ID             int64      `json:"id"`
	ConversationID int64      `json:"conversation_id"`
	SenderID       int64      `json:"sender_id"`
	ReceiverID     int64      `json:"receiver_id"`
	Content        string     `json:"content"`
	IsRead         bool       `json:"is_read"`
	CreatedAt      time.Time  `json:"created_at"`
	MessageType    string     `json:"message_type"` // text | image | video | voice | file
	MediaURL       *string    `json:"media_url,omitempty"`
	MediaType      *string    `json:"media_type,omitempty"`
	ReplyToID      *int64     `json:"reply_to_id,omitempty"`
	IsStarred      bool       `json:"is_starred"`
	IsPinned       bool       `json:"is_pinned"`
	Reaction       *string    `json:"reaction,omitempty"`
	IsEdited       bool       `json:"is_edited"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Latitude       *float64   `json:"latitude,omitempty"`
	Longitude      *float64   `json:"longitude,omitempty"`
}
