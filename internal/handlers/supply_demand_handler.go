package handlers

import (
	"database/sql"
	"fmt"
	"math"
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
		Kind        string   `json:"kind"` // supply | demand
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Price       float64  `json:"price"`
		Category    string   `json:"category"`
		Images      []string `json:"images"`
		Lat         *float64 `json:"lat"`
		Lng         *float64 `json:"lng"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid body"})
		return
	}
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	if req.Kind != "supply" && req.Kind != "demand" {
		c.JSON(400, gin.H{"error": "kind must be 'supply' or 'demand'"})
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		c.JSON(400, gin.H{"error": "title is required"})
		return
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
		(user_id, kind, title, description, price, category, images, location_lat, location_lng)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
		userID, req.Kind, req.Title, req.Description, req.Price, req.Category,
		pq.Array(req.Images), req.Lat, req.Lng).Scan(&id)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"id": id})
}

// GetListings returns every active listing of the given kind. Both Supply
// and Demand are meant to show everything to everyone — this isn't filtered
// per-viewer. Optional ?lat=&lng=&radius_km= narrows results to listings
// near that point and adds distance_km to each row. The poster's real
// profile photo is withheld (client shows a generic avatar) until a buyer
// has paid for their item — see anonymize().
func (h *SupplyDemandHandler) GetListings(c *gin.Context) {
	kind := strings.ToLower(c.DefaultQuery("kind", "supply"))
	if kind != "supply" && kind != "demand" {
		c.JSON(400, gin.H{"error": "kind must be 'supply' or 'demand'"})
		return
	}
	lat, latErr := strconv.ParseFloat(c.Query("lat"), 64)
	lng, lngErr := strconv.ParseFloat(c.Query("lng"), 64)
	radiusKm, _ := strconv.ParseFloat(c.DefaultQuery("radius_km", "50"), 64)
	hasLocation := latErr == nil && lngErr == nil

	query := `
		SELECT l.id, l.user_id, u.username, l.title, COALESCE(l.description,''), l.price,
		       COALESCE(l.category,''), l.images, l.radius_km, l.created_at,
		       l.location_lat, l.location_lng`
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
		var uname, title, desc, cat, ca string
		var price float64
		var images []string
		var radius int
		var distanceKm *float64
		var lat, lng sql.NullFloat64
		scanArgs := []interface{}{&id, &uid, &uname, &title, &desc, &price, &cat, pq.Array(&images), &radius, &ca, &lat, &lng}
		if hasLocation {
			scanArgs = append(scanArgs, &distanceKm)
		}
		if rows.Scan(scanArgs...) != nil {
			continue
		}
		entry := gin.H{
			"id": id, "user_id": uid,
			"username": uname, // shown; the poster's photo is intentionally NOT returned here
			"title":    title, "description": desc, "price": price, "category": cat,
			"images": images, "radius_km": radius, "created_at": ca,
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
	var actor interface{}
	if actorID > 0 {
		actor = actorID
	}
	h.DB.Exec(`INSERT INTO notifications(user_id, actor_id, type, title, body, ref_type, ref_id)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, userID, actor, typ, title, body, refType, refID)
	if h.Hub != nil {
		h.Hub.SendToUser(userID, gin.H{
			"type": "notification", "notif_type": typ, "title": title, "body": body,
			"ref_type": refType, "ref_id": refID, "created_at": time.Now().Format(time.RFC3339),
		})
	}
}
