package db

import (
	"database/sql"
	"strings"
)

// Customer is a Bale user's profile, captured on their first /start, plus
// a running wallet debt: money still owed after paying only the deposit on
// an order, settled later in cash/card on delivery and then zeroed out (or
// corrected) by the admin.
type Customer struct {
	ChatID     int64
	FirstName  string
	LastName   string
	WalletDebt int
}

// FullName joins first/last name for display, falling back to a dash if
// somehow both are empty.
func (c Customer) FullName() string {
	name := strings.TrimSpace(c.FirstName + " " + c.LastName)
	if name == "" {
		return "—"
	}
	return name
}

// UpsertCustomer creates a customer record (on their first /start) or
// updates their name if they re-enter it.
func (s *Store) UpsertCustomer(chatID int64, firstName, lastName string) error {
	_, err := s.conn.Exec(`
		INSERT INTO customers (chat_id, first_name, last_name) VALUES (?, ?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET first_name = excluded.first_name, last_name = excluded.last_name
	`, chatID, firstName, lastName)
	return err
}

// GetCustomer looks up a customer by chat ID, or (nil, nil) if they haven't
// gone through /start yet.
func (s *Store) GetCustomer(chatID int64) (*Customer, error) {
	var c Customer
	err := s.conn.QueryRow(`
		SELECT chat_id, first_name, last_name, wallet_debt FROM customers WHERE chat_id = ?
	`, chatID).Scan(&c.ChatID, &c.FirstName, &c.LastName, &c.WalletDebt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// AddToWalletDebt adjusts (positively or negatively) how much a customer
// still owes, creating their row if it somehow doesn't exist yet.
func (s *Store) AddToWalletDebt(chatID int64, amount int) error {
	_, err := s.conn.Exec(`
		INSERT INTO customers (chat_id, wallet_debt) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET wallet_debt = wallet_debt + excluded.wallet_debt
	`, chatID, amount)
	return err
}

// SetWalletDebt sets a customer's wallet debt to an exact amount. Used by
// the admin panel to zero it out once settled, or to correct it by hand.
func (s *Store) SetWalletDebt(chatID int64, amount int) error {
	_, err := s.conn.Exec(`
		INSERT INTO customers (chat_id, wallet_debt) VALUES (?, ?)
		ON CONFLICT(chat_id) DO UPDATE SET wallet_debt = excluded.wallet_debt
	`, chatID, amount)
	return err
}

// ListCustomersWithDebt returns every customer who currently owes money,
// largest debt first, for the admin wallet page.
func (s *Store) ListCustomersWithDebt() ([]Customer, error) {
	rows, err := s.conn.Query(`
		SELECT chat_id, first_name, last_name, wallet_debt FROM customers
		WHERE wallet_debt != 0
		ORDER BY wallet_debt DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ChatID, &c.FirstName, &c.LastName, &c.WalletDebt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
