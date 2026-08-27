package models

import "time"

// ── PRODUCT ─────────────────────────────────────────────────────────────────
// Business accounts list products (goods) or services.
// is_unlimited_stock = true → stock_count is ignored (unlimited).
// stock_count >= 0 for goods; for services it is usually set to unlimited.

type Product struct {
	ID              int64     `json:"id"`
	UserID          int64     `json:"user_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Category        string    `json:"category"` // goods | service | food | etc.
	Price           float64   `json:"price"`
	StockCount      int       `json:"stock_count"`
	IsUnlimitedStock bool     `json:"is_unlimited_stock"`
	Images          []string  `json:"images"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ── CART ITEM ────────────────────────────────────────────────────────────────
type CartItem struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	ProductID int64     `json:"product_id"`
	Quantity  int       `json:"quantity"`
	CreatedAt time.Time `json:"created_at"`

	// Joined fields (populated in queries)
	ProductName  string   `json:"product_name,omitempty"`
	ProductPrice float64  `json:"product_price,omitempty"`
	VendorID     int64    `json:"vendor_id,omitempty"`
	Images       []string `json:"images,omitempty"`
}

// ── ORDER ────────────────────────────────────────────────────────────────────
// Flow:
//  1. Buyer checks out → order created (status: pending)
//  2. Buyer pays → escrow_amount credited, status → paid
//  3. Buyer schedules delivery_date_scheduled
//  4. Vendor delivers before date → scans buyer's delivery_code → status → delivered
//     → escrow released to vendor wallet
//  5. If deadline passes without delivery → status → breached → auto-refund
//  6. Cancel: buyer requests (buyer_cancel_pin set), vendor approves
//     (vendor_cancel_approved), admin approves → status → cancelled → refund

type OrderStatus string

const (
	OrderPending    OrderStatus = "pending"
	OrderPaid       OrderStatus = "paid"
	OrderDelivered  OrderStatus = "delivered"
	OrderCancelled  OrderStatus = "cancelled"
	OrderRefunded   OrderStatus = "refunded"
	OrderBreached   OrderStatus = "breached"
)

type Order struct {
	ID                   int64       `json:"id"`
	BuyerID              int64       `json:"buyer_id"`
	VendorID             int64       `json:"vendor_id"`
	ProductID            int64       `json:"product_id"`
	Quantity             int         `json:"quantity"`
	TotalPrice           float64     `json:"total_price"`
	EscrowAmount         float64     `json:"escrow_amount"`
	Status               OrderStatus `json:"status"`
	DeliveryDateScheduled *time.Time `json:"delivery_date_scheduled"`
	DeliveryCode         string      `json:"delivery_code"`          // buyer holds this
	CancelRequestedBy    string      `json:"cancel_requested_by"`    // 'buyer' | ''
	CancelBuyerPin       string      `json:"cancel_buyer_pin"`       // hashed verification
	VendorCancelApproved bool        `json:"vendor_cancel_approved"`
	AdminApproved        bool        `json:"admin_approved"`
	CreatedAt            time.Time   `json:"created_at"`
	UpdatedAt            time.Time   `json:"updated_at"`

	// Joined
	ProductName string `json:"product_name,omitempty"`
	BuyerName   string `json:"buyer_name,omitempty"`
	VendorName  string `json:"vendor_name,omitempty"`
}

// ── WALLET TRANSACTION ───────────────────────────────────────────────────────
type WalletTxType string

const (
	TxCredit    WalletTxType = "credit"    // money in
	TxDebit     WalletTxType = "debit"     // money out
	TxEscrowIn  WalletTxType = "escrow_in" // buyer sends to escrow
	TxEscrowOut WalletTxType = "escrow_out"// escrow released to vendor
	TxRefund    WalletTxType = "refund"    // escrow returned to buyer
)

type WalletTransaction struct {
	ID          int64        `json:"id"`
	UserID      int64        `json:"user_id"`
	OrderID     *int64       `json:"order_id"`
	Type        WalletTxType `json:"type"`
	Amount      float64      `json:"amount"`
	Reference   string       `json:"reference"`
	Description string       `json:"description"`
	CreatedAt   time.Time    `json:"created_at"`
	// Set on transfer legs — the other side of the payment.
	CounterpartyID       *int64 `json:"counterparty_id,omitempty"`
	CounterpartyUsername string `json:"counterparty_username,omitempty"`
}

// ── WALLET BALANCE (view) ────────────────────────────────────────────────────
type WalletBalance struct {
	UserID        int64   `json:"user_id"`
	AvailBalance  float64 `json:"available_balance"`
	EscrowBalance float64 `json:"escrow_balance"` // locked in orders
}

// ── SCHEDULED TRANSFER ───────────────────────────────────────────────────────
type ScheduledTransfer struct {
	ID          int64     `json:"id"`
	SenderID    int64     `json:"sender_id"`
	ReceiverID  int64     `json:"receiver_id"`
	Amount      float64   `json:"amount"`
	Description string    `json:"description"`
	ScheduledAt time.Time `json:"scheduled_at"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	ExecutedAt  *time.Time `json:"executed_at,omitempty"`
}
