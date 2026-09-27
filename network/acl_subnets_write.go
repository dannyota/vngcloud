package network

import (
	"context"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// AssociateNetworkACLSubnetInput associates a subnet with a network ACL.
type AssociateNetworkACLSubnetInput struct {
	NetworkACLID string `vngcloud:"required"`
	SubnetID     string `vngcloud:"required"`

	NoWait bool
}

type AssociateNetworkACLSubnetOutput struct {
	ACL     ACL
	Changed bool

	// PreviousNetworkACLID is the id GetSubnet read from the subnet's own
	// record just before the move, naming the ACL it was associated with,
	// if any. It is set only when Changed is true: the no-op path (the
	// subnet was already in this ACL) never reads the subnet, so it has
	// nothing to report.
	PreviousNetworkACLID string
}

// AssociateNetworkACLSubnet associates a subnet with a network ACL. A
// subnet belongs to at most one ACL, so associating one already associated
// with a different ACL moves it there; its rules apply to the subnet's
// traffic at once. Output.PreviousNetworkACLID names the ACL it moved
// from.
//
// The API replaces an ACL's whole subnet list on every write, so this is a
// read-merge write like AddNetworkACLRule: it reads the ACL's current
// subnets with GetNetworkACL, waiting first for the ACL to reach ACTIVE
// within a pre-write bound (ErrBusy, nothing sent, if it does not).
// Immediately before sending that write, it re-reads the ACL and refuses
// with ErrBusy, again sending nothing, if the subnet list no longer
// matches the read this merge started from; see putACLSubnetsAndConfirm.
//
// A SubnetID already in that list makes this a no-op: Changed is false and
// nothing is sent, including no read of the subnet itself. Otherwise,
// before sending anything, it reads the subnet with GetSubnet under the
// ACL's own VPC (ACL.VPCID): a 404 there becomes core.ErrNotFound, so a
// subnet of a different VPC is never sent.
//
// Without NoWait, it waits for the ACL to return to ACTIVE after its PUT,
// then confirms that a fresh read names exactly the subnet list just sent.
// A failure at any of these points returns an error wrapping ErrFailed,
// ErrBusy, or ErrNotSettled, as AddNetworkACLRule's doc comment describes;
// the PUT itself is not resent on a rerun.
//
// Confirmed live, a successful call here leaves the ACL busy for about 20
// more seconds, unlike after a rules write: Status reads ACTIVE at once and
// stays there, so nothing this call returns marks the window. The very next
// write to this ACL, of any kind, can still get ErrBusy during that time.
// ErrBusy always means nothing was sent, so it is safe to wait a few seconds
// and call again; this SDK never retries automatically, since the caller,
// not the SDK, must decide whether that next write is itself idempotent.
func (c *Client) AssociateNetworkACLSubnet(ctx context.Context, in *AssociateNetworkACLSubnetInput) (*AssociateNetworkACLSubnetOutput, error) {
	const op = "network.AssociateNetworkACLSubnet"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "NetworkACLID", in.NetworkACLID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SubnetID", in.SubnetID); err != nil {
		return nil, err
	}

	acl, err := c.waitACLPreWriteActive(ctx, op, in.NetworkACLID)
	if err != nil {
		return nil, err
	}
	for _, id := range acl.SubnetIDs {
		if id == in.SubnetID {
			return &AssociateNetworkACLSubnetOutput{ACL: *acl, Changed: false}, nil
		}
	}

	subnet, err := c.GetSubnet(ctx, &GetSubnetInput{VPCID: acl.VPCID, SubnetID: in.SubnetID})
	if err != nil {
		return nil, err
	}

	subnetIDs := append(append([]string{}, acl.SubnetIDs...), in.SubnetID)
	updated, err := c.putACLSubnetsAndConfirm(ctx, op, in.NetworkACLID, subnetIDs, acl, in.NoWait)
	if updated == nil {
		return nil, err
	}
	return &AssociateNetworkACLSubnetOutput{ACL: *updated, Changed: true, PreviousNetworkACLID: subnet.Subnet.InterfaceACLPolicyID}, err
}

// DisassociateNetworkACLSubnetInput removes a subnet's association with a
// network ACL.
type DisassociateNetworkACLSubnetInput struct {
	NetworkACLID string `vngcloud:"required"`
	SubnetID     string `vngcloud:"required"`

	NoWait bool
}

type DisassociateNetworkACLSubnetOutput struct {
	ACL     ACL
	Changed bool
}

// DisassociateNetworkACLSubnet removes a subnet's association with a
// network ACL. It is the read-merge write AssociateNetworkACLSubnet's doc
// comment describes, in reverse: it waits for the ACL to be ACTIVE (ErrBusy,
// nothing sent, past the pre-write bound), then sends back every
// associated subnet it read except the one removed. As for
// AssociateNetworkACLSubnet, it also re-reads the ACL immediately before
// sending that write and refuses with ErrBusy, again sending nothing, if
// the subnet list has changed since the first read; see
// putACLSubnetsAndConfirm.
//
// A SubnetID not in the ACL's current subnet list makes this a no-op:
// Changed is false and nothing is sent, unlike RemoveNetworkACLRule, which
// fails with core.ErrNotFound for an absent rule. What a subnet falls back
// to once disassociated is not yet confirmed live.
//
// Without NoWait, it waits and confirms exactly as AssociateNetworkACLSubnet
// does; see its doc comment for the wait, the confirm, rerun safety, and the
// roughly 20-second busy window a successful call here leaves behind, which
// Status does not show and which can make the very next write to this ACL
// return ErrBusy.
func (c *Client) DisassociateNetworkACLSubnet(ctx context.Context, in *DisassociateNetworkACLSubnetInput) (*DisassociateNetworkACLSubnetOutput, error) {
	const op = "network.DisassociateNetworkACLSubnet"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "NetworkACLID", in.NetworkACLID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "SubnetID", in.SubnetID); err != nil {
		return nil, err
	}

	acl, err := c.waitACLPreWriteActive(ctx, op, in.NetworkACLID)
	if err != nil {
		return nil, err
	}
	idx := -1
	for i, id := range acl.SubnetIDs {
		if id == in.SubnetID {
			idx = i
			break
		}
	}
	if idx == -1 {
		return &DisassociateNetworkACLSubnetOutput{ACL: *acl, Changed: false}, nil
	}
	subnetIDs := append(append([]string{}, acl.SubnetIDs[:idx]...), acl.SubnetIDs[idx+1:]...)

	updated, err := c.putACLSubnetsAndConfirm(ctx, op, in.NetworkACLID, subnetIDs, acl, in.NoWait)
	if updated == nil {
		return nil, err
	}
	return &DisassociateNetworkACLSubnetOutput{ACL: *updated, Changed: true}, err
}

// aclSubnetsReplaceBody is the PUT .../subnets request body, inferred from
// the design: the whole subnet id list, plus the ACL's own id.
type aclSubnetsReplaceBody struct {
	ACLID       string   `json:"aclId"`
	SubnetUUIDs []string `json:"subnetUuids"`
}

// putACLSubnetsAndConfirm sends subnetIDs as networkACLID's whole
// associated-subnet list, then, unless noWait, waits for the ACL to return
// to ACTIVE and confirms that a fresh read names exactly subnetIDs. base is
// the pre-write read Associate or DisassociateNetworkACLSubnet already
// took; with noWait its SubnetIDs field is replaced with subnetIDs and
// returned as is.
//
// Immediately before sending the PUT, it re-reads the ACL and compares
// those subnets to base.SubnetIDs; see putACLRulesAndConfirm's doc comment
// for why a mismatch sends nothing and returns an error wrapping ErrBusy
// instead, for the narrower race that remains after the PUT itself is
// sent, and for wrapACLPutFailure, which classifies the PUT's own failure
// the same way here as there: Once true so a retry can never land in the
// busy window, a busy 400 on that single attempt wraps ErrBusy, any other
// 4xx is returned as is, and a 5xx, a network error, or a timeout wraps
// ErrNotSettled instead.
func (c *Client) putACLSubnetsAndConfirm(ctx context.Context, op, networkACLID string, subnetIDs []string, base *ACL, noWait bool) (*ACL, error) {
	recheck, err := c.GetNetworkACL(ctx, &GetNetworkACLInput{NetworkACLID: networkACLID})
	if err != nil {
		return nil, err
	}
	if !stringSetsEqual(recheck.ACL.SubnetIDs, base.SubnetIDs) {
		return nil, fmt.Errorf("%w: %s: network ACL %s changed since it was read; nothing sent, run the call again",
			ErrBusy, op, networkACLID)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.networkURL([]string{projectID, "network-acl", networkACLID, "subnets"}, nil),
		Body:      aclSubnetsReplaceBody{ACLID: networkACLID, SubnetUUIDs: subnetIDs},
		OK:        []int{200},
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, wrapACLPutFailure(op, networkACLID, err)
	}
	if noWait {
		fallback := *base
		fallback.SubnetIDs = subnetIDs
		return &fallback, nil
	}
	return c.waitACLSubnetsSettled(ctx, op, networkACLID, subnetIDs)
}

// stringSetsEqual reports whether a and b hold the same strings, regardless
// of order or duplicate count position (a duplicate must still appear the
// same number of times in both).
func stringSetsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	remaining := make(map[string]int, len(b))
	for _, s := range b {
		remaining[s]++
	}
	for _, s := range a {
		if remaining[s] == 0 {
			return false
		}
		remaining[s]--
	}
	return true
}

// waitACLSubnetsSettled is AssociateNetworkACLSubnet and
// DisassociateNetworkACLSubnet's post-write wait unless NoWait is set; see
// waitACLRulesSettled's doc comment, which it otherwise matches, comparing
// subnet ids instead of rules.
func (c *Client) waitACLSubnetsSettled(ctx context.Context, op, networkACLID string, sent []string) (*ACL, error) {
	acl, err := c.waitACLSettled(ctx, op, networkACLID)
	if err != nil {
		return acl, err
	}
	if !stringSetsEqual(acl.SubnetIDs, sent) {
		return acl, fmt.Errorf("%w: %s: network ACL %s: subnets read after the write do not match what was sent; another writer may have changed the ACL",
			ErrNotSettled, op, networkACLID)
	}
	return acl, nil
}
