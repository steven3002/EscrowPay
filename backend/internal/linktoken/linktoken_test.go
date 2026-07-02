package linktoken

import (
	"errors"
	"strings"
	"testing"
)

func TestMintParseRoundTrip(t *testing.T) {
	m := NewMinter([]byte("secret"))
	token, hash, err := m.Mint("pocket-123", "buyer")
	if err != nil {
		t.Fatal(err)
	}
	if hash != Hash(token) {
		t.Fatal("returned hash must be Hash(token)")
	}

	claims, err := m.Parse(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.PocketID != "pocket-123" || claims.Role != "buyer" {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestMintIsUniquePerCall(t *testing.T) {
	m := NewMinter([]byte("secret"))
	a, _, _ := m.Mint("p", "vendor")
	b, _, _ := m.Mint("p", "vendor")
	if a == b {
		t.Fatal("nonce must make each mint unique")
	}
}

func TestParseRejectsTamperedToken(t *testing.T) {
	m := NewMinter([]byte("secret"))
	token, _, _ := m.Mint("pocket-123", "buyer")

	// Swap the role claim without re-signing.
	parts := strings.Split(token, ".")
	parts[1] = "vendor"
	tampered := strings.Join(parts, ".")

	if _, err := m.Parse(tampered); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestParseRejectsForeignSecret(t *testing.T) {
	token, _, _ := NewMinter([]byte("secret")).Mint("pocket-123", "buyer")
	if _, err := NewMinter([]byte("other")).Parse(token); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	m := NewMinter([]byte("secret"))
	for _, bad := range []string{"", "a.b.c", "a.b.c.d.e", "..."} {
		if _, err := m.Parse(bad); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("Parse(%q) err = %v, want ErrInvalidToken", bad, err)
		}
	}
}
