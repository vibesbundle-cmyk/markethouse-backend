package services

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// onlineTTL is how long a presence key lives in Redis before it's
// considered stale. The client side should ping at a shorter interval
// than this (see ws_handler.go) to keep it refreshed while connected.
const onlineTTL = 45 * time.Second

type Client struct {
	UserID int64
	Conn   *websocket.Conn
	Send   chan interface{}

	// writeMu guards concurrent writes to Conn — gorilla/websocket does
	// not allow concurrent writers on the same connection, and without
	// this, a message pushed from an HTTP request goroutine could race
	// with another push and corrupt/drop frames.
	writeMu sync.Mutex
}

// WriteJSON is a safe wrapper that serializes writes to this client's
// underlying websocket connection.
func (c *Client) WriteJSON(v interface{}) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.Conn.WriteJSON(v)
}

type Hub struct {
	Clients    map[int64][]*Client
	Register   chan *Client
	Unregister chan *Client
	mu         sync.Mutex

	// Redis is optional — if nil, presence falls back to in-memory only
	// (still correct for a single server instance, just not shared
	// across multiple backend processes).
	Redis *redis.Client
}

func NewHub(redisClient *redis.Client) *Hub {
	return &Hub{
		Clients:    make(map[int64][]*Client),
		Register:   make(chan *Client),
		Unregister: make(chan *Client),
		Redis:      redisClient,
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.Clients[client.UserID] = append(h.Clients[client.UserID], client)
			h.mu.Unlock()
			h.markOnline(client.UserID)
			h.broadcastPresence(client.UserID, true)
		case client := <-h.Unregister:
			h.mu.Lock()
			stillOnline := false
			if clients, ok := h.Clients[client.UserID]; ok {
				for i, c := range clients {
					if c == client {
						h.Clients[client.UserID] = append(clients[:i], clients[i+1:]...)
						break
					}
				}
				stillOnline = len(h.Clients[client.UserID]) > 0
				if len(h.Clients[client.UserID]) == 0 {
					delete(h.Clients, client.UserID)
				}
			}
			h.mu.Unlock()
			if !stillOnline {
				h.markOffline(client.UserID)
				h.broadcastPresence(client.UserID, false)
			}
		}
	}
}

// Broadcast sends a payload to every connected client.
// Used for global events like post likes/comments visible on the home feed.
func (h *Hub) Broadcast(message interface{}) {
	h.mu.Lock()
	all := make([]*Client, 0)
	for _, clients := range h.Clients {
		all = append(all, clients...)
	}
	h.mu.Unlock()
	for _, c := range all {
		_ = c.WriteJSON(message)
	}
}

// SendToUser delivers a payload to every connection the user currently
// has open (they may be logged in on multiple devices/tabs).
func (h *Hub) SendToUser(userID int64, message interface{}) {
	h.mu.Lock()
	clients := append([]*Client(nil), h.Clients[userID]...)
	h.mu.Unlock()
	for _, c := range clients {
		_ = c.WriteJSON(message)
	}
}

// Heartbeat refreshes the TTL on a user's online presence. Call this
// whenever a ping/pong or inbound frame is received from the client.
func (h *Hub) Heartbeat(userID int64) {
	h.markOnline(userID)
}

func (h *Hub) markOnline(userID int64) {
	if h.Redis != nil {
		_ = h.Redis.Set(context.Background(), onlineKey(userID), "1", onlineTTL).Err()
	}
}

func (h *Hub) markOffline(userID int64) {
	if h.Redis != nil {
		_ = h.Redis.Del(context.Background(), onlineKey(userID)).Err()
	}
}

// IsOnline checks presence — Redis first (works across multiple backend
// instances), falling back to the in-memory hub if Redis isn't configured.
func (h *Hub) IsOnline(userID int64) bool {
	if h.Redis != nil {
		n, err := h.Redis.Exists(context.Background(), onlineKey(userID)).Result()
		if err == nil {
			return n > 0
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.Clients[userID]) > 0
}

func (h *Hub) broadcastPresence(userID int64, online bool) {
	h.mu.Lock()
	allUserIDs := make([]int64, 0, len(h.Clients))
	for uid := range h.Clients {
		allUserIDs = append(allUserIDs, uid)
	}
	h.mu.Unlock()

	payload := map[string]interface{}{
		"type":    "presence",
		"user_id": userID,
		"online":  online,
	}
	for _, uid := range allUserIDs {
		if uid == userID {
			continue
		}
		h.SendToUser(uid, payload)
	}
}

func onlineKey(userID int64) string {
	return "online:" + intToStr(userID)
}

func intToStr(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
