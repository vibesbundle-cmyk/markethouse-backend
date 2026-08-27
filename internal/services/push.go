package services

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type serviceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
}

type fcmTokenCache struct {
	token     string
	expiresAt time.Time
}

var (
	fcmCache      fcmTokenCache
	fcmCacheMu    sync.Mutex
	fcmSA         *serviceAccount
	fcmSALoadOnce sync.Once
)

func loadServiceAccount() *serviceAccount {
	fcmSALoadOnce.Do(func() {
		path := os.Getenv("FIREBASE_SERVICE_ACCOUNT")
		if path == "" {
			path = "firebase-service-account.json"
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Println("fcm: cannot read service account:", err)
			return
		}
		var sa serviceAccount
		if err := json.Unmarshal(data, &sa); err != nil {
			log.Println("fcm: invalid service account JSON:", err)
			return
		}
		fcmSA = &sa
	})
	return fcmSA
}

func getFCMAccessToken(sa *serviceAccount) (string, error) {
	fcmCacheMu.Lock()
	defer fcmCacheMu.Unlock()

	if fcmCache.token != "" && time.Now().Before(fcmCache.expiresAt.Add(-1*time.Minute)) {
		return fcmCache.token, nil
	}

	now := time.Now()
	claims := jwt.MapClaims{
		"iss":   sa.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)

	pemKey := sa.PrivateKey
	pemKey = strings.TrimSpace(pemKey)
	signed, err := token.SignedString(pemKey)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	resp, err := http.PostForm("https://oauth2.googleapis.com/token", map[string][]string{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {signed},
	})
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("token error: %s", result.Error)
	}

	_ = base64.StdEncoding // keep import for potential future use
	fcmCache.token = result.AccessToken
	fcmCache.expiresAt = now.Add(time.Duration(result.ExpiresIn) * time.Second)
	return result.AccessToken, nil
}

func SendPush(db *sql.DB, userID int64, title, body string, data map[string]string) {
	sa := loadServiceAccount()
	if sa == nil || db == nil {
		return
	}

	token, err := getFCMAccessToken(sa)
	if err != nil {
		log.Println("fcm: get token error:", err)
		return
	}

	rows, err := db.Query(`SELECT token FROM device_tokens WHERE user_id=$1`, userID)
	if err != nil {
		return
	}
	defer rows.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	for rows.Next() {
		var deviceToken string
		if rows.Scan(&deviceToken) != nil || deviceToken == "" {
			continue
		}

		msg := map[string]interface{}{
			"message": map[string]interface{}{
				"token": deviceToken,
				"notification": map[string]string{
					"title": title,
					"body":  body,
				},
				"data": data,
				"android": map[string]interface{}{
					"priority": "high",
				},
				"webpush": map[string]interface{}{
					"headers": map[string]string{
						"TTL": "86400",
					},
				},
			},
		}

		payload, _ := json.Marshal(msg)
		url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", sa.ProjectID)
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		if resp, e := client.Do(req); e == nil {
			resp.Body.Close()
		} else {
			log.Println("fcm send error:", e)
		}
	}
}
