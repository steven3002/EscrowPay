package releasecode

import "testing"

func TestGenerateProducesFourDigitCodes(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 500; i++ {
		code, err := Generate()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 4 {
			t.Fatalf("length = %d, code = %q", len(code), code)
		}
		for _, r := range code {
			if r < '0' || r > '9' {
				t.Fatalf("non-digit rune in %q", code)
			}
		}
		seen[code] = true
	}
	if len(seen) < 50 {
		t.Fatalf("suspiciously low variety across 500 draws: %d distinct", len(seen))
	}
}

func TestHashAndVerify(t *testing.T) {
	secret := []byte("server-secret")
	h := Hash("1234", secret)

	if !Verify(h, "1234", secret) {
		t.Fatal("correct code must verify")
	}
	if Verify(h, "1235", secret) {
		t.Fatal("wrong code must not verify")
	}
	if Verify(h, "1234", []byte("other-secret")) {
		t.Fatal("wrong secret must not verify")
	}
	if Hash("1234", secret) != h {
		t.Fatal("hash must be deterministic")
	}
}

func TestRegisterFailureLocksAtMax(t *testing.T) {
	attempts := 0
	for i := 1; i <= MaxAttempts; i++ {
		var locked bool
		attempts, locked = RegisterFailure(attempts)
		if attempts != i {
			t.Fatalf("attempts = %d, want %d", attempts, i)
		}
		if i < MaxAttempts && locked {
			t.Fatalf("locked prematurely at attempt %d", i)
		}
		if i == MaxAttempts && !locked {
			t.Fatal("must lock at MaxAttempts")
		}
	}
}
