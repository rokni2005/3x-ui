package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Order status values. An order starts pending as soon as the deposit is
// paid, moves to confirmed once the admin has checked it, and to shipped
// once it's handed to a courier (at which point we ask the customer for
// their live location so the admin can route the courier there).
const (
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

// Order is a finalized customer order (deposit receipt received).
type Order struct {
	ID          int64
	ChatID      int64
	Address     string
	Phone       string
	Items       []OrderItem
	Total       int
	Deposit     int
	Status      string
	CustomerLat *float64
	CustomerLng *float64
	CreatedAt   time.Time
}

// HasLocation reports whether the customer has shared their location for
// this order yet (relevant once the order is shipped).
func (o Order) HasLocation() bool {
	return o.CustomerLat != nil && o.CustomerLng != nil
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

// SaveOrder persists a finalized order and returns its ID.
func (s *Store) SaveOrder(o Order) (int64, error) {
	itemsJSON, err := json.Marshal(o.Items)
	if err != nil {
		return 0, err
	}
	res, err := s.conn.Exec(`
		INSERT INTO orders (chat_id, address, phone, items_json, total, deposit, status)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, o.ChatID, o.Address, o.Phone, string(itemsJSON), o.Total, o.Deposit, StatusPending)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

const orderColumns = `id, chat_id, address, phone, items_json, total, deposit, status, customer_lat, customer_lng, created_at`

func scanOrder(row interface{ Scan(...any) error }) (Order, error) {
	var o Order
	var itemsJSON string
	if err := row.Scan(&o.ID, &o.ChatID, &o.Address, &o.Phone, &itemsJSON, &o.Total, &o.Deposit, &o.Status, &o.CustomerLat, &o.CustomerLng, &o.CreatedAt); err != nil {
		return o, err
	}
	if err := json.Unmarshal([]byte(itemsJSON), &o.Items); err != nil {
		return o, err
	}
	return o, nil
}

// GetOrder looks up a single order by ID, returning (Order{}, nil) if not found.
func (s *Store) GetOrder(id int64) (*Order, error) {
	row := s.conn.QueryRow(fmt.Sprintf(`SELECT %s FROM orders WHERE id = ?`, orderColumns), id)
	o, err := scanOrder(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// ListRecentOrders returns the most recent orders, newest first, for the admin panel.
func (s *Store) ListRecentOrders(limit int) ([]Order, error) {
	rows, err := s.conn.Query(fmt.Sprintf(`SELECT %s FROM orders ORDER BY id DESC LIMIT ?`, orderColumns), limit)
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

// ListOrdersSince returns orders created at or after `since`, newest first.
// A zero `since` returns every order (still capped at limit).
func (s *Store) ListOrdersSince(since time.Time, limit int) ([]Order, error) {
	rows, err := s.conn.Query(fmt.Sprintf(`
		SELECT %s FROM orders
		WHERE created_at >= ?
		ORDER BY id DESC
		LIMIT ?
	`, orderColumns), since.UTC().Format("2006-01-02 15:04:05"), limit)
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

// OrderStats summarizes all orders for the admin panel's stats page.
type OrderStats struct {
	TotalOrders     int
	TotalRevenue    int
	TotalDeposits   int
	PendingCount    int
	ConfirmedCount  int
	ShippedCount    int
}

// Stats aggregates order counts and totals for the admin panel.
func (s *Store) Stats() (OrderStats, error) {
	var st OrderStats
	row := s.conn.QueryRow(`
		SELECT
			COUNT(*),
			COALESCE(SUM(total), 0),
			COALESCE(SUM(deposit), 0),
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'confirmed' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'shipped' THEN 1 ELSE 0 END), 0)
		FROM orders
	`)
	if err := row.Scan(&st.TotalOrders, &st.TotalRevenue, &st.TotalDeposits, &st.PendingCount, &st.ConfirmedCount, &st.ShippedCount); err != nil {
		return st, err
	}
	return st, nil
}
