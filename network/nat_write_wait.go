package network

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"danny.vn/vngcloud/internal/billingresources"
)

func (c *Client) natWaitSleep(ctx context.Context, deadline time.Time, interval time.Duration) error {
	remaining := deadline.Sub(c.now())
	if remaining <= 0 {
		return natUnsettled("network.NATWait")
	}
	return c.sleep(ctx, min(interval, remaining))
}
func (c *Client) waitNATCreate(ctx context.Context, op string, s *natPurchaseSpec, out *CreateNATInstanceOutput, deadline time.Time, accepted bool) error {
	waitCtx, cancel := context.WithTimeout(ctx, max(time.Duration(0), deadline.Sub(c.now())))
	defer cancel()
	for {
		if waitCtx.Err() != nil || !c.now().Before(deadline) {
			return natUnsettled(op)
		}
		rows, err := c.natCreateInventory(waitCtx, op, s.scope, deadline)
		if err != nil && rows == nil {
			return natUnsettled(op)
		}
		var candidate *NATInstance
		for _, row := range rows {
			if s.baseline[row.UUID] || row.NATName != s.info.NATName {
				continue
			}
			if candidate != nil || row.VPC.UUID != s.info.VPCUUID || row.VPC.RegionID != s.vpc.RegionID || row.NATPackage.UUID != s.info.PackageUUID {
				return natUnsettled(op)
			}
			candidate = &row
		}
		if candidate != nil {
			if out.NATInstance != nil && out.NATInstance.UUID != candidate.UUID {
				return natUnsettled(op)
			}
			out.NATInstance = candidate
			if waitCtx.Err() != nil || !c.now().Before(deadline) {
				return natUnsettled(op)
			}
			if candidate.Status == "ERROR" {
				if !accepted {
					return natUnsettled(op)
				}
				return fmt.Errorf("%w: %s: NAT reached ERROR", ErrFailed, op)
			}
			if candidate.Status == "ACTIVE" {
				row, renewal, err := c.natBilling(waitCtx, op, candidate.UUID, s.offer.BillingSKU)
				if err != nil {
					return natUnsettled(op)
				}
				if row != nil {
					out.AutoRenew = renewal
					if !accepted || !c.now().Before(deadline) || waitCtx.Err() != nil {
						return natUnsettled(op)
					}
					return c.natRenewalOff(waitCtx, op, s, out, row, deadline)
				}
			}
		}
		if err != nil {
			return natUnsettled(op)
		}
		if c.natWaitSleep(waitCtx, deadline, 5*time.Second) != nil {
			return natUnsettled(op)
		}
	}
}

func (c *Client) natBilling(ctx context.Context, op, id, sku string) (*billingresources.Resource, *bool, error) {
	data, err := billingresources.List(ctx, c.c)
	if err != nil {
		return nil, nil, natSafeError(op, err)
	}
	var wire struct {
		Items []json.RawMessage `json:"data"`
	}
	if decodeNATEnvelope(data, &wire, reflect.TypeFor[map[string]json.RawMessage]()) != nil || wire.Items == nil {
		return nil, nil, natInvalid(op)
	}
	var found *billingresources.Resource
	var renewal *bool
	for _, raw := range wire.Items {
		var row billingresources.Resource
		if decodeNATEnvelope(raw, &row, reflect.TypeFor[map[string]json.RawMessage]()) != nil {
			return nil, nil, natInvalid(op)
		}
		if row.Product != "vserver" || row.ArtifactType != "nat" || row.ArtifactID != id {
			continue
		}
		if found != nil {
			return nil, nil, natInvalid(op)
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil {
			return nil, nil, natInvalid(op)
		}
		var status string
		if json.Unmarshal(row.Status, &status) != nil || status != "active" || row.BillingType != "PREPAID" || row.Channel == nil || (row.IsRenewing != nil && *row.IsRenewing) || len(row.BillingElements) != 1 {
			return nil, nil, natInvalid(op)
		}
		element := row.BillingElements[0]
		var quantity *float64
		if element.SKU != sku || json.Unmarshal(element.Quantity, &quantity) != nil || quantity == nil || *quantity != 1 {
			return nil, nil, natInvalid(op)
		}
		enabled := false
		switch row.RenewType {
		case "MANUAL":
			if string(fields["renewPeriod"]) != "null" {
				return nil, nil, natInvalid(op)
			}
		case "AUTO-RENEW":
			if row.RenewPeriod == nil || *row.RenewPeriod != 1 {
				return nil, nil, natInvalid(op)
			}
			enabled = true
		default:
			return nil, nil, natInvalid(op)
		}
		found = &row
		renewal = &enabled
	}
	return found, renewal, nil
}
func (c *Client) natRenewalOff(ctx context.Context, op string, s *natPurchaseSpec, out *CreateNATInstanceOutput, row *billingresources.Resource, deadline time.Time) error {
	if out.AutoRenew != nil && !*out.AutoRenew {
		return nil
	}
	account, err := billingresources.Account(ctx, c.c)
	if err != nil {
		return natUnsettled(op)
	}
	if ctx.Err() != nil || !c.now().Before(deadline) {
		return natUnsettled(op)
	}
	putErr := billingresources.Put(ctx, c.c, op, account, billingresources.Setting{Product: "vserver", ArtifactType: "nat", ArtifactID: out.NATInstance.UUID, Channel: *row.Channel, AutoRenewInfo: billingresources.AutoRenewInfo{IsEnable: false, Period: 43200}})
	confirmDeadline := minTime(deadline, c.now().Add(30*time.Second))
	confirmCtx, cancel := context.WithTimeout(ctx, max(time.Duration(0), confirmDeadline.Sub(c.now())))
	defer cancel()
	for {
		if confirmCtx.Err() != nil || !c.now().Before(confirmDeadline) {
			return natUnsettled(op)
		}
		observed, renewal, readErr := c.natBilling(confirmCtx, op, out.NATInstance.UUID, s.offer.BillingSKU)
		if readErr != nil || observed == nil {
			return natUnsettled(op)
		}
		out.AutoRenew = renewal
		if putErr != nil {
			return natUnsettled(op)
		}
		if !*renewal {
			if confirmCtx.Err() != nil || !c.now().Before(confirmDeadline) {
				return natUnsettled(op)
			}
			return nil
		}
		if c.natWaitSleep(confirmCtx, confirmDeadline, 2*time.Second) != nil {
			return natUnsettled(op)
		}
	}
}
func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (c *Client) natCreateInventory(ctx context.Context, op string, scope natScope, deadline time.Time) ([]NATInstance, error) {
	remaining := deadline.Sub(c.now())
	if ctx.Err() != nil || remaining <= 0 {
		return nil, natUnsettled(op)
	}
	bounded, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	rows, err := c.natInventory(bounded, op, scope)
	if err != nil {
		return nil, natUnsettled(op)
	}
	if bounded.Err() != nil || !c.now().Before(deadline) {
		return rows, natUnsettled(op)
	}
	return rows, nil
}
