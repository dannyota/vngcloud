package monitor

import (
	"context"
	"errors"
	"time"

	"danny.vn/vngcloud/internal/core"
)

const (
	quotaPollInterval    = 2 * time.Second
	quotaSettlementBound = 120 * time.Second
)

func (c *Client) notificationCreateReconciler(op string, terms notificationCreateTerms, credentials *monitorReadCredentials) func(context.Context) (quotaAttemptResolution, error) {
	return func(ctx context.Context) (quotaAttemptResolution, error) {
		q, err := c.readNotificationQuota(ctx, op, terms.kind, credentials)
		if core.IsNotFound(err) {
			return quotaAttemptResolution{}, nil
		}
		if err != nil {
			return quotaAttemptResolution{}, err
		}
		if q.PackageID != terms.packageID || q.Status != QuotaStatusActive {
			return quotaAttemptResolution{}, nil
		}
		return quotaAttemptResolution{confirmed: true, quota: &q}, nil
	}
}

// Both the child context and injected clock bound settlement. The context also
// interrupts a slow read; elapsed fake time makes timeout tests deterministic.
func (c *Client) waitQuotaAttempt(ctx context.Context, op string, read func(context.Context) (quotaAttemptResolution, error), bound time.Duration) (quotaAttemptResolution, error) {
	if bound > quotaSettlementBound {
		bound = quotaSettlementBound
	}
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	deadline := c.now().Add(bound)
	for {
		if err := ctx.Err(); err != nil {
			return quotaAttemptResolution{}, quotaUnsettled(op, err)
		}
		if !c.now().Before(deadline) {
			return quotaAttemptResolution{}, quotaUnsettled(op, context.DeadlineExceeded)
		}
		result, err := read(ctx)
		// Even a successful late read cannot fulfill an expired wait.
		if cause := ctx.Err(); cause != nil {
			return quotaAttemptResolution{}, quotaUnsettled(op, cause)
		}
		if !c.now().Before(deadline) {
			return quotaAttemptResolution{}, quotaUnsettled(op, context.DeadlineExceeded)
		}
		if err != nil {
			return quotaAttemptResolution{}, quotaUnsettled(op, err)
		}
		if result.rejected != nil {
			return result, result.rejected
		}
		if result.confirmed {
			return result, nil
		}
		remaining := deadline.Sub(c.now())
		delay := min(quotaPollInterval, remaining)
		if err := c.sleep(ctx, delay); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return quotaAttemptResolution{}, quotaUnsettled(op, err)
			}
			return quotaAttemptResolution{}, quotaUnsettled(op, monitorReadCause(err))
		}
	}
}
