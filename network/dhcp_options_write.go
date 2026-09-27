package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// dhcpOptionsStatusActive is the Status SetVPCDHCPOptions requires of the
// target set before it sends the PATCH. The server gives no other status
// this SDK is known to observe.
const dhcpOptionsStatusActive = "ACTIVE"

// dhcpOptionsSystemPrefix names the sets enabling VPC Private DNS creates
// and attaches automatically (dhcp-option-dns-<n>, live). The API gives no
// field marking a set as one of these, so the SDK treats the name prefix as
// the signal; CreateDHCPOptions refuses it so a user set can never be
// mistaken for one.
const dhcpOptionsSystemPrefix = "dhcp-option-dns-"

// isDHCPOptionsSystemSet reports whether name is one enabling VPC Private
// DNS would create, by the dhcpOptionsSystemPrefix convention.
func isDHCPOptionsSystemSet(name string) bool {
	return strings.HasPrefix(name, dhcpOptionsSystemPrefix)
}

// ErrInUse is defined in vpcs_write.go; DeleteDHCPOptions returns it,
// nothing sent, when a pre-delete read shows the set still attached to a
// VPC. ErrDefaultResource and ErrBusy are defined in route_tables_write.go;
// SetVPCDHCPOptions returns ErrDefaultResource when the VPC has Private DNS
// enabled or already carries a system set, or when the target set is itself
// a system set, and ErrBusy when the target set is not ACTIVE. In every
// case nothing was sent.

// dhcpOptionsCreateBody is CreateDHCPOptions's request body. The API also
// takes tags and zoneId; the SDK sends neither.
type dhcpOptionsCreateBody struct {
	Name       string   `json:"name"`
	DNSServers []string `json:"dnsServers"`
	MTU        *int     `json:"mtu,omitempty"`
}

// dhcpOptionsWriteResponse is CreateDHCPOptions's response shape: the set
// wrapped in a "data" field. GetDHCPOptions returns the same fields at the
// top level instead.
type dhcpOptionsWriteResponse struct {
	Data dhcpOptionsWriteData `json:"data"`
}

// dhcpOptionsWriteData mirrors DHCPOptions's fields for CreateDHCPOptions's
// response. CreateDHCPOptions decodes into this private type and maps it
// with toDHCPOptions, rather than the bare DHCPOptions model, so a create
// response field that later turns out to differ from the read model can
// never fail the decode silently; see vpcWriteData for the same pattern.
type dhcpOptionsWriteData struct {
	UUID       string   `json:"uuid"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	DNSServers []string `json:"dnsServers"`
	MTU        int      `json:"mtu"`
	VPCIDs     []string `json:"associatedNetworks"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

func (d dhcpOptionsWriteData) toDHCPOptions() DHCPOptions {
	return DHCPOptions(d)
}

// CreateDHCPOptions creates a DHCP options set.
//
// DNSServers must hold at least one address, and each must parse with
// net/netip.ParseAddr as IPv4; the four-server limit stays on the server.
// Name must not start with dhcpOptionsSystemPrefix, reserved for the sets
// enabling VPC Private DNS creates; any of these shape problems fails with
// core.ErrInvalidInput and sends nothing. MTU is sent only when non-nil; the
// server's default is 1450. The request never sends tags or zoneId.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the set may exist, and the caller runs
// list-dhcp-options --name and matches Name exactly before creating it
// again, rather than retrying blind.
//
// CreateDHCPOptions has no post-create wait: the design's waits table gives
// it none, so the create response itself is the Output.
func (c *Client) CreateDHCPOptions(ctx context.Context, in *CreateDHCPOptionsInput) (*CreateDHCPOptionsOutput, error) {
	const op = "network.CreateDHCPOptions"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if len(in.DNSServers) == 0 {
		return nil, fmt.Errorf("%w: %s requires at least one DNS server", core.ErrInvalidInput, op)
	}
	for _, server := range in.DNSServers {
		addr, err := netip.ParseAddr(server)
		if err != nil || !addr.Is4() {
			return nil, fmt.Errorf("%w: %s: DNSServers must be IPv4 addresses, got %q", core.ErrInvalidInput, op, server)
		}
	}
	if isDHCPOptionsSystemSet(in.Name) {
		return nil, fmt.Errorf("%w: %s: Name must not start with %q, reserved for the set VPC Private DNS creates",
			core.ErrInvalidInput, op, dhcpOptionsSystemPrefix)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp dhcpOptionsWriteResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "dhcp_option"}, nil),
		Body:      dhcpOptionsCreateBody{Name: in.Name, DNSServers: in.DNSServers, MTU: in.MTU},
		OK:        []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousDHCPOptionsCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the set may exist, run list-dhcp-options --name and match the name exactly before creating it again"}
	}
	return &CreateDHCPOptionsOutput{DHCPOptions: resp.Data.toDHCPOptions()}, nil
}

// wrapAmbiguousDHCPOptionsCreateErr wraps err, from the create POST op just
// sent, with a hint to list DHCP options sets before creating again, unless
// err is already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so nothing was created and the exact same call is safe
// to retry. Any other error leaves whether the set was created unknown.
func wrapAmbiguousDHCPOptionsCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; run list-dhcp-options --name and match the name exactly before creating it again: %w", op, err)
}

// DeleteDHCPOptions deletes a DHCP options set. It reads the set first with
// GetDHCPOptions and refuses, sending nothing, when the read's UUID does not
// equal DHCPOptionsID: acting on a set the caller did not ask for is never
// safe, whatever caused the mismatch. It also sends nothing when VPCIDs is
// not empty (ErrInUse), naming the VPCs: the product docs require a set to
// be detached from every VPC before delete. An unattached set left by a
// deleted Private DNS VPC deletes like any other set.
//
// The server's own refusal is the final guard. DELETE is idempotent and
// keeps the transport's normal retries; a retry that finds the set already
// gone returns NotFound, as does a repeat call to DeleteDHCPOptions itself,
// since its own guard read runs first. DeleteDHCPOptions has no post-delete
// wait: the design's waits table gives it none.
func (c *Client) DeleteDHCPOptions(ctx context.Context, in *DeleteDHCPOptionsInput) (*DeleteDHCPOptionsOutput, error) {
	const op = "network.DeleteDHCPOptions"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "DHCPOptionsID", in.DHCPOptionsID); err != nil {
		return nil, err
	}

	current, err := c.GetDHCPOptions(ctx, &GetDHCPOptionsInput{DHCPOptionsID: in.DHCPOptionsID})
	if err != nil {
		return nil, err
	}
	if current.DHCPOptions.UUID != in.DHCPOptionsID {
		return nil, fmt.Errorf("%s: get returned DHCP options set %q instead of the requested %q; refusing to delete an unexpected set",
			op, current.DHCPOptions.UUID, in.DHCPOptionsID)
	}
	if len(current.DHCPOptions.VPCIDs) > 0 {
		return nil, fmt.Errorf("%w: %s: DHCP options set %s is attached to VPC(s) %s; detach it from every VPC first",
			ErrInUse, op, in.DHCPOptionsID, strings.Join(current.DHCPOptions.VPCIDs, ", "))
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "dhcp_option", in.DHCPOptionsID}, nil),
		OK:        []int{204},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	return &DeleteDHCPOptionsOutput{}, nil
}

// dhcpOptionsSetBody is SetVPCDHCPOptions's PATCH body. The API also takes
// tags and zoneId; the SDK sends neither.
type dhcpOptionsSetBody struct {
	DHCPOptionID string `json:"dhcpOptionId"`
}

// SetVPCDHCPOptions moves VPCID onto DHCPOptionsID's set. There is no call
// to clear a VPC's set, so this is a one-way write: the VPC can move to
// another set but never back to none.
//
// It reads the VPC first. When its DHCPOptionID already equals
// DHCPOptionsID, it returns at once with Changed false, sending nothing.
// Otherwise, a VPC whose DNSStatus is not DISABLED, or whose current set is
// already a system set (by name prefix), is refused with ErrDefaultResource
// and nothing sent: replacing the Private DNS set would cut the VPC off
// from its private zones, and the API cannot swap it back. When the VPC
// read gives a current DHCPOptionID but an empty DHCPOptionName, the name
// prefix cannot tell a system set from a user one, so SetVPCDHCPOptions
// reads that set by id instead of assuming either way; a failure reading it
// is returned as is, sending nothing.
//
// It then reads the target set with GetDHCPOptions: a 404 surfaces as
// NotFound, a system set (by name prefix) is refused with
// ErrDefaultResource, and any status other than ACTIVE is refused with
// ErrBusy. Each of these refusals sends nothing.
//
// The PATCH is marked idempotent: sending the same DHCPOptionsID twice is
// harmless. A PATCH failure that is a 4xx *core.APIError is returned as is,
// since the server never acted on it; any other failure, such as a 5xx or a
// network error after the dial succeeded, wraps a hint that the change may
// already be in place and that get-vpc shows the VPC's current set. The
// PATCH's own response is not decoded; on success, SetVPCDHCPOptions instead
// waits for a follow-up GetVPC to show DHCPOptionID equal to DHCPOptionsID,
// polling every 2 seconds for up to 60 seconds of elapsed time, per the
// design's waits table. A VPC that reaches ERROR during that wait fails
// with ErrFailed; the bound running out, or a read or a sleep failing,
// wraps ErrNotSettled. Either way Changed is true once the PATCH is sent,
// and the Output falls back to the VPC read before the PATCH when no read
// during the wait ever succeeds.
func (c *Client) SetVPCDHCPOptions(ctx context.Context, in *SetVPCDHCPOptionsInput) (*SetVPCDHCPOptionsOutput, error) {
	const op = "network.SetVPCDHCPOptions"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "VPCID", in.VPCID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "DHCPOptionsID", in.DHCPOptionsID); err != nil {
		return nil, err
	}

	current, err := c.GetVPC(ctx, &GetVPCInput{VPCID: in.VPCID})
	if err != nil {
		return nil, err
	}
	if current.VPC.DHCPOptionID == in.DHCPOptionsID {
		return &SetVPCDHCPOptionsOutput{VPC: current.VPC, Changed: false}, nil
	}
	currentIsSystemSet, err := c.currentDHCPOptionsIsSystemSet(ctx, &current.VPC)
	if err != nil {
		return nil, err
	}
	if current.VPC.DNSStatus != vpcDNSStatusDisabled || currentIsSystemSet {
		return nil, fmt.Errorf("%w: %s: VPC %s has Private DNS enabled or a system DHCP options set; setting another set would cut it off from its private zones",
			ErrDefaultResource, op, in.VPCID)
	}

	target, err := c.GetDHCPOptions(ctx, &GetDHCPOptionsInput{DHCPOptionsID: in.DHCPOptionsID})
	if err != nil {
		return nil, err
	}
	if isDHCPOptionsSystemSet(target.DHCPOptions.Name) {
		return nil, fmt.Errorf("%w: %s: DHCP options set %s is a system set", ErrDefaultResource, op, in.DHCPOptionsID)
	}
	if target.DHCPOptions.Status != dhcpOptionsStatusActive {
		return nil, fmt.Errorf("%w: %s: DHCP options set %s is %s, not ACTIVE", ErrBusy, op, in.DHCPOptionsID, target.DHCPOptions.Status)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation:  op,
		Method:     http.MethodPatch,
		URL:        c.networkURL([]string{projectID, "networks", in.VPCID, "updateDhcpOption"}, nil),
		Body:       dhcpOptionsSetBody{DHCPOptionID: in.DHCPOptionsID},
		OK:         []int{200},
		Idempotent: true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, wrapAmbiguousDHCPOptionsPatchErr(op, in.VPCID, err)
	}

	settled, waitErr := c.waitVPCDHCPOptionsSet(ctx, op, in.VPCID, in.DHCPOptionsID)
	if settled == nil {
		settled = &current.VPC
	}
	return &SetVPCDHCPOptionsOutput{VPC: *settled, Changed: true}, waitErr
}

// currentDHCPOptionsIsSystemSet reports whether vpc's current DHCP options
// set is a system set. vpc.DHCPOptionName usually names it, but the VPC read
// can give a current DHCPOptionID with an empty DHCPOptionName; the name
// prefix check cannot tell a system set from a user one on an empty name, so
// this reads the set by id instead of guessing, and never replaces a system
// set because its name happened to be missing. A VPC with no current set
// (DHCPOptionID empty) is never a system set.
func (c *Client) currentDHCPOptionsIsSystemSet(ctx context.Context, vpc *VPC) (bool, error) {
	if vpc.DHCPOptionID == "" {
		return false, nil
	}
	if vpc.DHCPOptionName != "" {
		return isDHCPOptionsSystemSet(vpc.DHCPOptionName), nil
	}
	current, err := c.GetDHCPOptions(ctx, &GetDHCPOptionsInput{DHCPOptionsID: vpc.DHCPOptionID})
	if err != nil {
		return false, err
	}
	return isDHCPOptionsSystemSet(current.DHCPOptions.Name), nil
}

// wrapAmbiguousDHCPOptionsPatchErr wraps err, from the PATCH SetVPCDHCPOptions
// just sent, with a hint to check the VPC's current set with get-vpc, unless
// err is already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so the VPC's set was never changed. Any other failure,
// such as a 5xx or a network error after the dial succeeded, leaves whether
// the change landed unknown; the PATCH is idempotent and safe to send again
// with the same DHCPOptionsID.
func wrapAmbiguousDHCPOptionsPatchErr(op, vpcID string, err error) error {
	if err == nil {
		return nil
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%s: the change may already be in place; run get-vpc to see VPC %s's current DHCP options set: %w", op, vpcID, err)
}

// waitVPCDHCPOptionsSet is SetVPCDHCPOptions's post-PATCH wait: it reads
// vpcID with GetVPC, using the package's generic pollInterval (2 seconds)
// and pollBound (60 seconds), per the design's waits table, until its
// DHCPOptionID equals target. A VPC that reaches vpcStatusError instead
// stops the wait with an error wrapping ErrFailed; any other read failure
// also stops it, wrapped in ErrNotSettled, as is the bound running out.
//
// It returns the last VPC a read returned alongside the outcome. The
// returned VPC is nil only when no read ever succeeded, in which case the
// caller falls back to the VPC it read before sending the PATCH.
func (c *Client) waitVPCDHCPOptionsSet(ctx context.Context, op, vpcID, target string) (*VPC, error) {
	var vpc *VPC
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetVPC(ctx, &GetVPCInput{VPCID: vpcID})
			if err != nil {
				return true, err
			}
			vpc = &out.VPC
			switch {
			case vpc.DHCPOptionID == target:
				return true, nil
			case vpc.Status == vpcStatusError:
				return true, fmt.Errorf("%w: %s: VPC %s is ERROR", ErrFailed, op, vpcID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: VPC %s did not show DHCP options set %s within %s; the PATCH was sent and is safe to repeat",
				ErrNotSettled, op, vpcID, target, pollBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: VPC %s: %w", ErrNotSettled, op, vpcID, err)
	}
	return vpc, err
}
