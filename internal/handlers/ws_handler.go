package handlers

import (
	"database/sql"
	"encoding/json"
	"markethouse/internal/services"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// inboundWSMessage covers the small set of frame types clients push up
// the socket. Right now that's just live-location sharing during an
// active chat (a one-off "share my location" message instead goes
// through POST /message/send with message_type="location", so it's
// saved to history like any other message).
type inboundWSMessage struct {
	Type       string  `json:"type"`
	ReceiverID int64   `json:"receiver_id"`
	Lat        float64 `json:"lat"`
	Lng        float64 `json:"lng"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for dev
	},
}

type WSHandler struct {
	Hub *services.Hub
	DB  *sql.DB
}

func (h *WSHandler) HandleWS(c *gin.Context) {
	userID := c.GetInt64("user_id")

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	client := &services.Client{
		UserID: userID,
		Conn:   conn,
		Send:   make(chan interface{}),
	}

	h.Hub.Register <- client

	defer func() {
		h.Hub.Unregister <- client
		conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		h.Hub.Heartbeat(userID)

		var msg inboundWSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue // not JSON / not a frame we handle — ignore
		}

		switch msg.Type {
		case "live_location":
			// Ephemeral: relayed straight to the other person, never
			// written to the DB. Stops as soon as the sender closes
			// their "share live location" screen client-side.
			h.Hub.SendToUser(msg.ReceiverID, map[string]interface{}{
				"type":      "live_location",
				"sender_id": userID,
				"lat":       msg.Lat,
				"lng":       msg.Lng,
			})
		case "call_offer", "call_answer", "call_reject", "call_end":
			// Relay call signaling to the other party.
			payload := map[string]interface{}{
				"type":      msg.Type,
				"sender_id": userID,
			}
			// Copy extra fields from the raw JSON
			var extra map[string]interface{}
			json.Unmarshal(raw, &extra)
			if v, ok := extra["is_video"]; ok {
				payload["is_video"] = v
			}
			h.Hub.SendToUser(msg.ReceiverID, payload)
			// For call_offer, also send a push notification so the
			// receiver gets pinged even if they aren't on WS right now.
			if msg.Type == "call_offer" && h.DB != nil {
				var callerName string
				h.DB.QueryRow("SELECT COALESCE(full_name, username) FROM users WHERE id=$1", userID).Scan(&callerName)
				if callerName == "" {
					callerName = "Someone"
				}
				isVideo := false
				if v, ok := extra["is_video"]; ok {
					isVideo, _ = v.(bool)
				}
				mediaType := "voice"
				if isVideo {
					mediaType = "video"
				}
				services.SendPush(h.DB, msg.ReceiverID, "Incoming "+mediaType+" call",
					callerName+" is calling you", map[string]string{
						"type":      "call_offer",
						"sender_id": strconv.FormatInt(userID, 10),
					})
			}
		}
	}
}