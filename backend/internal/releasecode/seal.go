package releasecode

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// ErrCiphertext is returned by Open when the stored ciphertext is malformed or
// fails authentication (wrong key or tampering).
var ErrCiphertext = errors.New("releasecode: invalid ciphertext")

// Seal encrypts code under secret and returns a hex-encoded token safe for text
// storage. It complements Hash: the hash verifies a submitted code without ever
// storing a recoverable copy, while Seal stores a recoverable copy so the
// buyer-only endpoint can reveal the plaintext on demand. Both keys live outside
// the database, so a database-only compromise recovers neither the code nor the
// ability to brute-force it.
//
// The scheme is AES-256-GCM with a random 96-bit nonce prepended to the
// ciphertext. The key is the SHA-256 of secret.
func Seal(code string, secret []byte) (string, error) {
	gcm, err := newGCM(secret)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("seal release code: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(code), nil)
	return hex.EncodeToString(sealed), nil
}

// Open reverses Seal, returning the plaintext code. It fails with ErrCiphertext
// if the token is malformed or authentication fails.
func Open(token string, secret []byte) (string, error) {
	gcm, err := newGCM(secret)
	if err != nil {
		return "", err
	}
	sealed, err := hex.DecodeString(token)
	if err != nil {
		return "", ErrCiphertext
	}
	if len(sealed) < gcm.NonceSize() {
		return "", ErrCiphertext
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", ErrCiphertext
	}
	return string(plaintext), nil
}

func newGCM(secret []byte) (cipher.AEAD, error) {
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("release code cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("release code gcm: %w", err)
	}
	return gcm, nil
}
