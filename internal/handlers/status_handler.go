package handlers

import (
	"database/sql"
	"strconv"
	"github.com/gin-gonic/gin"
)

type StatusHandler struct{ DB *sql.DB }

func (h *StatusHandler) GetFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	rows, err := h.DB.Query(`
		SELECT s.id,s.user_id,s.status_type,COALESCE(s.media_url,''),COALESCE(s.text_content,''),
		       COALESCE(s.bg_color,'#1DB954'),s.view_count,s.expires_at,s.created_at,
		       u.username,COALESCE(u.profile_photo,''),
		       EXISTS(SELECT 1 FROM status_views sv WHERE sv.status_id=s.id AND sv.viewer_id=$1) as viewed
		FROM statuses s JOIN users u ON u.id=s.user_id
		WHERE s.expires_at > NOW()
		  AND (s.user_id=$1
		    OR s.user_id IN (SELECT following_id FROM follows WHERE follower_id=$1))
		ORDER BY s.user_id, s.created_at DESC`, userID)
	if err != nil { c.JSON(500,gin.H{"error":err.Error()}); return }
	defer rows.Close()
	var list []gin.H
	for rows.Next() {
		var id,uid,vc int64; var st,mu,txt,bg,ea,ca,uname,photo string; var viewed bool
		rows.Scan(&id,&uid,&st,&mu,&txt,&bg,&vc,&ea,&ca,&uname,&photo,&viewed)
		list = append(list, gin.H{"id":id,"user_id":uid,"status_type":st,"media_url":mu,"text_content":txt,"bg_color":bg,"view_count":vc,"expires_at":ea,"created_at":ca,"username":uname,"profile_photo":photo,"viewed":viewed})
	}
	if list == nil { list = []gin.H{} }
	c.JSON(200,gin.H{"statuses":list})
}

func (h *StatusHandler) Create(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Type    string `json:"type"`
		MediaURL string `json:"media_url"`
		Text    string `json:"text_content"`
		BgColor string `json:"bg_color"`
		Privacy string `json:"privacy"`
	}
	c.ShouldBindJSON(&req)
	if req.Type == "" { req.Type = "text" }
	if req.Privacy == "" { req.Privacy = "followers" }
	if req.BgColor == "" { req.BgColor = "#1DB954" }
	var id int64
	err := h.DB.QueryRow(`INSERT INTO statuses(user_id,status_type,media_url,text_content,bg_color,privacy) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`,
		userID,req.Type,req.MediaURL,req.Text,req.BgColor,req.Privacy).Scan(&id)
	if err != nil { c.JSON(500,gin.H{"error":err.Error()}); return }
	c.JSON(200,gin.H{"id":id})
}

func (h *StatusHandler) View(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec(`INSERT INTO status_views(status_id,viewer_id) VALUES($1,$2) ON CONFLICT DO NOTHING`,statusID,userID)
	h.DB.Exec(`UPDATE statuses SET view_count=view_count+1 WHERE id=$1`,statusID)
	c.JSON(200,gin.H{"ok":true})
}

func (h *StatusHandler) React(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Reaction string `json:"reaction"` }
	c.ShouldBindJSON(&req)
	h.DB.Exec(`INSERT INTO status_reactions(status_id,user_id,reaction) VALUES($1,$2,$3) ON CONFLICT(status_id,user_id) DO UPDATE SET reaction=$3`,statusID,userID,req.Reaction)
	c.JSON(200,gin.H{"ok":true})
}

func (h *StatusHandler) Delete(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec(`DELETE FROM statuses WHERE id=$1 AND user_id=$2`,statusID,userID)
	c.JSON(200,gin.H{"ok":true})
}
