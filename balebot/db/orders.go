package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Order status values. An order is created as a draft the moment the
// customer taps a "pay" button (before any money has actually moved), so
// its cart/address/phone survive a bot restart mid-payment; it becomes
// pending once payment is confirmed, moves to confirmed once the admin has
// checked it, and to shipped once it's handed to a courier (at which point
// we ask the customer for their live location so the admin can route the
// courier there). Drafts are excluded from every admin-facing list/stat
// query below (orderListFilter) since nothing has actually happened yet.
const (
	StatusDraft     = "draft"
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
	StatusShipped   = "shipped"
)

// OrderItem is a line of a placed order, with the price frozen at the
// moment the customer added it to their cart.
type OrderItem struct {
	FruitID    string  `json:"fruit_id"`
	Emoji      string  `json:"emoji"`
	Name       string  `json:"name"`
	WeightKg   float64 `json:"weight_kg"`
	PricePerKg int     `json:"price_per_kg"`
}

// Order is a customer order, possibly still a draft (see Status).
type Order struct {
	ID          int64
	ChatID      int64
	Address     string
	Phone       string
	Items       []OrderItem
	Total       int
	Deposit     int // amount actually confirmed paid up front (0 for an unverified card-to-card receipt until the admin confirms it)
	Status      string
	CustomerLat *float64
	CustomerLng *float64
	// PaymentVerified is true for online wallet payments (Bale confirms the
	// exact amount) and false for a manual card-to-card transfer until the
	// admin reviews the receipt photo and records the real amount via
	// ConfirmManualPayment.
	PaymentVerified bool
	CustomerName    string // from the customers table; "" if the customer never set a name
	CreatedAt       time.Time
}

// Remaining is how much of Total is still owed after the up-front payment
// (0 once the customer paid in full, or the whole Total while an unverified
// manual receipt is still awaiting the admin's review).
func (o Order) Remaining() int {
	r := o.Total - o.Deposit
	if r < 0 {
		return 0
	}
	return r
}

// HasLocation reports whether the customer has shared their location for
// this order yet (relevant once the order is shipped).
func (o Order) HasLocation() bool {
	return o.CustomerLat != nil && o.CustomerLng != nil
}

// ItemsTotal sums the frozen line prices of Items, i.e. Total minus the
// delivery fee.
func (o Order) ItemsTotal() int {
	sum := 0
	for _, item := range o.Items {
		sum += int(item.WeightKg * float64(item.PricePerKg))
	}
	return sum
}

// DeliveryFee is the delivery portion of Total (0 for a free-delivery order).
func (o Order) DeliveryFee() int {
	return o.Total - o.ItemsTotal()
}

// StatusLabel renders Status in Persian for display in the admin panel and
// bot messages.
func (o Order) StatusLabel() string {
	switch o.Status {
	case StatusConfirmed:
		return "✅ تایید شده"
	case StatusShipped:
		return "🚚 ارسال شده"
	default:
		return "⏳ در انتظار تایید"
	}
}

// CreateDraftOrder persists a checkout attempt the moment the customer taps
// a "pay" button, before any payment has actually happened. This is the
// source of truth finalizeOrder reads back from once payment is confirmed,
// so an in-progress checkout survives a bot restart even though the
// in-memory session cart doesn't. verified should be true for an online
// wallet payment (Bale will confirm the exact amount) and false for a
// manual card-to-card transfer (nothing is confirmed until the admin
// reviews the receipt).
func (s *Store) CreateDraftOrder(o Order) (int64, error) {
	itemsJSON, err := json.Marshal(o.Items)
	if err != nil {
		return 0, err
	}
	res, err := s.conn.Exec(`
		INSERT INTO orders (chat_id, address, phone, items_json, total, deposit, status, payment_verified)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, o.ChatID, o.Address, o.Phone, string(itemsJSON), o.Total, o.Deposit, StatusDraft, boolToInt(o.PaymentVerified))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SaveOrder persists an already-finalized order directly (bypassing the
// draft stage). Used only as a fallback when finalizeOrder can't find a
// matching draft to promote.
func (s *Store) SaveOrder(o Order) (int64, error) {
	itemsJSON, err := json.Marshal(o.Items)
	if err != nil {
		return 0, err
	}
	res, err := s.conn.Exec(`
		INSERT INTO orders (chat_id, address, phone, items_json, total, deposit, status, payment_verified)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, o.ChatID, o.Address, o.Phone, string(itemsJSON), o.Total, o.Deposit, StatusPending, boolToInt(o.PaymentVerified))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetLatestDraftOrder returns a customer's most recent unpaid draft, or
// (nil, nil) if they don't have one (e.g. it was already finalized, or
// they never reached the payment step).
func (s *Store) GetLatestDraftOrder(chatID int64) (*Order, error) {
	row := s.conn.QueryRow(fmt.Sprintf(`
		SELECT %s FROM %s WHERE o.chat_id = ? AND o.status = '%s' ORDER BY o.id DESC LIMIT 1
	`, orderColumns, orderFromJoin, StatusDraft), chatID)
	o, err := scanOrder(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

const orderColumns = `o.id, o.chat_id, o.address, o.phone, o.items_json, o.total, o.deposit, o.status, o.customer_lat, o.customer_lng, o.payment_verified, o.created_at, COALESCE(c.first_name, ''), COALESCE(c.last_name, '')`

const orderFromJoin = `orders o LEFT JOIN customers c ON c.chat_id = o.chat_id`

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanOrder(row interface{ Scan(...any) error }) (Order, error) {
	var o Order
	var itemsJSON, firstName, lastName string
	var verified int
	if err := row.Scan(&o.ID, &o.ChatID, &o.Address, &o.Phone, &itemsJSON, &o.Total, &o.Deposit, &o.Status, &o.CustomerLat, &o.CustomerLng, &verified, &o.CreatedAt, &firstName, &lastName); err != nil {
		return o, err
	}
	if err := json.Unmarshal([]byte(itemsJSON), &o.Items); err != nil {
		return o, err
	}
	o.PaymentVerified = verified != 0
	o.CustomerName = strings.TrimSpace(firstName + " " + lastName)
	return o, nil
}

// GetOrder looks up a single order by ID, returning (Order{}, nil) if not found.
func (s *Store) GetOrder(id int64) (*Order, error) {
	row := s.conn.QueryRow(fmt.Sprintf(`SELECT %s FROM %s WHERE o.id = ?`, orderColumns, orderFromJoin), id)
	o, err := scanOrder(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListRecentOrders returns the most recent non-draft orders, newest first,
// for the admin panel.
func (s *Store) ListRecentOrders(limit int) ([]Order, error) {
	rows, err := s.conn.Query(fmt.Sprintf(`
		SELECT %s FROM %s WHERE o.status != '%s' ORDER BY o.id DESC LIMIT ?
	`, orderColumns, orderFromJoin, StatusDraft), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// ListOrdersSince returns non-draft orders created at or after `since`,
// newest first. A zero `since` returns every order (still capped at limit).
func (s *Store) ListOrdersSince(since time.Time, limit int) ([]Order, error) {
	rows, err := s.conn.Query(fmt.Sprintf(`
		SELECT %s FROM %s
		WHERE o.status != '%s' AND o.created_at >= ?
		ORDER BY o.id DESC
		LIMIT ?
	`, orderColumns, orderFromJoin, StatusDraft), since.UTC().Format("2006-01-02 15:04:05"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

// UpdateOrderStatus moves an order to a new status (see the Status*
// constants). It doesn't validate the transition; callers decide which
// buttons make sense to show for the order's current status.
func (s *Store) UpdateOrderStatus(id int64, status string) error {
	_, err := s.conn.Exec(`UPDATE orders SET status = ? WHERE id = ?`, status, id)
	return err
}

// SaveOrderLocation records the customer's shared live location for an
// order, once it's been shipped and the admin needs to route a courier.
func (s *Store) SaveOrderLocation(id int64, lat, lng float64) error {
	_, err := s.conn.Exec(`UPDATE orders SET customer_lat = ?, customer_lng = ? WHERE id = ?`, lat, lng, id)
	return err
}

// ConfirmManualPayment records the amount the admin actually saw arrive for
// a card-to-card receipt: it sets the order's confirmed Deposit, marks it
// verified, and credits the customer's wallet debt by that same amount
// (the debt was set to the full order total by default when the unverified
// receipt was first finalized).
func (s *Store) ConfirmManualPayment(orderID int64, amount int) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var chatID int64
	if err := tx.QueryRow(`SELECT chat_id FROM orders WHERE id = ?`, orderID).Scan(&chatID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE orders SET deposit = ?, payment_verified = 1 WHERE id = ?`, amount, orderID); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO customers (chat_id, wallet_debt) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET wallet_debt = wallet_debt + excluded.wallet_debt
	`, chatID, -amount); err != nil {
		return err
	}
	return tx.Commit()
}

// OrderStats summarizes all non-draft orders for the admin panel's stats page.
type OrderStats struct {
	TotalOrders    int
	TotalRevenue   int
	TotalDeposits  int
	PendingCount   int
	ConfirmedCount int
	ShippedCount   int
}

// Stats aggregates order counts and totals for the admin panel.
func (s *Store) Stats() (OrderStats, error) {
	var st OrderStats
	row := s.conn.QueryRow(fmt.Sprintf(`
		SELECT
			COUNT(*),
			COALESCE(SUM(total), 0),
			COALESCE(SUM(deposit), 0),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'shipped' THEN 1 ELSE 0 END), 0)
		FROM orders
		WHERE status != '%s'
	`, StatusDraft))
	if err := row.Scan(&st.TotalOrders, &st.TotalRevenue, &st.TotalDeposits, &st.PendingCount, &st.ConfirmedCount, &st.ShippedCount); err != nil {
		return st, err
	}
	return st, nil
}
