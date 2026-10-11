package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const natRecovery = "Run network list-nat-instances in the same scope and billing list-resources, matching the known NAT ID. If no NAT appears, inspect payment history without ordering again. Renewal may remain enabled; use the billing console for a deliberate disable and confirm by a billing read. Do not repeat create while purchase or renewal is unresolved. Deletion requires a separate deliberate action with VPC consent."

type natUnsettledError struct{ op string }

func (e *natUnsettledError) Error() string {
	return e.op + ": write outcome is unconfirmed; " + natRecovery
}
func (*natUnsettledError) Unwrap() error { return ErrNotSettled }
func natUnsettled(op string) error       { return &natUnsettledError{op: op} }

type natOrderBody struct {
	ResourceType string          `json:"resourceType"`
	Action       string          `json:"action"`
	ResourceInfo natResourceInfo `json:"resourceInfo"`
	TagDetails   []struct{}      `json:"tagDetails"`
}

// CreateNATInstance buys one month, waits for ACTIVE and paid billing, then
// disables renewal and confirms it. The quote cap is not a server price lock.
// Creating NAT changes the default egress route for every VM in the VPC.
func (c *Client) CreateNATInstance(ctx context.Context, in *CreateNATInstanceInput) (*CreateNATInstanceOutput, error) {
	const op = "network.CreateNATInstance"
	if err := c.natWriteAuth(op); err != nil {
		return nil, err
	}
	s, err := c.resolveNATPurchase(ctx, op, in)
	if err != nil {
		return nil, err
	}
	if err := c.guardNATBaseline(ctx, op, s); err != nil {
		return nil, err
	}
	quote, err := c.natQuote(ctx, op, s)
	if err != nil {
		return nil, err
	}
	if quote.TotalPrice > in.MaxPrice {
		return nil, fmt.Errorf("%w: %s: total %.0f VND exceeds MaxPrice %.0f VND", core.ErrPriceAboveMax, op, quote.TotalPrice, in.MaxPrice)
	}
	out := &CreateNATInstanceOutput{MonthlyPrice: quote.MonthlyPrice, TotalPrice: quote.TotalPrice, Currency: quote.Currency}
	orderCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	code := 0
	_, orderErr := c.natExchange(orderCtx, transport.Request{Operation: op, Method: http.MethodPost, URL: s.scope.collection(), Body: natOrderBody{ResourceType: "nat", Action: "create", ResourceInfo: s.info, TagDetails: []struct{}{}}, OK: []int{201}, Once: true}, &code)
	deadline := c.now().Add(15 * time.Minute)
	if natDefiniteRefusal(orderErr) {
		rows, readErr := c.natCreateInventory(ctx, op, s.scope, deadline)
		if readErr != nil {
			return out, natUnsettled(op)
		}
		candidate := false
		for _, row := range rows {
			if row.NATName == s.info.NATName && !s.baseline[row.UUID] {
				candidate = true
			}
		}
		if ctx.Err() != nil || !c.now().Before(deadline) {
			return out, natUnsettled(op)
		}
		if !candidate {
			return out, orderErr
		}
	}
	return out, c.waitNATCreate(ctx, op, s, out, deadline, orderErr == nil)
}

// DeleteNATInstance sends one delete after confirming the resource's VPC.
// NoWait confirms acceptance only, not absence, refund, or route restoration.
func (c *Client) DeleteNATInstance(ctx context.Context, in *DeleteNATInstanceInput) (*DeleteNATInstanceOutput, error) {
	const op = "network.DeleteNATInstance"
	if err := c.natWriteAuth(op); err != nil {
		return nil, err
	}
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	for _, id := range []struct{ name, value string }{{"ZoneID", in.ZoneID}, {"VPCID", in.VPCID}, {"NATID", in.NATID}} {
		if err := core.CheckPathID(op, id.name, id.value); err != nil {
			return nil, err
		}
	}
	s, err := c.natScope(ctx, op, in.ZoneID)
	if err != nil {
		return nil, err
	}
	vpc, err := c.natGuardVPC(ctx, op, s, in.VPCID, "")
	if err != nil {
		return nil, err
	}
	before, err := c.natInventory(ctx, op, s)
	if err != nil {
		return nil, err
	}
	found := false
	for _, row := range before {
		if row.UUID == in.NATID {
			found = true
			if row.VPC.UUID != in.VPCID || row.VPC.RegionID != vpc.RegionID {
				return nil, fmt.Errorf("%w: %s: NAT does not belong to the named VPC", core.ErrInvalidInput, op)
			}
		}
	}
	if !found {
		return nil, fmt.Errorf("%w: %s", core.ErrNotFound, op)
	}
	out := &DeleteNATInstanceOutput{}
	code := 0
	_, sendErr := c.natExchange(ctx, transport.Request{Operation: op, Method: http.MethodDelete, URL: s.collection(in.NATID), Body: struct{}{}, OK: []int{200}, Once: true}, &code)
	if in.NoWait {
		if sendErr != nil {
			return out, natUnsettled(op)
		}
		return out, nil
	}
	deadline := c.now().Add(10 * time.Minute)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		if waitCtx.Err() != nil || !c.now().Before(deadline) {
			return out, natUnsettled(op)
		}
		rows, readErr := c.natInventory(waitCtx, op, s)
		if readErr != nil {
			return out, natUnsettled(op)
		}
		if waitCtx.Err() != nil || !c.now().Before(deadline) {
			return out, natUnsettled(op)
		}
		present := false
		for _, row := range rows {
			if row.UUID == in.NATID {
				present = true
			}
		}
		if natDefiniteRefusal(sendErr) {
			if present {
				return out, sendErr
			}
			return out, natUnsettled(op)
		}
		if !present {
			return out, nil
		}
		if c.natWaitSleep(waitCtx, deadline, 5*time.Second) != nil {
			return out, natUnsettled(op)
		}
	}
}

func natDefiniteRefusal(err error) bool {
	var api *core.APIError
	return errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode < 500 && api.StatusCode != 408 && api.StatusCode != 429
}
