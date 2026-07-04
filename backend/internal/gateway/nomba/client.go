package nomba

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"escrowpay/internal/gateway"
)

// tokenTTLMargin is how early a cached access token is replaced. Tokens live
// about thirty minutes; renewing five minutes early keeps in-flight requests
// clear of the expiry edge.
const (
	tokenLifetime  = 30 * time.Minute
	tokenTTLMargin = 5 * time.Minute
)

// envelope is the response wrapper every endpoint returns. Code "00" is
// success; anything else is a definitive business rejection with a
// human-readable description.
type envelope struct {
	Code        string          `json:"code"`
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
}

// apiError is a non-success response, classified for the caller: definitive
// rejections unwrap to gateway.ErrRejected, transport failures that provably
// never reached the API unwrap to gateway.ErrNotSubmitted, and everything
// else stays ambiguous.
type apiError struct {
	method, path string
	status       int
	code         string
	description  string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("nomba: %s %s: http %d code %q: %s", e.method, e.path, e.status, e.code, e.description)
}

// Unwrap classifies the failure. 401 is excluded from rejection because it
// means a stale token, which the caller retries once with a fresh one; 408 and
// 429 are retryable transport conditions; 5xx is ambiguous.
func (e *apiError) Unwrap() error {
	switch {
	case e.status == http.StatusUnauthorized,
		e.status == http.StatusRequestTimeout,
		e.status == http.StatusTooManyRequests:
		return nil
	case e.status >= 400 && e.status < 500:
		return gateway.ErrRejected
	case e.status >= 200 && e.status < 300 && e.code != "" && e.code != "00":
		return gateway.ErrRejected
	default:
		return nil
	}
}

// token is one issued access token and the refresh credential that renews it.
type token struct {
	access  string
	refresh string
	renewAt time.Time
}

// bearer returns a valid access token, renewing the cached one when it is
// inside the expiry margin. Renewal prefers the refresh grant and falls back
// to a fresh client-credentials issue. The provider's expiresAt echo is not
// parsed; expiry is tracked locally against the documented lifetime.
func (c *Client) bearer(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok.access != "" && c.now().Before(c.tok.renewAt) {
		return c.tok.access, nil
	}
	if c.tok.refresh != "" {
		t, err := c.tokenGrant(ctx, "/v1/auth/token/refresh", map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": c.tok.refresh,
		})
		if err == nil {
			c.tok = t
			return t.access, nil
		}
		c.logger.Warn("nomba token refresh failed; issuing a new token", "error", err.Error())
	}
	t, err := c.tokenGrant(ctx, "/v1/auth/token/issue", map[string]string{
		"grant_type":    "client_credentials",
		"client_id":     c.cfg.ClientID,
		"client_secret": c.cfg.ClientSecret,
	})
	if err != nil {
		return "", err
	}
	c.tok = t
	return t.access, nil
}

// invalidate drops the cached token if it is still the one that just failed,
// so the retry path issues a fresh one.
func (c *Client) invalidate(access string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok.access == access {
		c.tok = token{}
	}
}

func (c *Client) tokenGrant(ctx context.Context, path string, body map[string]string) (token, error) {
	var data struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := c.do(ctx, http.MethodPost, path, "", body, &data); err != nil {
		return token{}, err
	}
	if data.AccessToken == "" {
		return token{}, fmt.Errorf("nomba: token grant %s returned no access token", path)
	}
	return token{
		access:  data.AccessToken,
		refresh: data.RefreshToken,
		renewAt: c.now().Add(tokenLifetime - tokenTTLMargin),
	}, nil
}

// call performs an authenticated request, retrying exactly once with a fresh
// token when the API reports the cached one stale.
func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	access, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	err = c.do(ctx, method, path, access, body, out)
	var apiErr *apiError
	if errors.As(err, &apiErr) && apiErr.status == http.StatusUnauthorized {
		c.invalidate(access)
		if access, err = c.bearer(ctx); err != nil {
			return err
		}
		return c.do(ctx, method, path, access, body, out)
	}
	return err
}

// do sends one request and decodes the response envelope's data into out.
func (c *Client) do(ctx context.Context, method, path, access string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("nomba: encode %s %s: %w", method, path, err)
		}
		payload = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, payload)
	if err != nil {
		return fmt.Errorf("nomba: build %s %s: %w", method, path, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("accountId", c.cfg.AccountID)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if access != "" {
		req.Header.Set("Authorization", "Bearer "+access)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if requestNeverSent(err) {
			return fmt.Errorf("nomba: %s %s: %v: %w", method, path, err, gateway.ErrNotSubmitted)
		}
		return fmt.Errorf("nomba: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("nomba: read %s %s response: %w", method, path, err)
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		env = envelope{Description: truncate(string(raw), 200)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || (env.Code != "" && env.Code != "00") {
		return &apiError{method: method, path: path, status: resp.StatusCode, code: env.Code, description: env.Description}
	}
	if out != nil && len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("nomba: decode %s %s data: %w", method, path, err)
		}
	}
	return nil
}

// requestNeverSent reports whether the transport failed before the request
// could reach the API — the only failure class that is safe to retry without
// an idempotency guarantee on the other side.
func requestNeverSent(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
