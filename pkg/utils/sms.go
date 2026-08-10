package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

var termiiHTTP = &http.Client{Timeout: 15 * time.Second}

// TermiiEnabled reports whether the Termii SMS provider is configured.
func TermiiEnabled() bool { return os.Getenv("TERMII_API_KEY") != "" }

// SendSMS is the dev-mode fallback — it logs the code to the console. Real
// SMS delivery for verification codes uses RequestOTP, where Termii generates
// the PIN itself and sends it.
func SendSMS(phone string, message string) error {
	fmt.Println("SMS dev-mode: to", phone, "message:", message)
	return nil
}

// RequestOTP asks Termii to generate and SMS a numeric PIN, returning the
// pinId needed to verify it later. Termii sends the SMS for us.
func RequestOTP(phone string) (string, error) {
	from := os.Getenv("TERMII_SENDER_ID")
	if from == "" {
		from = "Termii"
	}
	payload := map[string]any{
		"api_key":          os.Getenv("TERMII_API_KEY"),
		"message_type":     "NUMERIC",
		"to":               normalizePhone(phone),
		"from":             from,
		"channel":          "generic",
		"pin_attempts":     5,
		"pin_time_to_live": 5,
		"pin_length":       6,
		"pin_type":         "NUMERIC",
		"pin_placeholder":  "<pin>",
		"message_text":     "Your Market House verification code is <pin>",
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequest(http.MethodPost,
		termiiBase()+"/sms/otp/send", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := termiiHTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("termii: %w", err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("termii: status %d: %s", resp.StatusCode, strings.TrimSpace(body))
	}

	var out struct {
		PinID   string `json:"pinId"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	if out.PinID == "" {
		return "", fmt.Errorf("termii: no pinId in response: %s", strings.TrimSpace(body))
	}
	return out.PinID, nil
}

// VerifyOTP checks a user-entered PIN against a Termii pinId.
func VerifyOTP(pinID, pin string) (bool, error) {
	payload := map[string]any{
		"api_key": os.Getenv("TERMII_API_KEY"),
		"pin_id":  pinID,
		"pin":     pin,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequest(http.MethodPost,
		termiiBase()+"/sms/otp/verify", bytes.NewReader(raw))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := termiiHTTP.Do(req)
	if err != nil {
		return false, fmt.Errorf("termii: %w", err)
	}
	defer resp.Body.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	body := buf.String()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("termii: status %d: %s", resp.StatusCode, strings.TrimSpace(body))
	}

	var out struct {
		Verified bool `json:"verified"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return out.Verified, nil
}

// normalizePhone converts "+2348012345678" or "08012345678" to the
// international form Termii expects: "2348012345678".
func normalizePhone(p string) string {
	p = strings.TrimSpace(p)
	p = strings.TrimPrefix(p, "+")
	if strings.HasPrefix(p, "0") {
		p = "234" + p[1:]
	}
	return p
}

func termiiBase() string {
	if dc := os.Getenv("TERMII_BASE_URL"); dc != "" {
		return dc
	}
	return "https://api.ng.termii.com/api"
}
