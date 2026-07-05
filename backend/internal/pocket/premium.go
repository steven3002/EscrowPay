package pocket

// Protection Premium policy: the buyer-paid fee is fixed platform-wide and
// derived from the goods value (vendor allocation plus any broker commission),
// never chosen by a client. Flat bands keep the fee predictable and cheap at
// the low end of social-commerce order sizes.
//
//	goods value (₦)          fee (₦)
//	        0 –    10,000       100
//	   10,001 –    50,000       200
//	   50,001 –   100,000       500
//	  100,001 –   200,000     1,000
//	  200,001 –   500,000     3,000
//	  500,001 – 1,000,000     5,000
//	      above 1,000,000    10,000
type premiumBand struct {
	upToKobo   int64
	premiumKobo int64
}

var premiumBands = []premiumBand{
	{upToKobo: 10_000_00, premiumKobo: 100_00},
	{upToKobo: 50_000_00, premiumKobo: 200_00},
	{upToKobo: 100_000_00, premiumKobo: 500_00},
	{upToKobo: 200_000_00, premiumKobo: 1_000_00},
	{upToKobo: 500_000_00, premiumKobo: 3_000_00},
	{upToKobo: 1_000_000_00, premiumKobo: 5_000_00},
}

// premiumCapKobo applies above the last band's ceiling.
const premiumCapKobo = 10_000_00

// ProtectionPremiumKobo returns the platform's Protection Premium for a pocket
// whose goods value (AmountKobo + CommissionKobo) is goodsKobo. A non-positive
// goods value yields zero; spec validation rejects it separately.
func ProtectionPremiumKobo(goodsKobo int64) int64 {
	if goodsKobo <= 0 {
		return 0
	}
	for _, band := range premiumBands {
		if goodsKobo <= band.upToKobo {
			return band.premiumKobo
		}
	}
	return premiumCapKobo
}
