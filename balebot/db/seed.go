package db

// defaultFruits seeds a brand-new database with the shop's initial catalog.
// Prices and minimum order weight are only defaults — they are meant to be
// adjusted afterwards from the admin panel.
var defaultFruits = []Fruit{
	{ID: "watermelon", Emoji: "🍉", Name: "هندوانه", Price: 45000, MinWeightKg: 0.5},
	{ID: "cantaloupe", Emoji: "🍈", Name: "طالبی", Price: 55000, MinWeightKg: 0.5},
	{ID: "shapasand", Emoji: "🍈", Name: "شاپسند", Price: 58000, MinWeightKg: 0.5},
	{ID: "peach", Emoji: "🍑", Name: "هلو", Price: 325000, MinWeightKg: 0.5},
	{ID: "nectarine", Emoji: "🍑", Name: "شلیل", Price: 2800000, MinWeightKg: 0.5},
	{ID: "cherry", Emoji: "🍒", Name: "گیلاس", Price: 550000, MinWeightKg: 0.5},
	{ID: "sourcherry", Emoji: "🍒", Name: "آلبالو", Price: 480000, MinWeightKg: 0.5},
	{ID: "grape", Emoji: "🍇", Name: "انگور", Price: 275000, MinWeightKg: 0.5},
	{ID: "strawberry", Emoji: "🍓", Name: "توت‌فرنگی", Price: 380000, MinWeightKg: 0.5},
	{ID: "apricot", Emoji: "🍑", Name: "زردآلو", Price: 340000, MinWeightKg: 0.5},
	{ID: "mango", Emoji: "🥭", Name: "انبه", Price: 430000, MinWeightKg: 0.5},
	{ID: "pineapple", Emoji: "🍍", Name: "آناناس", Price: 265000, MinWeightKg: 0.5},
	{ID: "kiwi", Emoji: "🥝", Name: "کیوی", Price: 290000, MinWeightKg: 0.5},
	{ID: "pear", Emoji: "🍐", Name: "گلابی", Price: 350000, MinWeightKg: 0.5},
	{ID: "apple_golab", Emoji: "🍎", Name: "سیب گلاب", Price: 340000, MinWeightKg: 0.5},
	{ID: "apple", Emoji: "🍎", Name: "سیب", Price: 310000, MinWeightKg: 0.5},
	{ID: "melon", Emoji: "🍈", Name: "خربزه", Price: 65000, MinWeightKg: 0.5},
	{ID: "fig", Emoji: "🟣", Name: "انجیر تازه", Price: 285000, MinWeightKg: 0.5},
	{ID: "plum", Emoji: "🟠", Name: "آلو (قرمز، زرد، سبز)", Price: 295000, MinWeightKg: 0.5},
}

func (s *Store) seedFruitsIfEmpty() error {
	var count int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM fruits`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO fruits (id, emoji, name, price, min_weight_kg, sort_order)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i, f := range defaultFruits {
		if _, err := stmt.Exec(f.ID, f.Emoji, f.Name, f.Price, f.MinWeightKg, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
