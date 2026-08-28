package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
		snippet := r.snippetFor(msg)
		r.DB.Exec(
			"UPDATE conversations SET last_message=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2",
			snippet, msg.ConversationID)
		// A new message resurfaces the chat for anyone who hid/deleted it.
		r.DB.Exec(`
			UPDATE conversations SET
				hidden_at_one = CASE WHEN user_one_id=$2 THEN NULL ELSE hidden_at_one END,
				hidden_at_two = CASE WHEN user_two_id=$2 THEN NULL ELSE hidden_at_two END
			WHERE id=$1`, msg.ConversationID, msg.SenderID)
	}
	return err
}

// snippetFor builds the conversation-list preview line. Media messages get
// friendly labels (plus a duration for voice/video when the client stored
// one); structured payloads never leak raw JSON into the list.
func (r *MessageRepo) snippetFor(msg *models.Message) string {
	mt := msg.MessageType

	var payload struct {
		Name     string  `json:"name"`
		Amount   float64 `json:"amount"`
		Duration int     `json:"duration"`
		Caption  string  `json:"caption"`
	}
	hasPayload := false
	if msg.Content != "" && msg.Content[0] == '{' {
		if err := json.Unmarshal([]byte(msg.Content), &payload); err == nil &&
			(payload.Name != "" || payload.Amount > 0 || payload.Duration > 0) {
			hasPayload = true
		}
	}

	dur := ""
	if payload.Duration > 0 {
		dur = fmt.Sprintf(" · %d:%02d", payload.Duration/60, payload.Duration%60)
	}

	switch {
	case mt == "location":
		return "📍 Location"
	case mt == "transfer":
		if payload.Amount > 0 {
			return fmt.Sprintf("💸 Sent ₦%.2f", payload.Amount)
		}
		return "💸 Money transfer"
	case mt == "file" && hasPayload:
		return "📄 " + payload.Name
	case hasPayload && payload.Caption != "":
		return payload.Caption
	case mt == "audio" || mt == "voice" || *safeMediaType(msg) == "audio":
		return "🎤 Voice message" + dur
	case mt == "image" || *safeMediaType(msg) == "image":
		return "📷 Photo"
	case mt == "video" || *safeMediaType(msg) == "video":
		return "🎬 Video" + dur
	default:
		return msg.Content
	}
}

func safeMediaType(msg *models.Message) *string {
	if msg.MediaType != nil {
		return msg.MediaType
	}
	e := ""
	return &e
}

func (r *MessageRepo) GetMessages(convID, userID int64, limit int) ([]models.Message, error) {
	// Hide everything the calling user cleared out of the chat (their own
	// per-side marker), while keeping the other side's view untouched.
	rows, err := r.DB.Query(`
		SELECT
			m.id, m.sender_id, m.receiver_id, m.content, m.is_read, m.created_at,
			COALESCE(m.message_type,'text'),
			m.media_url, m.media_type, m.reply_to_id,
			COALESCE(m.is_starred,false), COALESCE(m.is_pinned,false),
			m.reaction, COALESCE(m.is_edited,false), m.expires_at,
			m.latitude, m.longitude
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		WHERE m.conversation_id = $1
		  AND m.created_at > COALESCE(
		        CASE WHEN c.user_one_id = $2 THEN c.cleared_at_one ELSE c.cleared_at_two END,
		        to_timestamp(0))
		ORDER BY m.created_at ASC
		LIMIT $3`, convID, userID, limit)
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
	res, err := r.DB.Exec(
		`UPDATE messages SET content='🗑️ This message was deleted', deleted_at=NOW(), message_type='deleted'
		 WHERE id=$1 AND sender_id=$2`, msgID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
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

func (r *MessageRepo) GetStarredMessages(userID int64) ([]map[string]interface{}, error) {
	rows, err := r.DB.Query(`
		SELECT m.id, m.conversation_id, m.sender_id, m.receiver_id, m.content, m.created_at,
		       COALESCE(u_sender.full_name, '') AS sender_name,
		       COALESCE(u_sender.profile_photo, '') AS sender_photo,
		       COALESCE(u_receiver.full_name, '') AS receiver_name,
		       COALESCE(u_receiver.profile_photo, '') AS receiver_photo
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		LEFT JOIN users u_sender ON u_sender.id = m.sender_id
		LEFT JOIN users u_receiver ON u_receiver.id = m.receiver_id
		WHERE m.is_starred = true
		  AND (m.sender_id = $1 OR m.receiver_id = $1)
		  AND (c.user_one_id = $1 OR c.user_two_id = $1)
		ORDER BY m.created_at DESC
		LIMIT 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []map[string]interface{}
	for rows.Next() {
		var id, convID, senderID, receiverID int64
		var content, createdAt, senderName, senderPhoto, receiverName, receiverPhoto string
		if err := rows.Scan(&id, &convID, &senderID, &receiverID, &content, &createdAt,
			&senderName, &senderPhoto, &receiverName, &receiverPhoto); err != nil {
			return nil, err
		}
		list = append(list, map[string]interface{}{
			"id":              id,
			"conversation_id": convID,
			"sender_id":       senderID,
			"receiver_id":     receiverID,
			"body":            content,
			"created_at":      createdAt,
			"sender_name":     senderName,
			"sender_photo":    senderPhoto,
			"receiver_name":   receiverName,
			"receiver_photo":  receiverPhoto,
		})
	}
	if list == nil {
		list = []map[string]interface{}{}
	}
	return list, nil
}

func (r *MessageRepo) SearchMessages(userID int64, query string) ([]map[string]interface{}, error) {
	like := "%" + query + "%"
	rows, err := r.DB.Query(`
		SELECT m.id, m.conversation_id, m.sender_id, m.receiver_id, m.content, m.created_at,
		       COALESCE(u_sender.full_name, '') AS sender_name,
		       COALESCE(u_sender.profile_photo, '') AS sender_photo,
		       COALESCE(u_receiver.full_name, '') AS receiver_name,
		       COALESCE(u_receiver.profile_photo, '') AS receiver_photo
		FROM messages m
		JOIN conversations c ON c.id = m.conversation_id
		LEFT JOIN users u_sender ON u_sender.id = m.sender_id
		LEFT JOIN users u_receiver ON u_receiver.id = m.receiver_id
		WHERE (m.sender_id = $1 OR m.receiver_id = $1)
		  AND (c.user_one_id = $1 OR c.user_two_id = $1)
		  AND m.content ILIKE $2
		ORDER BY m.created_at DESC
		LIMIT 100`, userID, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []map[string]interface{}
	for rows.Next() {
		var id, convID, senderID, receiverID int64
		var content, createdAt, senderName, senderPhoto, receiverName, receiverPhoto string
		if err := rows.Scan(&id, &convID, &senderID, &receiverID, &content, &createdAt,
			&senderName, &senderPhoto, &receiverName, &receiverPhoto); err != nil {
			return nil, err
		}
		list = append(list, map[string]interface{}{
			"id":              id,
			"conversation_id": convID,
			"sender_id":       senderID,
			"receiver_id":     receiverID,
			"body":            content,
			"created_at":      createdAt,
			"sender_name":     senderName,
			"sender_photo":    senderPhoto,
			"receiver_name":   receiverName,
			"receiver_photo":  receiverPhoto,
		})
	}
	if list == nil {
		list = []map[string]interface{}{}
	}
	return list, nil
}

// MarkMessagesRead flags every unread message the user received in this
// conversation as read. Called when they load the chat history.
func (r *MessageRepo) MarkMessagesRead(convID, userID int64) error {
	_, err := r.DB.Exec(
		`UPDATE messages SET is_read=true WHERE conversation_id=$1 AND receiver_id=$2 AND is_read=false`,
		convID, userID)
	return err
}

// ClearConversation marks "now" as the start of the calling user's view of
// the chat. Their history and unread badge ignore older messages; the other
// participant keeps seeing everything.
func (r *MessageRepo) ClearConversation(convID, userID int64) error {
	res, err := r.DB.Exec(`
		UPDATE conversations SET
			cleared_at_one = CASE WHEN user_one_id=$2 THEN NOW() ELSE cleared_at_one END,
			cleared_at_two = CASE WHEN user_two_id=$2 THEN NOW() ELSE cleared_at_two END
		WHERE id=$1 AND ($2 IN (user_one_id, user_two_id))`,
		convID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// HideConversation sets a per-user timestamp that hides the conversation
// from the caller's chat list. The other participant is unaffected.
func (r *MessageRepo) HideConversation(convID, userID int64) error {
	res, err := r.DB.Exec(`
		UPDATE conversations SET
			hidden_at_one = CASE WHEN user_one_id=$2 THEN NOW() ELSE hidden_at_one END,
			hidden_at_two = CASE WHEN user_two_id=$2 THEN NOW() ELSE hidden_at_two END
		WHERE id=$1 AND ($2 IN (user_one_id, user_two_id))`,
		convID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
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
	OtherUserHeader     string `json:"other_user_header"`
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
	BubbleOpacity       float64 `json:"bubble_opacity"`
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
			CASE WHEN c.user_one_id=$1 THEN COALESCE(u2.header_photo,'') ELSE COALESCE(u1.header_photo,'') END AS other_user_header,
			COALESCE(c.last_message,'')                     AS last_message,
			COALESCE(to_char(c.updated_at,'HH24:MI'),'')   AS last_time,
			(SELECT COUNT(*) FROM messages m WHERE m.conversation_id=c.id AND m.receiver_id=$1 AND m.is_read=false
			   AND m.created_at > COALESCE(CASE WHEN c.user_one_id=$1 THEN c.cleared_at_one ELSE c.cleared_at_two END, to_timestamp(0))) AS unread_count,
			COALESCE(c.is_pinned,false),
			COALESCE(c.is_archived,false),
			COALESCE(c.custom_category,''),
			COALESCE(c.wallpaper,''),
			COALESCE(c.wallpaper_color,''),
			COALESCE(c.wallpaper_dim,0.3),
			COALESCE(c.bubble_color,''),
			COALESCE(c.bubble_opacity,1),
			COALESCE(c.disappearing_seconds,0),
			COALESCE(c.is_muted,false)
		FROM conversations c
		JOIN users u1 ON u1.id=c.user_one_id
		JOIN users u2 ON u2.id=c.user_two_id
		WHERE (c.user_one_id=$1 OR c.user_two_id=$1)
		  AND (
			CASE WHEN c.user_one_id=$1 THEN c.hidden_at_one ELSE c.hidden_at_two END
			IS NULL
		  )
		ORDER BY COALESCE(c.is_pinned,false) DESC, c.updated_at DESC NULLS LAST
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []EnrichedConversation
	for rows.Next() {
		var c EnrichedConversation
		if err := rows.Scan(&c.ID,&c.OtherUserID,&c.OtherUserName,&c.OtherUserPhoto,&c.OtherUserHeader,
			&c.LastMessage,&c.LastTime,&c.UnreadCount,
			&c.IsPinned,&c.IsArchived,&c.CustomCategory,
			&c.Wallpaper,&c.WallpaperColor,&c.WallpaperDim,&c.BubbleColor,&c.BubbleOpacity,
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
			CASE WHEN c.user_one_id=$1 THEN COALESCE(u2.header_photo,'') ELSE COALESCE(u1.header_photo,'') END AS other_user_header,
			COALESCE(c.last_message,'')                     AS last_message,
			COALESCE(to_char(c.updated_at,'HH24:MI'),'')   AS last_time,
			(SELECT COUNT(*) FROM messages m WHERE m.conversation_id=c.id AND m.receiver_id=$1 AND m.is_read=false
			   AND m.created_at > COALESCE(CASE WHEN c.user_one_id=$1 THEN c.cleared_at_one ELSE c.cleared_at_two END, to_timestamp(0))) AS unread_count,
			COALESCE(c.is_pinned,false),
			COALESCE(c.is_archived,false),
			COALESCE(c.custom_category,''),
			COALESCE(c.wallpaper,''),
			COALESCE(c.wallpaper_color,''),
			COALESCE(c.wallpaper_dim,0.3),
			COALESCE(c.bubble_color,''),
			COALESCE(c.bubble_opacity,1),
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
	`, userID, convID).Scan(&c.ID,&c.OtherUserID,&c.OtherUserName,&c.OtherUserPhoto,&c.OtherUserHeader,
		&c.LastMessage,&c.LastTime,&c.UnreadCount,
		&c.IsPinned,&c.IsArchived,&c.CustomCategory,
		&c.Wallpaper,&c.WallpaperColor,&c.WallpaperDim,&c.BubbleColor,
		&c.DisappearingSeconds,&c.IsMuted)
	return c, err
}

// PurgeConversation wipes EVERY trace of a conversation for both people:
// all message rows are deleted (their media URLs are returned so the caller
// can remove the files), the preview resets, and the chat drops off both
// chat lists until someone messages again — then it starts completely fresh.
func (r *MessageRepo) PurgeConversation(convID, userID int64) ([]string, error) {
	var member int64
	if err := r.DB.QueryRow(`
		SELECT COUNT(1) FROM conversations
		WHERE id=$1 AND ($2 IN (user_one_id, user_two_id))`,
		convID, userID).Scan(&member); err != nil {
		return nil, err
	}
	if member == 0 {
		return nil, sql.ErrNoRows
	}
	rows, err := r.DB.Query(
		`SELECT COALESCE(media_url,'') FROM messages WHERE conversation_id=$1`, convID)
	if err != nil {
		return nil, err
	}
	var media []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err == nil && u != "" {
			media = append(media, u)
		}
	}
	rows.Close()
	if _, err := r.DB.Exec(`DELETE FROM messages WHERE conversation_id=$1`, convID); err != nil {
		return media, err
	}
	_, err = r.DB.Exec(`
		UPDATE conversations SET
			last_message = '',
			cleared_at_one = NULL,
			cleared_at_two = NULL,
			hidden_at_one = NOW(),
			hidden_at_two = NOW(),
			updated_at = CURRENT_TIMESTAMP
		WHERE id=$1`, convID)
	return media, err
}
