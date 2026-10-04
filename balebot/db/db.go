// Package db persists the fruit catalog (price, minimum order weight) and
// placed orders in a local SQLite file, so both the bot and the admin panel
// share the same live data.
package db

import (
	"database/sql"
	"fmt"
	"os"

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

CREATE TABLE IF NOT EXISTS admin_users (
	username    TEXT PRIMARY KEY,
	salt        TEXT NOT NULL,
	hash        TEXT NOT NULL,
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
	path      string
	photosDir string
}

// Path returns the filesystem path this Store was opened against.
func (s *Store) Path() string {
	return s.path
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
		{"delivery_mode", "TEXT NOT NULL DEFAULT 'tomorrow'"},
	} {
		if err := addColumnIfMissing(conn, "orders", col.name, col.def); err != nil {
			conn.Close()
			return nil, fmt.Errorf("db: migrate orders.%s: %w", col.name, err)
		}
	}

	store := &Store{conn: conn, path: path, photosDir: photosDir}
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

// Backup writes a consistent snapshot of the live database to destPath.
// Safe to call while the bot is running: VACUUM INTO takes its own read
// transaction, unlike a raw file copy of a WAL-mode database (which could
// catch a half-written page and produce a corrupt backup).
func (s *Store) Backup(destPath string) error {
	_, err := s.conn.Exec("VACUUM INTO ?", destPath)
	return err
}

// BackupSummary describes what a candidate restore file contains, so the
// admin can sanity-check it before confirming a restore.
type BackupSummary struct {
	Fruits    int
	Orders    int
	Customers int
}

// InspectBackupFile opens path read-only (a separate, short-lived
// connection — the live Store is untouched) and counts rows in the tables
// a real balebot database should have. An error here means the file isn't
// a usable balebot backup (wrong file, corrupted upload, etc.), and the
// caller should refuse to restore from it.
func InspectBackupFile(path string) (BackupSummary, error) {
	var sum BackupSummary
	conn, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(2000)")
	if err != nil {
		return sum, fmt.Errorf("open candidate file: %w", err)
	}
	defer conn.Close()

	if err := conn.QueryRow(`SELECT COUNT(*) FROM fruits`).Scan(&sum.Fruits); err != nil {
		return sum, fmt.Errorf("not a valid balebot database (fruits table): %w", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM orders`).Scan(&sum.Orders); err != nil {
		return sum, fmt.Errorf("not a valid balebot database (orders table): %w", err)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM customers`).Scan(&sum.Customers); err != nil {
		return sum, fmt.Errorf("not a valid balebot database (customers table): %w", err)
	}
	return sum, nil
}

// Restore closes the live connection and atomically replaces the store's
// own database file with stagedPath (which must be on the same filesystem
// — stage uploads next to Path() to guarantee this). It does not reopen
// the connection: the intended caller is the bot's restore flow, which
// exits the process right after so systemd restarts it fresh against the
// swapped file — safer than trying to keep serving requests through a
// live file-swap.
func (s *Store) Restore(stagedPath string) error {
	if err := s.conn.Close(); err != nil {
		return fmt.Errorf("close before restore: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(s.path + suffix) // sidecar files for the OLD data; best-effort cleanup
	}
	if err := os.Rename(stagedPath, s.path); err != nil {
		return fmt.Errorf("replace %s: %w", s.path, err)
	}
	return nil
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
