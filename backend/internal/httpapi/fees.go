package httpapi

import (
	"net/http"
	"strconv"

	"escrowpay/internal/pocket"
)

type feeQuoteResponse struct {
	GoodsKobo   int64 `json:"goods_kobo"`
	PremiumKobo int64 `json:"premium_kobo"`
	BuyerTotalKobo int64 `json:"buyer_total_kobo"`
}

// handleFeeQuote returns the platform's Protection Premium for a goods value
// (vendor allocation plus any commission), so a client can show the fee before
// creating a pocket. The schedule is public; creation recomputes it
// server-side regardless of what a client displays.
func (a *API) handleFeeQuote(w http.ResponseWriter, r *http.Request) {
	goods, err := strconv.ParseInt(r.URL.Query().Get("goods_kobo"), 10, 64)
	if err != nil || goods <= 0 {
		a.writeError(w, errBadRequest)
		return
	}
	premium := pocket.ProtectionPremiumKobo(goods)
	writeJSON(w, http.StatusOK, feeQuoteResponse{
		GoodsKobo:      goods,
		PremiumKobo:    premium,
		BuyerTotalKobo: goods + premium,
	})
}
