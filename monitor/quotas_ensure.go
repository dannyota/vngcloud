package monitor

import (
	"context"
	"fmt"
	"math"
	"time"

	"danny.vn/vngcloud/internal/core"
)

type EnsureSyntheticQuotaInput struct {
	ExpectedQuotaID string
	MaxPrice        float64
	MaxDaysLeft     int
	NoWait          bool
}

// EnsureSyntheticQuotaOutput identifies the selected action independently of
// transmission and confirmation. Only read-back sets Renewed or Recovered.
type EnsureSyntheticQuotaOutput struct {
	QuotaMutationOutcome
	Action          string
	Quota           *Quota
	PreviousEndTime *time.Time
	Renewed         bool
	Recovered       bool
	OrderID         string
	PaymentPending  bool
}

// EnsureSyntheticQuota selects renewal or recovery from a fresh quota read.
// It never creates or deletes, and never changes action after selection.
// MaxDaysLeft zero permits early renewal; positive values skip when not due.
func (c *Client) EnsureSyntheticQuota(ctx context.Context, in *EnsureSyntheticQuotaInput) (*EnsureSyntheticQuotaOutput, error) {
	const op = "monitor.EnsureSyntheticQuota"
	input := EnsureSyntheticQuotaInput{}
	if in != nil {
		input = *in
	}
	result, err := c.runSyntheticMutation(ctx, syntheticMutationInput{op: op, ensure: true, nilInput: in == nil, expectedID: input.ExpectedQuotaID, maxPrice: input.MaxPrice, maxDays: input.MaxDaysLeft, noWait: input.NoWait})
	if result.action == "" {
		result.action = "none"
	}
	return &EnsureSyntheticQuotaOutput{QuotaMutationOutcome: result.outcome, Action: result.action, Quota: result.quota, PreviousEndTime: result.previous, Renewed: result.confirmed && result.action == "renew", Recovered: result.confirmed && result.action == "recover", OrderID: result.orderID, PaymentPending: result.paymentPending}, err
}

func validateSyntheticEnsure(in syntheticMutationInput) error {
	if in.nilInput {
		return fmt.Errorf("%s: Input is required: %w", in.op, core.ErrInvalidInput)
	}
	if in.expectedID != "" {
		if err := core.CheckPathID(in.op, "ExpectedQuotaID", in.expectedID); err != nil {
			return err
		}
	}
	if in.maxDays < 0 || uint64(in.maxDays) > uint64(math.MaxInt64/int64(24*time.Hour)) {
		return fmt.Errorf("%s: MaxDaysLeft is out of range: %w", in.op, core.ErrInvalidInput)
	}
	return checkNotificationPrice(in.op, 0, 0, in.maxPrice)
}

// The caller holds quotaMu and passes the selection snapshot to the delegate.
func (c *Client) selectSyntheticAction(in syntheticMutationInput, terms syntheticMutationTerms) (syntheticMutationInput, time.Time, error) {
	q := terms.quota
	if in.expectedID != "" && in.expectedID != q.ID {
		return in, time.Time{}, fmt.Errorf("%s: quota guard differs: %w", in.op, ErrQuotaChanged)
	}
	switch {
	case q.Status == QuotaStatusActive && q.EndTime.After(c.now()):
		in.action = "renew"
	case q.Status == QuotaStatusInTrash && q.EndTime.Before(c.now()):
		in.action = "recover"
		in.recover = true
	default:
		switch q.Status {
		case QuotaStatusActive, QuotaStatusInTrash, "CREATING", "ERROR":
			return in, time.Time{}, fmt.Errorf("%s: quota is ineligible: %w", in.op, core.ErrInvalidInput)
		default:
			return in, time.Time{}, fmt.Errorf("%s: unknown quota status: %w", in.op, ErrUnexpectedStatus)
		}
	}
	in.expectedID = q.ID
	in.expectedEnd = q.EndTime.Format(time.RFC3339Nano)
	return in, q.EndTime, nil
}
