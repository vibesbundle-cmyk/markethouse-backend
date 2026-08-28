package handlers

import (
	"database/sql"
	"strconv"

	"github.com/gin-gonic/gin"
	"markethouse/internal/services"
)

type StatusHandler struct {
	DB  *sql.DB
	Hub *services.Hub
}

func (h *StatusHandler) GetFeed(c *gin.Context) {
	userID := c.GetInt64("user_id")
	// Visibility per status privacy setting:
	//   own posts | everyone ('all') | poster's followers | people the
	//   poster follows | a custom list of user ids (CSV in custom_ids)
	// Reshare origin rides along; when the ORIGINAL author hides credit
	// (hide_status_credit) the username is blanked → client shows anonymous.
	rows, err := h.DB.Query(`
		SELECT s.id,s.user_id,s.status_type,COALESCE(s.media_url,''),COALESCE(s.text_content,''),
		       COALESCE(s.bg_color,'#1DB954'),s.view_count,s.expires_at,s.created_at,
		       u.username,COALESCE(u.profile_photo,''),
		       EXISTS(SELECT 1 FROM status_views sv WHERE sv.status_id=s.id AND sv.viewer_id=$1) as viewed,
		       COALESCE(s.reshared_from_user_id,0),
		       COALESCE(s.reshared_from_id,0),
		       CASE WHEN COALESCE(ou.hide_status_credit,false) THEN '' ELSE COALESCE(s.reshared_from_username,'') END
		FROM statuses s JOIN users u ON u.id=s.user_id
		LEFT JOIN users ou ON ou.id=s.reshared_from_user_id
		WHERE s.expires_at > NOW()
		  AND (s.user_id=$1
		    OR s.privacy='all'
		    OR (s.privacy='followers' AND EXISTS(SELECT 1 FROM follows f WHERE f.following_id=s.user_id AND f.follower_id=$1))
		    OR (s.privacy='following' AND EXISTS(SELECT 1 FROM follows f WHERE f.follower_id=s.user_id AND f.following_id=$1))
		    OR (s.privacy='custom' AND COALESCE(s.custom_ids,'')<>'' AND CAST($1 AS TEXT)=ANY(string_to_array(s.custom_ids,','))))
		ORDER BY s.user_id, s.created_at DESC`, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []gin.H
	for rows.Next() {
		var id, uid, vc, rfUID, rfSID int64
		var st, mu, txt, bg, ea, ca, uname, photo, rfName string
		var viewed bool
		rows.Scan(&id, &uid, &st, &mu, &txt, &bg, &vc, &ea, &ca, &uname, &photo, &viewed, &rfUID, &rfSID, &rfName)
		list = append(list, gin.H{"id": id, "user_id": uid, "status_type": st, "media_url": mu, "text_content": txt, "bg_color": bg, "view_count": vc, "expires_at": ea, "created_at": ca, "username": uname, "profile_photo": photo, "viewed": viewed, "reshared_from_user_id": rfUID, "reshared_from_id": rfSID, "reshared_from_username": rfName})
	}
	if list == nil {
		list = []gin.H{}
	}
	c.JSON(200, gin.H{"statuses": list})
}

func (h *StatusHandler) Create(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Type         string `json:"type"`
		MediaURL     string `json:"media_url"`
		Text         string `json:"text_content"`
		BgColor      string `json:"bg_color"`
		Privacy      string `json:"privacy"`
		CustomIDs    string `json:"custom_ids"`
		ResharedFrom int64  `json:"reshared_from"`
	}
	c.ShouldBindJSON(&req)
	if req.Type == "" {
		req.Type = "text"
	}
	if req.Privacy == "" {
		req.Privacy = "followers"
	}
	if req.BgColor == "" {
		req.BgColor = "#1DB954"
	}
	if req.CustomIDs == "" {
		req.CustomIDs = ""
	}
	// Reshare: snapshot the original status's author id + username so the
	// attribution survives the original expiring/deleting.
	var rfID, rfUID int64
	var rfName string
	if req.ResharedFrom > 0 {
		err := h.DB.QueryRow(`
			SELECT s.id, s.user_id, u.username
			FROM statuses s JOIN users u ON u.id=s.user_id
			WHERE s.id=$1`, req.ResharedFrom).Scan(&rfID, &rfUID, &rfName)
		if err != nil {
			rfID, rfUID, rfName = 0, 0, ""
		}
	}
	var id int64
	err := h.DB.QueryRow(`INSERT INTO statuses(user_id,status_type,media_url,text_content,bg_color,privacy,custom_ids,reshared_from_id,reshared_from_user_id,reshared_from_username) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,0),NULLIF($9,0),$10) RETURNING id`,
		userID, req.Type, req.MediaURL, req.Text, req.BgColor, req.Privacy, req.CustomIDs, rfID, rfUID, rfName).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// Notify the original author when their status is reshared.
	if rfUID > 0 && rfUID != userID {
		NotifyWithWS(h.DB, h.Hub, rfUID, userID, "reshare",
			userName(h.DB, userID)+" reshared your status", "", "status", id)
	}
	c.JSON(200, gin.H{"id": id})
}

// View records one view per account: repeat views by the same user never
// bump view_count again, and your own view of your status doesn't count.
func (h *StatusHandler) View(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var vid int64
	err := h.DB.QueryRow(`
		INSERT INTO status_views(status_id,viewer_id)
		SELECT $1,$2 WHERE NOT EXISTS(SELECT 1 FROM statuses WHERE id=$1 AND user_id=$2)
		ON CONFLICT DO NOTHING RETURNING viewer_id`, statusID, userID).Scan(&vid)
	if err == nil {
		h.DB.Exec(`UPDATE statuses SET view_count=view_count+1 WHERE id=$1`, statusID)
	}
	c.JSON(200, gin.H{"ok": true})
}

// Views lists who watched a status — one row per account, newest first.
// Only the owner can see it.
func (h *StatusHandler) Views(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var owner int64
	if err := h.DB.QueryRow(`SELECT user_id FROM statuses WHERE id=$1`, statusID).Scan(&owner); err != nil || owner != userID {
		c.JSON(403, gin.H{"error": "forbidden"})
		return
	}
	rows, err := h.DB.Query(`
		SELECT DISTINCT ON (u.id)
		       u.id, u.username, COALESCE(u.profile_photo,''), sv.viewed_at
		FROM status_views sv JOIN users u ON u.id=sv.viewer_id
		WHERE sv.status_id=$1
		ORDER BY u.id, sv.viewed_at DESC`, statusID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []gin.H
	for rows.Next() {
		var uid int64
		var uname, photo, at string
		rows.Scan(&uid, &uname, &photo, &at)
		list = append(list, gin.H{"user_id": uid, "username": uname, "profile_photo": photo, "viewed_at": at})
	}
	if list == nil {
		list = []gin.H{}
	}
	c.JSON(200, gin.H{"viewers": list})
}

func (h *StatusHandler) React(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Reaction string `json:"reaction"`
	}
	c.ShouldBindJSON(&req)
	h.DB.Exec(`INSERT INTO status_reactions(status_id,user_id,reaction) VALUES($1,$2,$3) ON CONFLICT(status_id,user_id) DO UPDATE SET reaction=$3`, statusID, userID, req.Reaction)

	// Notify the status owner (skip self-likes)
	var ownerID int64
	h.DB.QueryRow(`SELECT user_id FROM statuses WHERE id=$1`, statusID).Scan(&ownerID)
	if ownerID > 0 && ownerID != userID {
		actor := userName(h.DB, userID)
		NotifyWithWS(h.DB, h.Hub, ownerID, userID, "status_reaction",
			actor+" reacted "+req.Reaction+" to your status", "", "status", statusID)
	}

	c.JSON(200, gin.H{"ok": true})
}

func (h *StatusHandler) Delete(c *gin.Context) {
	userID := c.GetInt64("user_id")
	statusID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	h.DB.Exec(`DELETE FROM statuses WHERE id=$1 AND user_id=$2`, statusID, userID)
	c.JSON(200, gin.H{"ok": true})
}
