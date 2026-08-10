package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// SendEmail delivers a verification email. When BREVO_API_KEY is set the
// message goes through Brevo's transactional API (real delivery, no domain
// needed). Otherwise it falls back to SMTP (MailHog on localhost:1025 for
// local dev).
func SendEmail(to string, body string) error {
	if key := os.Getenv("BREVO_API_KEY"); key != "" {
		return sendBrevo(key, to, body)
	}
	return sendSMTP(to, body)
}

// ---------------- BREVO ----------------
func sendBrevo(apiKey, to, body string) error {
	sender := os.Getenv("BREVO_SENDER")
	name, email := "", strings.TrimSpace(sender)
	if i := strings.IndexByte(sender, '<'); i >= 0 {
		if j := strings.IndexByte(sender[i:], '>'); j > 0 {
			name = strings.TrimSpace(sender[:i])
			email = strings.TrimSpace(sender[i+1 : i+j])
		}
	}
	if email == "" {
		email = "noreply@markethouse.app"
	}

	payload := struct {
		Sender      map[string]string   `json:"sender"`
		To          []map[string]string `json:"to"`
		Subject     string              `json:"subject"`
		HTMLContent string              `json:"htmlContent"`
	}{
		Sender:  map[string]string{"name": name, "email": email},
		To:      []map[string]string{{"email": to}},
		Subject: "Market House Verification Code",
		HTMLContent: `<!DOCTYPE html><html><body style="margin:0;background:#f4f4f5;font-family:-apple-system,'Segoe UI',Roboto,Arial,sans-serif">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:32px 0"><tr><td align="center">
<table role="presentation" width="420" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:16px;overflow:hidden">
<tr><td style="background:#16a34a;height:6px"></td></tr>
<tr><td style="padding:28px 28px 8px">
<div style="font-size:20px;font-weight:800;color:#111827">Market House</div>
<div style="font-size:13px;color:#6b7280;margin-top:2px">Your verification code</div>
</td></tr>
<tr><td style="padding:12px 28px">
<div style="font-size:13px;color:#374151;line-height:1.6">` + body + `</div>
<div style="font-size:12px;color:#9ca3af;margin-top:16px">If you didn't request this, you can ignore this email.</div>
</td></tr></table>
</td></tr></table></body></html>`,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost,
		"https://api.brevo.com/v3/smtp/email", bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("api-key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("brevo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		return fmt.Errorf("brevo: status %d: %s", resp.StatusCode, strings.TrimSpace(buf.String()))
	}
	return nil
}

// ---------------- SMTP (MailHog dev fallback) ----------------
func sendSMTP(to string, body string) error {
	from := os.Getenv("SMTP_FROM")
	if from == "" {
		from = "noreply@markethouse.app"
	}

	host := os.Getenv("SMTP_HOST")
	if host == "" {
		host = "localhost:1025" // MailHog for dev
	}

	var auth smtp.Auth
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	if smtpUser != "" && smtpPass != "" {
		// Extract host without port for PLAIN auth
		smtpHost := host
		if idx := strings.LastIndexByte(host, ':'); idx > 0 {
			smtpHost = host[:idx]
		}
		auth = smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	}

	msg := "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: Market House Verification Code\r\n" +
		"Content-Type: text/plain; charset=UTF-8\r\n\r\n" +
		body

	err := smtp.SendMail(host, auth, from, []string{to}, []byte(msg))
	if err != nil {
		fmt.Println("Email error:", err)
		return err
	}

	return nil
}
