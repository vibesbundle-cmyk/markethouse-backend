package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

// SendPush delivers a Firebase Cloud Messaging notification to every device
// registered for userID. It is intentionally a no-op when FCM_SERVER_KEY is
// unset (e.g. local dev), so the rest of the app runs without Firebase.
func SendPush(db *sql.DB, userID int64, title, body string, data map[string]string) {
	key := os.Getenv("FCM_SERVER_KEY")
	if key == "" || db == nil {
		return
	}
	rows, err := db.Query(`SELECT token FROM device_tokens WHERE user_id=$1`, userID)
	if err != nil {
		return
	}
	defer rows.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	for rows.Next() {
		var token string
		if rows.Scan(&token) != nil || token == "" {
			continue
		}
		payload, _ := json.Marshal(map[string]interface{}{
			"to":           token,
			"priority":     "high",
			"notification": map[string]string{"title": title, "body": body},
			"data":         data,
		})
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			"https://fcm.googleapis.com/fcm/send", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "key="+key)
		if resp, e := client.Do(req); e == nil {
			resp.Body.Close()
		} else {
			log.Println("fcm send error:", e)
		}
	}
}
