package pocket

import "testing"

func TestProtectionPremiumKobo(t *testing.T) {
	cases := []struct {
		name      string
		goodsKobo int64
		wantKobo  int64
	}{
		{"zero is free", 0, 0},
		{"negative is free", -100, 0},
		{"one naira", 1_00, 100_00},
		{"bottom band ceiling ₦10,000", 10_000_00, 100_00},
		{"just above ₦10,000", 10_000_01, 200_00},
		{"₦50,000 ceiling", 50_000_00, 200_00},
		{"just above ₦50,000", 50_000_01, 500_00},
		{"₦100,000 ceiling", 100_000_00, 500_00},
		{"just above ₦100,000", 100_000_01, 1_000_00},
		{"₦200,000 ceiling", 200_000_00, 1_000_00},
		{"just above ₦200,000", 200_000_01, 3_000_00},
		{"₦500,000 ceiling", 500_000_00, 3_000_00},
		{"just above ₦500,000", 500_000_01, 5_000_00},
		{"₦1,000,000 ceiling", 1_000_000_00, 5_000_00},
		{"above the top band is capped", 5_000_000_00, 10_000_00},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProtectionPremiumKobo(tc.goodsKobo); got != tc.wantKobo {
				t.Fatalf("ProtectionPremiumKobo(%d) = %d, want %d", tc.goodsKobo, got, tc.wantKobo)
			}
		})
	}
}

// TestProtectionPremiumMonotonic guards the schedule against a future edit that
// makes a larger order cheaper to protect than a smaller one.
func TestProtectionPremiumMonotonic(t *testing.T) {
	var prev int64
	for goods := int64(1_00); goods <= 2_000_000_00; goods += 137_00 {
		p := ProtectionPremiumKobo(goods)
		if p < prev {
			t.Fatalf("premium decreased at goods=%d: %d < %d", goods, p, prev)
		}
		prev = p
	}
}
