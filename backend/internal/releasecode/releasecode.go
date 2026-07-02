// Package releasecode implements the 4-digit delivery Release Code: generation,
// keyed hashing, constant-time verification, and the entry-attempt policy.
//
// The code is a low-entropy shared secret (10,000 possibilities) exchanged for
// the package at handoff, so its security cannot rest on the code's entropy; it
// rests on a server-held secret. Codes are persisted as HMAC-SHA256 digests
// keyed by that secret, so a database compromise alone does not let an attacker
// brute-force the 10,000-value space offline. Verification is constant-time,
// and repeated wrong entries lock the code after MaxAttempts.
package releasecode

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
)

const codeSpace = 10000

// MaxAttempts is the number of failed entries after which the code locks.
const MaxAttempts = 5

// Generate returns a uniformly random 4-digit code in the range 0000–9999.
func Generate() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(codeSpace))
	if err != nil {
		return "", fmt.Errorf("generate release code: %w", err)
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

// Hash returns the hex-encoded HMAC-SHA256 digest of code under secret.
func Hash(code string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether code matches expectedHash under secret. The digest
// comparison is constant-time.
func Verify(expectedHash, code string, secret []byte) bool {
	return hmac.Equal([]byte(Hash(code, secret)), []byte(expectedHash))
}

// RegisterFailure records one failed entry, returning the new attempt count and
// whether the code is now locked.
func RegisterFailure(current int) (attempts int, locked bool) {
	attempts = current + 1
	return attempts, attempts >= MaxAttempts
}
