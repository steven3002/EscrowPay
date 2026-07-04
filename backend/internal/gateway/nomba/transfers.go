package nomba

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"escrowpay/internal/gateway"
)

type transferRequest struct {
	Amount        json.Number `json:"amount"`
	AccountNumber string      `json:"accountNumber"`
	AccountName   string      `json:"accountName"`
	BankCode      string      `json:"bankCode"`
	MerchantTxRef string      `json:"merchantTxRef"`
	SenderName    string      `json:"senderName"`
	Narration     string      `json:"narration"`
}

type transferResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Payout disburses one settlement leg as a bank transfer from the sub-account.
// The leg's idempotency key rides as merchantTxRef; the API does not enforce
// its uniqueness in every environment, so the caller must never resubmit an
// ambiguous attempt (the settlement-leg claim protocol guarantees this).
func (c *Client) Payout(ctx context.Context, req gateway.PayoutRequest) (string, error) {
	to, err := c.beneficiary(req.BeneficiaryAccountRef, c.cfg.PayoutBeneficiary)
	if err != nil {
		return "", fmt.Errorf("payout %s: %w", req.IdempotencyKey, err)
	}
	return c.transfer(ctx, to, req.AmountKobo, req.IdempotencyKey,
		fmt.Sprintf("EscrowPay settlement %s", shortID(req.PocketID)))
}

// Refund returns escrowed funds to the buyer as a bank transfer. A transfer
// refund reaches the buyer immediately; refunding the original charge on the
// card rails instead would settle in days and needs the funding transaction
// id, which reconciliation records for that future path.
func (c *Client) Refund(ctx context.Context, req gateway.RefundRequest) (string, error) {
	to := c.cfg.RefundBeneficiary
	if !to.complete() {
		to = c.cfg.PayoutBeneficiary
	}
	if !to.complete() {
		return "", fmt.Errorf("refund %s: no refund beneficiary configured: %w", req.IdempotencyKey, gateway.ErrNotSubmitted)
	}
	return c.transfer(ctx, to, req.AmountKobo, req.IdempotencyKey,
		fmt.Sprintf("EscrowPay refund %s", shortID(req.PocketID)))
}

func (c *Client) transfer(ctx context.Context, to Beneficiary, amountKobo int64, merchantTxRef, narration string) (string, error) {
	body := transferRequest{
		Amount:        json.Number(nairaAmount(amountKobo)),
		AccountNumber: to.AccountNumber,
		AccountName:   to.AccountName,
		BankCode:      to.BankCode,
		MerchantTxRef: merchantTxRef,
		SenderName:    "EscrowPay",
		Narration:     narration,
	}
	var data transferResponse
	if err := c.call(ctx, http.MethodPost, "/v2/transfers/bank/"+c.cfg.SubAccountID, body, &data); err != nil {
		return "", err
	}
	// REFUND means the transfer failed and was automatically returned — a
	// definitive rejection. SUCCESS, PENDING_BILLING and NEW all mean the
	// transfer is accepted; the payout webhook reports the final outcome.
	if strings.EqualFold(data.Status, "REFUND") {
		return "", fmt.Errorf("nomba: transfer %s failed with status %s: %w", merchantTxRef, data.Status, gateway.ErrRejected)
	}
	if data.ID == "" {
		return "", fmt.Errorf("nomba: transfer %s returned no id", merchantTxRef)
	}
	return data.ID, nil
}

// beneficiary resolves a leg's destination account: an explicit
// "bankCode:accountNumber:accountName" reference when the leg carries one,
// otherwise the configured default. A missing destination is a configuration
// error that provably precedes submission.
func (c *Client) beneficiary(ref string, fallback Beneficiary) (Beneficiary, error) {
	if ref != "" {
		parts := strings.SplitN(ref, ":", 3)
		if len(parts) == 3 && parts[0] != "" && parts[1] != "" && parts[2] != "" {
			return Beneficiary{BankCode: parts[0], AccountNumber: parts[1], AccountName: parts[2]}, nil
		}
		return Beneficiary{}, fmt.Errorf("malformed beneficiary reference: %w", gateway.ErrNotSubmitted)
	}
	if !fallback.complete() {
		return Beneficiary{}, fmt.Errorf("no payout beneficiary configured: %w", gateway.ErrNotSubmitted)
	}
	return fallback, nil
}

// shortID renders the first segment of a UUID for narrations, which have tight
// length limits at receiving banks.
func shortID(id string) string {
	if i := strings.IndexByte(id, '-'); i > 0 {
		return id[:i]
	}
	return id
}
