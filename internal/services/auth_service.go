package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"mime/multipart"
	"net/mail"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"markethouse/internal/models"
	"markethouse/internal/repository"
	"markethouse/internal/storage"
	"markethouse/pkg/utils"

	"github.com/redis/go-redis/v9"
)

var usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,20}$`)

func isValidUsername(u string) bool {
	return usernameRegex.MatchString(u)
}

var bgCtx = context.Background()

type AuthService struct {
	Repo    *repository.AuthRepo
	Redis   *redis.Client
	Storage storage.Storage
}

type UserResponse struct {
	ID           int64  `json:"id"`
	Email        string `json:"email"`
	Mobile       string `json:"mobile"`
	FullName     string `json:"full_name"`
	ProfilePhoto string `json:"profile_photo"`
	HeaderPhoto  string `json:"header_photo"`
	Bio          string `json:"bio"`
	Username     string `json:"username"`
	Posts        int64  `json:"posts"`
	Following    int64  `json:"following"`
	Followers    int64  `json:"followers"`
	IsFollowing  bool   `json:"is_following"`
	AccountType  string `json:"account_type"`
	IsVerified   bool   `json:"is_verified"`
	Reputation   int    `json:"reputation"`
	Badges       []string `json:"badges"`

	// Business profile details — populated when AccountType == "business"
	BusinessName     string `json:"business_name,omitempty"`
	BusinessCategory string `json:"business_category,omitempty"`
	BusinessDesc     string `json:"business_desc,omitempty"`
	BusinessPhone    string `json:"business_phone,omitempty"`
	BusinessEmail    string `json:"business_email,omitempty"`
	BusinessWebsite  string `json:"business_website,omitempty"`
	BusinessAddress  string `json:"business_address,omitempty"`
	BusinessCountry  string `json:"business_country,omitempty"`
	BusinessState    string `json:"business_state,omitempty"`
	BusinessCity     string `json:"business_city,omitempty"`
}

type LoginResponse struct {
	User         UserResponse `json:"user"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	Onboarding   bool         `json:"onboarding"`
}

// ---------------- SIGNUP ----------------
func (s *AuthService) Signup(user models.User) error {

	if !isValidPassword(user.Password) {
		return errors.New("password must be at least 8 characters with uppercase, lowercase, and a number")
	}

	user.Email = strings.TrimSpace(strings.ToLower(user.Email))
	user.Mobile = strings.TrimSpace(user.Mobile)

	// Validate email format
	if _, err := mail.ParseAddress(user.Email); err != nil {
		return errors.New("invalid email format")
	}

	if s.Repo.EmailExists(user.Email) {
		return errors.New("user already exists (email)")
	}

	if user.Mobile != "" {
		if s.Repo.MobileExists(user.Mobile) {
			return errors.New("mobile number already in use")
		}
	}

	hashed, err := utils.HashPassword(user.Password)
	if err != nil {
		return err
	}

	user.Password = hashed
	user.IsVerified = false
	user.IsPhoneVerified = false

	userID, err := s.Repo.CreateUser(user)
	if err != nil {
		return err
	}

	// Email OTP
	emailOTP := generateOTP()
	s.Redis.Set(bgCtx, "otp:email:"+user.Email, emailOTP, 5*time.Minute)
	go utils.SendEmail(user.Email, "Your Market House code is: "+emailOTP)

	// Phone OTP (only if mobile provided)
	if user.Mobile != "" {
		if err := s.sendPhoneOTP(user.Mobile); err != nil {
			log.Printf("phone otp send failed: %v", err)
		}
	}

	// Auto-generate username if not provided
	if user.Username == "" {
		base := strings.ToLower(strings.ReplaceAll(user.FullName, " ", ""))
		if base == "" {
			base = "user"
		}
		username := base + strconv.Itoa(rand.Intn(10000))
		for s.Repo.UsernameExists(username) {
			username = base + strconv.Itoa(rand.Intn(10000))
		}
		if err = s.Repo.UpdateProfile(userID, user.FullName, username, ""); err != nil {
			return err
		}
	}

	return nil
}

// ---------------- VERIFY EMAIL OTP ----------------
func (s *AuthService) VerifyOTP(email, otp string) error {

	email = strings.TrimSpace(strings.ToLower(email))
	val, err := s.Redis.Get(bgCtx, "otp:email:"+email).Result()
	if err != nil {
		return errors.New("otp expired or not found")
	}

	if val != otp {
		return errors.New("invalid otp")
	}

	if err := s.Repo.MarkVerified(email); err != nil {
		return err
	}

	s.Redis.Del(bgCtx, "otp:email:"+email)
	return nil
}

// ---------------- VERIFY PHONE OTP ----------------
func (s *AuthService) VerifyPhoneOTP(mobile, otp string) error {

	val, err := s.Redis.Get(bgCtx, "otp:phone:"+mobile).Result()
	if err != nil {
		return errors.New("otp expired or not found")
	}

	// Termii flows store "termii:<pinId>"; local dev flows store the code.
	if strings.HasPrefix(val, "termii:") {
		ok, verr := utils.VerifyOTP(strings.TrimPrefix(val, "termii:"), otp)
		if verr != nil || !ok {
			return errors.New("invalid otp")
		}
	} else if val != otp {
		return errors.New("invalid otp")
	}

	if err := s.Repo.MarkPhoneVerified(mobile); err != nil {
		return err
	}

	s.Redis.Del(bgCtx, "otp:phone:"+mobile)
	return nil
}

// sendPhoneOTP delivers an OTP by SMS. With Termii configured the PIN is
// generated and sent by Termii itself and the pinId is stored for later
// verification. If Termii fails (e.g. country not activated on the account)
// we fall back to a locally generated code logged to the console so the
// flow is never blocked.
func (s *AuthService) sendPhoneOTP(mobile string) error {
	if utils.TermiiEnabled() {
		pinID, err := utils.RequestOTP(mobile)
		if err != nil {
			log.Printf("termii send failed (%v) — falling back to console OTP", err)
		} else {
			return s.Redis.Set(bgCtx, "otp:phone:"+mobile, "termii:"+pinID, 5*time.Minute).Err()
		}
	}
	otp := generateOTP()
	if err := s.Redis.Set(bgCtx, "otp:phone:"+mobile, otp, 5*time.Minute).Err(); err != nil {
		return err
	}
	go utils.SendSMS(mobile, "Your Market House verification code is: "+otp)
	return nil
}

// ---------------- LOGIN ----------------
func (s *AuthService) Login(identifier, password string) (LoginResponse, error) {
	if os.Getenv("AUTH_DEBUG") == "1" {
		log.Printf("[AUTH DEBUG] login request identifier=%q", identifier)
	}
	// normalize identifier: lowercase emails, trim spaces
	id := strings.TrimSpace(identifier)
	if strings.Contains(id, "@") {
		id = strings.ToLower(id)
	}

	user, err := s.Repo.GetUserByEmailOrMobile(id)
	if err != nil {
		if os.Getenv("AUTH_DEBUG") == "1" {
			log.Printf("[AUTH DEBUG] lookup failed for '%s' by email/mobile: %v", id, err)
		}

		if errors.Is(err, sql.ErrNoRows) {
			user, err = s.Repo.FindByUsername(id, 0)
			if err != nil {
				if os.Getenv("AUTH_DEBUG") == "1" {
					log.Printf("[AUTH DEBUG] lookup failed for '%s' by username: %v", id, err)
				}
				return LoginResponse{}, fmt.Errorf("lookup error: %w", err)
			}
		} else {
			return LoginResponse{}, fmt.Errorf("lookup error: %w", err)
		}
	}

	ok := utils.CheckPasswordHash(password, user.Password)
	if os.Getenv("AUTH_DEBUG") == "1" {
		log.Printf("[AUTH DEBUG] password check for '%s': %v", id, ok)
	}
	if !ok {
		return LoginResponse{}, fmt.Errorf("password mismatch")
	}

	if !user.IsVerified && !user.IsPhoneVerified {
		return LoginResponse{}, fmt.Errorf("not verified: email=%v phone=%v", user.IsVerified, user.IsPhoneVerified)
	}

	accessToken, err := utils.GenerateToken(user.ID, user.Email)
	if err != nil {
		return LoginResponse{}, err
	}

	refreshToken, err := utils.GenerateRefreshToken(user.ID, user.Email)
	if err != nil {
		return LoginResponse{}, err
	}

	s.Redis.Set(bgCtx, "refresh:"+user.Email, refreshToken, 7*24*time.Hour)

	onboarding := user.Username == "" || user.ProfilePhoto == ""

	return LoginResponse{
		User: UserResponse{
			ID:           int64(user.ID),
			Email:        user.Email,
			Mobile:       user.Mobile,
			FullName:     user.FullName,
			ProfilePhoto: user.ProfilePhoto,
			HeaderPhoto:  user.HeaderPhoto,
			Bio:          user.Bio,
			Username:     user.Username,
		},
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Onboarding:   onboarding,
	}, nil
}

// ---------------- REFRESH TOKEN ----------------
func (s *AuthService) RefreshToken(refreshToken string) (string, error) {

	claims, err := utils.ValidateToken(refreshToken)
	if err != nil {
		return "", errors.New("invalid refresh token")
	}

	stored, err := s.Redis.Get(bgCtx, "refresh:"+claims.Email).Result()
	if err != nil || stored != refreshToken {
		return "", errors.New("refresh token not valid")
	}

	return utils.GenerateToken(claims.UserID, claims.Email)
}

// ---------------- UPLOAD IMAGE ----------------
func (s *AuthService) UploadImage(userID int64, file *multipart.FileHeader, uploadType string) (string, error) {

	// Validate upload type
	if uploadType != "profile" && uploadType != "header" {
		return "", errors.New("invalid upload type")
	}

	// Validate file size (max 5MB)
	const maxSize = 5 * 1024 * 1024
	if file.Size > maxSize {
		return "", errors.New("file too large (max 5MB)")
	}

	// Detect file type by reading magic bytes — more reliable than
	// Content-Type header or file extension (both unreliable on Android).
	src, err := file.Open()
	if err != nil {
		return "", errors.New("could not open uploaded file")
	}
	magic := make([]byte, 12)
	_, _ = src.Read(magic)
	src.Close()
	isJPEG := magic[0] == 0xFF && magic[1] == 0xD8
	isPNG := magic[0] == 0x89 && magic[1] == 0x50
	isWEBP := len(magic) >= 12 && string(magic[0:4]) == "RIFF" && string(magic[8:12]) == "WEBP"
	if !isJPEG && !isPNG && !isWEBP {
		return "", errors.New("invalid file type: only jpeg, png, webp allowed")
	}

	user, err := s.Repo.GetUserByID(userID)
	if err != nil {
		return "", err
	}

	// Delete old file
	if uploadType == "profile" && user.ProfilePhoto != "" {
		s.Storage.Delete(user.ProfilePhoto)
	}
	if uploadType == "header" && user.HeaderPhoto != "" {
		s.Storage.Delete(user.HeaderPhoto)
	}

	filename := strconv.FormatInt(userID, 10) + "_" + uploadType + ".jpg"

	url, err := s.Storage.Upload(file, uploadType, filename)
	if err != nil {
		return "", err
	}

	if uploadType == "profile" {
		return url, s.Repo.UpdateProfilePhoto(userID, url)
	}
	return url, s.Repo.UpdateHeaderPhoto(userID, url)
}

// ---------------- RESEND EMAIL OTP ----------------
func (s *AuthService) ResendEmailOTP(email string) error {
	// Rate limit check BEFORE sending
	email = strings.TrimSpace(strings.ToLower(email))
	key := "otp_limit:" + email
	count, _ := s.Redis.Get(bgCtx, key).Int()
	if count >= 3 {
		return errors.New("too many requests, try again later")
	}

	otp := generateOTP()
	if err := s.Redis.Set(bgCtx, "otp:email:"+email, otp, 5*time.Minute).Err(); err != nil {
		return err
	}

	go utils.SendEmail(email, "Your Market House verification code is: "+otp)

	s.Redis.Incr(bgCtx, key)
	s.Redis.Expire(bgCtx, key, 15*time.Minute)

	return nil
}

// ---------------- RESEND PHONE OTP ----------------
func (s *AuthService) ResendPhoneOTP(mobile string) error {
	// Rate limit
	key := "otp_limit:phone:" + mobile
	count, _ := s.Redis.Get(bgCtx, key).Int()
	if count >= 3 {
		return errors.New("too many requests, try again later")
	}

	if err := s.sendPhoneOTP(mobile); err != nil {
		return err
	}

	s.Redis.Incr(bgCtx, key)
	s.Redis.Expire(bgCtx, key, 15*time.Minute)

	return nil
}

// ---------------- GET PROFILE ----------------
// computeBadges derives achievement badges from reputation. Kept simple and
// stateless (no separate awards table) — thresholds can be tuned freely.
func computeBadges(reputation int) []string {
	badges := []string{}
	if reputation >= 50 {
		badges = append(badges, "Active Member")
	}
	if reputation >= 500 {
		badges = append(badges, "Community Expert")
	}
	if reputation >= 1000 {
		badges = append(badges, "Top Contributor")
	}
	return badges
}

func (s *AuthService) GetProfile(userID int64) (UserResponse, error) {
	user, err := s.Repo.GetFullUserByID(userID)
	if err != nil {
		return UserResponse{}, err
	}

	return UserResponse{
		ID:               int64(user.ID),
		Email:            user.Email,
		Mobile:           user.Mobile,
		FullName:         user.FullName,
		Username:         user.Username,
		ProfilePhoto:     user.ProfilePhoto,
		HeaderPhoto:      user.HeaderPhoto,
		Bio:              user.Bio,
		Posts:            int64(user.Posts),
		Following:        int64(user.Following),
		Followers:        int64(user.Followers),
		AccountType:      user.AccountType,
		IsVerified:       user.IsVerified,
		Reputation:       user.Reputation,
		Badges:           computeBadges(user.Reputation),
		BusinessName:     user.BusinessName,
		BusinessCategory: user.BusinessCategory,
		BusinessDesc:     user.BusinessDesc,
		BusinessPhone:    user.BusinessPhone,
		BusinessEmail:    user.BusinessEmail,
		BusinessWebsite:  user.BusinessWebsite,
		BusinessAddress:  user.BusinessAddress,
		BusinessCountry:  user.BusinessCountry,
		BusinessState:    user.BusinessState,
		BusinessCity:     user.BusinessCity,
	}, nil
}

// ---------------- GET PUBLIC PROFILE ----------------
func (s *AuthService) GetUserByUsername(username string, viewerID int64) (UserResponse, error) {
	user, err := s.Repo.FindByUsername(username, viewerID)
	if err != nil {
		return UserResponse{}, err
	}

	return UserResponse{
		ID:               int64(user.ID),
		FullName:         user.FullName,
		Username:         user.Username,
		ProfilePhoto:     user.ProfilePhoto,
		HeaderPhoto:      user.HeaderPhoto,
		Bio:              user.Bio,
		Posts:            int64(user.Posts),
		Following:        int64(user.Following),
		Followers:        int64(user.Followers),
		IsFollowing:      user.IsFollowing,
		AccountType:      user.AccountType,
		IsVerified:       user.IsVerified,
		Reputation:       user.Reputation,
		Badges:           computeBadges(user.Reputation),
		BusinessName:     user.BusinessName,
		BusinessCategory: user.BusinessCategory,
		BusinessDesc:     user.BusinessDesc,
		BusinessPhone:    user.BusinessPhone,
		BusinessEmail:    user.BusinessEmail,
		BusinessWebsite:  user.BusinessWebsite,
		BusinessAddress:  user.BusinessAddress,
		BusinessCountry:  user.BusinessCountry,
		BusinessState:    user.BusinessState,
		BusinessCity:     user.BusinessCity,
	}, nil
}

// ---------------- CHECK USERNAME ----------------
func (s *AuthService) CheckUsername(username string) (bool, error) {
	if !isValidUsername(username) {
		return false, errors.New("invalid username format")
	}
	if s.Repo.UsernameExists(username) {
		return false, nil
	}
	return true, nil
}

func (s *AuthService) UsernameExists(username string) bool {
	return s.Repo.UsernameExists(username)
}

// ---------------- UPDATE PROFILE ----------------
func (s *AuthService) UpdateProfile(userID int64, fullName, username, bio string, biz models.BusinessUpdate) error {
	if username != "" {
		if !isValidUsername(username) {
			return errors.New("invalid username: 3-20 chars, letters/numbers/underscore only")
		}
		if s.Repo.UsernameExistsForOtherUser(username, userID) {
			return errors.New("username already taken")
		}
	}

	if len(bio) > 300 {
		return errors.New("bio too long (max 300 chars)")
	}

	if err := s.Repo.UpdateProfile(userID, fullName, username, bio); err != nil {
		return err
	}

	// Only touch business fields if this request is actually upgrading/editing
	// a business account — a plain name/bio edit shouldn't wipe them out.
	if biz.AccountType != "" || biz.BusinessName != "" {
		return s.Repo.UpdateBusinessProfile(userID, biz)
	}
	return nil
}

// ---------------- FORGOT PASSWORD ----------------
func (s *AuthService) ForgotPassword(email string) error {
	// Normalize email and always return success to prevent enumeration
	email = strings.TrimSpace(strings.ToLower(email))
	if !s.Repo.EmailExists(email) {
		return nil
	}

	// Rate limit
	key := "otp_limit:reset:" + email
	count, _ := s.Redis.Get(bgCtx, key).Int()
	if count >= 3 {
		return errors.New("too many requests, try again later")
	}

	otp := generateOTP()
	s.Redis.Set(bgCtx, "otp:reset:"+email, otp, 15*time.Minute)
	go utils.SendEmail(email, "Your Market House password reset code is: "+otp)

	s.Redis.Incr(bgCtx, key)
	s.Redis.Expire(bgCtx, key, 15*time.Minute)

	return nil
}

// ---------------- RESET PASSWORD ----------------
func (s *AuthService) ResetPassword(ctx context.Context, email, otp, newPassword string) error {
	email = strings.TrimSpace(strings.ToLower(email))
	val, err := s.Redis.Get(ctx, "otp:reset:"+email).Result()
	if err != nil || val != otp {
		return errors.New("invalid or expired otp")
	}

	if !isValidPassword(newPassword) {
		return errors.New("password must be at least 8 characters with uppercase, lowercase, and a number")
	}

	user, err := s.Repo.GetUserByEmailOrMobile(email)
	if err != nil {
		return fmt.Errorf("user not found: %w", err)
	}

	hashed, err := utils.HashPassword(newPassword)
	if err != nil {
		return err
	}

	if err = s.Repo.UpdatePassword(ctx, user.ID, hashed); err != nil {
		return err
	}

	s.Redis.Del(ctx, "otp:reset:"+email)
	// Invalidate all refresh tokens on password reset
	s.Redis.Del(ctx, "refresh:"+email)
	return nil
}

// ---------------- HELPERS ----------------
func isValidPassword(p string) bool {
	if len(p) < 8 {
		return false
	}
	var u, l, n bool
	for _, c := range p {
		switch {
		case c >= 'A' && c <= 'Z':
			u = true
		case c >= 'a' && c <= 'z':
			l = true
		case c >= '0' && c <= '9':
			n = true
		}
	}
	return u && l && n
}

var seededRand = rand.New(rand.NewSource(time.Now().UnixNano()))

func generateOTP() string {
	return strconv.Itoa(100000 + seededRand.Intn(900000))
}

// UpdateLocation saves the caller's current coordinates plus their
// approximate LGA/state. Coordinates are validated to be within legal
// lat/lng ranges before hitting the DB.
func (s *AuthService) UpdateLocation(userID int64, lat, lng float64, lga, state string) error {
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return fmt.Errorf("invalid coordinates")
	}
	return s.Repo.UpdateLocation(bgCtx, userID, lat, lng, lga, state)
}
