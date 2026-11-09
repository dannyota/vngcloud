package monitor

import (
	"context"
	"time"
)

// RecoverSyntheticQuotaInput requires ExpectedQuotaID.
type RecoverSyntheticQuotaInput struct {
	// ExpectedQuotaID is required; use ASCII letters, digits, or hyphens.
	ExpectedQuotaID string `vngcloud:"required"`
	// MaxPrice bounds the recovery quote.
	MaxPrice float64
	// NoWait skips the post-operation wait.
	NoWait bool
}

// RecoverSyntheticQuotaOutput confirms only a read of the frozen quota and a
// later expiry. The provider's recovery route cannot bind an expected ID.
type RecoverSyntheticQuotaOutput struct {
	QuotaMutationOutcome
	PreviousEndTime *time.Time
	Quota           *Quota
	OrderID         string
	PaymentPending  bool
	Recovered       bool
}

// RecoverSyntheticQuota starts a new one-month term for an expired free quota
// in trash. It sends once and retains unresolved attempts within this Client.
func (c *Client) RecoverSyntheticQuota(ctx context.Context, in *RecoverSyntheticQuotaInput) (*RecoverSyntheticQuotaOutput, error) {
	const op = "monitor.RecoverSyntheticQuota"
	input := RecoverSyntheticQuotaInput{}
	if in != nil {
		input = *in
	}
	result, err := c.runSyntheticMutation(ctx, syntheticMutationInput{op: op, recover: true, expectedID: input.ExpectedQuotaID, maxPrice: input.MaxPrice, noWait: input.NoWait})
	return &RecoverSyntheticQuotaOutput{QuotaMutationOutcome: result.outcome, PreviousEndTime: result.previous, Quota: result.quota, OrderID: result.orderID, PaymentPending: result.paymentPending, Recovered: result.confirmed}, err
}
