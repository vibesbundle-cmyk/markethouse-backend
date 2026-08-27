package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"markethouse/internal/models"
	"markethouse/internal/services"

	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	Service *services.AuthService
	Hub     *services.Hub
	DB      *sql.DB
}

// ---------------- SIGNUP ----------------
func (h *AuthHandler) Signup(c *gin.Context) {

	// The body is read once and decoded twice: once into the User model,
	// once as a raw map to pick up the optional "ref" referral code.
	raw, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	var user models.User
	if err := json.Unmarshal(raw, &user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var probe map[string]any
	_ = json.Unmarshal(raw, &probe)
	ref := ""
	if s, ok := probe["ref"].(string); ok {
		ref = strings.TrimSpace(s)
	}

	if err := h.Service.Signup(user); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Link the invite now; the referrer gets their points only after the
	// invitee verifies their email (see VerifyOTP).
	if ref != "" && h.DB != nil {
		h.DB.Exec(`UPDATE users SET referred_by=(SELECT id FROM users WHERE referral_code=$1)
			WHERE email=$2 AND referred_by IS NULL`, strings.ToUpper(ref), strings.ToLower(user.Email))
	}

	c.JSON(http.StatusOK, gin.H{"message": "user created"})
}

// ---------------- VERIFY OTP ----------------
func (h *AuthHandler) VerifyOTP(c *gin.Context) {

	var req struct {
		Email string `json:"email"`
		OTP   string `json:"otp"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Service.VerifyOTP(req.Email, req.OTP); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	h.rewardReferral(strings.ToLower(strings.TrimSpace(req.Email)))

	c.JSON(http.StatusOK, gin.H{"message": "verified"})
}

// rewardReferral pays the referrer +25 reputation the first time an invited
// user verifies. Runs best-effort — never blocks or fails the verification.
func (h *AuthHandler) rewardReferral(email string) {
	if h.DB == nil || email == "" {
		return
	}
	var uid, refBy int64
	var rewarded bool
	err := h.DB.QueryRow(`SELECT id, COALESCE(referred_by,0), ref_rewarded
		FROM users WHERE email=$1`, email).Scan(&uid, &refBy, &rewarded)
	if err != nil || refBy == 0 || rewarded {
		return
	}
	h.DB.Exec(`UPDATE users SET ref_rewarded=true WHERE id=$1`, uid)
	if _, err := h.DB.Exec(`UPDATE users SET reputation=reputation+25 WHERE id=$1`, refBy); err != nil {
		return
	}
	PushNotification(h.DB, refBy, uid, "referral",
		"Invite bonus: +25 reputation", "A friend you invited just joined MarketHouse",
		"user", refBy)
	if h.Hub != nil {
		h.Hub.SendToUser(refBy, gin.H{
			"type": "notification", "notif_type": "referral",
			"title": "Invite bonus: +25 reputation",
			"body":  "A friend you invited just joined MarketHouse",
		})
	}
}

// GetReferral returns (generating on first use) the caller's invite code and
// how many verified friends they've brought in.
func (h *AuthHandler) GetReferral(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var code sql.NullString
	h.DB.QueryRow(`SELECT referral_code FROM users WHERE id=$1`, userID).Scan(&code)
	if !code.Valid || code.String == "" {
		for i := 0; i < 5; i++ {
			gen := randomCode(8)
			var clash int
			h.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE referral_code=$1`, gen).Scan(&clash)
			if clash == 0 {
				if _, err := h.DB.Exec(`UPDATE users SET referral_code=$1 WHERE id=$2 AND (referral_code IS NULL OR referral_code='')`,
					gen, userID); err == nil {
					code = sql.NullString{String: gen, Valid: true}
					break
				}
			}
		}
	}
	var invited int
	h.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE referred_by=$1 AND ref_rewarded=true`, userID).Scan(&invited)
	c.JSON(200, gin.H{"code": code.String, "invited": invited, "bonus_per_invite": 25})
}

var codeRng = rand.New(rand.NewSource(time.Now().UnixNano()))

func randomCode(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[codeRng.Intn(len(alphabet))]
	}
	return string(b)
}

// ---------------- LOGIN ----------------
func (h *AuthHandler) Login(c *gin.Context) {

	var req struct {
		Identifier string `json:"identifier"`
		Password   string `json:"password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	res, err := h.Service.Login(req.Identifier, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, res)
}

// ---------------- PROFILE ----------------

// ---------------- REFRESH ----------------
func (h *AuthHandler) Refresh(c *gin.Context) {

	var req struct {
		RefreshToken string `json:"refresh_token"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	token, err := h.Service.RefreshToken(req.RefreshToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"access_token": token})
}

// ---------------- UPLOAD PROFILE ----------------
func (h *AuthHandler) UploadImage(c *gin.Context) {

	userID := c.GetInt64("user_id")

	file, err := c.FormFile("file")
	if err != nil {
		log.Printf("[UPLOAD] failed to read form file for user %d: %v", userID, err)
		c.JSON(400, gin.H{"error": "file required"})
		return
	}

	uploadType := strings.TrimPrefix(c.FullPath(), "/upload/") // profile or header
	log.Printf("[UPLOAD] user %d uploading %s (size=%d)", userID, uploadType, file.Size)

	url, err := h.Service.UploadImage(userID, file, uploadType)
	if err != nil {
		log.Printf("[UPLOAD] error for user %d type=%s: %v", userID, uploadType, err)
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[UPLOAD] success user %d type=%s url=%s", userID, uploadType, url)
	if h.Hub != nil {
		h.Hub.SendToUser(userID, map[string]interface{}{
			"type": "profile_updated", "user_id": userID, "upload_type": uploadType, "url": url,
		})
	}
	c.JSON(200, gin.H{"url": url})
}

func (h *AuthHandler) CheckUsername(c *gin.Context) {

	username := c.Query("username")
	if username == "" {
		c.JSON(400, gin.H{"error": "username required"})
		return
	}

	exists := h.Service.UsernameExists(username)

	c.JSON(200, gin.H{
		"available": !exists,
	})
}
func (h *AuthHandler) UpdateProfile(c *gin.Context) {

	userID := c.GetInt64("user_id")

	var req struct {
		FullName         string   `json:"full_name"`
		Username         string   `json:"username"`
		Bio              string   `json:"bio"`
		AccountType      string   `json:"account_type"`
		BusinessCategory string   `json:"business_category"`
		BusinessName     string   `json:"business_name"`
		BusinessDesc     string   `json:"business_desc"`
		BusinessPhone    string   `json:"business_phone"`
		BusinessEmail    string   `json:"business_email"`
		BusinessWebsite  string   `json:"business_website"`
		BusinessAddress  string   `json:"business_address"`
		BusinessCountry  string   `json:"business_country"`
		BusinessState    string   `json:"business_state"`
		BusinessCity     string   `json:"business_city"`
		SellingTypes     []string `json:"selling_types"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[UpdateProfile] bind error for user=%d: %v", userID, err)
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	err := h.Service.UpdateProfile(userID, req.FullName, req.Username, req.Bio, models.BusinessUpdate{
		AccountType:      req.AccountType,
		BusinessCategory: req.BusinessCategory,
		BusinessName:     req.BusinessName,
		BusinessDesc:     req.BusinessDesc,
		BusinessPhone:    req.BusinessPhone,
		BusinessEmail:    req.BusinessEmail,
		BusinessWebsite:  req.BusinessWebsite,
		BusinessAddress:  req.BusinessAddress,
		BusinessCountry:  req.BusinessCountry,
		BusinessState:    req.BusinessState,
		BusinessCity:     req.BusinessCity,
		SellingTypes:     req.SellingTypes,
	})
	if err != nil {
		log.Printf("[UpdateProfile] service error for user=%d: %v", userID, err)
	}
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "profile updated"})
}
// ---------------- STATUS RESHARE CREDIT ----------------
func (h *AuthHandler) GetHideStatusCredit(c *gin.Context) {
	userID := c.GetInt64("user_id")
	hide, err := h.Service.HideStatusCredit(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"hide": hide})
}

func (h *AuthHandler) SetHideStatusCredit(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Hide bool `json:"hide"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := h.Service.SetHideStatusCredit(userID, req.Hide); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (h *AuthHandler) VerifyPhone(c *gin.Context) {

	var req struct {
		Mobile string `json:"mobile"`
		OTP    string `json:"otp"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := h.Service.VerifyPhoneOTP(req.Mobile, req.OTP); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "phone verified"})
}
func (h *AuthHandler) ResendEmailOTP(c *gin.Context) {

	var req struct {
		Email string `json:"email"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	err := h.Service.ResendEmailOTP(req.Email)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "OTP sent"})
}
func (h *AuthHandler) ResendPhoneOTP(c *gin.Context) {

	var req struct {
		Mobile string `json:"mobile"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	err := h.Service.ResendPhoneOTP(req.Mobile)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "OTP sent"})
}

func (h *AuthHandler) GetPublicProfile(c *gin.Context) {

	username := c.Param("username")

	// viewerID is 0 for unauthenticated; authenticated version is at /auth/user/:username
	viewerID := int64(0)
	if id, exists := c.Get("user_id"); exists {
		viewerID = id.(int64)
	}

	user, err := h.Service.GetUserByUsername(username, viewerID)
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})
		return
	}

	c.JSON(200, gin.H{
		"user": user,
	})
}
func (h *AuthHandler) Profile(c *gin.Context) {
	userID := c.GetInt64("user_id")

	user, err := h.Service.GetProfile(userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("[PROFILE] user not found for id=%d", userID)
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}

		log.Printf("[PROFILE] unexpected error for id=%d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"user": user})
}

func (h *AuthHandler) ForgotPassword(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.Service.ForgotPassword(req.Email); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "OTP sent"})
}

func (h *AuthHandler) ResetPassword(c *gin.Context) {
	var req struct {
		Email       string `json:"email" binding:"required,email"`
		OTP         string `json:"otp" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=8"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Ensure you pass context if your service requires it
	ctx := c.Request.Context()                                                                // This 'ctx' is now used
	if err := h.Service.ResetPassword(ctx, req.Email, req.OTP, req.NewPassword); err != nil { // Pass ctx to the service
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "password reset successfully"})
}

// UploadMedia handles generic file uploads for chat, community posts, and
// statuses. Route: POST /upload/media?type=chat|community|status
// Returns: {"url": "..."}
//
// This now goes through the same h.Service.Storage.Upload() path used by
// profile/header/commerce uploads, instead of hand-building the URL from
// c.Request.Host. Building it from the request Host previously meant a
// chat/community/status image's URL depended on whatever host/port the
// client happened to connect through (which can differ from the app's
// configured BASE_URL behind a proxy, tunnel, or different network path),
// so the image would fail to load even though the upload itself succeeded.
// Routing through Storage keeps every upload type consistent.
func (h *AuthHandler) UploadMedia(c *gin.Context) {
	userID := c.GetInt64("user_id")
	mediaType := c.DefaultQuery("type", "chat") // chat | community | status

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "file required"})
		return
	}

	filename := strconv.FormatInt(userID, 10) + "_" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	// Preserve original extension
	if idx := strings.LastIndex(file.Filename, "."); idx >= 0 {
		filename += file.Filename[idx:]
	}

	url, err := h.Service.Storage.Upload(file, mediaType, filename)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"url": url})
}

// UpdateLocation lets the logged-in user push their current GPS
// coordinates (from OpenStreetMap/geolocator on the client) so they show
// up in the "nearby" feed/marketplace and can be shared in chat.
// Body: { "latitude": 6.5244, "longitude": 3.3792, "lga": "Ikeja", "state": "Lagos" }
func (h *AuthHandler) UpdateLocation(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		Latitude     float64 `json:"latitude"`
		Longitude    float64 `json:"longitude"`
		LGA          string  `json:"lga"`
		State        string  `json:"state"`
		LocationText string  `json:"location_text"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := h.Service.UpdateLocation(userID, req.Latitude, req.Longitude, req.LGA, req.State, req.LocationText); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "location updated"})
}
