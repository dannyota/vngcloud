package dns

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// zoneTypePrivate is the only zone type vDNS offers today. CreateHostedZone
// always sends it, so a later public type can be added as a new
// CreateHostedZoneInput field without breaking an existing caller.
const zoneTypePrivate = "PRIVATE"

type createHostedZoneBody struct {
	DomainName  string   `json:"domainName"`
	AssocVPCIDs []string `json:"assocVpcIds"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
}

// CreateHostedZoneInput creates a private hosted zone. NoWait skips the
// post-create wait for StatusActive; see the package doc's discussion of
// waits.
type CreateHostedZoneInput struct {
	DomainName string   `vngcloud:"required"`
	VPCIDs     []string `vngcloud:"required"`

	Description string
	NoWait      bool
}

type CreateHostedZoneOutput struct {
	HostedZone HostedZone
}

// CreateHostedZone creates a private hosted zone. It always sends type
// "PRIVATE", the only type vDNS offers.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the zone may exist, and the caller lists zones by
// DomainName before creating it again, rather than retrying blind.
//
// Without NoWait, CreateHostedZone then waits for the new zone to reach
// StatusActive. If the zone reaches StatusError instead, or the wait's
// bound runs out first, the returned error wraps ErrFailed or ErrNotSettled
// and the Output still holds the zone the SDK last read, so the caller
// keeps its id.
func (c *Client) CreateHostedZone(ctx context.Context, in *CreateHostedZoneInput) (*CreateHostedZoneOutput, error) {
	const op = "dns.CreateHostedZone"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	var resp struct {
		Data HostedZone `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.url([]string{"dns", "hosted-zone"}, nil),
		Body: createHostedZoneBody{
			DomainName:  in.DomainName,
			AssocVPCIDs: in.VPCIDs,
			Type:        zoneTypePrivate,
			Description: in.Description,
		},
		OK: []int{200},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, err
	}
	zone := resp.Data
	if zone.ID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status, Message: "create response had no id"}
	}
	if in.NoWait {
		return &CreateHostedZoneOutput{HostedZone: zone}, nil
	}

	settled, err := c.settleZone(ctx, op, zone.ID, func(z *HostedZone) bool {
		return z.Status == StatusActive
	})
	if settled == nil {
		return nil, err
	}
	return &CreateHostedZoneOutput{HostedZone: *settled}, err
}

type updateHostedZoneBody struct {
	AssocVPCIDs []string `json:"assocVpcIds"`
	Description string   `json:"description"`
}

// UpdateHostedZoneInput changes a zone's description, its associated VPCs,
// or both. VPCIDs pointing at a non-nil, empty slice sends an explicit []
// and detaches every VPC, which then resolves nowhere; VPCIDs left nil
// keeps the zone's current VPCs and never sends []. At least one of VPCIDs
// and Description must be set.
type UpdateHostedZoneInput struct {
	HostedZoneID string `vngcloud:"required"`

	VPCIDs      *[]string
	Description *string
	NoWait      bool
}

type UpdateHostedZoneOutput struct {
	HostedZone HostedZone
}

// UpdateHostedZone replaces a zone's description and VPC associations. The
// API takes a full body and clears any field left out of it, so
// UpdateHostedZone reads the zone (as part of the pre-write wait below) and
// resends every field the caller left nil unchanged: an unset Description
// resends the zone's current one, and an unset VPCIDs resends its current
// VPC ids.
//
// UpdateHostedZone first waits, always, for the zone to reach StatusActive
// or StatusError: nothing has been sent yet, so this is safe, and it turns
// the zone lock's 400 into a short wait instead. If the wait's bound runs
// out first, it returns ErrZoneBusy and sends nothing.
//
// Without NoWait, it then waits for the zone to reach StatusActive with the
// sent description and VPC ids, or returns an error wrapping ErrFailed if
// the zone reaches StatusError, or one wrapping ErrNotSettled if that
// wait's own bound runs out; both keep the Output. With NoWait, it returns
// after one read instead of waiting.
func (c *Client) UpdateHostedZone(ctx context.Context, in *UpdateHostedZoneInput) (*UpdateHostedZoneOutput, error) {
	const op = "dns.UpdateHostedZone"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}
	if in.VPCIDs == nil && in.Description == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	current, err := c.waitZoneReady(ctx, op, in.HostedZoneID)
	if err != nil {
		return nil, err
	}

	vpcIDs := current.AssociatedVPCIDs
	if in.VPCIDs != nil {
		vpcIDs = *in.VPCIDs
	}
	if vpcIDs == nil {
		// Never resend null: an unset VPCIDs must resend the zone's current,
		// possibly empty, VPC list, and an explicit empty VPCIDs must send
		// [], never null either.
		vpcIDs = []string{}
	}
	description := current.Description
	if in.Description != nil {
		description = *in.Description
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID}, nil),
		Body: updateHostedZoneBody{
			AssocVPCIDs: vpcIDs,
			Description: description,
		},
		OK: []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		zone, err := c.getHostedZone(ctx, op, in.HostedZoneID)
		if err != nil {
			return nil, err
		}
		return &UpdateHostedZoneOutput{HostedZone: *zone}, nil
	}

	settled, err := c.settleZone(ctx, op, in.HostedZoneID, func(z *HostedZone) bool {
		return z.Status == StatusActive && z.Description == description && equalStringSets(z.AssociatedVPCIDs, vpcIDs)
	})
	if settled == nil {
		return nil, err
	}
	return &UpdateHostedZoneOutput{HostedZone: *settled}, err
}

// DeleteHostedZoneInput identifies the zone to delete.
type DeleteHostedZoneInput struct {
	HostedZoneID string `vngcloud:"required"`
	NoWait       bool
}

type DeleteHostedZoneOutput struct{}

// DeleteHostedZone deletes a zone. Deleting a zone that still holds any
// record other than the server's own NS and SOA fails with the server's own
// 400; the SDK does not delete records first.
//
// It first runs the same pre-write wait UpdateHostedZone does, for the same
// reason: nothing has been sent yet, and a busy zone past the wait's bound
// returns ErrZoneBusy with nothing sent.
//
// Without NoWait, it then waits for a read of the zone to fail with
// NotFound, or returns an error wrapping ErrNotSettled if that wait's own
// bound runs out first. With NoWait, it returns at once after the delete
// request succeeds. DELETE is idempotent and keeps the transport's own
// retries; a retry that finds the zone already gone returns NotFound, which
// is not an error DeleteHostedZone itself needs to handle specially.
func (c *Client) DeleteHostedZone(ctx context.Context, in *DeleteHostedZoneInput) (*DeleteHostedZoneOutput, error) {
	const op = "dns.DeleteHostedZone"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "HostedZoneID", in.HostedZoneID); err != nil {
		return nil, err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if _, err := c.waitZoneReady(ctx, op, in.HostedZoneID); err != nil {
		return nil, err
	}

	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.url([]string{"dns", "hosted-zone", in.HostedZoneID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}

	if in.NoWait {
		return &DeleteHostedZoneOutput{}, nil
	}
	if err := c.waitZoneGone(ctx, op, in.HostedZoneID); err != nil {
		return nil, err
	}
	return &DeleteHostedZoneOutput{}, nil
}
