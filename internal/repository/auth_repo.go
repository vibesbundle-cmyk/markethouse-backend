package repository

import (
	"context"
	"database/sql"
	"fmt"
	"markethouse/internal/models"

	"github.com/lib/pq"
)

type AuthRepo struct {
	DB *sql.DB
}

func NewAuthRepo(db *sql.DB) *AuthRepo {
	return &AuthRepo{DB: db}
}

func (r *AuthRepo) UpdatePassword(ctx context.Context, userID int64, newHashedPassword string) error {
	query := `UPDATE users SET password = $1 WHERE id = $2`
	_, err := r.DB.ExecContext(ctx, query, newHashedPassword, userID)
	if err != nil {
		return fmt.Errorf("failed to update password for user %d: %w", userID, err)
	}
	return nil
}

func (r *AuthRepo) EmailExists(email string) bool {
	var exists bool
	r.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE email=$1)`, email).Scan(&exists)
	return exists
}

func (r *AuthRepo) MobileExists(mobile string) bool {
	var exists bool
	r.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE mobile=$1)`, mobile).Scan(&exists)
	return exists
}

func (r *AuthRepo) CreateUser(user models.User) (int64, error) {
	var id int64

	var mobile sql.NullString
	if user.Mobile != "" {
		mobile = sql.NullString{String: user.Mobile, Valid: true}
	}

	err := r.DB.QueryRow(`
	INSERT INTO users
	(full_name, email, mobile, password, dob, gender, username, profile_photo, header_photo, bio, account_type, is_verified, is_phone_verified)
	VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
	RETURNING id
	`,
		user.FullName,
		user.Email,
		mobile,
		user.Password,
		user.DOB,
		user.Gender,
		user.Username,
		user.ProfilePhoto,
		user.HeaderPhoto,
		user.Bio,
		user.AccountType,
		user.IsVerified,
		user.IsPhoneVerified,
	).Scan(&id)

	return id, err
}

func (r *AuthRepo) GetUserByEmailOrMobile(id string) (models.User, error) {
	var user models.User
	var mobile sql.NullString
	var username sql.NullString
	var profilePhoto sql.NullString
	var bio sql.NullString

	err := r.DB.QueryRow(`
	SELECT id, full_name, username, email, mobile, password, is_verified, is_phone_verified, profile_photo, bio, account_type, rating, sales_score
	FROM users
	WHERE lower(email) = lower($1) OR mobile = $1
	`, id).Scan(
		&user.ID,
		&user.FullName,
		&username,
		&user.Email,
		&mobile,
		&user.Password,
		&user.IsVerified,
		&user.IsPhoneVerified,
		&profilePhoto,
		&bio,
		&user.AccountType,
		&user.Rating,
		&user.SalesScore,
	)

	if err == nil {
		if mobile.Valid {
			user.Mobile = mobile.String
		}
		if username.Valid {
			user.Username = username.String
		}
		user.ProfilePhoto = profilePhoto.String
		user.Bio = bio.String
	}

	return user, err
}

func (r *AuthRepo) UsernameExists(username string) bool {
	var exists bool
	r.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username=$1)`, username).Scan(&exists)
	return exists
}

// UsernameExistsForOtherUser checks if username is taken by someone other than the given user
func (r *AuthRepo) UsernameExistsForOtherUser(username string, userID int64) bool {
	var exists bool
	r.DB.QueryRow(`SELECT EXISTS (SELECT 1 FROM users WHERE username=$1 AND id != $2)`, username, userID).Scan(&exists)
	return exists
}

func (r *AuthRepo) FindByUsername(username string, viewerID int64) (models.User, error) {
	var user models.User
	var mobile sql.NullString
	var profilePhoto sql.NullString
	var headerPhoto sql.NullString
	var bio sql.NullString
	var bName, bCategory, bDesc, bPhone, bEmail, bWebsite, bAddress, bCountry, bState, bCity sql.NullString

	err := r.DB.QueryRow(`
		SELECT id, full_name, username, email, mobile, password, profile_photo, header_photo, bio, account_type, rating, sales_score,
		  (SELECT COUNT(*) FROM posts WHERE user_id = users.id) as posts,
		  (SELECT COUNT(*) FROM follows WHERE follower_id = users.id) as following,
		  (SELECT COUNT(*) FROM follows WHERE following_id = users.id) as followers,
		  CASE WHEN $2 = 0 THEN false ELSE EXISTS(SELECT 1 FROM follows WHERE follower_id = $2 AND following_id = users.id) END as is_following,
		  COALESCE(is_verified, false), COALESCE(is_phone_verified, false), COALESCE(reputation,0),
		  COALESCE(lga,''), COALESCE(state,''),
		  business_name, business_category, business_desc, business_phone, business_email,
		  business_website, business_address, business_country, business_state, business_city
		FROM users
		WHERE username = $1
	`, username, viewerID).Scan(
		&user.ID,
		&user.FullName,
		&user.Username,
		&user.Email,
		&mobile,
		&user.Password,
		&profilePhoto,
		&headerPhoto,
		&bio,
		&user.AccountType,
		&user.Rating,
		&user.SalesScore,
		&user.Posts,
		&user.Following,
		&user.Followers,
		&user.IsFollowing,
		&user.IsVerified,
		&user.IsPhoneVerified,
		&user.Reputation,
		&user.LGA, &user.State,
		&bName, &bCategory, &bDesc, &bPhone, &bEmail,
		&bWebsite, &bAddress, &bCountry, &bState, &bCity,
	)

	if err == nil {
		user.Mobile = mobile.String
		user.ProfilePhoto = profilePhoto.String
		user.HeaderPhoto = headerPhoto.String
		user.Bio = bio.String
		user.BusinessName = bName.String
		user.BusinessCategory = bCategory.String
		user.BusinessDesc = bDesc.String
		user.BusinessPhone = bPhone.String
		user.BusinessEmail = bEmail.String
		user.BusinessWebsite = bWebsite.String
		user.BusinessAddress = bAddress.String
		user.BusinessCountry = bCountry.String
		user.BusinessState = bState.String
		user.BusinessCity = bCity.String
	}

	return user, err
}

func (r *AuthRepo) UpdateProfile(userID int64, fullName, username, bio string) error {
	_, err := r.DB.Exec(`
	UPDATE users
	SET full_name = COALESCE(NULLIF($1, ''), full_name),
	    username   = COALESCE(NULLIF($2, ''), username),
	    bio        = CASE WHEN $3 = '' THEN bio ELSE $3 END
	WHERE id=$4
	`, fullName, username, bio, userID)
	return err
}

func (r *AuthRepo) UpdateBusinessProfile(userID int64, biz models.BusinessUpdate) error {
	accountType := biz.AccountType
	if accountType == "" {
		accountType = "business" // editing business fields implies staying a business account
	}
	_, err := r.DB.Exec(`
		UPDATE users SET
			account_type      = $1,
			business_category = $2,
			business_name     = $3,
			business_desc     = $4,
			business_phone    = $5,
			business_email    = $6,
			business_website  = $7,
			business_address  = $8,
			business_country  = $9,
			business_state    = $10,
			business_city     = $11,
			selling_types     = $12
		WHERE id = $13
	`,
		accountType, biz.BusinessCategory, biz.BusinessName, biz.BusinessDesc,
		biz.BusinessPhone, biz.BusinessEmail, biz.BusinessWebsite, biz.BusinessAddress,
		biz.BusinessCountry, biz.BusinessState, biz.BusinessCity, pq.Array(biz.SellingTypes),
		userID)
	return err
}

func (r *AuthRepo) UpdateProfilePhoto(userID int64, url string) error {
	var photo sql.NullString
	if url != "" {
		photo = sql.NullString{String: url, Valid: true}
	}
	_, err := r.DB.Exec(`UPDATE users SET profile_photo=$1 WHERE id=$2`, photo, userID)
	return err
}

func (r *AuthRepo) UpdateHeaderPhoto(userID int64, url string) error {
	var photo sql.NullString
	if url != "" {
		photo = sql.NullString{String: url, Valid: true}
	}
	_, err := r.DB.Exec(`UPDATE users SET header_photo=$1 WHERE id=$2`, photo, userID)
	return err
}

func (r *AuthRepo) MarkVerified(email string) error {
	_, err := r.DB.Exec(`UPDATE users SET is_verified=true WHERE email=$1`, email)
	return err
}

func (r *AuthRepo) MarkPhoneVerified(mobile string) error {
	_, err := r.DB.Exec(`UPDATE users SET is_phone_verified=true WHERE mobile=$1`, mobile)
	return err
}

func (r *AuthRepo) GetUserByID(id int64) (models.User, error) {
	var user models.User
	var username sql.NullString
	var mobile sql.NullString
	var dob sql.NullString
	var gender sql.NullString
	var profilePhoto sql.NullString
	var headerPhoto sql.NullString
	var bio sql.NullString

	err := r.DB.QueryRow(`
		SELECT id, full_name, username, email, mobile, password, dob, gender, profile_photo, header_photo, bio, account_type, rating, sales_score, is_verified, is_phone_verified
		FROM users
		WHERE id=$1
	`, id).Scan(
		&user.ID,
		&user.FullName, &username, &user.Email, &mobile, &user.Password, &dob, &gender,
		&profilePhoto, &headerPhoto, &bio, &user.AccountType, &user.Rating, &user.SalesScore,
		&user.IsVerified, &user.IsPhoneVerified,
	)

	if err == nil {
		if username.Valid {
			user.Username = username.String
		}
		user.Mobile = mobile.String
		user.DOB = dob.String
		user.Gender = gender.String
		user.ProfilePhoto = profilePhoto.String
		user.HeaderPhoto = headerPhoto.String
		user.Bio = bio.String
	}

	return user, err
}

func (r *AuthRepo) GetFullUserByID(id int64) (models.User, error) {
	var user models.User
	var username sql.NullString
	var mobile sql.NullString
	var profilePhoto sql.NullString
	var headerPhoto sql.NullString
	var bio sql.NullString
	var bName, bCategory, bDesc, bPhone, bEmail, bWebsite, bAddress, bCountry, bState, bCity sql.NullString

	err := r.DB.QueryRow(`
		SELECT id, full_name, username, email, mobile, profile_photo, header_photo, bio, account_type, rating, sales_score,
		  (SELECT COUNT(*) FROM posts WHERE user_id = users.id) as posts,
		  (SELECT COUNT(*) FROM follows WHERE follower_id = users.id) as following,
		  (SELECT COUNT(*) FROM follows WHERE following_id = users.id) as followers,
		  COALESCE(is_verified, false), COALESCE(reputation,0),
		  COALESCE(lga,''), COALESCE(state,''),
		  business_name, business_category, business_desc, business_phone, business_email,
		  business_website, business_address, business_country, business_state, business_city
		FROM users
		WHERE id=$1
	`, id).Scan(
		&user.ID,
		&user.FullName,
		&username,
		&user.Email,
		&mobile,
		&profilePhoto,
		&headerPhoto,
		&bio,
		&user.AccountType,
		&user.Rating,
		&user.SalesScore,
		&user.Posts,
		&user.Following,
		&user.Followers,
		&user.IsVerified,
		&user.Reputation,
		&user.LGA, &user.State,
		&bName, &bCategory, &bDesc, &bPhone, &bEmail,
		&bWebsite, &bAddress, &bCountry, &bState, &bCity,
	)

	if err == nil {
		if username.Valid {
			user.Username = username.String
		}
		user.Mobile = mobile.String
		user.ProfilePhoto = profilePhoto.String
		user.HeaderPhoto = headerPhoto.String
		user.Bio = bio.String
		user.BusinessName = bName.String
		user.BusinessCategory = bCategory.String
		user.BusinessDesc = bDesc.String
		user.BusinessPhone = bPhone.String
		user.BusinessEmail = bEmail.String
		user.BusinessWebsite = bWebsite.String
		user.BusinessAddress = bAddress.String
		user.BusinessCountry = bCountry.String
		user.BusinessState = bState.String
		user.BusinessCity = bCity.String
	}

	return user, err
}

// UpdateLocation stores the user's last-known coordinates + LGA/state,
// used for the "nearby" feed/marketplace and for showing a user's
// approximate location to their chat contacts.
func (r *AuthRepo) UpdateLocation(ctx context.Context, userID int64, lat, lng float64, lga, state string) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE users SET latitude = $1, longitude = $2, lga = COALESCE(NULLIF($4,''), lga), state = COALESCE(NULLIF($5,''), state) WHERE id = $3`,
		lat, lng, userID, lga, state)
	return err
}
