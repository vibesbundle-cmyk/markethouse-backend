package handlers

import (
	"mime/multipart"
	"strconv"

	"github.com/gin-gonic/gin"
	"markethouse/internal/models"
	"markethouse/internal/services"
)

type MarketplaceHandler struct {
	Service *services.MarketplaceService
}

// ── POST /demand ──────────────────────────────────────────────────────────────
func (h *MarketplaceHandler) CreateDemand(c *gin.Context) {
	userID := c.GetInt64("user_id")

	// NOTE: this used to read c.PostForm(...) for every field, but the
	// Flutter client (Api.createDemand) sends a JSON body — PostForm only
	// parses application/x-www-form-urlencoded or multipart bodies, so
	// every field silently came through as "". Demands were being created
	// with blank looking_for/category/prices/etc. every time. Bind JSON.
	var req struct {
		LookingFor       string   `json:"looking_for"`
		Category         string   `json:"category"`
		ConditionPref    []string `json:"condition_pref"`
		MinPrice         float64  `json:"min_price"`
		MaxPrice         float64  `json:"max_price"`
		Location         string   `json:"location"`
		Latitude         float64  `json:"latitude"`
		Longitude        float64  `json:"longitude"`
		SearchRadius     int      `json:"search_radius"`
		Description      string   `json:"description"`
		Urgency          string   `json:"urgency"`
		ContactNumber    string   `json:"contact_number"`
		PreferredContact string   `json:"preferred_contact"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid body"})
		return
	}

	d := models.Demand{
		LookingFor:       req.LookingFor,
		Category:         req.Category,
		ConditionPref:    req.ConditionPref,
		MinPrice:         req.MinPrice,
		MaxPrice:         req.MaxPrice,
		Location:         req.Location,
		Latitude:         req.Latitude,
		Longitude:        req.Longitude,
		SearchRadius:     req.SearchRadius,
		Description:      req.Description,
		Urgency:          req.Urgency,
		ContactNumber:    req.ContactNumber,
		PreferredContact: req.PreferredContact,
	}

	id, err := h.Service.CreateDemand(userID, d)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": id, "message": "demand posted"})
}

// ── POST /supply ──────────────────────────────────────────────────────────────
func (h *MarketplaceHandler) CreateSupply(c *gin.Context) {
	userID := c.GetInt64("user_id")

	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	neg, _ := strconv.ParseBool(c.PostForm("negotiable"))
	lat, _ := strconv.ParseFloat(c.PostForm("latitude"), 64)
	lng, _ := strconv.ParseFloat(c.PostForm("longitude"), 64)
	delRadius, _ := strconv.Atoi(c.PostForm("delivery_radius"))
	delAvail, _ := strconv.ParseBool(c.PostForm("delivery_available"))
	ageVal, _ := strconv.Atoi(c.PostForm("age_value"))

	sup := models.Supply{
		GoodsName:         c.PostForm("goods_name"),
		Category:          c.PostForm("category"),
		Condition:         c.PostForm("condition"),
		AgeValue:          ageVal,
		AgeUnit:           c.PostForm("age_unit"),
		Brand:             c.PostForm("brand"),
		Price:             price,
		Negotiable:        neg,
		Description:       c.PostForm("description"),
		Location:          c.PostForm("location"),
		Latitude:          lat,
		Longitude:         lng,
		DeliveryRadius:    delRadius,
		DeliveryAvailable: delAvail,
		ContactNumber:     c.PostForm("contact_number"),
		WhatsappNumber:    c.PostForm("whatsapp_number"),
	}

	form, _ := c.MultipartForm()
	var files []*multipart.FileHeader
	if form != nil {
		files = form.File["photos"]
		if len(files) > 5 {
			files = files[:5]
		}
	}

	id, err := h.Service.CreateSupply(userID, sup, files)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": id, "message": "supply posted"})
}

// ── GET /demands/mine ─────────────────────────────────────────────────────────
func (h *MarketplaceHandler) MyDemands(c *gin.Context) {
	userID := c.GetInt64("user_id")
	list, err := h.Service.GetMyDemands(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"demands": list})
}

// ── GET /supplies/mine ────────────────────────────────────────────────────────
func (h *MarketplaceHandler) MySupplies(c *gin.Context) {
	userID := c.GetInt64("user_id")
	list, err := h.Service.GetMySupplies(userID)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"supplies": list})
}

// ── GET /supplies?category=Electronics ───────────────────────────────────────
func (h *MarketplaceHandler) PublicSupplies(c *gin.Context) {
	cat := c.Query("category")
	list, err := h.Service.GetPublicSupplies(cat)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"supplies": list})
}

// ── GET /demands ──────────────────────────────────────────────────────────────
func (h *MarketplaceHandler) PublicDemands(c *gin.Context) {
	list, err := h.Service.GetPublicDemands()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"demands": list})
}
