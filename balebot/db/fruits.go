package db

import "database/sql"

// Fruit is one catalog item. Price and MinWeightKg are the fields the admin
// panel is allowed to change.
type Fruit struct {
	ID          string
	Emoji       string
	Name        string
	Price       int
	MinWeightKg float64
	SortOrder   int
}

// ListFruits returns the catalog ordered the way it should be shown to customers.
func (s *Store) ListFruits() ([]Fruit, error) {
	rows, err := s.conn.Query(`
		SELECT id, emoji, name, price, min_weight_kg, sort_order
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
		if err := rows.Scan(&f.ID, &f.Emoji, &f.Name, &f.Price, &f.MinWeightKg, &f.SortOrder); err != nil {
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
		SELECT id, emoji, name, price, min_weight_kg, sort_order
		FROM fruits WHERE id = ?
	`, id).Scan(&f.ID, &f.Emoji, &f.Name, &f.Price, &f.MinWeightKg, &f.SortOrder)
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
