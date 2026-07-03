package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// fakeIssuer is a minimal OIDC provider: discovery, JWKS, and a token endpoint
// that returns an ID token signed with a locally generated RSA key. It proves
// the full sign-in path — discovery, PKCE exchange, signature and claim
// verification — without Google credentials.
type fakeIssuer struct {
	srv      *httptest.Server
	key      *rsa.PrivateKey
	clientID string

	lastForm url.Values
	idToken  func(issuerURL string) string
}

func newFakeIssuer(t *testing.T, clientID string) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: key, clientID: clientID}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":                 f.srv.URL,
			"authorization_endpoint": f.srv.URL + "/authorize",
			"token_endpoint":         f.srv.URL + "/token",
			"jwks_uri":               f.srv.URL + "/jwks",
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := &f.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": "test-key",
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.lastForm = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": f.idToken(f.srv.URL)})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// signToken produces an RS256 JWT over the given claims.
func (f *fakeIssuer) signToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key"})
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	signing := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signing + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func stdClaims(issuer, clientID, nonce string) map[string]any {
	return map[string]any{
		"iss":            issuer,
		"aud":            clientID,
		"sub":            "google-sub-1",
		"exp":            time.Now().Add(time.Hour).Unix(),
		"nonce":          nonce,
		"email":          "ada@example.com",
		"email_verified": true,
		"name":           "Ada Stores",
		"picture":        "https://example.com/a.png",
	}
}

func newProvider(f *fakeIssuer) *Google {
	return NewGoogle(GoogleConfig{
		Issuer:       f.srv.URL,
		ClientID:     f.clientID,
		ClientSecret: "secret",
		RedirectURL:  "http://localhost:3000/api/auth/google/callback",
	})
}

func TestGoogleSignInFlow(t *testing.T) {
	f := newFakeIssuer(t, "client-1")
	g := newProvider(f)

	fs, challenge, err := NewFlowState("/dashboard", 10*time.Minute, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	authURL, err := g.AuthCodeURL(context.Background(), fs.State, fs.Nonce, challenge)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("state") != fs.State || q.Get("nonce") != fs.Nonce {
		t.Fatalf("auth url missing state/nonce: %s", authURL)
	}
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") != challenge {
		t.Fatalf("auth url missing PKCE challenge: %s", authURL)
	}
	if !strings.HasPrefix(authURL, f.srv.URL+"/authorize") {
		t.Fatalf("auth url does not target the discovered endpoint: %s", authURL)
	}

	f.idToken = func(issuer string) string { return f.signToken(t, stdClaims(issuer, "client-1", fs.Nonce)) }
	id, err := g.Exchange(context.Background(), "code-1", fs.Verifier, fs.Nonce)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if id.Sub != "google-sub-1" || id.Email != "ada@example.com" || !id.EmailVerified {
		t.Fatalf("identity = %+v", id)
	}
	// The PKCE verifier travelled to the token endpoint.
	if f.lastForm.Get("code_verifier") != fs.Verifier {
		t.Fatalf("code_verifier not sent: %v", f.lastForm)
	}
}

func TestGoogleIDTokenRejections(t *testing.T) {
	f := newFakeIssuer(t, "client-1")
	g := newProvider(f)
	nonce := "nonce-1"

	cases := map[string]func() string{
		"tampered signature": func() string {
			tok := f.signToken(t, stdClaims(f.srv.URL, "client-1", nonce))
			return tok[:len(tok)-4] + "AAAA"
		},
		"wrong audience": func() string {
			return f.signToken(t, stdClaims(f.srv.URL, "other-client", nonce))
		},
		"wrong issuer": func() string {
			return f.signToken(t, stdClaims("https://evil.example.com", "client-1", nonce))
		},
		"expired": func() string {
			c := stdClaims(f.srv.URL, "client-1", nonce)
			c["exp"] = time.Now().Add(-time.Minute).Unix()
			return f.signToken(t, c)
		},
		"nonce mismatch": func() string {
			return f.signToken(t, stdClaims(f.srv.URL, "client-1", "someone-elses-nonce"))
		},
	}
	for name, make := range cases {
		t.Run(name, func(t *testing.T) {
			f.idToken = func(string) string { return make() }
			_, err := g.Exchange(context.Background(), "code", "verifier", nonce)
			if !errors.Is(err, ErrIDToken) {
				t.Fatalf("err = %v, want ErrIDToken", err)
			}
		})
	}
}

func TestFlowStateRoundTripAndTamper(t *testing.T) {
	secret := []byte("flow-secret")
	now := time.Now()
	fs, _, err := NewFlowState("/p/abc", 10*time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := fs.Encode(secret)
	if err != nil {
		t.Fatal(err)
	}

	got, err := DecodeFlowState(secret, encoded, now)
	if err != nil {
		t.Fatal(err)
	}
	if got != fs {
		t.Fatalf("round trip = %+v, want %+v", got, fs)
	}

	if _, err := DecodeFlowState([]byte("other-secret"), encoded, now); !errors.Is(err, ErrFlowState) {
		t.Fatalf("wrong secret err = %v, want ErrFlowState", err)
	}
	if _, err := DecodeFlowState(secret, encoded+"x", now); !errors.Is(err, ErrFlowState) {
		t.Fatalf("tampered err = %v, want ErrFlowState", err)
	}
	if _, err := DecodeFlowState(secret, encoded, now.Add(11*time.Minute)); !errors.Is(err, ErrFlowState) {
		t.Fatalf("expired err = %v, want ErrFlowState", err)
	}

	// Three independent secrets per flow.
	if fs.State == fs.Nonce || fs.Nonce == fs.Verifier || fs.State == fs.Verifier {
		t.Fatal("flow state reused a random value")
	}
	if fmt.Sprintf("%d", fs.ExpiresAt) == "" || fs.Next != "/p/abc" {
		t.Fatalf("flow state fields = %+v", fs)
	}
}
