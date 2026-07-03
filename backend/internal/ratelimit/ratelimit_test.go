package ratelimit

import (
	"testing"
	"time"
)

func TestBurstThenSustainedRate(t *testing.T) {
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	l := New(60, 5, clock) // 1/s sustained, burst of 5

	for i := 0; i < 5; i++ {
		if !l.Allow("k") {
			t.Fatalf("burst request %d denied", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("request beyond burst admitted")
	}

	// One token refills after one second at 60/min.
	now = now.Add(time.Second)
	if !l.Allow("k") {
		t.Fatal("refilled token denied")
	}
	if l.Allow("k") {
		t.Fatal("second request admitted with only one token refilled")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(60, 1, func() time.Time { return now })

	if !l.Allow("a") {
		t.Fatal("first key denied")
	}
	if l.Allow("a") {
		t.Fatal("first key over budget admitted")
	}
	if !l.Allow("b") {
		t.Fatal("independent key denied")
	}
}

func TestTokensCapAtBurst(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(60, 3, func() time.Time { return now })

	if !l.Allow("k") {
		t.Fatal("first request denied")
	}
	// A long idle period must not accumulate more than the burst.
	now = now.Add(time.Hour)
	admitted := 0
	for i := 0; i < 10; i++ {
		if l.Allow("k") {
			admitted++
		}
	}
	if admitted != 3 {
		t.Fatalf("admitted %d after idle, want burst cap 3", admitted)
	}
}
