package db

import "database/sql"

// Address is one saved (address, phone) pair a customer has used before, so
// they can pick it again at checkout instead of retyping it.
type Address struct {
	ID      int64
	ChatID  int64
	Address string
	Phone   string
}

// SaveAddress records a new address/phone pair for a customer, skipping the
// insert if it's an exact duplicate of one they already have.
func (s *Store) SaveAddress(chatID int64, address, phone string) error {
	var existing int
	err := s.conn.QueryRow(`
		SELECT COUNT(*) FROM addresses WHERE chat_id = ? AND address = ? AND phone = ?
	`, chatID, address, phone).Scan(&existing)
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	_, err = s.conn.Exec(`
		INSERT INTO addresses (chat_id, address, phone) VALUES (?, ?, ?)
	`, chatID, address, phone)
	return err
}

// ListAddresses returns a customer's saved addresses, most recently used first.
func (s *Store) ListAddresses(chatID int64) ([]Address, error) {
	rows, err := s.conn.Query(`
		SELECT id, chat_id, address, phone FROM addresses
		WHERE chat_id = ?
		ORDER BY id DESC
		LIMIT 5
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Address
	for rows.Next() {
		var a Address
		if err := rows.Scan(&a.ID, &a.ChatID, &a.Address, &a.Phone); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAddress looks up one saved address by ID, or (nil, nil) if it doesn't exist.
func (s *Store) GetAddress(id int64) (*Address, error) {
	var a Address
	err := s.conn.QueryRow(`SELECT id, chat_id, address, phone FROM addresses WHERE id = ?`, id).
		Scan(&a.ID, &a.ChatID, &a.Address, &a.Phone)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}
