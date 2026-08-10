package handlers

import (
	"database/sql"
	"github.com/gin-gonic/gin"
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

func PushNotification(db *sql.DB, toUserID, actorID int64, ntype, title, body, entityType string, entityID int64) {
	db.Exec(`INSERT INTO notifications(user_id,actor_id,type,title,body,entity_type,entity_id) VALUES($1,$2,$3,$4,$5,$6,$7)`,
		toUserID, actorID, ntype, title, body, entityType, entityID)
}
