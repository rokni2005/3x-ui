package db

import "database/sql"

// Fruit is one catalog item. Price, MinWeightKg and PhotoPath are the
// fields the admin panel is allowed to change.
type Fruit struct {
	ID          string
	Emoji       string
	Name        string
	Price       int
	MinWeightKg float64
	SortOrder   int
	// PhotoPath is the filename (relative to the shared photos directory)
	// of the fruit's display photo, or "" if none has been uploaded yet.
	PhotoPath string
}

// ListFruits returns the catalog ordered the way it should be shown to customers.
func (s *Store) ListFruits() ([]Fruit, error) {
	rows, err := s.conn.Query(`
		SELECT id, emoji, name, price, min_weight_kg, sort_order, photo_path
		FROM fruits
		ORDER BY sort_order, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fruits []Fruit
	for rows.Next() {
		var f Fruit
		if err := rows.Scan(&f.ID, &f.Emoji, &f.Name, &f.Price, &f.MinWeightKg, &f.SortOrder, &f.PhotoPath); err != nil {
			return nil, err
		}
		fruits = append(fruits, f)
	}
	return fruits, rows.Err()
}

// GetFruit fetches one fruit by ID, or (nil, nil) if it doesn't exist.
func (s *Store) GetFruit(id string) (*Fruit, error) {
	var f Fruit
	err := s.conn.QueryRow(`
		SELECT id, emoji, name, price, min_weight_kg, sort_order, photo_path
		FROM fruits WHERE id = ?
	`, id).Scan(&f.ID, &f.Emoji, &f.Name, &f.Price, &f.MinWeightKg, &f.SortOrder, &f.PhotoPath)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// UpdateFruit sets a fruit's price (Toman/kg) and minimum order weight (kg)
// from the admin panel.
func (s *Store) UpdateFruit(id string, price int, minWeightKg float64) error {
	_, err := s.conn.Exec(`
		UPDATE fruits SET price = ?, min_weight_kg = ? WHERE id = ?
	`, price, minWeightKg, id)
	return err
}

// UpdateFruitPhoto sets (or clears, with photoPath = "") a fruit's display photo.
func (s *Store) UpdateFruitPhoto(id, photoPath string) error {
	_, err := s.conn.Exec(`
		UPDATE fruits SET photo_path = ? WHERE id = ?
	`, photoPath, id)
	return err
}

// AddFruit inserts a brand-new catalog item, placed after every existing
// one in display order.
func (s *Store) AddFruit(id, emoji, name string, price int, minWeightKg float64) error {
	var nextOrder int
	if err := s.conn.QueryRow(`SELECT COALESCE(MAX(sort_order), -1) + 1 FROM fruits`).Scan(&nextOrder); err != nil {
		return err
	}
	_, err := s.conn.Exec(`
		INSERT INTO fruits (id, emoji, name, price, min_weight_kg, sort_order)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, emoji, name, price, minWeightKg, nextOrder)
	return err
}

// DeleteFruit removes a catalog item. Orders that already reference it keep
// their frozen line items (items_json), so past orders are unaffected.
func (s *Store) DeleteFruit(id string) error {
	_, err := s.conn.Exec(`DELETE FROM fruits WHERE id = ?`, id)
	return err
}
