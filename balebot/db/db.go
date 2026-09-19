// Package db persists the fruit catalog (price, minimum order weight) and
// placed orders in a local SQLite file, so both the bot and the admin panel
// share the same live data.
package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS fruits (
	id             TEXT PRIMARY KEY,
	emoji          TEXT NOT NULL,
	name           TEXT NOT NULL,
	price          INTEGER NOT NULL,
	min_weight_kg  REAL NOT NULL DEFAULT 0.5,
	sort_order     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id     INTEGER NOT NULL,
	address     TEXT NOT NULL,
	phone       TEXT NOT NULL,
	items_json  TEXT NOT NULL,
	total       INTEGER NOT NULL,
	deposit     INTEGER NOT NULL,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// Store wraps the SQLite connection used by the bot and the admin panel.
type Store struct {
	conn *sql.DB
}

// Open creates/opens the SQLite file at path, applies the schema and, on a
// fresh database, seeds the default fruit catalog.
func Open(path string) (*Store, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", path, err)
	}
	// SQLite has a single writer; avoid "database is locked" errors under
	// the bot's polling loop and the admin panel hitting it concurrently.
	conn.SetMaxOpenConns(1)

	if _, err := conn.Exec(schema); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: migrate: %w", err)
	}

	store := &Store{conn: conn}
	if err := store.seedFruitsIfEmpty(); err != nil {
		conn.Close()
		return nil, err
	}
	return store, nil
}

// Close releases the underlying database connection.
func (s *Store) Close() error {
	return s.conn.Close()
}
