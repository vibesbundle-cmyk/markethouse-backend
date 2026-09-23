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
		case "call_offer", "call_answer", "call_reject", "call_end", "call_ice":
			// Relay call signaling to the other party. The SDP offer/answer
			// and ICE candidates ride along as extra JSON fields forward.
			payload := map[string]interface{}{
				"type":      msg.Type,
				"sender_id": userID,
			}
			// Copy extra fields from the raw JSON
			var extra map[string]interface{}
			json.Unmarshal(raw, &extra)
			for _, k := range []string{"is_video", "sdp", "ice_candidate", "candidate"} {
				if v, ok := extra[k]; ok {
					payload[k] = v
				}
			}
			// DEBUG: trace call signaling delivery
			if h.Hub != nil {
				online := h.Hub.IsOnline(msg.ReceiverID)
				sdpLen := 0
				if s, ok := payload["sdp"].(map[string]interface{}); ok {
					if str, ok2 := s["sdp"].(string); ok2 {
						sdpLen = len(str)
					}
				}
				println("[WS] call type=", msg.Type, " from=", userID,
					" to=", msg.ReceiverID, " recvOnline=", online)
				if msg.Type == "call_offer" || msg.Type == "call_answer" {
					println("[WS] ", msg.Type, " sdpLen=", sdpLen)
				}
			}
			// Include caller identity on call_offer so the receiver's incoming-
			// call UI can show their name + photo without doing a follow-up
			// lookup (avoids the "Unknown" placeholder when the caller isn't
			// already in the receiver's conversation list).
			if msg.Type == "call_offer" && h.DB != nil {
				var fullName, username, photo string
				h.DB.QueryRow(`SELECT COALESCE(full_name,''), COALESCE(username,''), COALESCE(profile_photo,'')
					FROM users WHERE id=$1`, userID).Scan(&fullName, &username, &photo)
				payload["sender_full_name"] = fullName
				payload["sender_username"]  = username
				payload["sender_photo"]     = photo
			}
			h.Hub.SendToUser(msg.ReceiverID, payload)
			// For call_offer, also send a push notification so the
			// receiver gets pinged even if they aren't on WS right now.
			// Run async — FCM HTTP latency must not stall the WS read loop
			// (which also relays ICE/answer frames for in-progress calls).
			if msg.Type == "call_offer" && h.DB != nil {
				db := h.DB
				receiverID := msg.ReceiverID
				fromUserID := userID
				isVideo := false
				if v, ok := extra["is_video"]; ok {
					isVideo, _ = v.(bool)
				}
				go func() {
					var callerName string
					db.QueryRow("SELECT COALESCE(full_name, username) FROM users WHERE id=$1", fromUserID).Scan(&callerName)
					if callerName == "" {
						callerName = "Someone"
					}
					mediaType := "voice"
					if isVideo {
						mediaType = "video"
					}
					services.SendPush(db, receiverID, "Incoming "+mediaType+" call",
						callerName+" is calling you", map[string]string{
							"type":      "call_offer",
							"sender_id": strconv.FormatInt(fromUserID, 10),
							"is_video":  strconv.FormatBool(isVideo),
						})
				}()
			}
		}
	}
}