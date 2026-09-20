package db

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var safeFruitID = regexp.MustCompile(`^[a-z0-9_]+$`)

// SavePhoto writes an uploaded fruit photo to the shared photos directory
// and returns the filename to store in the fruits table. ext should include
// the leading dot (e.g. ".jpg").
func (s *Store) SavePhoto(fruitID, ext string, data io.Reader) (string, error) {
	if !safeFruitID.MatchString(fruitID) {
		return "", fmt.Errorf("db: invalid fruit id %q", fruitID)
	}
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		return "", fmt.Errorf("db: unsupported photo extension %q", ext)
	}

	if err := os.MkdirAll(s.photosDir, 0o755); err != nil {
		return "", fmt.Errorf("db: create photos dir: %w", err)
	}

	filename := fruitID + ext
	fullPath := filepath.Join(s.photosDir, filename)

	f, err := os.Create(fullPath)
	if err != nil {
		return "", fmt.Errorf("db: create photo file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, data); err != nil {
		return "", fmt.Errorf("db: write photo file: %w", err)
	}
	return filename, nil
}

// PhotoFullPath resolves a stored photo filename to its path on disk.
func (s *Store) PhotoFullPath(filename string) string {
	return filepath.Join(s.photosDir, filename)
}
