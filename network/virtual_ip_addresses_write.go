package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// VirtualIPModeActiveActive and VirtualIPModeActivePassive name the Mode
// values the API documents. The SDK sends Mode as given and picks no
// default; a create or update with any other Mode value is the server's own
// refusal, per the rule that the SDK checks only required fields and input
// shape, not value rules.
const (
	VirtualIPModeActiveActive  = "Active/Active"
	VirtualIPModeActivePassive = "Active/Passive"
)

// Status values GetVirtualIPAddress and the create wait observe. A status
// this SDK does not recognize keeps the wait polling rather than treating it
// as settled.
const (
	virtualIPStatusActive = "ACTIVE"
	virtualIPStatusError  = "ERROR"
)

// virtualIPTypePrivate is the Type value a private virtual IP carries,
// confirmed live: the create response, and the GreenNode console it feeds,
// both return the lowercase string "private". No other spelling is recorded,
// so DeleteVirtualIPAddress fails it closed with core.ErrInvalidInput.
const virtualIPTypePrivate = "private"

// isPrivateVirtualIPType reports whether t is the recorded private type.
func isPrivateVirtualIPType(t string) bool {
	return t == virtualIPTypePrivate
}

// virtualIPWriteResponse is CreateVirtualIPAddress and
// UpdateVirtualIPAddress's response shape: the virtual IP wrapped in a
// "data" field. GetVirtualIPAddress returns the same fields the same way.
// Both writes decode into virtualIPWriteData rather than the bare
// VirtualIPAddress model, so a field type that differs from the read model
// can never fail the decode (see vpcWriteData).
type virtualIPWriteResponse struct {
	Data virtualIPWriteData `json:"data"`
}

// virtualIPWriteData mirrors VirtualIPAddress's fields for
// CreateVirtualIPAddress and UpdateVirtualIPAddress's responses.
type virtualIPWriteData struct {
	UUID            string   `json:"uuid"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	EndpointAddress string   `json:"ipAddress"`
	VPCID           string   `json:"networkId"`
	SubnetID        string   `json:"subnetId"`
	Description     string   `json:"description"`
	SubnetCIDR      string   `json:"subnetCIDR"`
	VPCCIDR         string   `json:"networkCIDR"`
	AddressPairIPs  []string `json:"addressPairIps"`
	Status          string   `json:"status"`
	CreatedAt       string   `json:"createdAt"`
	NetworkName     string   `json:"networkName"`
	SubnetName      string   `json:"subnetName"`
	Type            string   `json:"type"`
	Mode            string   `json:"mode"`
	Zone            Zone     `json:"zone"`
}

// toVirtualIPAddress converts d to VirtualIPAddress. The two share an
// identical field layout today; a future field that must diverge breaks
// this conversion at compile time, forcing an explicit field-by-field
// mapping then, rather than a silent decode mismatch now.
func (d virtualIPWriteData) toVirtualIPAddress() VirtualIPAddress {
	return VirtualIPAddress(d)
}

// checkIPv4Address returns an error wrapping core.ErrInvalidInput unless
// value parses with net/netip.ParseAddr as an IPv4 address. Whether the
// address lies in the subnet, and whether it is already in use, stay the
// server's checks.
func checkIPv4Address(op, field, value string) error {
	addr, err := netip.ParseAddr(value)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("%w: %s: %s must be an IPv4 address such as 10.20.0.10, got %q",
			core.ErrInvalidInput, op, field, value)
	}
	return nil
}

// CreateVirtualIPAddressInput creates a private virtual IP in SubnetID.
//
// Mode is required and sent as given; VirtualIPModeActiveActive and
// VirtualIPModeActivePassive name the documented values, but the SDK does
// not check Mode against them, since the exact set of values the server
// accepts is a value rule, not a shape rule.
//
// IPAddress, when set, must parse with net/netip.ParseAddr as an IPv4
// address; otherwise it is chosen by the server. CreateVirtualIPAddress
// never sends tags, zoneId, or a public type.
type CreateVirtualIPAddressInput struct {
	SubnetID string `vngcloud:"required"`
	Name     string `vngcloud:"required"`
	Mode     string `vngcloud:"required"`

	IPAddress   string
	Description string
}

type CreateVirtualIPAddressOutput struct {
	VirtualIPAddress VirtualIPAddress
}

// virtualIPCreateBody is CreateVirtualIPAddress's request body. The API also
// takes tags and zoneId; the SDK sends neither.
type virtualIPCreateBody struct {
	SubnetID    string `json:"subnetId"`
	Name        string `json:"name"`
	Mode        string `json:"mode"`
	IPAddress   string `json:"ipAddress"`
	Description string `json:"description"`
}

// CreateVirtualIPAddress creates a private virtual IP in SubnetID.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError, the
// virtual IP may exist, and the caller lists virtual IP addresses and
// matches Name exactly, or the address given, since the server keeps an
// address unique within a subnet, before creating it again, rather than
// retrying blind.
//
// The create response's own Status decides whether CreateVirtualIPAddress
// waits: ACTIVE returns at once. Any other status polls GetVirtualIPAddress
// every 2 seconds for up to 60 seconds of elapsed time, tolerating a 404 (a
// virtual IP just created may not be readable at once). If the virtual IP
// reaches ERROR instead, or the wait's bound runs out, or a read or a sleep
// in that wait fails, the returned error wraps ErrFailed or ErrNotSettled
// and the Output still holds the virtual IP: the last one a read returned,
// or, if none did, the one the create response itself carried.
func (c *Client) CreateVirtualIPAddress(ctx context.Context, in *CreateVirtualIPAddressInput) (*CreateVirtualIPAddressOutput, error) {
	const op = "network.CreateVirtualIPAddress"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if in.IPAddress != "" {
		if err := checkIPv4Address(op, "IPAddress", in.IPAddress); err != nil {
			return nil, err
		}
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp virtualIPWriteResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "virtualIpAddress"}, nil),
		Body: virtualIPCreateBody{
			SubnetID:    in.SubnetID,
			Name:        in.Name,
			Mode:        in.Mode,
			IPAddress:   in.IPAddress,
			Description: in.Description,
		},
		OK: []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousVirtualIPCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the virtual IP may exist, list virtual ip addresses and match the name exactly, or the address if one was given, before creating it again"}
	}
	vip := resp.Data.toVirtualIPAddress()
	if vip.Status == virtualIPStatusActive {
		return &CreateVirtualIPAddressOutput{VirtualIPAddress: vip}, nil
	}

	settled, waitErr := c.waitVirtualIPActive(ctx, op, vip.UUID)
	if settled == nil {
		settled = &vip
	}
	return &CreateVirtualIPAddressOutput{VirtualIPAddress: *settled}, waitErr
}

// wrapAmbiguousVirtualIPCreateErr wraps err, from the create POST op just
// sent, with a hint to list virtual IP addresses before creating again,
// unless err is already a 4xx *core.APIError: a 4xx means the server
// rejected the request outright, so nothing was created and the exact same
// call is safe to retry. Any other error leaves whether the virtual IP was
// created unknown.
func wrapAmbiguousVirtualIPCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list virtual ip addresses and match the name exactly, or the address if one was given, before creating it again: %w", op, err)
}

// waitVirtualIPActive is CreateVirtualIPAddress's post-create wait, run only
// when the create response's own Status is not already virtualIPStatusActive:
// it reads virtualIPID with GetVirtualIPAddress until its Status reaches
// virtualIPStatusActive or virtualIPStatusError; any other status, or a 404,
// keeps it polling. Any other read failure stops the wait and is returned as
// is.
//
// It returns the last virtual IP a read returned alongside the outcome: nil
// error once ACTIVE, an error wrapping ErrFailed on ERROR, or an error
// wrapping ErrNotSettled once the bound runs out or a read or a sleep fails.
// The returned virtual IP is nil only when no read ever succeeded.
func (c *Client) waitVirtualIPActive(ctx context.Context, op, virtualIPID string) (*VirtualIPAddress, error) {
	var vip *VirtualIPAddress
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVirtualIPAddress(ctx, &GetVirtualIPAddressInput{VirtualIPAddressID: virtualIPID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			vip = &out.VirtualIPAddress
			switch vip.Status {
			case virtualIPStatusActive:
				return true, nil
			case virtualIPStatusError:
				return true, fmt.Errorf("%w: %s: virtual IP %s is ERROR", ErrFailed, op, virtualIPID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: virtual IP %s did not reach ACTIVE within %s; the virtual IP exists and this create must not be repeated",
				ErrNotSettled, op, virtualIPID, pollBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: virtual IP %s: %w", ErrNotSettled, op, virtualIPID, err)
	}
	return vip, err
}

// UpdateVirtualIPAddressInput changes a virtual IP's Name, Description, or
// Mode, leaving any field left nil unchanged. At least one field must be
// set. The API replaces every field on each PUT and requires Mode on every
// call, so UpdateVirtualIPAddress reads the virtual IP first and resends
// whichever field the caller leaves nil unchanged.
type UpdateVirtualIPAddressInput struct {
	VirtualIPAddressID string `vngcloud:"required"`

	Name        *string
	Description *string
	Mode        *string
}

type UpdateVirtualIPAddressOutput struct {
	VirtualIPAddress VirtualIPAddress
}

// virtualIPUpdateBody is UpdateVirtualIPAddress's request body.
type virtualIPUpdateBody struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

// UpdateVirtualIPAddress changes a virtual IP's name, description, mode, or
// any combination, read-merged against the virtual IP's current values so a
// field the caller leaves nil is resent unchanged.
//
// The PUT is marked idempotent: resending the same three fields is
// harmless, so it keeps the transport's normal retries. Its own response
// decodes into a fallback VirtualIPAddress, used only if the confirm read
// below fails. UpdateVirtualIPAddress then reads the virtual IP once more
// and returns that read as the Output; if that second read fails, the write
// has already succeeded and the returned error wraps ErrNotSettled instead.
func (c *Client) UpdateVirtualIPAddress(ctx context.Context, in *UpdateVirtualIPAddressInput) (*UpdateVirtualIPAddressOutput, error) {
	const op = "network.UpdateVirtualIPAddress"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VirtualIPAddressID", in.VirtualIPAddressID); err != nil {
		return nil, err
	}
	if in.Name == nil && in.Description == nil && in.Mode == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	current, err := c.GetVirtualIPAddress(ctx, &GetVirtualIPAddressInput{VirtualIPAddressID: in.VirtualIPAddressID})
	if err != nil {
		return nil, err
	}

	name := current.VirtualIPAddress.Name
	if in.Name != nil {
		name = *in.Name
	}
	description := current.VirtualIPAddress.Description
	if in.Description != nil {
		description = *in.Description
	}
	mode := current.VirtualIPAddress.Mode
	if in.Mode != nil {
		mode = *in.Mode
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp virtualIPWriteResponse
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPut,
		URL:        c.networkURL([]string{projectID, "virtualIpAddress", in.VirtualIPAddressID}, nil),
		Body:       virtualIPUpdateBody{Name: name, Description: description, Mode: mode},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, &resp); err != nil {
		return nil, err
	}

	// The PUT above already succeeded, so from here on every error means
	// the write may have landed and must not be sent again; fallback is
	// what the caller falls back to when the confirm read below fails,
	// taken from the PUT's own response.
	fallback := resp.Data.toVirtualIPAddress()
	updated, err := c.GetVirtualIPAddress(ctx, &GetVirtualIPAddressInput{VirtualIPAddressID: in.VirtualIPAddressID})
	if err != nil {
		return &UpdateVirtualIPAddressOutput{VirtualIPAddress: fallback}, fmt.Errorf("%w: %s: virtual IP %s: %w", ErrNotSettled, op, in.VirtualIPAddressID, err)
	}
	return &UpdateVirtualIPAddressOutput{VirtualIPAddress: updated.VirtualIPAddress}, nil
}

// DeleteVirtualIPAddressInput identifies the virtual IP to delete.
type DeleteVirtualIPAddressInput struct {
	VirtualIPAddressID string `vngcloud:"required"`
}

type DeleteVirtualIPAddressOutput struct{}

// DeleteVirtualIPAddress deletes a private virtual IP. It reads the virtual
// IP first and sends nothing when that read shows any address pair still
// attached, checked both by AddressPairIPs on the read and by
// ListAddressPairsByVirtualIPAddress (ErrInUse): a pair binds the address to
// a server interface, and deleting it moves traffic. It also sends nothing
// when the read's Type is not the recorded lowercase private value
// (core.ErrInvalidInput), so a public virtual IP, which has its own delete
// call and price, is never deleted through this call.
//
// DELETE is idempotent and keeps the transport's normal retries; a retry
// that finds the virtual IP already gone returns NotFound. The delete is
// taken as synchronous: the Output is {} once the 204 response comes back.
func (c *Client) DeleteVirtualIPAddress(ctx context.Context, in *DeleteVirtualIPAddressInput) (*DeleteVirtualIPAddressOutput, error) {
	const op = "network.DeleteVirtualIPAddress"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VirtualIPAddressID", in.VirtualIPAddressID); err != nil {
		return nil, err
	}

	current, err := c.GetVirtualIPAddress(ctx, &GetVirtualIPAddressInput{VirtualIPAddressID: in.VirtualIPAddressID})
	if err != nil {
		return nil, err
	}
	if !isPrivateVirtualIPType(current.VirtualIPAddress.Type) {
		return nil, fmt.Errorf("%w: %s: virtual IP %s has type %q, not a private virtual IP; it must be deleted through the public virtual IP call instead",
			core.ErrInvalidInput, op, in.VirtualIPAddressID, current.VirtualIPAddress.Type)
	}
	if len(current.VirtualIPAddress.AddressPairIPs) > 0 {
		return nil, fmt.Errorf("%w: %s: virtual IP %s has %d address pair(s) attached",
			ErrInUse, op, in.VirtualIPAddressID, len(current.VirtualIPAddress.AddressPairIPs))
	}
	pairs, err := c.ListAddressPairsByVirtualIPAddress(ctx, &ListAddressPairsByVirtualIPAddressInput{VirtualIPAddressID: in.VirtualIPAddressID})
	if err != nil {
		return nil, err
	}
	if len(pairs.Items) > 0 {
		return nil, fmt.Errorf("%w: %s: virtual IP %s has %d address pair(s) attached",
			ErrInUse, op, in.VirtualIPAddressID, len(pairs.Items))
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "virtualIpAddress", in.VirtualIPAddressID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteVirtualIPAddressOutput{}, nil
}
