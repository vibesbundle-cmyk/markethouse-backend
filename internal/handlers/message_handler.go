package handlers

import (
	"log"
	"markethouse/internal/models"
	"markethouse/internal/services"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type MessageHandler struct {
	Service *services.MessageService
}

func (h *MessageHandler) SendMessage(c *gin.Context) {
	senderID := c.GetInt64("user_id")
	var req struct {
		ReceiverID   int64    `json:"receiver_id"`
		Content      string   `json:"content"`
		MessageType  string   `json:"message_type"`
		MediaURL     *string  `json:"media_url"`
		MediaType    *string  `json:"media_type"`
		ReplyToID    *int64   `json:"reply_to_id"`
		Latitude     *float64 `json:"latitude"`
		Longitude    *float64 `json:"longitude"`
		IsForwarded  bool     `json:"is_forwarded"`
		ExpiresIn    int      `json:"expires_in"` // seconds; per-message disappearing
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.MessageType == "" {
		req.MessageType = "text"
	}
	if req.MessageType == "location" {
		if req.Latitude == nil || req.Longitude == nil {
			c.JSON(400, gin.H{"error": "latitude and longitude required for location messages"})
			return
		}
		if *req.Latitude < -90 || *req.Latitude > 90 ||
			*req.Longitude < -180 || *req.Longitude > 180 {
			c.JSON(400, gin.H{"error": "invalid coordinates"})
			return
		}
	}
	msg := models.Message{
		ReceiverID:   req.ReceiverID,
		Content:      req.Content,
		MessageType:  req.MessageType,
		MediaURL:     req.MediaURL,
		MediaType:    req.MediaType,
		ReplyToID:    req.ReplyToID,
		Latitude:     req.Latitude,
		Longitude:    req.Longitude,
		IsForwarded:  req.IsForwarded,
	}
	if req.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(req.ExpiresIn) * time.Second)
		msg.ExpiresAt = &t
	}
	result, err := h.Service.SendMessage(senderID, req.ReceiverID, msg)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// OS push for DMs. WS only reaches sockets that are still open — a
	// backgrounded/killed app never shows a tray banner from new_message alone.
	if h.Service.Repo == nil || h.Service.Repo.DB == nil {
		log.Println("dm push: skipped (no db)")
	} else {
		db := h.Service.Repo.DB
		if !notifyAllowed(db, req.ReceiverID, "message") {
			log.Printf("dm push: skipped (prefs off) user=%d", req.ReceiverID)
		} else {
			var muted bool
			db.QueryRow(`SELECT COALESCE(is_muted,false) FROM conversations WHERE id=$1`,
				result.ConversationID).Scan(&muted)
			if muted {
				log.Printf("dm push: skipped (muted) conv=%d", result.ConversationID)
			} else {
				title := userName(db, senderID)
				body := pushPreview(req.MessageType, req.Content)
				data := map[string]string{
					"type":            "message",
					"entity_type":     "message",
					"entity_id":       strconv.FormatInt(result.ConversationID, 10),
					"conversation_id": strconv.FormatInt(result.ConversationID, 10),
					"sender_id":       strconv.FormatInt(senderID, 10),
				}
				log.Printf("dm push: sending to user=%d conv=%d title=%q", req.ReceiverID, result.ConversationID, title)
				go services.SendPush(db, req.ReceiverID, title, body, data)
			}
		}
	}
	c.JSON(200, gin.H{"conversation_id": result.ConversationID, "message": result})
}

// pushPreview is the tray body for a DM: text (or caption) truncated, with
// friendly labels for media types that have no caption.
func pushPreview(msgType, content string) string {
	content = strings.TrimSpace(content)
	if content == "" || (len(content) > 0 && content[0] == '{') {
		switch msgType {
		case "image":
			return "Sent a photo"
		case "video":
			return "Sent a video"
		case "audio", "voice":
			return "Sent a voice message"
		case "file":
			return "Sent a file"
		case "location":
			return "Sent a location"
		case "transfer":
			return "Sent a money transfer"
		default:
			return "Sent a message"
		}
	}
	const max = 120
	runes := []rune(content)
	if len(runes) > max {
		return string(runes[:max]) + "…"
	}
	return content
}

func (h *MessageHandler) GetHistory(c *gin.Context) {
	userID := c.GetInt64("user_id")
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	messages, err := h.Service.GetChatHistory(convID, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"messages": messages})
}

func (h *MessageHandler) GetConversations(c *gin.Context) {
	userID := c.GetInt64("user_id")
	chats, err := h.Service.GetUserChats(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"conversations": chats})
}

func (h *MessageHandler) StarMessage(c *gin.Context) {
	msgID, _ := strconv.ParseInt(c.Param("msg_id"), 10, 64)
	var req struct{ Star bool `json:"star"` }
	c.ShouldBindJSON(&req)
	h.Service.StarMessage(msgID, req.Star)
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) PinMessage(c *gin.Context) {
	msgID, _ := strconv.ParseInt(c.Param("msg_id"), 10, 64)
	var req struct{ Pin bool `json:"pin"` }
	c.ShouldBindJSON(&req)
	h.Service.PinMessage(msgID, req.Pin)
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) ReactMessage(c *gin.Context) {
	userID := c.GetInt64("user_id")
	msgID, _ := strconv.ParseInt(c.Param("msg_id"), 10, 64)
	var req struct {
		Emoji string `json:"emoji"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Emoji) == "" {
		c.JSON(400, gin.H{"error": "emoji required"})
		return
	}
	reactions, err := h.Service.ReactMessage(userID, msgID, req.Emoji)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "reactions": reactions})
}

func (h *MessageHandler) EditMessage(c *gin.Context) {
	userID := c.GetInt64("user_id")
	msgID, _ := strconv.ParseInt(c.Param("msg_id"), 10, 64)
	var req struct{ Content string `json:"content"` }
	c.ShouldBindJSON(&req)
	h.Service.EditMessage(userID, msgID, req.Content)
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) DeleteMessage(c *gin.Context) {
	userID := c.GetInt64("user_id")
	msgID, _ := strconv.ParseInt(c.Param("msg_id"), 10, 64)
	h.Service.DeleteMessage(userID, msgID)
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) GetPinnedMessages(c *gin.Context) {
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	msgs, err := h.Service.GetPinnedMessages(convID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"messages": msgs})
}

func (h *MessageHandler) GetStarredMessages(c *gin.Context) {
	userID := c.GetInt64("user_id")
	msgs, err := h.Service.GetStarredMessages(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"messages": msgs})
}

func (h *MessageHandler) GetConversation(c *gin.Context) {
	userID := c.GetInt64("user_id")
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	conv, err := h.Service.GetConversation(convID, userID)
	if err != nil {
		c.JSON(404, gin.H{"error": "conversation not found"})
		return
	}
	c.JSON(200, gin.H{"conversation": conv})
}

func (h *MessageHandler) ClearConversation(c *gin.Context) {
	userID := c.GetInt64("user_id")
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	if err := h.Service.ClearChat(convID, userID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) HideConversation(c *gin.Context) {
	userID := c.GetInt64("user_id")
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	if err := h.Service.HideChat(convID, userID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// PurgeConversation deletes the entire chat for both people — messages,
// media files, everything. The next message starts the chat fresh.
func (h *MessageHandler) PurgeConversation(c *gin.Context) {
	userID := c.GetInt64("user_id")
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	if err := h.Service.PurgeChat(convID, userID); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) UpdateConversationSettings(c *gin.Context) {
	userID := c.GetInt64("user_id")
	convID, _ := strconv.ParseInt(c.Param("conv_id"), 10, 64)
	var settings map[string]interface{}
	c.ShouldBindJSON(&settings)
	if err := h.Service.UpdateConversationSettings(convID, userID, settings); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// Return the freshly-persisted settings so the client can apply them
	// without a second round trip.
	conv, err := h.Service.GetConversation(convID, userID)
	if err != nil {
		c.JSON(200, gin.H{"ok": true})
		return
	}
	c.JSON(200, gin.H{"ok": true, "conversation": conv})
}

func (h *MessageHandler) LogCall(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		ReceiverID int64  `json:"receiver_id"`
		CallType   string `json:"call_type"`
		Duration   int    `json:"duration"`
		EndedBy    string `json:"ended_by"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.CallType == "" {
		req.CallType = "voice"
	}
	if err := h.Service.LogCall(userID, req.ReceiverID, req.CallType, req.Duration, req.EndedBy); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (h *MessageHandler) GetCallLogs(c *gin.Context) {
	userID := c.GetInt64("user_id")
	logs, err := h.Service.GetCallLogs(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"call_logs": logs})
}

func (h *MessageHandler) SearchMessages(c *gin.Context) {
	userID := c.GetInt64("user_id")
	q := c.Query("q")
	if q == "" {
		c.JSON(200, gin.H{"messages": []map[string]interface{}{}})
		return
	}
	msgs, err := h.Service.SearchMessages(userID, q)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"messages": msgs})
}
