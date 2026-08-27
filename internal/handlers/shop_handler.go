package handlers

import (
	"log"
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
	Hub     *services.Hub
}

// notifyWallet pushes a realtime balance nudge over the WS hub so open
// wallet screens refresh instantly instead of waiting for a manual reload.
func (h *ShopHandler) notifyWallet(userIDs ...int64) {
	if h.Hub == nil {
		return
	}
	for _, id := range userIDs {
		h.Hub.SendToUser(id, map[string]interface{}{"type": "wallet_update"})
	}
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

// POST /shop/checkout/batch  — pay for several cart items in one payment
// Body: { items: [{product_id, quantity}], delivery_date_scheduled }
func (h *ShopHandler) CheckoutBatch(c *gin.Context) {
	userID := c.GetInt64("user_id")
	email := c.GetString("email")

	var req struct {
		Items                 []struct {
			ProductID int64 `json:"product_id"`
			Quantity  int   `json:"quantity"`
		} `json:"items"`
		DeliveryDateScheduled string `json:"delivery_date_scheduled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(req.Items) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no items"})
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

	batch := make([]services.BatchItem, len(req.Items))
	for i, it := range req.Items {
		batch[i] = services.BatchItem{ProductID: it.ProductID, Quantity: it.Quantity}
	}

	result, err := h.Service.CheckoutBatch(userID, batch, dd, email)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"reference":         result.Reference,
		"authorization_url": result.AuthorizationURL,
		"total":             result.Total,
		"orders":            result.Orders,
	})
}

// POST /shop/checkout/confirm-batch  — verify shared payment, mark all orders paid
// Body: { reference }
func (h *ShopHandler) ConfirmBatchPayment(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		Reference string `json:"reference"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	orders, err := h.Service.ConfirmBatchPayment(userID, req.Reference)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"message": "payment confirmed — orders placed",
		"orders":  orders,
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

	// Diagnostic: check wallet_transactions columns on first call
	if !h.Service.CheckedWalletSchema {
		h.Service.CheckedWalletSchema = true
		var cols string
		_ = h.Service.Repo.DB.QueryRow(`SELECT string_agg(column_name, ',') FROM information_schema.columns WHERE table_name='wallet_transactions' ORDER BY ordinal_position`).Scan(&cols)
		log.Printf("[WALLET] wallet_transactions columns: %s", cols)

		// Force-add wallet_id if missing
		if _, err := h.Service.Repo.DB.Exec(`ALTER TABLE wallet_transactions ADD COLUMN IF NOT EXISTS wallet_id INTEGER`); err != nil {
			log.Printf("[WALLET] force-add wallet_id failed: %v", err)
		}
	}

	log.Printf("[WALLET] deposit user=%d amount=%.2f ref=%q desc=%q", userID, req.Amount, req.Reference, req.Description)
	err := h.Service.CreditWallet(userID, req.Amount, "credit", req.Description, req.Reference, 0)
	if err != nil { log.Printf("[WALLET] deposit ERROR: %v", err); c.JSON(500, gin.H{"error": err.Error()}); return }
	h.notifyWallet(userID)
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
	err := h.Service.DebitWallet(userID, req.Amount, "debit", req.Description, "", 0)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	h.notifyWallet(userID)
	c.JSON(200, gin.H{"ok": true})
}

// POST /wallet/send
func (h *ShopHandler) Send(c *gin.Context) {
	senderID := c.GetInt64("user_id")
	var req struct {
		ReceiverID  int64   `json:"receiver_id"`
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
		Pin         string  `json:"pin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	ref, err := h.Service.Transfer(senderID, req.ReceiverID, req.Amount, req.Description, req.Pin)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()}); return
	}
	h.notifyWallet(senderID, req.ReceiverID)

	// Persistent + realtime transaction notifications for both parties.
	if h.Service != nil && h.Service.Repo != nil {
		db := h.Service.Repo.DB
		amt := strconv.FormatFloat(req.Amount, 'f', 2, 64)
		sender := userName(db, senderID)
		receiver := userName(db, req.ReceiverID)
		NotifyWithWS(db, h.Hub, req.ReceiverID, senderID, "transaction",
			"You received ₦" + amt, sender + " sent you ₦" + amt, "user", senderID)
		NotifyWithWS(db, h.Hub, senderID, senderID, "transaction",
			"Money sent", "You sent ₦" + amt + " to " + receiver, "user", req.ReceiverID)
	}

	c.JSON(200, gin.H{"ok": true, "reference": ref})
}

// GET /wallet/pin — is a transfer password set?
func (h *ShopHandler) PinStatus(c *gin.Context) {
	userID := c.GetInt64("user_id")
	c.JSON(200, gin.H{"set": h.Service.TransferPinEnabled(userID)})
}

// POST /wallet/pin — set or rotate the transfer password
func (h *ShopHandler) SetPin(c *gin.Context) {
	userID := c.GetInt64("user_id")
	var req struct {
		CurrentPin string `json:"current_pin"`
		NewPin     string `json:"new_pin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	if len(req.NewPin) < 4 { c.JSON(400, gin.H{"error": "transfer password must be at least 4 characters"}); return }
	if err := h.Service.SetTransferPin(userID, req.CurrentPin, req.NewPin); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "incorrect current transfer password" { status = http.StatusForbidden }
		c.JSON(status, gin.H{"error": err.Error()}); return
	}
	c.JSON(200, gin.H{"ok": true})
}

// POST /wallet/schedule — create a scheduled transfer
func (h *ShopHandler) ScheduleTransfer(c *gin.Context) {
	senderID := c.GetInt64("user_id")
	var req struct {
		ReceiverID  int64   `json:"receiver_id"`
		Amount      float64 `json:"amount"`
		Description string  `json:"description"`
		ScheduledAt string  `json:"scheduled_at"` // RFC3339
	}
	if err := c.ShouldBindJSON(&req); err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	t, err := time.Parse(time.RFC3339, req.ScheduledAt)
	if err != nil { c.JSON(400, gin.H{"error": "invalid scheduled_at — use RFC3339"}); return }
	id, err := h.Service.CreateScheduledTransfer(senderID, req.ReceiverID, req.Amount, req.Description, t)
	if err != nil { c.JSON(400, gin.H{"error": err.Error()}); return }
	c.JSON(201, gin.H{"id": id, "message": "scheduled transfer created"})
}

// DELETE /wallet/schedule/:id — cancel a pending scheduled transfer
func (h *ShopHandler) CancelSchedule(c *gin.Context) {
	userID := c.GetInt64("user_id")
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := h.Service.CancelScheduledTransfer(id, userID); err != nil {
		c.JSON(400, gin.H{"error": err.Error()}); return
	}
	c.JSON(200, gin.H{"message": "scheduled transfer cancelled"})
}

// GET /wallet/schedule — list user's scheduled transfers
func (h *ShopHandler) ListScheduled(c *gin.Context) {
	userID := c.GetInt64("user_id")
	transfers, err := h.Service.GetScheduledTransfers(userID)
	if err != nil { c.JSON(500, gin.H{"error": err.Error()}); return }
	c.JSON(200, gin.H{"scheduled": transfers})
}
