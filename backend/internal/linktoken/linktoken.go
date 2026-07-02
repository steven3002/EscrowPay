// Package linktoken issues and verifies role-scoped access tokens for a pocket.
// It is EscrowPay's demo-grade authentication: every pocket link a participant
// receives carries one of these tokens, which names exactly one (pocket, role)
// pair. Production authentication replaces this package wholesale; nothing
// outside it should assume how tokens are shaped.
//
// A token is stateless and self-authenticating: its signature is an
// HMAC-SHA256 over the claims under a server-held secret, so the server trusts
// the (pocket, role) it names without a database lookup. A random nonce makes
// each mint unique, so the SHA-256 of the token (see Hash) is a meaningful
// per-participant handle the store can persist and later match or revoke.
package linktoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidToken is returned by Parse for any malformed or unauthentic token.
var ErrInvalidToken = errors.New("linktoken: invalid token")

// Claims are the identity a token asserts: one role within one pocket.
type Claims struct {
	PocketID string
	Role     string
}

// Minter issues and verifies tokens under a fixed secret.
type Minter struct {
	secret []byte
}

// NewMinter returns a Minter keyed by secret. The secret must be non-empty.
func NewMinter(secret []byte) *Minter {
	return &Minter{secret: secret}
}

// Mint returns a fresh token for (pocketID, role) and the SHA-256 hash the
// caller should persist against the participant row. Two mints for the same
// pair differ, because each carries a random nonce.
func (m *Minter) Mint(pocketID, role string) (token, hash string, err error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", fmt.Errorf("mint link token: %w", err)
	}
	payload := strings.Join([]string{pocketID, role, hex.EncodeToString(nonce)}, ".")
	sig := m.sign(payload)
	token = payload + "." + sig
	return token, Hash(token), nil
}

// Parse verifies the token's signature and returns its claims. It does not
// consult any store; binding a token to a specific participant row is the
// caller's concern (compare Hash(token) to the stored hash).
func (m *Minter) Parse(token string) (Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return Claims{}, ErrInvalidToken
	}
	pocketID, role, nonce, sig := parts[0], parts[1], parts[2], parts[3]
	if pocketID == "" || role == "" || nonce == "" {
		return Claims{}, ErrInvalidToken
	}
	payload := strings.Join([]string{pocketID, role, nonce}, ".")
	if !hmac.Equal([]byte(sig), []byte(m.sign(payload))) {
		return Claims{}, ErrInvalidToken
	}
	return Claims{PocketID: pocketID, Role: role}, nil
}

func (m *Minter) sign(payload string) string {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// Hash returns the hex-encoded SHA-256 of a token. The store persists this, not
// the token, so a database-only compromise cannot present a valid link.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EqualHash compares two token hashes in constant time.
func EqualHash(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
