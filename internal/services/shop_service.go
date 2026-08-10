package services

import (
	"errors"
	"fmt"
	"mime/multipart"
	"time"

	"markethouse/internal/config"
	"markethouse/internal/models"
	"markethouse/internal/repository"
	"markethouse/internal/storage"
)

type ShopService struct {
	Repo     *repository.ShopRepo
	Storage  storage.Storage
	Payment  config.PaymentProvider
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

func (s *ShopService) AddToCart(userID, productID int64, qty int) error {
	if qty <= 0 {
		return errors.New("quantity must be at least 1")
	}
	prod, err := s.Repo.GetProductByID(productID)
	if err != nil {
		return errors.New("product not found")
	}
	if !prod.IsUnlimitedStock && prod.StockCount < qty {
		return fmt.Errorf("only %d left in stock", prod.StockCount)
	}
	return s.Repo.AddToCart(userID, productID, qty)
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

func (s *ShopService) CreditWallet(userID int64, amount float64, txType, desc, ref string) error {
	_, err := s.Repo.DB.Exec(`
		WITH w AS (
			INSERT INTO wallets(user_id,balance) VALUES($1,0)
			ON CONFLICT(user_id) DO UPDATE SET balance=wallets.balance+$2
			RETURNING id
		)
		INSERT INTO wallet_transactions(wallet_id,user_id,type,amount,description,reference,status)
		SELECT id,$1,$3,$2,$4,$5,'completed' FROM w`,
		userID, amount, txType, desc, ref)
	return err
}

func (s *ShopService) DebitWallet(userID int64, amount float64, txType, desc, ref string) error {
	var balance float64
	err := s.Repo.DB.QueryRow(`SELECT balance FROM wallets WHERE user_id=$1`, userID).Scan(&balance)
	if err != nil || balance < amount {
		return fmt.Errorf("insufficient balance")
	}
	_, err = s.Repo.DB.Exec(`
		WITH w AS (
			UPDATE wallets SET balance=balance-$2 WHERE user_id=$1 RETURNING id
		)
		INSERT INTO wallet_transactions(wallet_id,user_id,type,amount,description,reference,status)
		SELECT id,$1,$3,$2,$4,$5,'completed' FROM w`,
		userID, amount, txType, desc, ref)
	return err
}

func (s *ShopService) Transfer(senderID, receiverID int64, amount float64, desc string) error {
	if err := s.DebitWallet(senderID, amount, "transfer", "Sent: "+desc, ""); err != nil {
		return err
	}
	return s.CreditWallet(receiverID, amount, "credit", "Received: "+desc, "")
}
