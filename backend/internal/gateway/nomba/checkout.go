package nomba

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"escrowpay/internal/gateway"
)

type checkoutOrder struct {
	OrderReference string      `json:"orderReference"`
	CallbackURL    string      `json:"callbackUrl"`
	CustomerEmail  string      `json:"customerEmail"`
	Amount         json.Number `json:"amount"`
	Currency       string      `json:"currency"`
	AccountID      string      `json:"accountId"`
}

// CreateFundingLink creates a hosted checkout order collecting the pocket's
// buyer total into the configured sub-account, and returns the link the buyer
// pays through. The link's expiry is enforced by the caller's own funding
// window, not by the provider.
//
// Two references coexist: the deterministic one submitted (gateway.FundingRef,
// stable so a retried creation is benign) and the provider-generated one the
// response returns. The returned Ref is the provider's, which payments through
// the hosted link are keyed on; a notification carrying the submitted
// reference instead still resolves via gateway.PocketIDFromRef.
func (c *Client) CreateFundingLink(ctx context.Context, req gateway.CreateFundingLinkRequest) (gateway.FundingLink, error) {
	email := req.CustomerEmail
	if email == "" {
		email = c.cfg.FallbackCustomerEmail
	}
	order := checkoutOrder{
		OrderReference: gateway.FundingRef(req.PocketID),
		CallbackURL:    c.cfg.PublicBaseURL + "/p/" + req.ShortCode,
		CustomerEmail:  email,
		Amount:         json.Number(nairaAmount(req.AmountKobo)),
		Currency:       "NGN",
		AccountID:      c.cfg.SubAccountID,
	}
	var data struct {
		CheckoutLink   string `json:"checkoutLink"`
		OrderReference string `json:"orderReference"`
	}
	if err := c.call(ctx, http.MethodPost, "/v1/checkout/order", map[string]any{"order": order}, &data); err != nil {
		return gateway.FundingLink{}, err
	}
	if data.CheckoutLink == "" {
		return gateway.FundingLink{}, fmt.Errorf("nomba: checkout order %s returned no link", order.OrderReference)
	}
	ref := data.OrderReference
	if ref == "" {
		ref = order.OrderReference
	}
	return gateway.FundingLink{Ref: ref, URL: data.CheckoutLink, ExpiresAt: req.ExpiresAt}, nil
}
