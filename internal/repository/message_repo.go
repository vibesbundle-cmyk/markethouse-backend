package repository

import (
	"database/sql"
	"markethouse/internal/models"
)

type MessageRepo struct {
	DB *sql.DB
}

func (r *MessageRepo) GetOrCreateConversation(userOne, userTwo int64) (int64, error) {
	var id int64
	if userOne > userTwo {
		userOne, userTwo = userTwo, userOne
	}
	err := r.DB.QueryRow(`
		INSERT INTO conversations (user_one_id, user_two_id)
		VALUES ($1, $2)
		ON CONFLICT (user_one_id, user_two_id)
		DO UPDATE SET updated_at = CURRENT_TIMESTAMP
		RETURNING id`, userOne, userTwo).Scan(&id)
	return id, err
}

func (r *MessageRepo) CreateMessage(msg *models.Message) error {
	err := r.DB.QueryRow(`
		INSERT INTO messages
			(conversation_id, sender_id, receiver_id, content, message_type, media_url, media_type, reply_to_id, latitude, longitude)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at`,
		msg.ConversationID, msg.SenderID, msg.ReceiverID, msg.Content,
		msg.MessageType, msg.MediaURL, msg.MediaType, msg.ReplyToID,
		msg.Latitude, msg.Longitude,
	).Scan(&msg.ID, &msg.CreatedAt)
	if err == nil {
		snippet := msg.Content
		if snippet == "" && msg.MessageType == "location" {
			snippet = "📍 Location"
		} else if snippet == "" && msg.MediaType != nil {
			snippet = "[" + *msg.MediaType + "]"
		}
		r.DB.Exec(
			"UPDATE conversations SET last_message=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2",
			snippet, msg.ConversationID)
	}
	return err
}

func (r *MessageRepo) GetMessages(convID int64, limit int) ([]models.Message, error) {
	rows, err := r.DB.Query(`
		SELECT
			m.id, m.sender_id, m.receiver_id, m.content, m.is_read, m.created_at,
			COALESCE(m.message_type,'text'),
			m.media_url, m.media_type, m.reply_to_id,
			COALESCE(m.is_starred,false), COALESCE(m.is_pinned,false),
			m.reaction, COALESCE(m.is_edited,false), m.expires_at,
			m.latitude, m.longitude
		FROM messages m
		WHERE m.conversation_id = $1
		ORDER BY m.created_at ASC
		LIMIT $2`, convID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.Message
	for rows.Next() {
		var m models.Message
		m.ConversationID = convID
		var mediaURL, mediaType, reaction sql.NullString
		var replyToID sql.NullInt64
		var expiresAt sql.NullTime
		var lat, lng sql.NullFloat64
		if err := rows.Scan(&m.ID, &m.SenderID, &m.ReceiverID, &m.Content,
			&m.IsRead, &m.CreatedAt, &m.MessageType,
			&mediaURL, &mediaType, &replyToID,
			&m.IsStarred, &m.IsPinned, &reaction,
			&m.IsEdited, &expiresAt, &lat, &lng); err != nil {
			return nil, err
		}
		if mediaURL.Valid  { m.MediaURL  = &mediaURL.String  }
		if mediaType.Valid { m.MediaType = &mediaType.String  }
		if replyToID.Valid { v := replyToID.Int64; m.ReplyToID = &v }
		if reaction.Valid  { m.Reaction  = &reaction.String  }
		if expiresAt.Valid { t := expiresAt.Time; m.ExpiresAt = &t }
		if lat.Valid       { m.Latitude  = &lat.Float64  }
		if lng.Valid       { m.Longitude = &lng.Float64  }
		list = append(list, m)
	}
	return list, nil
}

func (r *MessageRepo) StarMessage(msgID int64, star bool) error {
	_, err := r.DB.Exec(`UPDATE messages SET is_starred=$1 WHERE id=$2`, star, msgID)
	return err
}

func (r *MessageRepo) PinMessage(msgID int64, pin bool) error {
	_, err := r.DB.Exec(`UPDATE messages SET is_pinned=$1 WHERE id=$2`, pin, msgID)
	return err
}

func (r *MessageRepo) ReactMessage(msgID int64, reaction string) error {
	_, err := r.DB.Exec(`UPDATE messages SET reaction=$1 WHERE id=$2`, reaction, msgID)
	return err
}

func (r *MessageRepo) EditMessage(userID, msgID int64, newContent string) error {
	_, err := r.DB.Exec(
		`UPDATE messages SET content=$1, is_edited=true, edited_at=NOW() WHERE id=$2 AND sender_id=$3`,
		newContent, msgID, userID)
	return err
}

func (r *MessageRepo) DeleteMessage(userID, msgID int64) error {
	_, err := r.DB.Exec(`DELETE FROM messages WHERE id=$1 AND sender_id=$2`, msgID, userID)
	return err
}

func (r *MessageRepo) GetPinnedMessages(convID int64) ([]models.Message, error) {
	rows, err := r.DB.Query(
		`SELECT id,sender_id,receiver_id,content,is_read,created_at,COALESCE(message_type,'text'),
		 media_url,media_type,reply_to_id,COALESCE(is_starred,false),COALESCE(is_pinned,false),reaction,COALESCE(is_edited,false),expires_at,
		 latitude,longitude
		 FROM messages WHERE conversation_id=$1 AND is_pinned=true ORDER BY created_at DESC`, convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.Message
	for rows.Next() {
		var m models.Message
		m.ConversationID = convID
		var mediaURL, mediaType, reaction sql.NullString
		var replyToID sql.NullInt64
		var expiresAt sql.NullTime
		var lat, lng sql.NullFloat64
		if err := rows.Scan(&m.ID,&m.SenderID,&m.ReceiverID,&m.Content,
			&m.IsRead,&m.CreatedAt,&m.MessageType,
			&mediaURL,&mediaType,&replyToID,
			&m.IsStarred,&m.IsPinned,&reaction,
			&m.IsEdited,&expiresAt,&lat,&lng); err != nil {
			return nil, err
		}
		if mediaURL.Valid  { m.MediaURL  = &mediaURL.String }
		if mediaType.Valid { m.MediaType = &mediaType.String }
		if replyToID.Valid { v:=replyToID.Int64; m.ReplyToID=&v }
		if reaction.Valid  { m.Reaction  = &reaction.String }
		if expiresAt.Valid { t:=expiresAt.Time; m.ExpiresAt=&t }
		if lat.Valid       { m.Latitude  = &lat.Float64 }
		if lng.Valid       { m.Longitude = &lng.Float64 }
		list = append(list, m)
	}
	return list, nil
}

func (r *MessageRepo) UpdateConversationSettings(convID int64, settings map[string]interface{}) error {
	for k, v := range settings {
		r.DB.Exec(`UPDATE conversations SET `+k+`=$1 WHERE id=$2`, v, convID)
	}
	return nil
}

// EnrichedConversation holds conversation data + other user info for the Flutter client
type EnrichedConversation struct {
	ID                  int64  `json:"id"`
	OtherUserID         int64  `json:"other_user_id"`
	OtherUserName       string `json:"other_user_name"`
	OtherUserPhoto      string `json:"other_user_photo"`
	LastMessage         string `json:"last_message"`
	LastTime            string `json:"last_time"`
	UnreadCount         int    `json:"unread_count"`
	IsOnline            bool   `json:"is_online"`
	IsPinned            bool   `json:"is_pinned"`
	IsArchived          bool   `json:"is_archived"`
	CustomCategory      string `json:"custom_category"`
	Wallpaper           string `json:"wallpaper"`
	WallpaperColor      string `json:"wallpaper_color"`
	WallpaperDim        float64 `json:"wallpaper_dim"`
	BubbleColor         string `json:"bubble_color"`
	DisappearingSeconds int    `json:"disappearing_seconds"`
	IsMuted             bool   `json:"is_muted"`
}

func (r *MessageRepo) GetConversations(userID int64) ([]EnrichedConversation, error) {
	rows, err := r.DB.Query(`
		SELECT
			c.id,
			CASE WHEN c.user_one_id=$1 THEN c.user_two_id ELSE c.user_one_id END AS other_user_id,
			CASE WHEN c.user_one_id=$1 THEN u2.full_name   ELSE u1.full_name   END AS other_user_name,
			CASE WHEN c.user_one_id=$1 THEN COALESCE(u2.profile_photo,'') ELSE COALESCE(u1.profile_photo,'') END AS other_user_photo,
			COALESCE(c.last_message,'')                     AS last_message,
			COALESCE(to_char(c.updated_at,'HH24:MI'),'')   AS last_time,
			(SELECT COUNT(*) FROM messages m WHERE m.conversation_id=c.id AND m.receiver_id=$1 AND m.is_read=false) AS unread_count,
			COALESCE(c.is_pinned,false),
			COALESCE(c.is_archived,false),
			COALESCE(c.custom_category,''),
			COALESCE(c.wallpaper,''),
			COALESCE(c.wallpaper_color,''),
			COALESCE(c.wallpaper_dim,0.3),
			COALESCE(c.bubble_color,''),
			COALESCE(c.disappearing_seconds,0),
			COALESCE(c.is_muted,false)
		FROM conversations c
		JOIN users u1 ON u1.id=c.user_one_id
		JOIN users u2 ON u2.id=c.user_two_id
		WHERE c.user_one_id=$1 OR c.user_two_id=$1
		ORDER BY COALESCE(c.is_pinned,false) DESC, c.updated_at DESC NULLS LAST
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []EnrichedConversation
	for rows.Next() {
		var c EnrichedConversation
		if err := rows.Scan(&c.ID,&c.OtherUserID,&c.OtherUserName,&c.OtherUserPhoto,
			&c.LastMessage,&c.LastTime,&c.UnreadCount,
			&c.IsPinned,&c.IsArchived,&c.CustomCategory,
			&c.Wallpaper,&c.WallpaperColor,&c.WallpaperDim,&c.BubbleColor,
			&c.DisappearingSeconds,&c.IsMuted); err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	if list == nil { list = []EnrichedConversation{} }
	return list, nil
}

const enrichedConversationColumns = `
			c.id,
			CASE WHEN c.user_one_id=$1 THEN c.user_two_id ELSE c.user_one_id END AS other_user_id,
			CASE WHEN c.user_one_id=$1 THEN u2.full_name   ELSE u1.full_name   END AS other_user_name,
			CASE WHEN c.user_one_id=$1 THEN COALESCE(u2.profile_photo,'') ELSE COALESCE(u1.profile_photo,'') END AS other_user_photo,
			COALESCE(c.last_message,'')                     AS last_message,
			COALESCE(to_char(c.updated_at,'HH24:MI'),'')   AS last_time,
			(SELECT COUNT(*) FROM messages m WHERE m.conversation_id=c.id AND m.receiver_id=$1 AND m.is_read=false) AS unread_count,
			COALESCE(c.is_pinned,false),
			COALESCE(c.is_archived,false),
			COALESCE(c.custom_category,''),
			COALESCE(c.wallpaper,''),
			COALESCE(c.wallpaper_color,''),
			COALESCE(c.wallpaper_dim,0.3),
			COALESCE(c.bubble_color,''),
			COALESCE(c.disappearing_seconds,0),
			COALESCE(c.is_muted,false)`

// GetConversation returns one conversation (with the other user's info) for
// the calling user. Used to reload per-chat settings after they change.
func (r *MessageRepo) GetConversation(convID, userID int64) (EnrichedConversation, error) {
	var c EnrichedConversation
	err := r.DB.QueryRow(`
		SELECT `+enrichedConversationColumns+`
		FROM conversations c
		JOIN users u1 ON u1.id=c.user_one_id
		JOIN users u2 ON u2.id=c.user_two_id
		WHERE c.id=$2 AND (c.user_one_id=$1 OR c.user_two_id=$1)
	`, userID, convID).Scan(&c.ID,&c.OtherUserID,&c.OtherUserName,&c.OtherUserPhoto,
		&c.LastMessage,&c.LastTime,&c.UnreadCount,
		&c.IsPinned,&c.IsArchived,&c.CustomCategory,
		&c.Wallpaper,&c.WallpaperColor,&c.WallpaperDim,&c.BubbleColor,
		&c.DisappearingSeconds,&c.IsMuted)
	return c, err
}
