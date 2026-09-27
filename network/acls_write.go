package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Status values GetNetworkACL and the ACL waits observe. A status this SDK
// does not recognize keeps a wait polling rather than treating it as
// settled.
const (
	aclStatusActive = "ACTIVE"
	aclStatusError  = "ERROR"
)

// isACLServerError reports whether err is a *core.APIError with a 5xx
// status: the trigger for GetNetworkACL and DeleteNetworkACL's list
// confirm. A plain 404 already becomes core.ErrNotFound on its own, through
// the shared transport, and never reaches this check.
func isACLServerError(err error) bool {
	var apiErr *core.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode >= 500 && apiErr.StatusCode < 600
}

// aclFoundAfterServerError lists network ACLs once and reports whether id
// is present. GetNetworkACL and DeleteNetworkACL call this only after a 5xx,
// never after a 404: a deleted ACL's own get returns 500, not 404, unlike
// every other resource this package deletes, so a 5xx here needs the same
// list confirm a 404 gets automatically elsewhere. Listing once, rather
// than paging through every ACL, matches the design; ListNetworkACLs itself
// defaults to a page of 10000, so this misses an id only in a project with
// more network ACLs than that.
func (c *Client) aclFoundAfterServerError(ctx context.Context, id string) (bool, error) {
	out, err := c.ListNetworkACLs(ctx, nil)
	if err != nil {
		return false, err
	}
	for _, acl := range out.Items {
		if acl.UUID == id || acl.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// GetNetworkACLInput identifies the network ACL to read.
type GetNetworkACLInput struct {
	NetworkACLID string `vngcloud:"required"`
}

type GetNetworkACLOutput struct {
	ACL ACL
}

// GetNetworkACL reads a network ACL, including its rules and associated
// subnets.
//
// Confirmed live, a deleted ACL's get returns 500, not 404. Because of that,
// this SDK never treats a bare 5xx here as NotFound on its own: after any
// error with a 5xx status, GetNetworkACL calls ListNetworkACLs once and
// checks whether NetworkACLID is still listed. Absent, it returns an error
// wrapping core.ErrNotFound; listed, or if the list call itself fails, it
// returns the original 5xx. A plain 404 (an id that was never valid) still
// becomes core.ErrNotFound through the shared transport, with no list call.
func (c *Client) GetNetworkACL(ctx context.Context, in *GetNetworkACLInput) (*GetNetworkACLOutput, error) {
	const op = "network.GetNetworkACL"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "NetworkACLID", in.NetworkACLID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data ACL `json:"data"`
	}
	getErr := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.networkURL([]string{projectID, "network-acl", in.NetworkACLID}, nil),
		OK:        []int{200},
	}, &resp)
	if getErr == nil {
		return &GetNetworkACLOutput{ACL: resp.Data}, nil
	}
	if !isACLServerError(getErr) {
		return nil, getErr
	}
	found, listErr := c.aclFoundAfterServerError(ctx, in.NetworkACLID)
	if listErr != nil || found {
		return nil, getErr
	}
	return nil, fmt.Errorf("%w: %s: network ACL %s", core.ErrNotFound, op, in.NetworkACLID)
}

// createACLBody is CreateNetworkACL's request body, confirmed live. The API
// also takes tags and zoneId; the SDK sends neither.
type createACLBody struct {
	Name string `json:"name"`
	VPC  string `json:"vpc"`
}

// CreateNetworkACLInput creates a network ACL in a VPC. Whether ACL names
// are unique per VPC or per project is not yet confirmed live; a duplicate
// name fails with the server's own message either way.
type CreateNetworkACLInput struct {
	VPCID string `vngcloud:"required"`
	Name  string `vngcloud:"required"`
}

type CreateNetworkACLOutput struct {
	ACL ACL
}

// CreateNetworkACL creates a network ACL in a VPC. The new ACL is ACTIVE at
// once (confirmed live) and holds the server's default rules, at least an
// inbound allow-all rule at priority 0; it starts with no associated
// subnets. CreateNetworkACL takes no wait: its response is final.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the ACL may exist, and the caller lists network
// ACLs by Name and matches it exactly (the list's own name filter may match
// by substring) before creating it again, rather than retrying blind.
func (c *Client) CreateNetworkACL(ctx context.Context, in *CreateNetworkACLInput) (*CreateNetworkACLOutput, error) {
	const op = "network.CreateNetworkACL"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data ACL `json:"data"`
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "network-acl"}, nil),
		Body:      createACLBody{Name: in.Name, VPC: in.VPCID},
		OK:        []int{201},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousACLCreateErr(op, err)
	}
	if resp.Data.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the ACL may exist, list network ACLs and match the name exactly before creating it again"}
	}
	return &CreateNetworkACLOutput{ACL: resp.Data}, nil
}

// wrapAmbiguousACLCreateErr wraps err, from the create POST op just sent,
// with a hint to list network ACLs before creating again, unless err is
// already a 4xx *core.APIError: a 4xx means the server rejected the request
// outright, so nothing was created and the exact same call is safe to
// retry. Any other error leaves whether the ACL was created unknown.
func wrapAmbiguousACLCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list network ACLs and match the name exactly before creating it again: %w", op, err)
}

// DeleteNetworkACLInput identifies the network ACL to delete.
type DeleteNetworkACLInput struct {
	NetworkACLID string `vngcloud:"required"`
}

type DeleteNetworkACLOutput struct{}

// DeleteNetworkACL deletes a network ACL. It reads the ACL first with
// GetNetworkACL and sends nothing when that read shows the ACL is a
// project's default ACL (DefaultACL, ErrDefaultResource) or has any
// associated subnet (SubnetIDs, ErrInUse); disassociate every subnet with
// DisassociateNetworkACLSubnet first.
//
// The delete itself is taken as synchronous: a 204 confirms it, and there
// is no wait. Confirmed live, a deleted ACL's get returns 500, not 404, so
// a repeat delete would too; after a 5xx on the DELETE, DeleteNetworkACL
// calls ListNetworkACLs once, the same list confirm GetNetworkACL uses: the
// ACL absent from that list means the delete already took effect, and this
// call returns success; listed, or if the list call itself fails, it
// returns the original 5xx.
func (c *Client) DeleteNetworkACL(ctx context.Context, in *DeleteNetworkACLInput) (*DeleteNetworkACLOutput, error) {
	const op = "network.DeleteNetworkACL"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "NetworkACLID", in.NetworkACLID); err != nil {
		return nil, err
	}

	acl, err := c.GetNetworkACL(ctx, &GetNetworkACLInput{NetworkACLID: in.NetworkACLID})
	if err != nil {
		return nil, err
	}
	if acl.ACL.DefaultACL {
		return nil, fmt.Errorf("%w: %s: network ACL %s is a default ACL", ErrDefaultResource, op, in.NetworkACLID)
	}
	if len(acl.ACL.SubnetIDs) > 0 {
		return nil, fmt.Errorf("%w: %s: network ACL %s has %d associated subnet(s); disassociate them first",
			ErrInUse, op, in.NetworkACLID, len(acl.ACL.SubnetIDs))
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	delErr := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "network-acl", in.NetworkACLID}, nil),
		OK:        []int{204},
	}, nil)
	if delErr == nil {
		return &DeleteNetworkACLOutput{}, nil
	}
	if !isACLServerError(delErr) {
		return nil, delErr
	}
	found, listErr := c.aclFoundAfterServerError(ctx, in.NetworkACLID)
	if listErr != nil || found {
		return nil, delErr
	}
	return &DeleteNetworkACLOutput{}, nil
}

// waitACLPreWriteActive is AddNetworkACLRule, RemoveNetworkACLRule,
// AssociateNetworkACLSubnet, and DisassociateNetworkACLSubnet's pre-write
// read: it reads networkACLID with GetNetworkACL until its Status reaches
// aclStatusActive, within the shorter pollBound (60 seconds, the design's
// pre-write bound for any replace). A read failure, including NotFound,
// stops the wait at once rather than tolerating it, as with the route
// table's own pre-write wait: the ACL this wait reads is an existing one the
// caller did not just create, so a failure here is real, not a race with
// the server making a brand new id readable.
//
// The design defines no failure status for this wait, so past the bound it
// returns ErrBusy rather than ErrNotSettled: nothing has been sent yet, and
// the caller may simply run the same call again later.
func (c *Client) waitACLPreWriteActive(ctx context.Context, op, networkACLID string) (*ACL, error) {
	var acl *ACL
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetNetworkACL(ctx, &GetNetworkACLInput{NetworkACLID: networkACLID})
			if err != nil {
				return true, err
			}
			acl = &out.ACL
			return acl.Status == aclStatusActive, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: network ACL %s is not ACTIVE within %s; nothing sent",
				ErrBusy, op, networkACLID, pollBound)
		},
	)
	return acl, err
}

// waitACLSettled polls networkACLID with GetNetworkACL, using the shorter
// pollBound, until its Status reaches aclStatusActive or aclStatusError. A
// 404 during the wait keeps polling, since a write in flight may briefly
// make the read fail; any other read failure stops it and is returned as
// is. waitACLRulesSettled and waitACLSubnetsSettled each add their own
// confirm check on top of this.
func (c *Client) waitACLSettled(ctx context.Context, op, networkACLID string) (*ACL, error) {
	var acl *ACL
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetNetworkACL(ctx, &GetNetworkACLInput{NetworkACLID: networkACLID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			acl = &out.ACL
			switch acl.Status {
			case aclStatusActive:
				return true, nil
			case aclStatusError:
				return true, fmt.Errorf("%w: %s: network ACL %s is ERROR", ErrFailed, op, networkACLID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: network ACL %s did not settle within %s; the write may have reached the server and is safe to run again",
				ErrNotSettled, op, networkACLID, pollBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: network ACL %s: %w", ErrNotSettled, op, networkACLID, err)
	}
	return acl, err
}
