package db

import (
	"encoding/json"
	"time"
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
	ID        int64
	ChatID    int64
	Address   string
	Phone     string
	Items     []OrderItem
	Total     int
	Deposit   int
	CreatedAt time.Time
}

// SaveOrder persists a finalized order and returns its ID.
func (s *Store) SaveOrder(o Order) (int64, error) {
	itemsJSON, err := json.Marshal(o.Items)
	if err != nil {
		return 0, err
	}
	res, err := s.conn.Exec(`
		INSERT INTO orders (chat_id, address, phone, items_json, total, deposit)
		VALUES (?, ?, ?, ?, ?, ?)
	`, o.ChatID, o.Address, o.Phone, string(itemsJSON), o.Total, o.Deposit)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListRecentOrders returns the most recent orders, newest first, for the admin panel.
func (s *Store) ListRecentOrders(limit int) ([]Order, error) {
	rows, err := s.conn.Query(`
		SELECT id, chat_id, address, phone, items_json, total, deposit, created_at
		FROM orders
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orders []Order
	for rows.Next() {
		var o Order
		var itemsJSON string
		if err := rows.Scan(&o.ID, &o.ChatID, &o.Address, &o.Phone, &itemsJSON, &o.Total, &o.Deposit, &o.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(itemsJSON), &o.Items); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}
