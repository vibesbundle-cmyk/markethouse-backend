package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"markethouse/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/lib/pq"
)

// SupplyDemandHandler implements the thrift/used-item "Supply & Demand"
// marketplace plus the escrow wallet that money moves through.
//
// IMPORTANT — payment gateway: nothing in here talks to a real payment
// provider yet. Checkout creates an escrow transaction in "awaiting_payment";
// ConfirmPayment is the placeholder that will be replaced by a real
// Paystack/etc webhook handler (verifying the provider's signature) once a
// gateway is wired up. Until then ConfirmPayment just marks it paid, which
// is fine for building/testing the rest of the flow but MUST NOT be exposed
// to real users before a real gateway sits in front of it.
type SupplyDemandHandler struct {
	DB  *sql.DB
	Hub *services.Hub
}

// ── App settings (admin-editable) ────────────────────────────────────────

func (h *SupplyDemandHandler) getSetting(key, fallback string) string {
	var v string
	if err := h.DB.QueryRow(`SELECT value FROM app_settings WHERE key=$1`, key).Scan(&v); err != nil {
		return fallback
	}
	return v
}

func (h *SupplyDemandHandler) getSettingFloat(key string, fallback float64) float64 {
	v, err := strconv.ParseFloat(h.getSetting(key, ""), 64)
	if err != nil {
		return fallback
	}
	return v
}

// GetSettings is admin-only: current app fee / radius fee / daily post limit.
func (h *SupplyDemandHandler) GetSettings(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	rows, err := h.DB.Query(`SELECT key, value FROM app_settings`)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := gin.H{}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			out[k] = v
		}
	}
	c.JSON(200, out)
}

// UpdateSettings is admin-only. Body is any subset of the known keys:
// app_fee_percent, app_fee_flat, radius_fee_per_km, max_supply_posts_per_day.
func (h *SupplyDemandHandler) UpdateSettings(c *gin.Context) {
	if !h.requireAdmin(c) {
		return
	}
	var req map[string]string
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid body"})
		return
	}
	allowed := map[string]bool{
		"app_fee_percent": true, "app_fee_flat": true,
		"radius_fee_per_km": true, "max_supply_posts_per_day": true,
	}
	for k, v := range req {
		if !allowed[k] {
			continue
		}
		h.DB.Exec(`INSERT INTO app_settings(key,value) VALUES($1,$2)
			ON CONFLICT (key) DO UPDATE SET value=$2`, k, v)
	}
	c.JSON(200, gin.H{"ok": true})
}

func (h *SupplyDemandHandler) requireAdmin(c *gin.Context) bool {
	userID := c.GetInt64("user_id")
	var isAdmin bool
	h.DB.QueryRow(`SELECT COALESCE(is_admin,false) FROM users WHERE id=$1`, userID).Scan(&isAdmin)
	if !isAdmin {
		c.JSON(403, gin.H{"error": "admin only"})
		return false
	}
	return true
}

// ── Listings ──────────────────────────────────────────────────────────────

// CreateListing posts a new supply or demand item. Supply is capped at
// max_supply_posts_per_day (admin-configurable, default 3) per user — demand
// posts (wanted ads) aren't capped since they're not "selling" anything.
func (h *SupplyDemandHandler) CreateListing(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Kind        string   `json:"kind"` // supply | demand | ask_around
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Price       float64  `json:"price"`
		MinPrice    float64  `json:"min_price"`
		MaxPrice    float64  `json:"max_price"`
		Negotiable  bool     `json:"negotiable"`
		Category    string   `json:"category"`
		Condition   string   `json:"condition"`
		Quantity    int      `json:"quantity"`
		Images      []string `json:"images"`
		LocationText string  `json:"location_text"`
		Lat         *float64 `json:"lat"`
		Lng         *float64 `json:"lng"`
		RadiusKm    int      `json:"radius_km"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid body"})
		return
	}
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if req.Kind != "supply" && req.Kind != "demand" && req.Kind != "ask_around" {
		c.JSON(400, gin.H{"error": "kind must be 'supply', 'demand', or 'ask_around'"})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}
	if req.RadiusKm <= 0 {
		req.RadiusKm = 5
	}

	// Geocode a plain location name (e.g. "Awka") into coordinates so the
	// listing shows up in distance-based searches and on the map.
	if (req.Lat == nil || req.Lng == nil) && strings.TrimSpace(req.LocationText) != "" {
		if la, lo, ok := geocodeAddress(req.LocationText); ok {
			req.Lat = &la
			req.Lng = &lo
		}
	}

	if req.Kind == "supply" {
		limit := int(h.getSettingFloat("max_supply_posts_per_day", 3))
		var countToday int
		h.DB.QueryRow(`SELECT COUNT(*) FROM supply_demand_listings
			WHERE user_id=$1 AND kind='supply' AND created_at >= CURRENT_DATE`, userID).Scan(&countToday)
		if countToday >= limit {
			c.JSON(429, gin.H{"error": fmt.Sprintf("you've hit today's limit of %d supply posts — try again tomorrow", limit)})
			return
		}
	}

	var id int64
	err := h.DB.QueryRow(`INSERT INTO supply_demand_listings
		(user_id, kind, title, description, price, min_price, max_price, negotiable,
		 category, condition, quantity, images, location_text, location_lat, location_lng, radius_km)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) RETURNING id`,
		userID, req.Kind, req.Title, req.Description, req.Price, req.MinPrice, req.MaxPrice,
		req.Negotiable, req.Category, req.Condition, req.Quantity, pq.Array(req.Images),
		req.LocationText, req.Lat, req.Lng, req.RadiusKm).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Supply/Demand match notifications — when a new listing goes up, ping
	// the people on the other side of the market who are looking for it.
	if req.Kind == "supply" {
		h.notifyMatchesForSupply(userID, id, req.Title, req.Category, req.Lat, req.Lng, req.RadiusKm)
	} else if req.Kind == "demand" {
		h.notifyMatchesForDemand(userID, id, req.Title, req.Category, req.Lat, req.Lng, req.RadiusKm)
	}

	c.JSON(200, gin.H{"id": id})
}

// notifyMatchesForSupply pings owners of "ask_around" demands (buyers) whose
// wanted ad matches this new supply by category (and proximity, when set).
func (h *SupplyDemandHandler) notifyMatchesForSupply(sellerID, listingID int64, title, category string, lat, lng *float64, radiusKm int) {
	if category == "" {
		return
	}
	q := `SELECT DISTINCT l.user_id, l.id FROM supply_demand_listings l
	      WHERE l.kind='ask_around' AND l.status='active' AND l.category=$1 AND l.user_id<>$2`
	args := []interface{}{category, sellerID}
	if lat != nil && lng != nil {
		q += ` AND l.location_lat IS NOT NULL AND l.location_lng IS NOT NULL
		       AND (6371 * acos(LEAST(1, GREATEST(-1,
		         cos(radians($3)) * cos(radians(l.location_lat)) * cos(radians(l.location_lng) - radians($4))
		         + sin(radians($3)) * sin(radians(l.location_lat))
		       )))) <= GREATEST(l.radius_km, $5, 5)`
		args = append(args, *lat, *lng, radiusKm)
	}
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var buyerID, lid int64
		if rows.Scan(&buyerID, &lid) == nil {
			h.notify(buyerID, sellerID, "supply_match", "New match for your request",
				fmt.Sprintf("\"%s\" matches what you're looking for", title), "sd_listing", lid)
		}
	}
}

// notifyMatchesForDemand pings suppliers who already have a matching "supply"
// listing for this new wanted ad.
func (h *SupplyDemandHandler) notifyMatchesForDemand(buyerID, listingID int64, title, category string, lat, lng *float64, radiusKm int) {
	if category == "" {
		return
	}
	q := `SELECT DISTINCT l.user_id, l.id FROM supply_demand_listings l
	      WHERE l.kind='supply' AND l.status='active' AND l.category=$1 AND l.user_id<>$2`
	args := []interface{}{category, buyerID}
	if lat != nil && lng != nil {
		q += ` AND l.location_lat IS NOT NULL AND l.location_lng IS NOT NULL
		       AND (6371 * acos(LEAST(1, GREATEST(-1,
		         cos(radians($3)) * cos(radians(l.location_lat)) * cos(radians(l.location_lng) - radians($4))
		         + sin(radians($3)) * sin(radians(l.location_lat))
		       )))) <= GREATEST(l.radius_km, $5, 5)`
		args = append(args, *lat, *lng, radiusKm)
	}
	rows, err := h.DB.Query(q, args...)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var sellerID, lid int64
		if rows.Scan(&sellerID, &lid) == nil {
			h.notify(sellerID, buyerID, "demand_match", "New buyer looking for your item",
				fmt.Sprintf("Someone wants \"%s\" — you may have a match", title), "sd_listing", lid)
		}
	}
}

// GetListings returns every active listing of the given kind. Both Supply
// and Demand are meant to show everything to everyone — this isn't filtered
// per-viewer. Optional ?lat=&lng=&radius_km= narrows results to listings
// near that point and adds distance_km to each row. The poster's real
// profile photo is withheld (client shows a generic avatar) until a buyer
// has paid for their item — see anonymize().
func (h *SupplyDemandHandler) GetListings(c *gin.Context) {
	kind := strings.ToLower(c.DefaultQuery("kind", "supply"))
	if kind != "supply" && kind != "demand" && kind != "ask_around" {
		c.JSON(400, gin.H{"error": "kind must be 'supply', 'demand', or 'ask_around'"})
		return
	}
	lat, latErr := strconv.ParseFloat(c.Query("lat"), 64)
	lng, lngErr := strconv.ParseFloat(c.Query("lng"), 64)
	radiusKm, _ := strconv.ParseFloat(c.DefaultQuery("radius_km", "50"), 64)
	hasLocation := latErr == nil && lngErr == nil

	query := `
		SELECT l.id, l.user_id, l.kind, u.username, l.title, COALESCE(l.description,''), l.price,
		       COALESCE(l.category,''), l.images, l.radius_km, l.created_at,
		       l.location_lat, l.location_lng,
		       COALESCE(l.min_price,0), COALESCE(l.max_price,0),
		       COALESCE(l.negotiable,false), COALESCE(l.condition,''),
		       COALESCE(l.quantity,1), COALESCE(l.location_text,'')`
	args := []interface{}{}
	if hasLocation {
		query += `,
		       (6371 * acos(LEAST(1, GREATEST(-1,
		         cos(radians($1)) * cos(radians(l.location_lat)) * cos(radians(l.location_lng) - radians($2))
		         + sin(radians($1)) * sin(radians(l.location_lat))
		       )))) AS distance_km`
		args = append(args, lat, lng)
	}
	query += `
		FROM supply_demand_listings l
		JOIN users u ON u.id = l.user_id
		WHERE l.kind=$` + strconv.Itoa(len(args)+1) + ` AND l.status='active'`
	args = append(args, kind)
	if hasLocation {
		query += ` AND l.location_lat IS NOT NULL AND l.location_lng IS NOT NULL AND
		  (6371 * acos(LEAST(1, GREATEST(-1,
		    cos(radians($1)) * cos(radians(l.location_lat)) * cos(radians(l.location_lng) - radians($2))
		    + sin(radians($1)) * sin(radians(l.location_lat))
		  )))) <= ` + strconv.FormatFloat(radiusKm, 'f', 2, 64)
	}
	if hasLocation {
		query += ` ORDER BY distance_km ASC`
	} else {
		query += ` ORDER BY l.created_at DESC`
	}

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []gin.H
	for rows.Next() {
		var id, uid int64
		var uname, kind, title, desc, cat, ca, cond, locText string
		var price, minPrice, maxPrice float64
		var images []string
		var radius, quantity int
		var negotiable bool
		var distanceKm *float64
		var lat, lng sql.NullFloat64
		scanArgs := []interface{}{&id, &uid, &kind, &uname, &title, &desc, &price, &cat, pq.Array(&images), &radius, &ca, &lat, &lng,
			&minPrice, &maxPrice, &negotiable, &cond, &quantity, &locText}
		if hasLocation {
			scanArgs = append(scanArgs, &distanceKm)
		}
		if rows.Scan(scanArgs...) != nil {
			continue
		}
		entry := gin.H{
			"id": id, "user_id": uid, "kind": kind,
			"username": uname,
			"title": title, "description": desc, "price": price, "category": cat,
			"images": images, "radius_km": radius, "created_at": ca,
			"min_price": minPrice, "max_price": maxPrice, "negotiable": negotiable,
			"condition": cond, "quantity": quantity, "location_text": locText,
		}
		if lat.Valid {
			entry["lat"] = lat.Float64
		}
		if lng.Valid {
			entry["lng"] = lng.Float64
		}
		if distanceKm != nil {
			entry["distance_km"] = math.Round(*distanceKm*100) / 100
		}
		out = append(out, entry)
	}
	if out == nil {
		out = []gin.H{}
	}
	c.JSON(200, gin.H{"listings": out, "kind": kind,
		"tagline": supplyDemandTagline(kind)})
}

func supplyDemandTagline(kind string) string {
	if kind == "supply" {
		return "Thrift. Cheap. Link buyers close to you. Not for business — mainly for thrift or used items."
	}
	return "Say what you want, sellers near you will find it."
}

// ── Buyer interest ping ──────────────────────────────────────────────────
//
// NOTE ON CHECKOUT/ESCROW/WALLET: this codebase already has a full escrow +
// wallet system built for the Shop feature (see shop_handler.go /
// shop_service.go / shop_repo.go — cart, checkout, delivery-code release,
// cancel+refund, wallet balance/history). Supply & Demand purchases should
// go through THAT system once its schema is fixed (see the note left in
// postgres.go / my summary to the user) rather than getting a second,
// competing wallet — so Checkout/pay/escrow/radius-boost for Supply &
// Demand aren't wired up yet. What IS wired: posting, browsing, and this
// interest ping.

// ExpressInterest pings the poster's — er, the *buyer's* own reminder ("pay
// before it's taken") the moment they say they want an item. No cart is
// persisted server-side yet (that lands with the checkout integration).
func (h *SupplyDemandHandler) ExpressInterest(c *gin.Context) {
	buyerID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var sellerID int64
	var title, status string
	err := h.DB.QueryRow(`SELECT user_id, title, status FROM supply_demand_listings WHERE id=$1`, listingID).
		Scan(&sellerID, &title, &status)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "listing not found"})
		return
	}
	if status != "active" {
		c.JSON(409, gin.H{"error": "no longer available"})
		return
	}
	if sellerID == buyerID {
		c.JSON(400, gin.H{"error": "you can't buy your own listing"})
		return
	}

	h.notify(buyerID, 0, "cart_reminder", "Complete your payment",
		fmt.Sprintf("\"%s\" is in your cart — pay now before someone else takes it.", title),
		"sd_listing", listingID)
	h.notify(sellerID, buyerID, "buyer_interested", "Someone wants your item",
		fmt.Sprintf("A buyer is interested in \"%s\".", title),
		"sd_listing", listingID)

	c.JSON(200, gin.H{"ok": true})
}

func (h *SupplyDemandHandler) notify(userID, actorID int64, typ, title, body, refType string, refID int64) {
	if !notifyAllowed(h.DB, userID, typ) {
		return
	}
	var actor interface{}
	if actorID > 0 {
		actor = actorID
	}
	h.DB.Exec(`INSERT INTO notifications(user_id, actor_id, type, title, body, ref_type, ref_id, entity_type, entity_id)
		VALUES($1,$2,$3,$4,$5,$6,$7,$6,$7)`, userID, actor, typ, title, body, refType, refID)
	if h.Hub != nil {
		h.Hub.SendToUser(userID, gin.H{
			"type": "notification", "notif_type": typ, "title": title, "body": body,
			"ref_type": refType, "ref_id": refID,
			"entity_type": refType, "entity_id": refID,
			"created_at": time.Now().Format(time.RFC3339),
		})
	}
	services.SendPush(h.DB, userID, title, body, map[string]string{
		"type":        typ,
		"entity_type": refType,
		"entity_id":   strconv.FormatInt(refID, 10),
		"actor_id":    strconv.FormatInt(actorID, 10),
	})
}

// ── My listings ─────────────────────────────────────────────────────────

func (h *SupplyDemandHandler) GetMyListings(c *gin.Context) {
	userID := c.GetInt64("user_id")
	kind := strings.ToLower(c.DefaultQuery("kind", ""))
	query := `SELECT l.id, l.kind, l.title, COALESCE(l.description,''), l.price,
	           COALESCE(l.category,''), l.images, l.status, l.created_at,
	           COALESCE(l.condition,''), COALESCE(l.quantity,1), COALESCE(l.location_text,''),
	           COALESCE(l.min_price,0), COALESCE(l.max_price,0),
	           COALESCE(l.negotiable,false), l.location_lat, l.location_lng,
	           COALESCE(l.radius_km,5)
	           FROM supply_demand_listings l
	           WHERE l.user_id=$1 AND l.status != 'removed'`
	args := []interface{}{userID}
	if kind == "supply" || kind == "demand" {
		query += ` AND l.kind=$2`
		args = append(args, kind)
	}
	query += ` ORDER BY l.created_at DESC`

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []gin.H
	for rows.Next() {
		var id int64
		var kind, title, desc, cat, status, ca, cond, locText string
		var price, minPrice, maxPrice float64
		var negotiable bool
		var radius, quantity int
		var images []string
		var lat, lng sql.NullFloat64
		rows.Scan(&id, &kind, &title, &desc, &price, &cat, pq.Array(&images), &status, &ca, &cond, &quantity, &locText,
			&minPrice, &maxPrice, &negotiable, &lat, &lng, &radius)
		entry := gin.H{
			"id": id, "kind": kind, "title": title, "description": desc, "price": price,
			"category": cat, "images": images, "status": status, "created_at": ca,
			"condition": cond, "quantity": quantity, "location_text": locText,
			"min_price": minPrice, "max_price": maxPrice, "negotiable": negotiable,
			"radius_km": radius,
		}
		if lat.Valid {
			entry["lat"] = lat.Float64
		}
		if lng.Valid {
			entry["lng"] = lng.Float64
		}
		out = append(out, entry)
	}
	if out == nil {
		out = []gin.H{}
	}
	c.JSON(200, gin.H{"listings": out})
}

// ── Nearby suppliers (for Ask Around) ──────────────────────────────────

func (h *SupplyDemandHandler) GetNearbySuppliers(c *gin.Context) {
	lat, latErr := strconv.ParseFloat(c.Query("lat"), 64)
	lng, lngErr := strconv.ParseFloat(c.Query("lng"), 64)
	if latErr != nil || lngErr != nil {
		c.JSON(400, gin.H{"error": "lat and lng required"})
		return
	}
	category := strings.ToLower(strings.TrimSpace(c.Query("category")))

	query := `
		SELECT u.id, u.username, sp.supply_radius_km, sp.categories,
		       (6371 * acos(LEAST(1, GREATEST(-1,
		         cos(radians($1)) * cos(radians(u.location_lat)) * cos(radians(u.location_lng) - radians($2))
		         + sin(radians($1)) * sin(radians(u.location_lat))
		       )))) AS distance_km
		FROM supplier_preferences sp
		JOIN users u ON u.id = sp.user_id
		WHERE sp.is_active = true
		  AND u.location_lat IS NOT NULL AND u.location_lng IS NOT NULL
		  AND (6371 * acos(LEAST(1, GREATEST(-1,
		        cos(radians($1)) * cos(radians(u.location_lat)) * cos(radians(u.location_lng) - radians($2))
		        + sin(radians($1)) * sin(radians(u.location_lat))
		      )))) <= sp.supply_radius_km`
	args := []interface{}{lat, lng}
	if category != "" {
		query += ` AND ($3 = ANY(sp.categories) OR '{}'::text[] <@ sp.categories)`
		args = append(args, category)
	}
	query += ` ORDER BY distance_km ASC LIMIT 20`

	rows, err := h.DB.Query(query, args...)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var out []gin.H
	for rows.Next() {
		var uid int64
		var uname string
		var radius int
		var cats []string
		var dist float64
		rows.Scan(&uid, &uname, &radius, pq.Array(&cats), &dist)
		out = append(out, gin.H{
			"user_id": uid, "username": uname,
			"supply_radius_km": radius, "categories": cats,
			"distance_km": math.Round(dist*100) / 100,
		})
	}
	if out == nil {
		out = []gin.H{}
	}
	c.JSON(200, gin.H{"suppliers": out})
}

// ── Supplier preferences ───────────────────────────────────────────────

func (h *SupplyDemandHandler) GetSupplierPreferences(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var cats []string
	var radius int
	var active bool
	err := h.DB.QueryRow(`SELECT COALESCE(categories,'{}'), supply_radius_km, is_active
		FROM supplier_preferences WHERE user_id=$1`, userID).
		Scan(pq.Array(&cats), &radius, &active)
	if err == sql.ErrNoRows {
		c.JSON(200, gin.H{"categories": []string{}, "supply_radius_km": 10, "is_active": false})
		return
	}
	c.JSON(200, gin.H{"categories": cats, "supply_radius_km": radius, "is_active": active})
}

func (h *SupplyDemandHandler) SaveSupplierPreferences(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Categories []string `json:"categories"`
		RadiusKm   int      `json:"supply_radius_km"`
		IsActive   bool     `json:"is_active"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid body"})
		return
	}
	if req.RadiusKm <= 0 {
		req.RadiusKm = 10
	}
	_, err := h.DB.Exec(`INSERT INTO supplier_preferences (user_id, categories, supply_radius_km, is_active)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (user_id) DO UPDATE SET categories=$2, supply_radius_km=$3, is_active=$4`,
		userID, pq.Array(req.Categories), req.RadiusKm, req.IsActive)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ── Ask Around ─────────────────────────────────────────────────────────
//
// PostAskAround creates an ask_around listing and pings nearby suppliers
// who have opted in and match the category. Unlike regular demands, ask
// around triggers push notifications to relevant suppliers immediately.

func (h *SupplyDemandHandler) PostAskAround(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Title        string  `json:"title"`
		Description  string  `json:"description"`
		Category     string  `json:"category"`
		Budget       float64 `json:"budget"`
		LocationText string  `json:"location_text"`
		Lat          float64 `json:"lat"`
		Lng          float64 `json:"lng"`
		RadiusKm     int     `json:"radius_km"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Title) == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
	}
	if req.RadiusKm <= 0 {
		req.RadiusKm = 10
	}

	// Geocode a plain location name into coordinates (same as CreateListing).
	if req.Lat == 0 && req.Lng == 0 && strings.TrimSpace(req.LocationText) != "" {
		if la, lo, ok := geocodeAddress(req.LocationText); ok {
			req.Lat = la
			req.Lng = lo
		}
	}

	var id int64
	err := h.DB.QueryRow(`INSERT INTO supply_demand_listings
		(user_id, kind, title, description, price, category, location_text, location_lat, location_lng, radius_km)
		VALUES($1,'ask_around',$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		userID, req.Title, req.Description, req.Budget, req.Category,
		req.LocationText, req.Lat, req.Lng, req.RadiusKm).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	// Notify nearby suppliers
	supplierQuery := `
		SELECT u.id FROM supplier_preferences sp
		JOIN users u ON u.id = sp.user_id
		WHERE sp.is_active = true AND u.id != $1
		  AND u.location_lat IS NOT NULL AND u.location_lng IS NOT NULL
		  AND ($4 = ANY(sp.categories) OR '{}'::text[] <@ sp.categories)
		  AND (6371 * acos(LEAST(1, GREATEST(-1,
		        cos(radians($2)) * cos(radians(u.location_lat)) * cos(radians(u.location_lng) - radians($3))
		        + sin(radians($2)) * sin(radians(u.location_lat))
		      )))) <= sp.supply_radius_km`
	rows, err := h.DB.Query(supplierQuery, userID, req.Lat, req.Lng, req.Category)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var sid int64
			if rows.Scan(&sid) == nil {
				h.notify(sid, userID, "ask_around", "Someone needs what you sell",
					fmt.Sprintf("\"%s\" — check if you can supply this.", req.Title),
					"sd_listing", id)
			}
		}
	}

	c.JSON(200, gin.H{"id": id})
}

// -- Update listing -----------------------------------------------------

func (h *SupplyDemandHandler) UpdateListing(c *gin.Context) {
	userID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var ownerID int64
	var status string
	err := h.DB.QueryRow(`SELECT user_id, status FROM supply_demand_listings WHERE id=$1`, listingID).
		Scan(&ownerID, &status)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "listing not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not your listing"})
		return
	}
	if status == "removed" {
		c.JSON(409, gin.H{"error": "listing was removed"})
		return
	}

	var req struct {
		Title        string   `json:"title"`
		Description  string   `json:"description"`
		Price        float64  `json:"price"`
		MinPrice     float64  `json:"min_price"`
		MaxPrice     float64  `json:"max_price"`
		Negotiable   bool     `json:"negotiable"`
		Category     string   `json:"category"`
		Condition    string   `json:"condition"`
		Quantity     int      `json:"quantity"`
		Images       []string `json:"images"`
		LocationText string   `json:"location_text"`
		Lat          *float64 `json:"lat"`
		Lng          *float64 `json:"lng"`
		RadiusKm     int      `json:"radius_km"`
		Status       string   `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid body"})
		return
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}

	_, err = h.DB.Exec(`UPDATE supply_demand_listings SET
		title=$1, description=$2, price=$3, min_price=$4, max_price=$5,
		negotiable=$6, category=$7, condition=$8, quantity=$9, images=$10,
		location_text=$11, location_lat=$12, location_lng=$13, radius_km=$14,
		status=COALESCE(NULLIF($15,''), status)
		WHERE id=$16`,
		req.Title, req.Description, req.Price, req.MinPrice, req.MaxPrice,
		req.Negotiable, req.Category, req.Condition, req.Quantity, pq.Array(req.Images),
		req.LocationText, req.Lat, req.Lng, req.RadiusKm, req.Status, listingID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// -- Delete listing (soft) ----------------------------------------------

func (h *SupplyDemandHandler) DeleteListing(c *gin.Context) {
	userID := c.GetInt64("user_id")
	listingID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var ownerID int64
	err := h.DB.QueryRow(`SELECT user_id FROM supply_demand_listings WHERE id=$1 AND status != 'removed'`, listingID).
		Scan(&ownerID)
	if err == sql.ErrNoRows {
		c.JSON(404, gin.H{"error": "listing not found"})
		return
	}
	if ownerID != userID {
		c.JSON(403, gin.H{"error": "not your listing"})
		return
	}

	h.DB.Exec(`UPDATE supply_demand_listings SET status='removed' WHERE id=$1`, listingID)
	c.JSON(200, gin.H{"ok": true})
}

// geocodeAddress resolves a free-text place name (e.g. "Awka") to lat/lng using
// the public OpenStreetMap Nominatim service (no API key required). Best-effort:
// on any failure it returns ok=false and the caller simply stores NULL
// coordinates, so a listing still works — it just won't be distance-ranked.
func geocodeAddress(address string) (float64, float64, bool) {
	address = strings.TrimSpace(address)
	if address == "" {
		return 0, 0, false
	}
	q := url.QueryEscape(address)
	req, err := http.NewRequest("GET",
		"https://nominatim.openstreetmap.org/search?format=json&limit=1&q="+q, nil)
	if err != nil {
		return 0, 0, false
	}
	req.Header.Set("User-Agent", "MarketHouse/1.0 (marketplace app)")
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, false
	}
	defer resp.Body.Close()
	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil || len(results) == 0 {
		return 0, 0, false
	}
	lat, err1 := strconv.ParseFloat(results[0].Lat, 64)
	lng, err2 := strconv.ParseFloat(results[0].Lon, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return lat, lng, true
}
