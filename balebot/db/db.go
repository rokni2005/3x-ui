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
	sort_order     INTEGER NOT NULL DEFAULT 0,
	photo_path     TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS orders (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id     INTEGER NOT NULL,
	address     TEXT NOT NULL,
	phone       TEXT NOT NULL,
	items_json  TEXT NOT NULL,
	total       INTEGER NOT NULL,
	deposit     INTEGER NOT NULL,
	status      TEXT NOT NULL DEFAULT 'pending',
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS customers (
	chat_id     INTEGER PRIMARY KEY,
	first_name  TEXT NOT NULL DEFAULT '',
	last_name   TEXT NOT NULL DEFAULT '',
	wallet_debt INTEGER NOT NULL DEFAULT 0,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS addresses (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	chat_id     INTEGER NOT NULL,
	address     TEXT NOT NULL,
	phone       TEXT NOT NULL,
	created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

// Store wraps the SQLite connection used by the bot and the admin panel.
type Store struct {
	conn      *sql.DB
	photosDir string
}

// Open creates/opens the SQLite file at path, applies the schema and, on a
// fresh database, seeds the default fruit catalog. photosDir is where fruit
// display photos uploaded from the admin panel are stored on disk; both the
// admin panel and the bot read/write through this same Store, so they always
// agree on where a fruit's photo lives.
func Open(path, photosDir string) (*Store, error) {
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
	if err := addColumnIfMissing(conn, "fruits", "photo_path", "TEXT NOT NULL DEFAULT ''"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("db: migrate fruits.photo_path: %w", err)
	}
	for _, col := range []struct{ name, def string }{
		{"status", "TEXT NOT NULL DEFAULT 'pending'"},
		{"customer_lat", "REAL"},
		{"customer_lng", "REAL"},
		{"payment_verified", "INTEGER NOT NULL DEFAULT 1"},
	} {
		if err := addColumnIfMissing(conn, "orders", col.name, col.def); err != nil {
			conn.Close()
			return nil, fmt.Errorf("db: migrate orders.%s: %w", col.name, err)
		}
	}

	store := &Store{conn: conn, photosDir: photosDir}
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

// addColumnIfMissing lets us evolve the schema (e.g. adding fruits.photo_path
// to a database created before this field existed) without erroring on
// every later startup once the column is already there.
func addColumnIfMissing(conn *sql.DB, table, column, definition string) error {
	rows, err := conn.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid, notNull, pk int
			name, colType    string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	_, err = conn.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, definition))
	return err
}
