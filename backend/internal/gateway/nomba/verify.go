package nomba

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"escrowpay/internal/gateway"
)

// checkoutTransaction is the payload of GET /v1/checkout/transaction. success
// is true only when a completed payment exists for the order; unpaid orders
// return success=false with a "no transaction found" message rather than an
// error envelope, so the endpoint distinguishes "not paid yet" from failure.
type checkoutTransaction struct {
	Success bool `json:"success"`
	Order   *struct {
		OrderID        string `json:"orderId"`
		OrderReference string `json:"orderReference"`
		Amount         string `json:"amount"`
	} `json:"order"`
	TransactionDetails *struct {
		PaymentReference       string `json:"paymentReference"`
		PaymentVendorReference string `json:"paymentVendorReference"`
	} `json:"transactionDetails"`
}

// VerifyFunding reports whether the checkout order identified by orderRef has
// been paid. It accepts either of an order's references (the provider's
// generated id or the submitted one — the endpoint resolves both). Hosted
// checkout payments are visible here as soon as the payer completes them,
// which makes this the pull-side complement to the payment webhook.
func (c *Client) VerifyFunding(ctx context.Context, orderRef string) (gateway.FundingStatus, error) {
	if orderRef == "" {
		return gateway.FundingStatus{}, fmt.Errorf("nomba: verify funding: empty order reference")
	}
	var data checkoutTransaction
	path := "/v1/checkout/transaction?idType=ORDER_REFERENCE&id=" + url.QueryEscape(orderRef)
	if err := c.call(ctx, http.MethodGet, path, nil, &data); err != nil {
		return gateway.FundingStatus{}, err
	}
	if !data.Success || data.TransactionDetails == nil {
		return gateway.FundingStatus{}, nil
	}
	status := gateway.FundingStatus{
		Paid:       true,
		PaymentRef: data.TransactionDetails.PaymentReference,
	}
	if data.Order != nil {
		status.AmountKobo = koboFromNaira(data.Order.Amount)
	}
	return status, nil
}

// koboFromNaira parses the API's naira-decimal string ("300.00") into kobo
// without passing through floating point. Malformed input yields 0, which
// callers treat as "amount unstated".
func koboFromNaira(s string) int64 {
	whole, frac, _ := strings.Cut(strings.TrimSpace(s), ".")
	if whole == "" {
		whole = "0"
	}
	var naira int64
	for _, r := range whole {
		if r < '0' || r > '9' {
			return 0
		}
		naira = naira*10 + int64(r-'0')
	}
	kobo := naira * 100
	switch len(frac) {
	case 0:
		return kobo
	case 1:
		frac += "0"
	case 2:
	default:
		frac = frac[:2]
	}
	for _, r := range frac {
		if r < '0' || r > '9' {
			return 0
		}
	}
	return kobo + (int64(frac[0]-'0')*10 + int64(frac[1]-'0'))
}
