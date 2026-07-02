package releasecode

import (
	"errors"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	secret := []byte("server-secret")
	token, err := Seal("0421", secret)
	if err != nil {
		t.Fatal(err)
	}
	if token == "0421" {
		t.Fatal("sealed token must not equal the plaintext")
	}

	code, err := Open(token, secret)
	if err != nil {
		t.Fatal(err)
	}
	if code != "0421" {
		t.Fatalf("opened code = %q, want 0421", code)
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	secret := []byte("server-secret")
	a, err := Seal("0421", secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal("0421", secret)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two seals of the same code must differ (random nonce)")
	}
}

func TestOpenWrongSecretFails(t *testing.T) {
	token, err := Seal("0421", []byte("server-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(token, []byte("other-secret")); !errors.Is(err, ErrCiphertext) {
		t.Fatalf("err = %v, want ErrCiphertext", err)
	}
}

func TestOpenMalformedFails(t *testing.T) {
	if _, err := Open("not-hex-zz", []byte("server-secret")); !errors.Is(err, ErrCiphertext) {
		t.Fatalf("err = %v, want ErrCiphertext", err)
	}
	if _, err := Open(" abcd", []byte("server-secret")); !errors.Is(err, ErrCiphertext) {
		t.Fatalf("err = %v, want ErrCiphertext", err)
	}
}
