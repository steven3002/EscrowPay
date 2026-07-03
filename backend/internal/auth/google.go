package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ErrIDToken is returned for any ID token that fails structural, signature, or
// claim validation. The cause is wrapped for logs; callers treat it uniformly.
var ErrIDToken = errors.New("auth: invalid id token")

// Google runs the OIDC authorization-code sign-in against a Google-compatible
// issuer. Endpoints are taken from the issuer's discovery document at runtime,
// never hardcoded, so a test can stand in a fake issuer and the real one is
// always current. ID tokens are verified locally against the issuer's JWKS
// (RS256), including nonce binding and PKCE on the code exchange.
type Google struct {
	issuer       string
	clientID     string
	clientSecret string
	redirectURL  string
	client       *http.Client
	now          func() time.Time

	mu          sync.Mutex
	disco       *discovery
	keys        map[string]*rsa.PublicKey
	keysFetched time.Time
}

// GoogleConfig configures the provider. Issuer defaults to the public Google
// issuer; HTTPClient and Now default to sensible values.
type GoogleConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	HTTPClient   *http.Client
	Now          func() time.Time
}

// Identity is the verified subject a completed sign-in yields.
type Identity struct {
	Sub           string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}

type discovery struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// NewGoogle builds a provider. ClientID, ClientSecret and RedirectURL must be
// set; the caller decides whether sign-in is offered at all.
func NewGoogle(cfg GoogleConfig) *Google {
	issuer := strings.TrimSuffix(cfg.Issuer, "/")
	if issuer == "" {
		issuer = "https://accounts.google.com"
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Google{
		issuer:       issuer,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURL:  cfg.RedirectURL,
		client:       client,
		now:          now,
	}
}

// AuthCodeURL returns the issuer's authorization URL for one sign-in attempt,
// bound to the given state, nonce and PKCE challenge.
func (g *Google) AuthCodeURL(ctx context.Context, state, nonce, codeChallenge string) (string, error) {
	d, err := g.discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {g.clientID},
		"redirect_uri":          {g.redirectURL},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return d.AuthorizationEndpoint + "?" + q.Encode(), nil
}

// Exchange redeems an authorization code (with its PKCE verifier) and returns
// the verified identity from the ID token.
func (g *Google) Exchange(ctx context.Context, code, verifier, nonce string) (Identity, error) {
	d, err := g.discover(ctx)
	if err != nil {
		return Identity{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {g.redirectURL},
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Identity{}, fmt.Errorf("token exchange read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Identity{}, fmt.Errorf("token exchange: status %d", resp.StatusCode)
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.IDToken == "" {
		return Identity{}, fmt.Errorf("token exchange: no id_token")
	}
	return g.verifyIDToken(ctx, tok.IDToken, nonce)
}

// verifyIDToken validates structure, signature (RS256 against the issuer's
// JWKS), issuer, audience, expiry and nonce, returning the identity claims.
func (g *Google) verifyIDToken(ctx context.Context, raw, nonce string) (Identity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Identity{}, fmt.Errorf("%w: not a JWT", ErrIDToken)
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Identity{}, fmt.Errorf("%w: header encoding", ErrIDToken)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return Identity{}, fmt.Errorf("%w: header parse", ErrIDToken)
	}
	if header.Alg != "RS256" {
		return Identity{}, fmt.Errorf("%w: unexpected alg %q", ErrIDToken, header.Alg)
	}
	key, err := g.signingKey(ctx, header.Kid)
	if err != nil {
		return Identity{}, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Identity{}, fmt.Errorf("%w: signature encoding", ErrIDToken)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return Identity{}, fmt.Errorf("%w: signature", ErrIDToken)
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Identity{}, fmt.Errorf("%w: payload encoding", ErrIDToken)
	}
	var claims struct {
		Iss           string `json:"iss"`
		Aud           string `json:"aud"`
		Sub           string `json:"sub"`
		Exp           int64  `json:"exp"`
		Nonce         string `json:"nonce"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Identity{}, fmt.Errorf("%w: claims parse", ErrIDToken)
	}
	// Google's issuer claim historically appears both with and without the
	// scheme; both forms of the configured issuer are accepted.
	if claims.Iss != g.issuer && claims.Iss != strings.TrimPrefix(g.issuer, "https://") {
		return Identity{}, fmt.Errorf("%w: issuer %q", ErrIDToken, claims.Iss)
	}
	if claims.Aud != g.clientID {
		return Identity{}, fmt.Errorf("%w: audience", ErrIDToken)
	}
	if claims.Exp <= g.now().Unix() {
		return Identity{}, fmt.Errorf("%w: expired", ErrIDToken)
	}
	if claims.Nonce == "" || claims.Nonce != nonce {
		return Identity{}, fmt.Errorf("%w: nonce mismatch", ErrIDToken)
	}
	if claims.Sub == "" {
		return Identity{}, fmt.Errorf("%w: missing subject", ErrIDToken)
	}
	return Identity{
		Sub:           claims.Sub,
		Email:         claims.Email,
		EmailVerified: claims.EmailVerified,
		Name:          claims.Name,
		Picture:       claims.Picture,
	}, nil
}

// discover fetches and caches the issuer's OIDC discovery document.
func (g *Google) discover(ctx context.Context) (*discovery, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.disco != nil {
		return g.disco, nil
	}
	var d discovery
	if err := g.getJSON(ctx, g.issuer+"/.well-known/openid-configuration", &d); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return nil, fmt.Errorf("oidc discovery: incomplete document from %s", g.issuer)
	}
	g.disco = &d
	return g.disco, nil
}

// signingKey returns the issuer's RSA key for kid, consulting the cached JWKS
// and refetching once on an unknown kid (key rotation).
func (g *Google) signingKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	g.mu.Lock()
	key, ok := g.keys[kid]
	stale := g.now().Sub(g.keysFetched) > time.Hour
	g.mu.Unlock()
	if ok && !stale {
		return key, nil
	}
	if err := g.fetchKeys(ctx); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if key, ok := g.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("%w: unknown signing key", ErrIDToken)
}

func (g *Google) fetchKeys(ctx context.Context) error {
	d, err := g.discover(ctx)
	if err != nil {
		return err
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := g.getJSON(ctx, d.JWKSURI, &jwks); err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{
			N: new(big.Int).SetBytes(n),
			E: int(new(big.Int).SetBytes(e).Int64()),
		}
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks fetch: no usable RSA keys")
	}
	g.mu.Lock()
	g.keys = keys
	g.keysFetched = g.now()
	g.mu.Unlock()
	return nil
}

func (g *Google) getJSON(ctx context.Context, rawURL string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}
