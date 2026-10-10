package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"danny.vn/vngcloud/internal/billingresources"
	"danny.vn/vngcloud/internal/core"
)

const autoRenewRecovery = "Read GetProjectAutoRenew and reconcile both lists before a new deliberate setting."
const autoRenewConfirmBound = 30 * time.Second

// PutProjectAutoRenew authorizes future renewal charges using today's estimate.
// MaxPrice is a local cap, not a server limit. No conditional update exists;
// another actor can change the setting after the confirmation read.
func (c *Client) PutProjectAutoRenew(ctx context.Context, in *PutProjectAutoRenewInput) (*PutProjectAutoRenewOutput, error) {
	const op = "storage.PutProjectAutoRenew"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkAutoRenewInput(op, in.ProjectID, in.PeriodMonths); err != nil {
		return nil, err
	}
	if in.Enabled == nil || (!*in.Enabled && in.PeriodMonths != nil) || math.IsNaN(in.MaxPrice) || math.IsInf(in.MaxPrice, 0) || in.MaxPrice < 0 {
		return nil, fmt.Errorf("%w: %s: requires Enabled, no period on disable, and finite nonnegative MaxPrice", core.ErrInvalidInput, op)
	}
	o, err := c.readAutoRenew(ctx, op, in.Region, in.ProjectID)
	if err != nil {
		return &PutProjectAutoRenewOutput{}, err
	}
	out := &PutProjectAutoRenewOutput{State: o.state}
	if o.state.RenewType == "NON-RENEWABLE" {
		return out, fmt.Errorf("%w: %s: resource is non-renewable", core.ErrInvalidInput, op)
	}
	if o.state.Enabled == nil {
		return out, projectResponseError(op, "unknown resource renewal state")
	}
	months := 1
	if *in.Enabled {
		months = autoRenewPreview(o.state, in.PeriodMonths)
	}
	if *o.state.Enabled == *in.Enabled && (!*in.Enabled || *o.state.PeriodMonths == months) {
		if *in.Enabled {
			err = c.priceAutoRenew(ctx, o, months)
			if ctx.Err() != nil {
				return out, ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return out, err
			}
		}
		return out, nil
	}
	if o.resource.BillingType == "" || o.resource.Channel == nil || o.resource.IsRenewing == nil {
		return out, projectResponseError(op, "billing eligibility fields are missing")
	}
	if o.resource.BillingType != "PREPAID" && o.resource.BillingType != "POSTPAID" {
		return out, projectResponseError(op, "unknown resource billing type")
	}
	if o.resource.BillingType != "PREPAID" {
		return out, fmt.Errorf("%w: %s: resource is not prepaid", core.ErrInvalidInput, op)
	}
	if *o.resource.IsRenewing {
		return out, fmt.Errorf("%w: %s: renewal is in progress", core.ErrInvalidInput, op)
	}
	if *in.Enabled {
		if !validRenewalPeriod(months) {
			return out, fmt.Errorf("%w: %s: unsupported renewal period", core.ErrInvalidInput, op)
		}
		rawStatus := o.project.fields["status"]
		if len(rawStatus) == 0 || string(rawStatus) == "null" {
			return out, projectResponseError(op, "project status is missing")
		}
		if o.project.project.Status != 1 {
			return out, projectResponseError(op, "project active status is unknown")
		}
		if !o.state.EndTime.After(c.now()) {
			return out, fmt.Errorf("%w: %s: project has no active future term", core.ErrInvalidInput, op)
		}
		if err = c.priceAutoRenew(ctx, o, months); err != nil {
			return out, err
		}
		if *o.state.QuotedRenewalCharge > in.MaxPrice {
			return out, fmt.Errorf("%w: %s: renewal estimate exceeds MaxPrice", core.ErrPriceAboveMax, op)
		}
	}
	account, err := billingresources.Account(ctx, c.c)
	if err != nil {
		return out, err
	}
	err = billingresources.Put(ctx, c.c, op, account, billingresources.Setting{Product: "vstorage", ArtifactType: "object-storage", ArtifactID: in.ProjectID, Channel: *o.resource.Channel, AutoRenewInfo: billingresources.AutoRenewInfo{IsEnable: *in.Enabled, Period: 43200 * months}})
	if err != nil {
		if autoRenewKnownRefusal(err) {
			return out, err
		}
		// Reconciliation can attach observed state but cannot erase a failed reply.
		_ = c.pollProject(ctx, autoRenewConfirmBound, func(readCtx context.Context) (bool, error) {
			observed, readErr := c.readAutoRenew(readCtx, op, in.Region, in.ProjectID)
			if readErr == nil {
				copyAutoRenewPrices(observed.state, o.state)
				out.State = observed.state
			}
			return true, readErr
		}, func() error { return projectResponseError(op, "auto-renew reconciliation timed out") })
		var api *core.APIError
		if errors.As(err, &api) && api.Code == "AutoRenewRejected" && !autoRenewTarget(out.State, *in.Enabled, months) {
			return out, err
		}
		return out, projectUnsettled(op, err, autoRenewRecovery)
	}
	err = c.pollProject(ctx, autoRenewConfirmBound, func(readCtx context.Context) (bool, error) {
		observed, readErr := c.readAutoRenew(readCtx, op, in.Region, in.ProjectID)
		if readErr != nil {
			return true, readErr
		}
		copyAutoRenewPrices(observed.state, o.state)
		out.State = observed.state
		return autoRenewTarget(out.State, *in.Enabled, months), nil
	}, func() error { return projectResponseError(op, "auto-renew confirmation timed out after 30s") })
	if err != nil {
		return out, projectUnsettled(op, err, autoRenewRecovery)
	}
	out.Changed = true
	return out, nil
}

func autoRenewTarget(s *ProjectAutoRenew, enabled bool, months int) bool {
	return s != nil && s.Enabled != nil && *s.Enabled == enabled && (!enabled || (s.PeriodMonths != nil && *s.PeriodMonths == months))
}

func copyAutoRenewPrices(to, from *ProjectAutoRenew) {
	to.QuotePeriodMonths = from.QuotePeriodMonths
	to.MonthlyPrice = from.MonthlyPrice
	to.QuotedRenewalCharge = from.QuotedRenewalCharge
	to.Currency = from.Currency
	to.PriceStatus = from.PriceStatus
	to.PriceErrorCode = from.PriceErrorCode
	if to.Enabled != nil && *to.Enabled && to.MonthlyPrice != nil {
		next := *to.MonthlyPrice * float64(*to.PeriodMonths)
		if finitePositive(next) {
			to.NextCharge = &next
		} else {
			to.MonthlyPrice = nil
			to.QuotedRenewalCharge = nil
			to.NextCharge = nil
			to.Currency = ""
			to.PriceStatus = "Unavailable"
			to.PriceErrorCode = "QuoteFailed"
		}
	}
}

func autoRenewKnownRefusal(err error) bool {
	var api *core.APIError
	if errors.As(err, &api) && api.StatusCode/100 == 4 {
		return true
	}
	// Once marks only a failed dial retryable when no HTTP status exists.
	return api != nil && api.StatusCode == 0 && api.Retryable
}
