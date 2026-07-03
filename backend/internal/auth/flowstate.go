package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// FlowState is the server-minted context of one in-flight OIDC sign-in: the
// CSRF state echoed by the issuer, the nonce bound into the ID token, the PKCE
// verifier, and where to send the user afterwards. It travels in a short-lived
// HMAC-signed cookie so the callback needs no server-side flow storage.
type FlowState struct {
	State     string `json:"s"`
	Nonce     string `json:"n"`
	Verifier  string `json:"v"`
	Next      string `json:"next"`
	ExpiresAt int64  `json:"exp"`
}

// ErrFlowState is returned for a missing, tampered, or expired sign-in flow.
var ErrFlowState = errors.New("auth: invalid sign-in flow state")

// NewFlowState mints the random values for one sign-in attempt and the PKCE
// S256 challenge derived from its verifier.
func NewFlowState(next string, ttl time.Duration, now time.Time) (FlowState, string, error) {
	vals := make([]string, 3)
	for i := range vals {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return FlowState{}, "", fmt.Errorf("mint flow state: %w", err)
		}
		vals[i] = base64.RawURLEncoding.EncodeToString(b)
	}
	fs := FlowState{
		State:     vals[0],
		Nonce:     vals[1],
		Verifier:  vals[2],
		Next:      next,
		ExpiresAt: now.Add(ttl).Unix(),
	}
	challenge := sha256.Sum256([]byte(fs.Verifier))
	return fs, base64.RawURLEncoding.EncodeToString(challenge[:]), nil
}

// Encode serializes and signs the flow state under secret.
func (fs FlowState) Encode(secret []byte) (string, error) {
	payload, err := json.Marshal(fs)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	return body + "." + signFlow(secret, body), nil
}

// DecodeFlowState verifies and deserializes an encoded flow state, rejecting
// bad signatures and expired flows.
func DecodeFlowState(secret []byte, raw string, now time.Time) (FlowState, error) {
	body, sig, ok := strings.Cut(raw, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(signFlow(secret, body))) {
		return FlowState{}, ErrFlowState
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return FlowState{}, ErrFlowState
	}
	var fs FlowState
	if err := json.Unmarshal(payload, &fs); err != nil {
		return FlowState{}, ErrFlowState
	}
	if fs.ExpiresAt <= now.Unix() {
		return FlowState{}, ErrFlowState
	}
	return fs, nil
}

func signFlow(secret []byte, body string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
