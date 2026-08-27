package services

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"mime/multipart"
	"time"

	"markethouse/internal/config"
	"markethouse/internal/models"
	"markethouse/internal/repository"
	"markethouse/internal/storage"
	"markethouse/pkg/utils"
)

type ShopService struct {
	Repo               *repository.ShopRepo
	Storage            storage.Storage
	Payment            config.PaymentProvider
	CheckedWalletSchema bool
}

// ── PRODUCTS ─────────────────────────────────────────────────────────────────

func (s *ShopService) CreateProduct(vendorID int64, p models.Product, files []*multipart.FileHeader) (int64, error) {
	if p.Name == "" {
		return 0, errors.New("product name is required")
	}
	if p.Price <= 0 {
		return 0, errors.New("price must be greater than 0")
	}
	if !p.IsUnlimitedStock && p.StockCount < 0 {
		return 0, errors.New("stock count cannot be negative")
	}

	var imageURLs []string
	for _, f := range files {
		url, err := s.Storage.Upload(f, "products", f.Filename)
		if err != nil {
			return 0, fmt.Errorf("failed to upload image: %w", err)
		}
		imageURLs = append(imageURLs, url)
	}

	return s.Repo.CreateProduct(vendorID, p, imageURLs)
}

func (s *ShopService) GetPublicProducts(category string) ([]models.Product, error) {
	return s.Repo.GetPublicProducts(category)
}

func (s *ShopService) GetMyProducts(vendorID int64) ([]models.Product, error) {
	return s.Repo.GetProductsByVendor(vendorID)
}

// ── CART ─────────────────────────────────────────────────────────────────────

func (s *ShopService) AddToCart(userID, listingID int64, qty int) error {
	if qty <= 0 {
		return errors.New("quantity must be at least 1")
	}
	// listingID is a commerce_listings.id; the mirror product row is created
	// on demand so listings created before this bridge still work.
	prod, err := s.Repo.EnsureProductForListing(listingID)
	if err != nil {
		return errors.New("listing not found")
	}
	if !prod.IsUnlimitedStock && prod.StockCount < qty {
		return fmt.Errorf("only %d left in stock", prod.StockCount)
	}
	return s.Repo.AddToCart(userID, prod.ID, qty)
}

func (s *ShopService) GetCart(userID int64) ([]models.CartItem, float64, error) {
	items, err := s.Repo.GetCart(userID)
	if err != nil {
		return nil, 0, err
	}
	var total float64
	for _, it := range items {
		total += it.ProductPrice * float64(it.Quantity)
	}
	return items, total, nil
}

func (s *ShopService) RemoveFromCart(userID, itemID int64) error {
	return s.Repo.RemoveFromCart(userID, itemID)
}

// ── CHECKOUT / PAYMENT ───────────────────────────────────────────────────────

type CheckoutRequest struct {
	ProductID             int64      `json:"product_id"`
	Quantity              int        `json:"quantity"`
	DeliveryDateScheduled *time.Time `json:"delivery_date_scheduled"`
	BuyerEmail            string
}

func (s *ShopService) Checkout(buyerID int64, req CheckoutRequest) (*CheckoutResult, error) {
	prod, err := s.Repo.GetProductByID(req.ProductID)
	if err != nil {
		return nil, errors.New("product not found")
	}
	if !prod.IsUnlimitedStock && prod.StockCount < req.Quantity {
		return nil, fmt.Errorf("only %d items in stock", prod.StockCount)
	}

	total := prod.Price * float64(req.Quantity)
	ref := repository.GenerateTxRef()

	// Initialise payment (mock always succeeds immediately)
	initResp, err := s.Payment.InitializePayment(config.InitPaymentRequest{
		AmountKobo:  int64(total * 100), // NGN kobo
		Email:       req.BuyerEmail,
		Reference:   ref,
		CallbackURL: "markethouse://payment/callback",
		Metadata:    map[string]string{"product_id": fmt.Sprint(req.ProductID)},
	})
	if err != nil {
		return nil, fmt.Errorf("payment init failed: %w", err)
	}

	return &CheckoutResult{
		Reference:        initResp.Reference,
		AuthorizationURL: initResp.AuthorizationURL,
		Total:            total,
		ProductName:      prod.Name,
		VendorID:         prod.UserID,
		Quantity:         req.Quantity,
	}, nil
}

type CheckoutResult struct {
	Reference        string
	AuthorizationURL string
	Total            float64
	ProductName      string
	VendorID         int64
	Quantity         int
}

// ── BATCH CHECKOUT (pay the whole cart / a selection in one payment) ────────

type BatchItem struct {
	ProductID int64 `json:"product_id"`
	Quantity  int   `json:"quantity"`
}

type BatchOrderOut struct {
	OrderID      int64   `json:"order_id"`
	ProductID    int64   `json:"product_id"`
	ProductName  string  `json:"product_name"`
	VendorID     int64   `json:"vendor_id"`
	Quantity     int     `json:"quantity"`
	Total        float64 `json:"total"`
	DeliveryCode string  `json:"delivery_code"`
}

type BatchCheckoutResult struct {
	Reference        string          `json:"reference"`
	AuthorizationURL string          `json:"authorization_url"`
	Total            float64         `json:"total"`
	Orders           []BatchOrderOut `json:"orders"`
}

// CheckoutBatch validates every cart item, initialises ONE payment for the
// grand total and parks each item as a 'pending' order sharing the reference.
func (s *ShopService) CheckoutBatch(buyerID int64, items []BatchItem, deliveryDate *time.Time, buyerEmail string) (*BatchCheckoutResult, error) {
	if len(items) == 0 {
		return nil, errors.New("no items to check out")
	}
	var grandTotal float64
	prods := make(map[int64]*models.Product)
	for _, it := range items {
		if it.Quantity <= 0 {
			return nil, errors.New("quantity must be at least 1")
		}
		prod, err := s.Repo.GetProductByID(it.ProductID)
		if err != nil {
			return nil, fmt.Errorf("product #%d not found", it.ProductID)
		}
		if !prod.IsUnlimitedStock && prod.StockCount < it.Quantity {
			return nil, fmt.Errorf("only %d of %s left in stock", prod.StockCount, prod.Name)
		}
		prods[it.ProductID] = prod
		grandTotal += prod.Price * float64(it.Quantity)
	}

	ref := repository.GenerateTxRef()
	initResp, err := s.Payment.InitializePayment(config.InitPaymentRequest{
		AmountKobo:  int64(grandTotal * 100),
		Email:       buyerEmail,
		Reference:   ref,
		CallbackURL: "markethouse://payment/callback",
		Metadata:    map[string]string{"batch": "true"},
	})
	if err != nil {
		return nil, fmt.Errorf("payment init failed: %w", err)
	}

	out := BatchCheckoutResult{Reference: initResp.Reference, AuthorizationURL: initResp.AuthorizationURL, Total: grandTotal}
	for _, it := range items {
		prod := prods[it.ProductID]
		total := prod.Price * float64(it.Quantity)
		id, code, err := s.Repo.CreatePendingOrder(models.Order{
			BuyerID:               buyerID,
			VendorID:              prod.UserID,
			ProductID:             prod.ID,
			Quantity:              it.Quantity,
			TotalPrice:            total,
			DeliveryDateScheduled: deliveryDate,
		}, initResp.Reference)
		if err != nil {
			return nil, fmt.Errorf("could not create order for %s: %w", prod.Name, err)
		}
		out.Orders = append(out.Orders, BatchOrderOut{
			OrderID: id, ProductID: prod.ID, ProductName: prod.Name,
			VendorID: prod.UserID, Quantity: it.Quantity, Total: total, DeliveryCode: code,
		})
	}
	return &out, nil
}

// ConfirmBatchPayment verifies the shared payment, flips every pending order
// under that reference to 'paid', moves escrow and bumps vendor sales scores.
func (s *ShopService) ConfirmBatchPayment(buyerID int64, reference string) ([]models.Order, error) {
	verify, err := s.Payment.VerifyPayment(reference)
	if err != nil || !verify.Paid {
		return nil, errors.New("payment verification failed")
	}
	orders, err := s.Repo.GetPendingOrdersByReference(buyerID, reference)
	if err != nil || len(orders) == 0 {
		return nil, errors.New("no pending orders for this payment")
	}
	for i := range orders {
		o := orders[i]
		if err := s.Repo.MarkOrderPaid(o.ID); err != nil {
			continue
		}
		_ = s.Repo.UpdateProductStock(o.ProductID, -o.Quantity)
		_ = s.Repo.AddWalletTransaction(models.WalletTransaction{
			UserID:      o.BuyerID,
			OrderID:     &o.ID,
			Type:        models.TxEscrowIn,
			Amount:      o.EscrowAmount,
			Description: fmt.Sprintf("Escrow for order #%d — %s", o.ID, o.ProductName),
		})
		_, _ = s.Repo.DB.Exec(`UPDATE users SET sales_score = sales_score + 1 WHERE id=$1`, o.VendorID)
		orders[i].Status = models.OrderPaid
	}
	return orders, nil
}

// ConfirmPayment — called after the payment gateway callback/webhook.
// Verifies with the provider, creates the order, deducts stock, records wallet tx.
func (s *ShopService) ConfirmPayment(buyerID int64, req CheckoutRequest, reference string) (*models.Order, error) {
	// Verify with provider
	verify, err := s.Payment.VerifyPayment(reference)
	if err != nil || !verify.Paid {
		return nil, errors.New("payment verification failed")
	}

	prod, err := s.Repo.GetProductByID(req.ProductID)
	if err != nil {
		return nil, errors.New("product not found")
	}
	total := prod.Price * float64(req.Quantity)

	// Create order (escrow held by app)
	order := models.Order{
		BuyerID:               buyerID,
		VendorID:              prod.UserID,
		ProductID:             req.ProductID,
		Quantity:              req.Quantity,
		TotalPrice:            total,
		EscrowAmount:          total,
		Status:                models.OrderPaid,
		DeliveryDateScheduled: req.DeliveryDateScheduled,
	}
	orderID, err := s.Repo.CreateOrder(order)
	if err != nil {
		return nil, fmt.Errorf("order creation failed: %w", err)
	}

	// Every completed payment bumps the vendor's sales score (the number on
	// their business badge).
	_, _ = s.Repo.DB.Exec(`UPDATE users SET sales_score = sales_score + 1 WHERE id=$1`, prod.UserID)

	// Deduct stock (for limited goods)
	_ = s.Repo.UpdateProductStock(req.ProductID, -req.Quantity)

	// Record wallet escrow transaction for buyer
	_ = s.Repo.AddWalletTransaction(models.WalletTransaction{
		UserID:      buyerID,
		OrderID:     &orderID,
		Type:        models.TxEscrowIn,
		Amount:      total,
		Reference:   reference,
		Description: fmt.Sprintf("Escrow for order #%d — %s", orderID, prod.Name),
	})

	return s.Repo.GetOrderByID(orderID)
}

// ── DELIVERY ─────────────────────────────────────────────────────────────────

// ConfirmDelivery — vendor scans QR code (delivery_code) from buyer.
// Releases escrow to vendor.
func (s *ShopService) ConfirmDelivery(vendorID, orderID int64, deliveryCode string) error {
	order, err := s.Repo.ConfirmDelivery(orderID, deliveryCode)
	if err != nil {
		return err
	}
	if order.VendorID != vendorID {
		return errors.New("not your order")
	}

	// Release escrow to vendor wallet
	return s.Repo.AddWalletTransaction(models.WalletTransaction{
		UserID:      vendorID,
		OrderID:     &orderID,
		Type:        models.TxEscrowOut,
		Amount:      order.EscrowAmount,
		Reference:   repository.GenerateTxRef(),
		Description: fmt.Sprintf("Payment released for order #%d", orderID),
	})
}

// ProcessOverdueOrders — run this as a cron / background job.
// Refunds buyers whose delivery date has passed.
func (s *ShopService) ProcessOverdueOrders() error {
	orders, err := s.Repo.GetOverdueOrders()
	if err != nil {
		return err
	}
	for _, o := range orders {
		if err := s.Repo.UpdateOrderStatus(o.ID, models.OrderBreached); err != nil {
			continue
		}
		// Refund buyer
		_ = s.Repo.AddWalletTransaction(models.WalletTransaction{
			UserID:      o.BuyerID,
			OrderID:     &o.ID,
			Type:        models.TxRefund,
			Amount:      o.EscrowAmount,
			Reference:   repository.GenerateTxRef(),
			Description: fmt.Sprintf("Refund — delivery breach on order #%d", o.ID),
		})
		// Restore stock
		_ = s.Repo.UpdateProductStock(o.ProductID, o.Quantity)
	}
	return nil
}

// ── CANCEL ORDER ─────────────────────────────────────────────────────────────

func (s *ShopService) RequestCancel(buyerID, orderID int64, hashedPin string) error {
	order, err := s.Repo.GetOrderByID(orderID)
	if err != nil {
		return errors.New("order not found")
	}
	if order.BuyerID != buyerID {
		return errors.New("not your order")
	}
	if order.Status != models.OrderPaid {
		return fmt.Errorf("cannot cancel order in state: %s", order.Status)
	}
	return s.Repo.RequestCancelOrder(orderID, hashedPin)
}

func (s *ShopService) VendorApproveCancel(vendorID, orderID int64) error {
	order, err := s.Repo.GetOrderByID(orderID)
	if err != nil {
		return errors.New("order not found")
	}
	if order.VendorID != vendorID {
		return errors.New("not your order")
	}
	return s.Repo.VendorApproveCancelOrder(orderID)
}

func (s *ShopService) AdminApproveCancel(orderID int64) error {
	order, err := s.Repo.GetOrderByID(orderID)
	if err != nil {
		return errors.New("order not found")
	}
	if !order.VendorCancelApproved {
		return errors.New("vendor has not approved the cancellation yet")
	}
	if err := s.Repo.AdminApproveCancelOrder(orderID); err != nil {
		return err
	}
	// Refund buyer
	return s.Repo.AddWalletTransaction(models.WalletTransaction{
		UserID:      order.BuyerID,
		OrderID:     &orderID,
		Type:        models.TxRefund,
		Amount:      order.EscrowAmount,
		Reference:   repository.GenerateTxRef(),
		Description: fmt.Sprintf("Refund — cancelled order #%d", orderID),
	})
}

// ── WALLET ───────────────────────────────────────────────────────────────────

func (s *ShopService) GetWallet(userID int64) (*models.WalletBalance, error) {
	return s.Repo.GetWalletBalance(userID)
}

func (s *ShopService) GetWalletHistory(userID int64) ([]models.WalletTransaction, error) {
	return s.Repo.GetWalletHistory(userID)
}

func (s *ShopService) CreditWallet(userID int64, amount float64, txType, desc, ref string, counterparty int64) error {
	if ref == "" {
		ref = repository.GenerateTxRef()
	}
	log.Printf("[CREDIT] user=%d amt=%.2f type=%s ref=%s", userID, amount, txType, ref)
	_, err := s.Repo.DB.Exec(`
		INSERT INTO wallet_transactions(user_id,type,amount,description,reference,status,counterparty_id)
		VALUES($1,$2,$3,$4,$5,'completed',NULLIF($6,0))`,
		userID, txType, amount, desc, ref, counterparty)
	if err != nil {
		log.Printf("[CREDIT] SQL ERROR: %v", err)
	}
	return err
}

func (s *ShopService) DebitWallet(userID int64, amount float64, txType, desc, ref string, counterparty int64) error {
	if ref == "" {
		ref = repository.GenerateTxRef()
	}
	tx, err := s.Repo.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`SELECT pg_advisory_xact_lock($1)`, userID); err != nil {
		return err
	}
	var balance float64
	if err := tx.QueryRow(repository.WalletBalanceSQL, userID).Scan(&balance); err != nil {
		return fmt.Errorf("could not read balance: %w", err)
	}
	if balance < amount {
		return fmt.Errorf("insufficient balance")
	}
	if _, err := tx.Exec(`
		INSERT INTO wallet_transactions(user_id,type,amount,description,reference,status,counterparty_id)
		VALUES($1,$2,$3,$4,$5,'completed',NULLIF($6,0))`,
		userID, txType, amount, desc, ref, counterparty); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *ShopService) Transfer(senderID, receiverID int64, amount float64, desc, pin string) (string, error) {
	// Enforce the optional transfer password.
	var hash sql.NullString
	if err := s.Repo.DB.QueryRow(`SELECT transfer_pin_hash FROM users WHERE id=$1`, senderID).Scan(&hash); err == nil && hash.Valid && hash.String != "" {
		if !utils.CheckPasswordHash(pin, hash.String) {
			return "", errors.New("incorrect transfer password")
		}
	}
	// One shared reference for both legs — the sender's receipt and the
	// receiver's credit can be matched on it.
	ref := repository.GenerateTxRef()
	if err := s.DebitWallet(senderID, amount, "transfer", "Sent: "+desc, ref, receiverID); err != nil {
		return "", err
	}
	return ref, s.CreditWallet(receiverID, amount, "credit", "Received: "+desc, ref, senderID)
}

// ── SCHEDULED TRANSFERS ──────────────────────────────────────────────────────

func (s *ShopService) CreateScheduledTransfer(senderID, receiverID int64, amount float64, desc string, scheduledAt time.Time) (int64, error) {
	// Verify sender has enough balance
	balance, err := s.Repo.GetWalletBalance(senderID)
	if err != nil {
		return 0, fmt.Errorf("could not check balance: %w", err)
	}
	if balance.AvailBalance < amount {
		return 0, fmt.Errorf("insufficient balance (have ₦%.2f, need ₦%.2f)", balance.AvailBalance, amount)
	}
	// Verify scheduled time is in the future
	if scheduledAt.Before(time.Now()) {
		return 0, fmt.Errorf("scheduled time must be in the future")
	}
	// Verify receiver exists
	_, err = s.Repo.GetWalletBalance(receiverID)
	if err != nil {
		return 0, fmt.Errorf("receiver not found")
	}

	// Debit sender immediately (held until execution)
	if err := s.DebitWallet(senderID, amount, "transfer_scheduled",
		fmt.Sprintf("Scheduled transfer to @%s — %s", s.usernameOf(receiverID), desc), "", receiverID); err != nil {
		return 0, err
	}

	return s.Repo.CreateScheduledTransfer(senderID, receiverID, amount, desc, scheduledAt)
}

func (s *ShopService) ProcessScheduledTransfers() error {
	pending, err := s.Repo.GetPendingScheduledTransfers()
	if err != nil {
		return err
	}
	for _, t := range pending {
		// Credit receiver (money was already debited from sender at creation)
		if err := s.CreditWallet(t.ReceiverID, t.Amount, "credit",
			fmt.Sprintf("Scheduled transfer from @%s — %s", s.usernameOf(t.SenderID), t.Description), "", t.SenderID); err != nil {
			continue
		}
		_ = s.Repo.MarkScheduledTransferDone(t.ID)
	}
	return nil
}

// usernameOf resolves a display username for ledger descriptions.
func (s *ShopService) usernameOf(userID int64) string {
	var u string
	_ = s.Repo.DB.QueryRow(`SELECT COALESCE(username,'') FROM users WHERE id=$1`, userID).Scan(&u)
	if u == "" {
		return fmt.Sprintf("user%d", userID)
	}
	return u
}

// TransferPinEnabled reports whether the user has a transfer password set.
func (s *ShopService) TransferPinEnabled(userID int64) bool {
	var hash sql.NullString
	if err := s.Repo.DB.QueryRow(`SELECT transfer_pin_hash FROM users WHERE id=$1`, userID).Scan(&hash); err != nil {
		return false
	}
	return hash.Valid && hash.String != ""
}

// SetTransferPin stores (or rotates) the bcrypt-hashed transfer password.
// When one already exists, currentPin must match.
func (s *ShopService) SetTransferPin(userID int64, currentPin, newPin string) error {
	var hash sql.NullString
	err := s.Repo.DB.QueryRow(`SELECT transfer_pin_hash FROM users WHERE id=$1`, userID).Scan(&hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && err.Error() != "sql: no rows in result set" {
		return err
	}
	if hash.Valid && hash.String != "" && !utils.CheckPasswordHash(currentPin, hash.String) {
		return errors.New("incorrect current transfer password")
	}
	hashed, err := utils.HashPassword(newPin)
	if err != nil {
		return err
	}
	_, err = s.Repo.DB.Exec(`UPDATE users SET transfer_pin_hash=$2 WHERE id=$1`, userID, hashed)
	return err
}

func (s *ShopService) CancelScheduledTransfer(id, senderID int64) error {
	// Get the transfer first to know the amount
	transfers, err := s.Repo.GetScheduledTransfersBySender(senderID)
	if err != nil {
		return err
	}
	for _, t := range transfers {
		if t.ID == id && t.Status == "pending" {
			// Refund sender
			if err := s.CreditWallet(senderID, t.Amount, "refund",
				fmt.Sprintf("Cancelled scheduled transfer #%d", id), "", 0); err != nil {
				return err
			}
			return s.Repo.CancelScheduledTransfer(id, senderID)
		}
	}
	return fmt.Errorf("scheduled transfer not found or not cancellable")
}

func (s *ShopService) GetScheduledTransfers(senderID int64) ([]models.ScheduledTransfer, error) {
	return s.Repo.GetScheduledTransfersBySender(senderID)
}
