package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lib/pq"
	"markethouse/internal/models"
)

type ShopRepo struct {
	DB *sql.DB
}

// ── PRODUCTS ─────────────────────────────────────────────────────────────────

func (r *ShopRepo) CreateProduct(vendorID int64, p models.Product, imageURLs []string) (int64, error) {
	var id int64
	err := r.DB.QueryRow(`
		INSERT INTO products
		  (user_id, name, description, category, price, stock_count, is_unlimited_stock, images, is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
		RETURNING id`,
		vendorID, p.Name, p.Description, p.Category,
		p.Price, p.StockCount, p.IsUnlimitedStock,
		pq.Array(imageURLs),
	).Scan(&id)
	return id, err
}

func (r *ShopRepo) GetProductByID(id int64) (*models.Product, error) {
	p := &models.Product{}
	err := r.DB.QueryRow(`
		SELECT id, user_id, name, description, category, price,
		       stock_count, is_unlimited_stock, images, is_active, created_at, updated_at
		FROM products WHERE id=$1`, id).
		Scan(&p.ID, &p.UserID, &p.Name, &p.Description, &p.Category,
			&p.Price, &p.StockCount, &p.IsUnlimitedStock,
			pq.Array(&p.Images), &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// EnsureProductForListing bridges the newer commerce_listings flow onto the
// legacy products table that cart/checkout/orders key off. The first time a
// listing is added to a cart it gets a mirror product row; the link is kept
// in commerce_listings.product_id so later calls reuse it.
func (r *ShopRepo) EnsureProductForListing(listingID int64) (*models.Product, error) {
	var (
		pid         sql.NullInt64
		userID      int64
		title       sql.NullString
		description sql.NullString
		category    sql.NullString
		price       float64
		discount    float64
		stock       sql.NullInt64
		images      pq.StringArray
	)
	err := r.DB.QueryRow(`
		SELECT product_id, user_id, title, description, category, price,
		       COALESCE(discount_price, 0), stock, images
		FROM commerce_listings WHERE id=$1`, listingID).
		Scan(&pid, &userID, &title, &description, &category, &price,
			&discount, &stock, &images)
	if err != nil {
		return nil, fmt.Errorf("listing #%d not found", listingID)
	}
	if pid.Valid {
		if p, perr := r.GetProductByID(pid.Int64); perr == nil {
			return p, nil
		}
	}
	effective := price
	if discount > 0 && discount < price {
		effective = discount
	}
	unlimited := !stock.Valid // NULL stock on a listing == always available
	var stockCount int64
	if stock.Valid {
		stockCount = stock.Int64
	}
	var newPID int64
	if err := r.DB.QueryRow(`
		INSERT INTO products
		  (user_id, name, description, category, price, stock_count, is_unlimited_stock, images, is_active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,true)
		RETURNING id`,
		userID, title.Value, description.Value, category.Value,
		effective, stockCount, unlimited, images,
	).Scan(&newPID); err != nil {
		return nil, err
	}
	r.DB.Exec(`UPDATE commerce_listings SET product_id=$1 WHERE id=$2`, newPID, listingID)
	return r.GetProductByID(newPID)
}

func (r *ShopRepo) GetProductsByVendor(vendorID int64) ([]models.Product, error) {
	rows, err := r.DB.Query(`
		SELECT id, user_id, name, description, category, price,
		       stock_count, is_unlimited_stock, images, is_active, created_at, updated_at
		FROM products WHERE user_id=$1 AND is_active=true ORDER BY created_at DESC`, vendorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProducts(rows)
}

func (r *ShopRepo) GetPublicProducts(category string) ([]models.Product, error) {
	q := `SELECT id, user_id, name, description, category, price,
		         stock_count, is_unlimited_stock, images, is_active, created_at, updated_at
		  FROM products WHERE is_active=true`
	args := []interface{}{}
	if category != "" {
		q += ` AND category=$1`
		args = append(args, category)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := r.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProducts(rows)
}

func (r *ShopRepo) UpdateProductStock(productID int64, delta int) error {
	_, err := r.DB.Exec(`
		UPDATE products
		SET stock_count = GREATEST(0, stock_count + $1), updated_at = NOW()
		WHERE id=$2 AND is_unlimited_stock=false`, delta, productID)
	return err
}

func scanProducts(rows *sql.Rows) ([]models.Product, error) {
	var list []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.Name, &p.Description, &p.Category,
			&p.Price, &p.StockCount, &p.IsUnlimitedStock,
			pq.Array(&p.Images), &p.IsActive, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		list = append(list, p)
	}
	return list, rows.Err()
}

// ── CART ─────────────────────────────────────────────────────────────────────

func (r *ShopRepo) AddToCart(userID, productID int64, qty int) error {
	_, err := r.DB.Exec(`
		INSERT INTO cart_items (user_id, product_id, quantity)
		VALUES ($1,$2,$3)
		ON CONFLICT (user_id, product_id)
		DO UPDATE SET quantity = cart_items.quantity + EXCLUDED.quantity`,
		userID, productID, qty)
	return err
}

func (r *ShopRepo) GetCart(userID int64) ([]models.CartItem, error) {
	rows, err := r.DB.Query(`
		SELECT ci.id, ci.user_id, ci.product_id, ci.quantity, ci.created_at,
		       p.name, p.price, p.user_id, COALESCE(p.images,'{}')
		FROM cart_items ci
		JOIN products p ON p.id = ci.product_id
		WHERE ci.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []models.CartItem
	for rows.Next() {
		var it models.CartItem
		if err := rows.Scan(
			&it.ID, &it.UserID, &it.ProductID, &it.Quantity, &it.CreatedAt,
			&it.ProductName, &it.ProductPrice, &it.VendorID,
			pq.Array(&it.Images),
		); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (r *ShopRepo) RemoveFromCart(userID, cartItemID int64) error {
	_, err := r.DB.Exec(
		`DELETE FROM cart_items WHERE id=$1 AND user_id=$2`, cartItemID, userID)
	return err
}

func (r *ShopRepo) ClearCart(userID int64) error {
	_, err := r.DB.Exec(`DELETE FROM cart_items WHERE user_id=$1`, userID)
	return err
}

// ── ORDERS ───────────────────────────────────────────────────────────────────

func (r *ShopRepo) CreateOrder(o models.Order) (int64, error) {
	code := generateDeliveryCode()
	var id int64
	err := r.DB.QueryRow(`
		INSERT INTO orders
		  (buyer_id, vendor_id, product_id, quantity, total_price, escrow_amount,
		   status, delivery_date_scheduled, delivery_code)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id`,
		o.BuyerID, o.VendorID, o.ProductID, o.Quantity,
		o.TotalPrice, o.TotalPrice, models.OrderPaid,
		o.DeliveryDateScheduled, code,
	).Scan(&id)
	return id, err
}

func (r *ShopRepo) GetOrderByID(id int64) (*models.Order, error) {
	o := &models.Order{}
	err := r.DB.QueryRow(`
		SELECT o.id, o.buyer_id, o.vendor_id, o.product_id, o.quantity,
		       o.total_price, o.escrow_amount, o.status,
		       o.delivery_date_scheduled, o.delivery_code,
		       o.cancel_requested_by, o.cancel_buyer_pin,
		       o.vendor_cancel_approved, o.admin_approved,
		       o.created_at, o.updated_at,
		       p.name, u_b.full_name, u_v.full_name
		FROM orders o
		JOIN products p  ON p.id  = o.product_id
		JOIN users u_b   ON u_b.id = o.buyer_id
		JOIN users u_v   ON u_v.id = o.vendor_id
		WHERE o.id=$1`, id).
		Scan(
			&o.ID, &o.BuyerID, &o.VendorID, &o.ProductID, &o.Quantity,
			&o.TotalPrice, &o.EscrowAmount, &o.Status,
			&o.DeliveryDateScheduled, &o.DeliveryCode,
			&o.CancelRequestedBy, &o.CancelBuyerPin,
			&o.VendorCancelApproved, &o.AdminApproved,
			&o.CreatedAt, &o.UpdatedAt,
			&o.ProductName, &o.BuyerName, &o.VendorName,
		)
	if err != nil {
		return nil, err
	}
	return o, nil
}

func (r *ShopRepo) GetOrdersByBuyer(buyerID int64) ([]models.Order, error) {
	return r.queryOrders(`WHERE o.buyer_id=$1 ORDER BY o.created_at DESC`, buyerID)
}

// CreatePendingOrder — batch checkout: order sits as 'pending' until the
// shared payment reference is confirmed.
func (r *ShopRepo) CreatePendingOrder(o models.Order, reference string) (int64, string, error) {
	code := generateDeliveryCode()
	var id int64
	err := r.DB.QueryRow(`
		INSERT INTO orders
		  (buyer_id, vendor_id, product_id, quantity, total_price, escrow_amount,
		   status, delivery_date_scheduled, delivery_code, payment_reference)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, delivery_code`,
		o.BuyerID, o.VendorID, o.ProductID, o.Quantity,
		o.TotalPrice, o.TotalPrice, models.OrderPending,
		o.DeliveryDateScheduled, code, reference,
	).Scan(&id, &code)
	return id, code, err
}

func (r *ShopRepo) GetPendingOrdersByReference(buyerID int64, reference string) ([]models.Order, error) {
	return r.queryOrders(`WHERE o.payment_reference=$1 AND o.buyer_id=$2 AND o.status='pending' ORDER BY o.id ASC`, reference, buyerID)
}

func (r *ShopRepo) MarkOrderPaid(orderID int64) error {
	_, err := r.DB.Exec(
		`UPDATE orders SET status=$1, updated_at=NOW() WHERE id=$2`, models.OrderPaid, orderID)
	return err
}

func (r *ShopRepo) GetOrdersByVendor(vendorID int64) ([]models.Order, error) {
	return r.queryOrders(`WHERE o.vendor_id=$1 ORDER BY o.created_at DESC`, vendorID)
}

func (r *ShopRepo) queryOrders(where string, args ...interface{}) ([]models.Order, error) {
	rows, err := r.DB.Query(fmt.Sprintf(`
		SELECT o.id, o.buyer_id, o.vendor_id, o.product_id, o.quantity,
		       o.total_price, o.escrow_amount, o.status,
		       o.delivery_date_scheduled, o.delivery_code,
		       o.cancel_requested_by, o.cancel_buyer_pin,
		       o.vendor_cancel_approved, o.admin_approved,
		       o.created_at, o.updated_at,
		       p.name, u_b.full_name, u_v.full_name
		FROM orders o
		JOIN products p  ON p.id  = o.product_id
		JOIN users u_b   ON u_b.id = o.buyer_id
		JOIN users u_v   ON u_v.id = o.vendor_id
		%s`, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.Order
	for rows.Next() {
		var o models.Order
		if err := rows.Scan(
			&o.ID, &o.BuyerID, &o.VendorID, &o.ProductID, &o.Quantity,
			&o.TotalPrice, &o.EscrowAmount, &o.Status,
			&o.DeliveryDateScheduled, &o.DeliveryCode,
			&o.CancelRequestedBy, &o.CancelBuyerPin,
			&o.VendorCancelApproved, &o.AdminApproved,
			&o.CreatedAt, &o.UpdatedAt,
			&o.ProductName, &o.BuyerName, &o.VendorName,
		); err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

func (r *ShopRepo) UpdateOrderStatus(orderID int64, status models.OrderStatus) error {
	_, err := r.DB.Exec(
		`UPDATE orders SET status=$1, updated_at=NOW() WHERE id=$2`, status, orderID)
	return err
}

// ConfirmDelivery — vendor scans buyer's delivery_code
func (r *ShopRepo) ConfirmDelivery(orderID int64, code string) (*models.Order, error) {
	o, err := r.GetOrderByID(orderID)
	if err != nil {
		return nil, err
	}
	if o.DeliveryCode != code {
		return nil, fmt.Errorf("invalid delivery code")
	}
	if o.Status != models.OrderPaid {
		return nil, fmt.Errorf("order is not in paid state (current: %s)", o.Status)
	}
	if err := r.UpdateOrderStatus(orderID, models.OrderDelivered); err != nil {
		return nil, err
	}
	o.Status = models.OrderDelivered
	return o, nil
}

// RequestCancelOrder — buyer sets cancel pin
func (r *ShopRepo) RequestCancelOrder(orderID int64, hashedPin string) error {
	_, err := r.DB.Exec(`
		UPDATE orders
		SET cancel_requested_by='buyer', cancel_buyer_pin=$1, updated_at=NOW()
		WHERE id=$2`, hashedPin, orderID)
	return err
}

// VendorApproveCancelOrder — vendor signs off
func (r *ShopRepo) VendorApproveCancelOrder(orderID int64) error {
	_, err := r.DB.Exec(`
		UPDATE orders SET vendor_cancel_approved=true, updated_at=NOW() WHERE id=$1`, orderID)
	return err
}

// AdminApproveCancelOrder — admin finalises cancel → triggers refund
func (r *ShopRepo) AdminApproveCancelOrder(orderID int64) error {
	_, err := r.DB.Exec(`
		UPDATE orders
		SET admin_approved=true, status='cancelled', updated_at=NOW()
		WHERE id=$1`, orderID)
	return err
}

// GetOverdueOrders — orders where delivery date passed and still "paid"
func (r *ShopRepo) GetOverdueOrders() ([]models.Order, error) {
	return r.queryOrders(
		`WHERE o.status='paid' AND o.delivery_date_scheduled < NOW()`)
}

// ── WALLET ───────────────────────────────────────────────────────────────────

// WalletBalanceSQL is the single source of truth for available balance —
// the wallet_transactions ledger. Deposit/withdraw/send/scheduled all check
// against this so the displayed balance can never drift from spendable.
const WalletBalanceSQL = `
		SELECT COALESCE(SUM(CASE WHEN type IN ('credit','escrow_out','refund') THEN amount
		                          WHEN type IN ('debit','escrow_in','transfer','transfer_scheduled') THEN -amount
		                          ELSE 0 END), 0)
		FROM wallet_transactions WHERE user_id=$1`

func (r *ShopRepo) GetWalletBalance(userID int64) (*models.WalletBalance, error) {
	b := &models.WalletBalance{UserID: userID}
	r.DB.QueryRow(WalletBalanceSQL, userID).Scan(&b.AvailBalance)
	// escrow = money currently locked in paid orders as buyer
	r.DB.QueryRow(`
		SELECT COALESCE(SUM(escrow_amount),0) FROM orders
		WHERE buyer_id=$1 AND status='paid'`, userID).Scan(&b.EscrowBalance)
	return b, nil
}

func (r *ShopRepo) AddWalletTransaction(tx models.WalletTransaction) error {
	_, err := r.DB.Exec(`
		INSERT INTO wallet_transactions (user_id, order_id, type, amount, reference, description)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		tx.UserID, tx.OrderID, tx.Type, tx.Amount, tx.Reference, tx.Description)
	return err
}

func (r *ShopRepo) GetWalletHistory(userID int64) ([]models.WalletTransaction, error) {
	rows, err := r.DB.Query(`
		SELECT wt.id, wt.user_id, wt.order_id, wt.type, wt.amount, wt.reference,
		       wt.description, wt.created_at, wt.counterparty_id,
		       COALESCE(u.username, '')
		FROM wallet_transactions wt
		LEFT JOIN users u ON u.id = wt.counterparty_id
		WHERE wt.user_id=$1 ORDER BY wt.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var txs []models.WalletTransaction
	for rows.Next() {
		var t models.WalletTransaction
		if err := rows.Scan(
			&t.ID, &t.UserID, &t.OrderID, &t.Type, &t.Amount,
			&t.Reference, &t.Description, &t.CreatedAt, &t.CounterpartyID,
			&t.CounterpartyUsername,
		); err != nil {
			return nil, err
		}
		txs = append(txs, t)
	}
	return txs, rows.Err()
}

// ─────────────────────────────────────────────────────────────────────────────

func generateDeliveryCode() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b) // 16-char hex — shown as QR to buyer
}

func generateTxRef() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	ts := time.Now().UnixMilli()
	return fmt.Sprintf("MKT_%d_%s", ts, hex.EncodeToString(b))
}

// Exported for service layer
var GenerateTxRef = generateTxRef

// ── SCHEDULED TRANSFERS ──────────────────────────────────────────────────────

func (r *ShopRepo) CreateScheduledTransfer(senderID, receiverID int64, amount float64, desc string, scheduledAt time.Time) (int64, error) {
	var id int64
	err := r.DB.QueryRow(`
		INSERT INTO scheduled_transfers(sender_id, receiver_id, amount, description, scheduled_at)
		VALUES($1,$2,$3,$4,$5) RETURNING id`,
		senderID, receiverID, amount, desc, scheduledAt).Scan(&id)
	return id, err
}

func (r *ShopRepo) GetPendingScheduledTransfers() ([]models.ScheduledTransfer, error) {
	rows, err := r.DB.Query(`
		SELECT id, sender_id, receiver_id, amount, description, scheduled_at, status, created_at
		FROM scheduled_transfers
		WHERE status='pending' AND scheduled_at <= NOW()
		ORDER BY scheduled_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.ScheduledTransfer
	for rows.Next() {
		var t models.ScheduledTransfer
		if err := rows.Scan(&t.ID, &t.SenderID, &t.ReceiverID, &t.Amount,
			&t.Description, &t.ScheduledAt, &t.Status, &t.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}

func (r *ShopRepo) MarkScheduledTransferDone(id int64) error {
	_, err := r.DB.Exec(`
		UPDATE scheduled_transfers SET status='completed', executed_at=NOW() WHERE id=$1`, id)
	return err
}

func (r *ShopRepo) CancelScheduledTransfer(id, senderID int64) error {
	_, err := r.DB.Exec(`
		UPDATE scheduled_transfers SET status='cancelled'
		WHERE id=$1 AND sender_id=$2 AND status='pending'`, id, senderID)
	return err
}

func (r *ShopRepo) GetScheduledTransfersBySender(senderID int64) ([]models.ScheduledTransfer, error) {
	rows, err := r.DB.Query(`
		SELECT id, sender_id, receiver_id, amount, description, scheduled_at, status, created_at
		FROM scheduled_transfers
		WHERE sender_id=$1 AND status IN ('pending','completed')
		ORDER BY scheduled_at DESC`, senderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []models.ScheduledTransfer
	for rows.Next() {
		var t models.ScheduledTransfer
		if err := rows.Scan(&t.ID, &t.SenderID, &t.ReceiverID, &t.Amount,
			&t.Description, &t.ScheduledAt, &t.Status, &t.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, t)
	}
	return list, rows.Err()
}
