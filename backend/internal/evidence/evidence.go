// Package evidence is the blob store for dispute media. It writes uploaded files
// to a local directory and returns an opaque storage reference the database
// records. It is deliberately a thin filesystem stand-in for object storage: the
// production deployment swaps the implementation without touching callers.
package evidence

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ErrTooLarge is returned when an upload exceeds the caller's byte cap.
var ErrTooLarge = errors.New("evidence: file exceeds maximum size")

// FileStore persists evidence under a base directory, one subdirectory per
// pocket. Construct it with NewFileStore.
type FileStore struct {
	baseDir string
}

// NewFileStore returns a FileStore rooted at baseDir, creating the directory if
// it does not exist.
func NewFileStore(baseDir string) (*FileStore, error) {
	if err := os.MkdirAll(baseDir, 0o750); err != nil {
		return nil, fmt.Errorf("create evidence dir: %w", err)
	}
	return &FileStore{baseDir: baseDir}, nil
}

// Put streams r to a new file under the pocket's directory, refusing to write
// more than maxBytes. It returns a storage reference (relative path) and the
// number of bytes stored. The original filename is sanitised into the stored
// name; a random prefix prevents collisions.
func (s *FileStore) Put(_ context.Context, pocketID, filename string, r io.Reader, maxBytes int64) (ref string, size int64, err error) {
	dir := filepath.Join(s.baseDir, safeSegment(pocketID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", 0, fmt.Errorf("create pocket evidence dir: %w", err)
	}

	prefix, err := randomPrefix()
	if err != nil {
		return "", 0, err
	}
	name := prefix + "_" + safeSegment(filename)
	full := filepath.Join(dir, name)

	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return "", 0, fmt.Errorf("create evidence file: %w", err)
	}
	defer f.Close()

	// Read one byte past the cap so an exactly-at-cap file is accepted while an
	// over-cap file is detected and its partial write discarded.
	written, err := io.Copy(f, io.LimitReader(r, maxBytes+1))
	if err != nil {
		_ = os.Remove(full)
		return "", 0, fmt.Errorf("write evidence: %w", err)
	}
	if written > maxBytes {
		_ = os.Remove(full)
		return "", 0, ErrTooLarge
	}

	return filepath.Join(safeSegment(pocketID), name), written, nil
}

// safeSegment reduces s to a single safe path segment, so a caller-supplied
// filename or id can never escape the base directory.
func safeSegment(s string) string {
	s = filepath.Base(filepath.Clean("/" + s))
	s = strings.ReplaceAll(s, "..", "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, s)
	if s == "" || s == "." {
		return "file"
	}
	return s
}

func randomPrefix() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("evidence name entropy: %w", err)
	}
	return hex.EncodeToString(b), nil
}
