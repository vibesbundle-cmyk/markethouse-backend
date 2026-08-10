package handlers

import (
	"database/sql"
	"encoding/json"
	"markethouse/internal/storage"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

type CommerceHandler struct {
	DB      *sql.DB
	Storage storage.Storage
}

var validListingTypes = map[string]bool{
	"product": true, "service": true, "job": true, "hotel": true,
	"property": true, "vehicle": true, "event": true,
}

// GET /commerce?type=product — list all active listings of a given type.
// Optional ?lat=&lng=&radius_km= filters/sorts by distance from that point
// ("near me" browsing) — omit them to get the plain unfiltered list.
func (h *CommerceHandler) List(c *gin.Context) {
	userID := c.GetInt64("user_id") // 0 if unauthenticated
	listingType := c.Query("type")
	if !validListingTypes[listingType] {
		c.JSON(400, gin.H{"error": "invalid or missing type"})
		return
	}
	lat, latErr := strconv.ParseFloat(c.Query("lat"), 64)
	lng, lngErr := strconv.ParseFloat(c.Query("lng"), 64)
	radiusKm, _ := strconv.ParseFloat(c.DefaultQuery("radius_km", "0"), 64)
	hasLocation := latErr == nil && lngErr == nil

	query := `
		SELECT l.id, l.user_id, l.title, COALESCE(l.description,''), l.price, COALESCE(l.discount_price,0),
		       COALESCE(l.category,''), COALESCE(l.brand,''), COALESCE(l.condition,''), COALESCE(l.stock,0),
		       COALESCE(l.sku,''), l.delivery_available, COALESCE(l.location,''), COALESCE(l.images,'{}'),
		       COALESCE(l.video_url,''), COALESCE(l.metadata::text,'{}'), l.views, l.created_at,
		       COALESCE(u.is_verified,false), COALESCE(u.username,''), l.upvotes, l.downvotes,
		       COALESCE(uv.vote,0),
		       l.latitude, l.longitude`
	args := []interface{}{userID}
	if hasLocation {
		// Haversine distance in km — plain trig, no PostGIS/earthdistance
		// extension required.
		query += `,
		       (6371 * acos(LEAST(1, GREATEST(-1,
		         cos(radians($2)) * cos(radians(l.latitude)) * cos(radians(l.longitude) - radians($3))
		         + sin(radians($2)) * sin(radians(l.latitude))
		       )))) AS distance_km`
		args = append(args, lat, lng)
	}
	query += `
		FROM commerce_listings l
		JOIN users u ON u.id = l.user_id
		LEFT JOIN commerce_listing_votes uv ON uv.listing_id = l.id AND uv.user_id = $1
		WHERE l.listing_type = $` + strconv.Itoa(len(args)+1) + ` AND l.status = 'active'`
	args = append(args, listingType)
	if hasLocation && radiusKm > 0 {
		query += ` AND l.latitude IS NOT NULL AND l.longitude IS NOT NULL AND
		  (6371 * acos(LEAST(1, GREATEST(-1,
		    cos(radians($2)) * cos(radians(l.latitude)) * cos(radians(l.longitude) - radians($3))
		    + sin(radians($2)) * sin(radians(l.latitude))
		  )))) <= ` + strconv.FormatFloat(radiusKm, 'f', 2, 64)
	}
	if hasLocation {
		query += ` ORDER BY distance_km ASC LIMIT 100`
	} else {
		query += ` ORDER BY l.created_at DESC LIMIT 100`
	}

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		var id, uid int64
		var title, desc, category, brand, condition, sku, location, videoURL, username string
		var price, discount float64
		var stock, views, upvotes, downvotes, myVote int
		var delivery bool
		var images pq.StringArray
		var metadataRaw string
		var created string
		var isVerified bool
		var distanceKm *float64
		var lLat, lLng sql.NullFloat64

		scanArgs := []interface{}{&id, &uid, &title, &desc, &price, &discount, &category, &brand, &condition,
			&stock, &sku, &delivery, &location, &images, &videoURL, &metadataRaw, &views, &created,
			&isVerified, &username, &upvotes, &downvotes, &myVote, &lLat, &lLng}
		if hasLocation {
			scanArgs = append(scanArgs, &distanceKm)
		}
		if rows.Scan(scanArgs...) != nil {
			continue
		}
		var metadata map[string]interface{}
		json.Unmarshal([]byte(metadataRaw), &metadata)

		row := gin.H{
			"id": id, "user_id": uid, "username": username, "title": title, "description": desc,
			"price": price, "discount_price": discount, "category": category, "brand": brand,
			"condition": condition, "stock": stock, "sku": sku, "delivery_available": delivery,
			"location": location, "images": []string(images), "video_url": videoURL,
			"metadata": metadata, "view_count": views, "created_at": created,
			"is_verified": isVerified, "type": listingType,
			"upvotes": upvotes, "downvotes": downvotes, "my_vote": myVote,
		}
		if distanceKm != nil {
			row["distance_km"] = *distanceKm
		}
		if lLat.Valid {
			row["lat"] = lLat.Float64
		}
		if lLng.Valid {
			row["lng"] = lLng.Float64
		}
		results = append(results, row)
	}
	c.JSON(200, gin.H{"listings": results})
}

// GET /commerce/mine — every listing (any type) the current user has posted
func (h *CommerceHandler) GetMine(c *gin.Context) {
	userID := c.GetInt64("user_id")
	rows, err := h.DB.Query(`
		SELECT l.id, l.listing_type, l.title, COALESCE(l.description,''), l.price, COALESCE(l.discount_price,0),
		       COALESCE(l.category,''), COALESCE(l.brand,''), COALESCE(l.condition,''), COALESCE(l.stock,0),
		       COALESCE(l.sku,''), l.delivery_available, COALESCE(l.location,''), COALESCE(l.images,'{}'),
		       COALESCE(l.video_url,''), COALESCE(l.metadata::text,'{}'), l.views, l.created_at, l.status
		FROM commerce_listings l
		WHERE l.user_id = $1
		ORDER BY l.created_at DESC LIMIT 200`, userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	results := []gin.H{}
	for rows.Next() {
		var id int64
		var listingType, title, desc, category, brand, condition, sku, location, videoURL, status string
		var price, discount float64
		var stock, views int
		var delivery bool
		var images pq.StringArray
		var metadataRaw, created string

		if err := rows.Scan(&id, &listingType, &title, &desc, &price, &discount, &category, &brand, &condition,
			&stock, &sku, &delivery, &location, &images, &videoURL, &metadataRaw, &views, &created, &status); err != nil {
			continue
		}
		var metadata map[string]interface{}
		json.Unmarshal([]byte(metadataRaw), &metadata)

		results = append(results, gin.H{
			"id": id, "type": listingType, "title": title, "description": desc,
			"price": price, "discount_price": discount, "category": category, "brand": brand,
			"condition": condition, "stock": stock, "sku": sku, "delivery_available": delivery,
			"location": location, "images": []string(images), "video_url": videoURL,
			"metadata": metadata, "view_count": views, "created_at": created, "status": status,
		})
	}
	c.JSON(200, gin.H{"listings": results})
}

// POST /commerce/listing (multipart) — create a listing of any of the 7 types.
// Shared fields go as normal form fields; anything type-specific (amenities,
// salary, mileage, etc.) is passed as a single "metadata" JSON string field.
func (h *CommerceHandler) Create(c *gin.Context) {
	userID := c.GetInt64("user_id")
	listingType := c.PostForm("listing_type")
	if !validListingTypes[listingType] {
		c.JSON(400, gin.H{"error": "invalid or missing listing_type"})
		return
	}
	title := c.PostForm("title")
	if strings.TrimSpace(title) == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
	}
	price, _ := strconv.ParseFloat(c.DefaultPostForm("price", "0"), 64)
	discount, _ := strconv.ParseFloat(c.DefaultPostForm("discount_price", "0"), 64)
	var stock interface{} // NULL = always in stock (matches the nullable `stock` column)
	if v, err := strconv.Atoi(c.PostForm("stock")); err == nil {
		stock = v
	}
	delivery, _ := strconv.ParseBool(c.DefaultPostForm("delivery_available", "false"))
	metadata := c.DefaultPostForm("metadata", "{}")
	if strings.TrimSpace(metadata) == "" {
		metadata = "{}"
	}
	var lat, lng interface{}
	if v, err := strconv.ParseFloat(c.PostForm("latitude"), 64); err == nil {
		lat = v
	}
	if v, err := strconv.ParseFloat(c.PostForm("longitude"), 64); err == nil {
		lng = v
	}

	var imageURLs []string
	if form, err := c.MultipartForm(); err == nil {
		for i, f := range form.File["images"] {
			filename := "listing_" + strconv.FormatInt(userID, 10) + "_" + strconv.Itoa(i) + "_" + f.Filename
			if url, uerr := h.Storage.Upload(f, "commerce", filename); uerr == nil {
				imageURLs = append(imageURLs, url)
			}
		}
	}

	var id int64
	err := h.DB.QueryRow(`
		INSERT INTO commerce_listings
			(user_id, listing_type, title, description, price, discount_price, category, brand,
			 condition, stock, sku, delivery_available, location, images, video_url, metadata, status,
			 latitude, longitude)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16::jsonb,'active',$17,$18)
		RETURNING id`,
		userID, listingType, title, c.PostForm("description"), price, discount,
		c.PostForm("category"), c.PostForm("brand"), c.PostForm("condition"), stock, c.PostForm("sku"),
		delivery, c.PostForm("location"), pq.Array(imageURLs), c.PostForm("video_url"), metadata,
		lat, lng,
	).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"id": id})
}

// POST /commerce/:id/vote  {"vote": 1 | -1 | 0}   (0 clears an existing vote)
// Thumbs up/down, one per user per listing — replaces the old heart icon
// that only ever changed local UI state and never touched the server.
func (h *CommerceHandler) Vote(c *gin.Context) {
	userID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Vote int `json:"vote"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Vote != 1 && req.Vote != -1 && req.Vote != 0) {
		c.JSON(400, gin.H{"error": "vote must be 1, -1, or 0"})
		return
	}

	if req.Vote == 0 {
		h.DB.Exec(`DELETE FROM commerce_listing_votes WHERE listing_id=$1 AND user_id=$2`, listingID, userID)
	} else {
		h.DB.Exec(`INSERT INTO commerce_listing_votes(listing_id, user_id, vote) VALUES($1,$2,$3)
			ON CONFLICT (listing_id, user_id) DO UPDATE SET vote=$3, created_at=NOW()`,
			listingID, userID, req.Vote)
	}
	// Recompute denormalized counts straight from the vote table so they can
	// never drift, rather than incrementing/decrementing in place.
	h.DB.Exec(`UPDATE commerce_listings SET
		upvotes = (SELECT COUNT(*) FROM commerce_listing_votes WHERE listing_id=$1 AND vote=1),
		downvotes = (SELECT COUNT(*) FROM commerce_listing_votes WHERE listing_id=$1 AND vote=-1)
		WHERE id=$1`, listingID)

	var up, down int
	h.DB.QueryRow(`SELECT upvotes, downvotes FROM commerce_listings WHERE id=$1`, listingID).Scan(&up, &down)
	c.JSON(200, gin.H{"upvotes": up, "downvotes": down, "my_vote": req.Vote})
}

// kListingReportReasons is the fixed pick-list shown to the reporter —
// mirrored on the client (commerce.dart) so the picker doesn't need a
// round trip; kept here too as the source of truth / server-side validation.
var kListingReportReasons = map[string]bool{
	"Prohibited or illegal item": true,
	"Scam or fraud":              true,
	"Misleading description":     true,
	"Wrong category":             true,
	"Offensive content":          true,
	"Spam or duplicate":          true,
	"Other":                      true,
}

// POST /commerce/:id/report
func (h *CommerceHandler) Report(c *gin.Context) {
	userID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Reason  string `json:"reason"`
		Details string `json:"details"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !kListingReportReasons[req.Reason] {
		c.JSON(400, gin.H{"error": "please pick a valid reason"})
		return
	}
	_, err := h.DB.Exec(`INSERT INTO commerce_listing_reports(listing_id, reporter_id, reason, details)
		VALUES($1,$2,$3,$4)`, listingID, userID, req.Reason, req.Details)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// GET /admin/commerce-reports?status=pending — admin-only queue of reports.
// Someone has to actually read these; this is that screen's data source.
func (h *CommerceHandler) ListReports(c *gin.Context) {
	var isAdmin bool
	h.DB.QueryRow(`SELECT COALESCE(is_admin,false) FROM users WHERE id=$1`, c.GetInt64("user_id")).Scan(&isAdmin)
	if !isAdmin {
		c.JSON(403, gin.H{"error": "admin only"})
		return
	}
	status := c.DefaultQuery("status", "pending")
	rows, err := h.DB.Query(`
		SELECT r.id, r.listing_id, l.title, r.reporter_id, u.username, r.reason, r.details, r.status, r.created_at
		FROM commerce_listing_reports r
		JOIN commerce_listings l ON l.id = r.listing_id
		JOIN users u ON u.id = r.reporter_id
		WHERE r.status = $1
		ORDER BY r.created_at DESC LIMIT 200`, status)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []gin.H{}
	for rows.Next() {
		var id, listingID, reporterID int64
		var title, reporter, reason, details, st, ca string
		if rows.Scan(&id, &listingID, &title, &reporterID, &reporter, &reason, &details, &st, &ca) != nil {
			continue
		}
		out = append(out, gin.H{
			"id": id, "listing_id": listingID, "listing_title": title,
			"reporter": reporter, "reason": reason, "details": details,
			"status": st, "created_at": ca,
		})
	}
	c.JSON(200, gin.H{"reports": out})
}

// PUT /admin/commerce-reports/:id  {"status": "reviewed"|"dismissed", "remove_listing": bool}
func (h *CommerceHandler) ResolveReport(c *gin.Context) {
	var isAdmin bool
	h.DB.QueryRow(`SELECT COALESCE(is_admin,false) FROM users WHERE id=$1`, c.GetInt64("user_id")).Scan(&isAdmin)
	if !isAdmin {
		c.JSON(403, gin.H{"error": "admin only"})
		return
	}
	reportID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Status        string `json:"status"`
		RemoveListing bool   `json:"remove_listing"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Status != "reviewed" && req.Status != "dismissed") {
		c.JSON(400, gin.H{"error": "status must be 'reviewed' or 'dismissed'"})
		return
	}
	h.DB.Exec(`UPDATE commerce_listing_reports SET status=$1 WHERE id=$2`, req.Status, reportID)
	if req.RemoveListing {
		var listingID int64
		h.DB.QueryRow(`SELECT listing_id FROM commerce_listing_reports WHERE id=$1`, reportID).Scan(&listingID)
		if listingID > 0 {
			h.DB.Exec(`UPDATE commerce_listings SET status='removed' WHERE id=$1`, listingID)
		}
	}
	c.JSON(200, gin.H{"ok": true})
}
