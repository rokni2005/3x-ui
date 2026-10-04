package db

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
)

// Admin web-panel accounts. Passwords are stored as salted PBKDF2-HMAC-SHA256
// hashes; every account has full access (the point is letting the owner give
// a partner their own login, not role separation).

const pbkdf2Iterations = 120000

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,32}$`)

// ErrInvalidUsername / ErrWeakPassword / ErrUserExists / ErrLastUser are the
// validation failures the admin panel maps to user-facing messages.
var (
	ErrInvalidUsername = errors.New("invalid username (3-32 chars: letters, digits, _ . -)")
	ErrWeakPassword    = errors.New("password must be at least 8 characters")
	ErrUserExists      = errors.New("username already exists")
	ErrLastUser        = errors.New("cannot delete the last admin user")
	ErrUserNotFound    = errors.New("user not found")
)

// pbkdf2 is RFC 8018 PBKDF2 with HMAC-SHA256 (single 32-byte block, which is
// all we need), implemented here so the module doesn't depend on a newer
// Go standard library or an extra dependency.
func pbkdf2(password, salt []byte, iter int) []byte {
	mac := hmac.New(sha256.New, password)
	mac.Write(salt)
	mac.Write([]byte{0, 0, 0, 1})
	u := mac.Sum(nil)
	out := make([]byte, len(u))
	copy(out, u)
	for i := 1; i < iter; i++ {
		mac.Reset()
		mac.Write(u)
		u = mac.Sum(u[:0])
		for j := range out {
			out[j] ^= u[j]
		}
	}
	return out
}

func hashPassword(password string, salt []byte) string {
	return hex.EncodeToString(pbkdf2([]byte(password), salt, pbkdf2Iterations))
}

func validateCredentials(username, password string) error {
	if !usernamePattern.MatchString(username) {
		return ErrInvalidUsername
	}
	if len(password) < 8 {
		return ErrWeakPassword
	}
	return nil
}

// EnsureAdminUser seeds the very first admin account (from the
// ADMIN_USERNAME/ADMIN_PASSWORD env vars) when no accounts exist yet. After
// that the database is the source of truth and the env vars are ignored, so
// password changes made in the panel stick.
func (s *Store) EnsureAdminUser(username, password string) error {
	var n int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	// The seed account is trusted config, so skip the strength check but
	// still store it hashed.
	return s.insertAdminUser(username, password)
}

func (s *Store) insertAdminUser(username, password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	_, err := s.conn.Exec(`INSERT INTO admin_users (username, salt, hash) VALUES (?, ?, ?)`,
		username, hex.EncodeToString(salt), hashPassword(password, salt))
	return err
}

// CreateAdminUser adds a new panel account.
func (s *Store) CreateAdminUser(username, password string) error {
	if err := validateCredentials(username, password); err != nil {
		return err
	}
	var exists int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM admin_users WHERE username = ?`, username).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return ErrUserExists
	}
	return s.insertAdminUser(username, password)
}

// VerifyAdminUser reports whether username/password match an account. It
// does a dummy hash for unknown usernames so response time doesn't reveal
// which usernames exist.
func (s *Store) VerifyAdminUser(username, password string) (bool, error) {
	var saltHex, wantHash string
	err := s.conn.QueryRow(`SELECT salt, hash FROM admin_users WHERE username = ?`, username).Scan(&saltHex, &wantHash)
	if errors.Is(err, sql.ErrNoRows) {
		hashPassword(password, make([]byte, 16))
		return false, nil
	}
	if err != nil {
		return false, err
	}
	salt, err := hex.DecodeString(saltHex)
	if err != nil {
		return false, fmt.Errorf("corrupt salt for %q: %w", username, err)
	}
	got := hashPassword(password, salt)
	return subtle.ConstantTimeCompare([]byte(got), []byte(wantHash)) == 1, nil
}

// UpdateAdminUser changes an account's username and/or password. Empty
// newUsername keeps the current name; empty newPassword keeps the password.
func (s *Store) UpdateAdminUser(username, newUsername, newPassword string) error {
	var exists int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM admin_users WHERE username = ?`, username).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrUserNotFound
	}

	tx, err := s.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if newPassword != "" {
		if len(newPassword) < 8 {
			return ErrWeakPassword
		}
		salt := make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE admin_users SET salt = ?, hash = ? WHERE username = ?`,
			hex.EncodeToString(salt), hashPassword(newPassword, salt), username); err != nil {
			return err
		}
	}
	if newUsername != "" && newUsername != username {
		if !usernamePattern.MatchString(newUsername) {
			return ErrInvalidUsername
		}
		var taken int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM admin_users WHERE username = ?`, newUsername).Scan(&taken); err != nil {
			return err
		}
		if taken > 0 {
			return ErrUserExists
		}
		if _, err := tx.Exec(`UPDATE admin_users SET username = ? WHERE username = ?`, newUsername, username); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteAdminUser removes an account, refusing to remove the last one.
func (s *Store) DeleteAdminUser(username string) error {
	var n int
	if err := s.conn.QueryRow(`SELECT COUNT(*) FROM admin_users`).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastUser
	}
	res, err := s.conn.Exec(`DELETE FROM admin_users WHERE username = ?`, username)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// ListAdminUsers returns every account name with its creation time.
type AdminUser struct {
	Username  string
	CreatedAt string
}

func (s *Store) ListAdminUsers() ([]AdminUser, error) {
	rows, err := s.conn.Query(`SELECT username, created_at FROM admin_users ORDER BY created_at, username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminUser
	for rows.Next() {
		var u AdminUser
		if err := rows.Scan(&u.Username, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
