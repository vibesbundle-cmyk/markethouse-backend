package handlers

import (
	"database/sql"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"markethouse/internal/services"
)

type NotificationHandler struct{ DB *sql.DB }

func (h *NotificationHandler) GetAll(c *gin.Context) {
	userID := c.GetInt64("user_id")
	rows, err := h.DB.Query(`
		SELECT n.id,n.type,n.title,COALESCE(n.body,''),n.entity_type,COALESCE(n.entity_id,0),n.is_read,n.created_at,
		       COALESCE(u.username,''),COALESCE(u.profile_photo,'')
		FROM notifications n LEFT JOIN users u ON u.id=n.actor_id
		WHERE n.user_id=$1 ORDER BY n.created_at DESC LIMIT 50`, userID)
	if err != nil { c.JSON(500,gin.H{"error":err.Error()}); return }
	defer rows.Close()
	var list []gin.H
	for rows.Next() {
		var id,eid int64; var ntype,title,body,etype,ca,uname,photo string; var read bool
		rows.Scan(&id,&ntype,&title,&body,&etype,&eid,&read,&ca,&uname,&photo)
		list = append(list, gin.H{"id":id,"type":ntype,"title":title,"body":body,"entity_type":etype,"entity_id":eid,"is_read":read,"created_at":ca,"actor_username":uname,"actor_photo":photo})
	}
	if list == nil { list = []gin.H{} }
	c.JSON(200, gin.H{"notifications":list})
}

func (h *NotificationHandler) MarkRead(c *gin.Context) {
	userID := c.GetInt64("user_id")
	h.DB.Exec(`UPDATE notifications SET is_read=true WHERE user_id=$1`, userID)
	c.JSON(200, gin.H{"ok":true})
}

func (h *NotificationHandler) UnreadCount(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var count int
	h.DB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id=$1 AND is_read=false`, userID).Scan(&count)
	c.JSON(200, gin.H{"unread": count})
}

func PushNotification(db *sql.DB, toUserID, actorID int64, ntype, title, body, entityType string, entityID int64) {
	if toUserID == 0 || toUserID == actorID {
		return
	}
	if !notifyAllowed(db, toUserID, ntype) {
		return
	}
	db.Exec(`INSERT INTO notifications(user_id,actor_id,type,title,body,entity_type,entity_id) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		toUserID, actorID, ntype, title, body, entityType, entityID)
	services.SendPush(db, toUserID, title, body, map[string]string{
		"type":        ntype,
		"entity_type": entityType,
		"entity_id":   strconv.FormatInt(entityID, 10),
		"actor_id":    strconv.FormatInt(actorID, 10),
	})
}

// userName is a tiny helper for building human-readable notification copy.
func userName(db *sql.DB, id int64) string {
	var n sql.NullString
	if db != nil {
		db.QueryRow(`SELECT COALESCE(NULLIF(username,''), NULLIF(full_name,'')) FROM users WHERE id=$1`, id).Scan(&n)
	}
	if n.Valid {
		return n.String
	}
	return "Someone"
}

// NotifyWithWS inserts a persistent notification row AND pushes it over the
// websocket hub so the recipient sees it instantly. It is a no-op when the
// recipient is the actor (you don't notify yourself) or when db is nil.
func NotifyWithWS(db *sql.DB, hub *services.Hub, toUserID, actorID int64, ntype, title, body, entityType string, entityID int64) {
	if toUserID == 0 || toUserID == actorID {
		return
	}
	if !notifyAllowed(db, toUserID, ntype) {
		return
	}
	if db != nil {
		var actor interface{}
		if actorID > 0 {
			actor = actorID
		}
		db.Exec(`INSERT INTO notifications(user_id,actor_id,type,title,body,entity_type,entity_id) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			toUserID, actor, ntype, title, body, entityType, entityID)
	}
	if hub != nil {
		hub.SendToUser(toUserID, gin.H{
			"type":        "notification",
			"notif_type":  ntype,
			"title":       title,
			"body":        body,
			"entity_type": entityType,
			"entity_id":   entityID,
			"actor_id":    actorID,
			"created_at":  time.Now().Format(time.RFC3339),
		})
	}
	services.SendPush(db, toUserID, title, body, map[string]string{
		"type":        ntype,
		"entity_type": entityType,
		"entity_id":   strconv.FormatInt(entityID, 10),
		"actor_id":    strconv.FormatInt(actorID, 10),
	})
}

// NotifPrefs is the per-user notification preference payload returned to / fetched from the client.
type NotifPrefs struct {
	Master            bool `json:"master"`
	CommunityMessages bool `json:"community_messages"`
	Wallet            bool `json:"wallet"`
	Likes             bool `json:"likes"`
	Comments          bool `json:"comments"`
	Reshares          bool `json:"reshares"`
	Views             bool `json:"views"`
}

// prefColumns maps a notification type to the toggle column that gates it.
// Types not listed here are only gated by the master switch.
var prefColumns = map[string]string{
	"community_message": "community_messages",
	"community_mention": "community_messages",
	"community_post":    "community_messages",
	"transaction":       "wallet",
	"like":              "likes",
	"comment":           "comments",
	"reshare":           "reshares",
	"view":              "views",
}

// notifyAllowed returns true when the recipient should receive a notification
// of the given type, honouring their stored preferences. Missing prefs row
// (or nil db) defaults to "allow everything".
func notifyAllowed(db *sql.DB, userID int64, ntype string) bool {
	if db == nil {
		return true
	}
	col, mapped := prefColumns[ntype]
	if !mapped {
		var master bool
		if err := db.QueryRow(`SELECT COALESCE(master,true) FROM notification_preferences WHERE user_id=$1`, userID).Scan(&master); err != nil {
			return true
		}
		return master
	}
	var master, colVal bool
	if err := db.QueryRow(`SELECT master, `+col+` FROM notification_preferences WHERE user_id=$1`, userID).Scan(&master, &colVal); err != nil {
		return true
	}
	if !master {
		return false
	}
	return colVal
}

func (h *NotificationHandler) GetPrefs(c *gin.Context) {
	userID := c.GetInt64("user_id")
	p := NotifPrefs{Master: true, CommunityMessages: true, Wallet: true, Likes: true, Comments: true, Reshares: true, Views: true}
	h.DB.QueryRow(`SELECT master, community_messages, wallet, likes, comments, reshares, views FROM notification_preferences WHERE user_id=$1`, userID).
		Scan(&p.Master, &p.CommunityMessages, &p.Wallet, &p.Likes, &p.Comments, &p.Reshares, &p.Views)
	c.JSON(200, p)
}

func (h *NotificationHandler) UpdatePrefs(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var p NotifPrefs
	c.ShouldBindJSON(&p)
	h.DB.Exec(`INSERT INTO notification_preferences(user_id, master, community_messages, wallet, likes, comments, reshares, views)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (user_id) DO UPDATE SET
			master=EXCLUDED.master, community_messages=EXCLUDED.community_messages, wallet=EXCLUDED.wallet,
			likes=EXCLUDED.likes, comments=EXCLUDED.comments, reshares=EXCLUDED.reshares, views=EXCLUDED.views`,
		userID, p.Master, p.CommunityMessages, p.Wallet, p.Likes, p.Comments, p.Reshares, p.Views)
	c.JSON(200, gin.H{"ok": true})
}

// RegisterDevice stores (upserts) an FCM/APNs push token for the calling user.
func (h *NotificationHandler) RegisterDevice(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Token == "" {
		c.JSON(400, gin.H{"error": "token required"})
		return
	}
	if req.Platform == "" {
		req.Platform = "android"
	}
	h.DB.Exec(`INSERT INTO device_tokens(user_id, token, platform) VALUES($1,$2,$3)
		ON CONFLICT (user_id, token) DO UPDATE SET platform=EXCLUDED.platform, updated_at=NOW()`,
		userID, req.Token, req.Platform)
	c.JSON(200, gin.H{"ok": true})
}
