package handlers

import (
	"mime/multipart"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"markethouse/internal/models"
	"markethouse/internal/services"
	"markethouse/pkg/utils"
)

type ShopHandler struct {
	Service *services.ShopService
}

// ─────────────────────────────────────────────────────────────────────────────
// PRODUCTS
// ─────────────────────────────────────────────────────────────────────────────

// POST /shop/product
func (h *ShopHandler) CreateProduct(c *gin.Context) {
	vendorID := c.GetInt64("user_id")

	price, _ := strconv.ParseFloat(c.PostForm("price"), 64)
	stockCount, _ := strconv.Atoi(c.PostForm("stock_count"))
	unlimited, _ := strconv.ParseBool(c.DefaultPostForm("is_unlimited_stock", "false"))

	p := models.Product{
		Name:             c.PostForm("name"),
		Description:      c.PostForm("description"),
		Category:         c.PostForm("category"),
		Price:            price,
		StockCount:       stockCount,
		IsUnlimitedStock: unlimited,
	}

	form, _ := c.MultipartForm()
	var files []*multipart.FileHeader
	if form != nil {
		files = form.File["images"]
		if len(files) > 5 {
			files = files[:5]
		}
	}

	id, err := h.Service.CreateProduct(vendorID, p, files)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "product created"})
}

// GET /shop/products?category=goods
func (h *ShopHandler) PublicProducts(c *gin.Context) {
	cat := c.Query("category")
	products, err := h.Service.GetPublicProducts(cat)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"products": products})
}

// GET /shop/products/mine
func (h *ShopHandler) MyProducts(c *gin.Context) {
	vendorID := c.GetInt64("user_id")
	products, err := h.Service.GetMyProducts(vendorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"products": products})
}

// ─────────────────────────────────────────────────────────────────────────────
// CART
// ─────────────────────────────────────────────────────────────────────────────

// POST /shop/cart
func (h *ShopHandler) AddToCart(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		ProductID int64 `json:"product_id"`
		Quantity  int   `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.Service.AddToCart(userID, req.ProductID, req.Quantity); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "added to cart"})
}

// GET /shop/cart
func (h *ShopHandler) GetCart(c *gin.Context) {
	userID := c.GetInt64("user_id")
	items, total, err := h.Service.GetCart(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": total})
}

// DELETE /shop/cart/:item_id
func (h *ShopHandler) RemoveFromCart(c *gin.Context) {
	userID := c.GetInt64("user_id")
	itemID, _ := strconv.ParseInt(c.Param("item_id"), 10, 64)
	if err := h.Service.RemoveFromCart(userID, itemID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "removed"})
}

// ─────────────────────────────────────────────────────────────────────────────
// CHECKOUT / PAYMENT
// ─────────────────────────────────────────────────────────────────────────────

// POST /shop/checkout  — initialise payment
// Body: { product_id, quantity, delivery_date_scheduled (RFC3339 optional) }
func (h *ShopHandler) Checkout(c *gin.Context) {
	userID := c.GetInt64("user_id")
	email := c.GetString("email") // set by JWT middleware

	var req struct {
		ProductID             int64  `json:"product_id"`
		Quantity              int    `json:"quantity"`
		DeliveryDateScheduled string `json:"delivery_date_scheduled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var dd *time.Time
	if req.DeliveryDateScheduled != "" {
		t, err := time.Parse(time.RFC3339, req.DeliveryDateScheduled)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid delivery_date_scheduled — use RFC3339"})
			return
		}
		dd = &t
	}

	result, err := h.Service.Checkout(userID, services.CheckoutRequest{
		ProductID:             req.ProductID,
		Quantity:              req.Quantity,
		DeliveryDateScheduled: dd,
		BuyerEmail:            email,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reference":         result.Reference,
		"authorization_url": result.AuthorizationURL,
		"total":             result.Total,
		"product_name":      result.ProductName,
	})
}

// POST /shop/checkout/confirm  — called after payment callback
// Body: { product_id, quantity, delivery_date_scheduled, reference }
func (h *ShopHandler) ConfirmPayment(c *gin.Context) {
	userID := c.GetInt64("user_id")

	var req struct {
		ProductID             int64  `json:"product_id"`
		Quantity              int    `json:"quantity"`
		DeliveryDateScheduled string `json:"delivery_date_scheduled"`
		Reference             string `json:"reference"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var dd *time.Time
	if req.DeliveryDateScheduled != "" {
		t, err := time.Parse(time.RFC3339, req.DeliveryDateScheduled)
		if err == nil {
			dd = &t
		}
	}

	order, err := h.Service.ConfirmPayment(userID, services.CheckoutRequest{
		ProductID:             req.ProductID,
		Quantity:              req.Quantity,
		DeliveryDateScheduled: dd,
	}, req.Reference)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"message":       "order placed — escrow held",
		"order":         order,
		"delivery_code": "share this QR code with the vendor upon delivery",
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// ORDERS
// ─────────────────────────────────────────────────────────────────────────────

// GET /orders/mine?role=buyer  OR  role=vendor
func (h *ShopHandler) MyOrders(c *gin.Context) {
	userID := c.GetInt64("user_id")
	role := c.DefaultQuery("role", "buyer")
	var (
		orders []models.Order
		err    error
	)
	if role == "vendor" {
		orders, err = h.Service.Repo.GetOrdersByVendor(userID)
	} else {
		orders, err = h.Service.Repo.GetOrdersByBuyer(userID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

// POST /orders/:id/deliver  — vendor scans code
// Body: { delivery_code }
func (h *ShopHandler) ConfirmDelivery(c *gin.Context) {
	vendorID := c.GetInt64("user_id")
	orderID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		DeliveryCode string `json:"delivery_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.Service.ConfirmDelivery(vendorID, orderID, req.DeliveryCode); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "delivery confirmed — payment released to vendor"})
}

// ─────────────────────────────────────────────────────────────────────────────
// CANCEL FLOW
// ─────────────────────────────────────────────────────────────────────────────

// POST /orders/:id/cancel/request  — buyer initiates
// Body: { pin }  (plain, hashed server-side)
func (h *ShopHandler) RequestCancel(c *gin.Context) {
	buyerID := c.GetInt64("user_id")
	orderID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct {
		Pin string `json:"pin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Pin == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "pin required"})
		return
	}
	hashed, err := utils.HashPassword(req.Pin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server error"})
		return
	}
	if err := h.Service.RequestCancel(buyerID, orderID, hashed); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "cancel request sent — awaiting vendor approval"})
}

// POST /orders/:id/cancel/vendor  — vendor approves
func (h *ShopHandler) VendorApproveCancel(c *gin.Context) {
	vendorID := c.GetInt64("user_id")
	orderID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.Service.VendorApproveCancel(vendorID, orderID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "vendor approved — awaiting admin approval"})
}

// POST /orders/:id/cancel/admin  — admin finalises (add admin middleware in prod)
func (h *ShopHandler) AdminApproveCancel(c *gin.Context) {
	orderID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.Service.AdminApproveCancel(orderID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "order cancelled — refund issued to buyer wallet"})
}

// ─────────────────────────────────────────────────────────────────────────────
// WALLET
// ─────────────────────────────────────────────────────────────────────────────

// GET /wallet
func (h *ShopHandler) GetWallet(c *gin.Context) {
	userID := c.GetInt64("user_id")
	balance, err := h.Service.GetWallet(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, balance)
}

// GET /wallet/history
func (h *ShopHandler) WalletHistory(c *gin.Context) {
	userID := c.GetInt64("user_id")
	txs, err := h.Service.GetWalletHistory(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"transactions": txs})
}

// POST /wallet/deposit
func (h *ShopHandler) Deposit(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Amount      float64 `json:"amount"`
		Reference   string  `json:"reference"`
		Description string  `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	if req.Amount <= 0 { c.JSON(400, gin.H{"error": "amount must be positive"}); return }
	err := h.Service.CreditWallet(userID, req.Amount, "credit", req.Description, req.Reference)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	c.JSON(200, gin.H{"ok": true})
}

// POST /wallet/withdraw
func (h *ShopHandler) Withdraw(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	err := h.Service.DebitWallet(userID, req.Amount, "debit", req.Description, "")
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	c.JSON(200, gin.H{"ok": true})
}

// POST /wallet/send
func (h *ShopHandler) Send(c *gin.Context) {
	senderID := c.GetInt64("user_id")
	var req struct {
		ReceiverID  int64   `json:"receiver_id"`
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	if err := h.Service.Transfer(senderID, req.ReceiverID, req.Amount, req.Description); err != nil {
		c.JSON(500, gin.H{"error": err.Error()}); return
	}
	c.JSON(200, gin.H{"ok": true})
}
