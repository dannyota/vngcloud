package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

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

// aclBusyMessages are the two message substrings, each matched
// case-insensitively, that mark a 400 as the ACL refusing a write because it
// is still busy settling an earlier one, rather than rejecting the request
// outright. Both are confirmed live: a rules PUT leaves the ACL busy for
// roughly 18 to 23 seconds, visible in Status leaving aclStatusActive during
// that window, and a write sent into it gets "is busy doing something". A
// subnets PUT (associate or disassociate) leaves the ACL busy for about 20
// seconds too, but Status stays aclStatusActive throughout, so nothing in a
// read marks the window; a write sent into it gets "is being updated"
// instead. Both messages leave the ACL unchanged.
var aclBusyMessages = []string{
	"is busy doing something",
	"is being updated",
}

// isACLBusyErr reports whether err is a *core.APIError whose message
// contains one of aclBusyMessages: the trigger for wrapACLBusyErr and
// wrapACLPutFailure, checked on a rules or subnets PUT and on a DELETE.
func isACLBusyErr(err error) bool {
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	for _, phrase := range aclBusyMessages {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}

// wrapACLBusyErr rewraps err with ErrBusy when isACLBusyErr(err) is true.
// DeleteNetworkACL calls this directly on its own DELETE's error;
// wrapACLPutFailure calls it too, as one case of its own broader
// classification. Any other error passes through unchanged.
func wrapACLBusyErr(err error) error {
	if isACLBusyErr(err) {
		var apiErr *core.APIError
		errors.As(err, &apiErr)
		return fmt.Errorf("%w: %w", ErrBusy, apiErr)
	}
	return err
}

// wrapACLPutFailure classifies a failure from the rules or subnets PUT,
// each sent with Once true (never retried; see putACLRulesAndConfirm and
// putACLSubnetsAndConfirm) so a transport-level retry landing in the ACL's
// own busy window after an earlier write can never happen. A busy 400
// (isACLBusyErr) is a clean refusal on that single
// attempt: the server never acted, so it wraps ErrBusy. Any other 4xx
// *core.APIError is returned unchanged: the server rejected the request
// outright for some other reason, such as a malformed body or an unknown
// ACL. Every other failure, a 5xx, a network error, or a timeout, may have
// reached the server and changed the ACL, so it wraps ErrNotSettled
// instead of ErrBusy: the recovery is to read the ACL again, which the
// caller's own read-merge operation already does on a rerun.
func wrapACLPutFailure(op, networkACLID string, err error) error {
	if err == nil {
		return nil
	}
	if isACLBusyErr(err) {
		return wrapACLBusyErr(err)
	}
	if is4xxAPIError(err) {
		return err
	}
	return fmt.Errorf("%w: %s: network ACL %s: the write was sent once and may have reached the server; read the ACL again before retrying: %w",
		ErrNotSettled, op, networkACLID, err)
}

// aclFoundAfterServerError lists network ACLs once and reports whether id
// is present. GetNetworkACL calls this only after a 5xx on its own GET,
// never after a 404: a deleted ACL's own get returns 500, not 404, unlike
// every other resource this package deletes, so a 5xx here needs the same
// list confirm a 404 gets automatically elsewhere. Listing once, rather
// than paging through every ACL, matches the design; ListNetworkACLs itself
// defaults to a page of 10000, so this misses an id only in a project with
// more network ACLs than that.
//
// DeleteNetworkACL's pre-delete read goes through GetNetworkACL, so a 5xx
// on that read is confirmed the same way; DeleteNetworkACL's own DELETE
// uses waitACLAbsentAfterDelete instead, which pages through every ACL and
// polls rather than trusting one page read once (see its doc comment for
// why: a delete is destructive, so a false "still there" costs only time,
// while a false "gone" would leave a caller believing a live ACL is
// deleted).
//
// A list that comes back short of the account's own count, more than one
// page or fewer items than TotalItem, is not a reliable absence: id could
// simply be on a page this call never asked for. GetNetworkACL already
// treats any error from this method as "fall back to the original 5xx," so
// this returns an error instead of a bare false in that case, rather than
// answering a question the single-page list cannot actually settle.
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
	if out.TotalPage > 1 || len(out.Items) < out.TotalItem {
		return false, fmt.Errorf("network ACL list confirm: got %d of %d item(s) across %d page(s); inconclusive",
			len(out.Items), out.TotalItem, out.TotalPage)
	}
	return false, nil
}

// listAllNetworkACLs walks every page of ListNetworkACLs and returns every
// item seen. It returns an error, rather than a partial slice, when the
// items seen across every page fall short of the total the server itself
// reported: listNetworkACLsInVPC (subnets_write.go) and
// waitACLAbsentAfterDelete both need every ACL the account has, not just
// whatever a first page happens to hold.
func (c *Client) listAllNetworkACLs(ctx context.Context) ([]ACL, error) {
	var items []ACL
	var seen, total int
	for page := 1; ; page++ {
		out, err := c.ListNetworkACLs(ctx, &ListNetworkACLsInput{Page: page})
		if err != nil {
			return nil, err
		}
		total = out.TotalItem
		seen += len(out.Items)
		items = append(items, out.Items...)
		if page >= out.TotalPage || len(out.Items) == 0 {
			break
		}
	}
	if seen < total {
		return nil, fmt.Errorf("network ACL list: got %d of %d item(s) across every page; the list is incomplete", seen, total)
	}
	return items, nil
}

// aclListedAfterDelete reports whether id is still present across every
// page of ListNetworkACLs, for waitACLAbsentAfterDelete's poll. Any error
// from listAllNetworkACLs, including an incomplete listing, is returned
// as is: the caller treats it the same as "still listed," since neither
// answers whether id is truly gone.
func (c *Client) aclListedAfterDelete(ctx context.Context, id string) (bool, error) {
	all, err := c.listAllNetworkACLs(ctx)
	if err != nil {
		return false, err
	}
	for _, acl := range all {
		if acl.UUID == id || acl.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// waitACLAbsentAfterDelete is DeleteNetworkACL's confirm after a 5xx on its
// own DELETE, sent with Once so this confirm, not a resend, decides the
// outcome. It polls aclListedAfterDelete every pollInterval, paging through
// every network ACL each time, until id is absent or pollBound elapses.
// DeleteNetworkACL treats a nil error here as success and any non-nil error,
// including one from a failed list or from the bound running out, as a
// signal to return the original DELETE error unchanged: a delete is
// destructive, so this never itself invents a reason to call the ACL gone.
func (c *Client) waitACLAbsentAfterDelete(ctx context.Context, id string) error {
	return poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			found, err := c.aclListedAfterDelete(ctx, id)
			if err != nil {
				return true, err
			}
			return !found, nil
		},
		func() error {
			return fmt.Errorf("network ACL %s still listed %s after the delete", id, pollBound)
		},
	)
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

// CreateNetworkACLInput creates a network ACL in a VPC. Confirmed live, ACL
// names are not unique: creating a second ACL with the same Name in the
// same VPC succeeds, so a caller cannot tell two same-named ACLs apart by
// Name alone; compare CreatedAt too.
type CreateNetworkACLInput struct {
	VPCID string `vngcloud:"required"`
	Name  string `vngcloud:"required"`
}

type CreateNetworkACLOutput struct {
	ACL ACL
}

// CreateNetworkACL creates a network ACL in a VPC. The new ACL is ACTIVE at
// once (confirmed live) and holds four default rules: an inbound and an
// outbound pass-all rule at priority (seqNumber) 0, and an inbound and an
// outbound deny-all rule at priority 2000; it starts with no associated
// subnets. CreateNetworkACL takes no wait: its response is final.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the ACL may exist, and the caller lists network
// ACLs by Name in the VPC and compares CreatedAt before creating it again,
// rather than retrying blind, since a same-name ACL may already exist for
// another reason entirely (see CreateNetworkACLInput).
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
			Message: "create response had no id; the ACL may exist, list network ACLs by name in this VPC and compare createdAt, since a same-name ACL may already exist"}
	}
	return &CreateNetworkACLOutput{ACL: resp.Data}, nil
}

// wrapAmbiguousACLCreateErr wraps err, from the create POST op just sent,
// with a hint to list network ACLs by name and compare createdAt before
// creating again, unless err is already a 4xx *core.APIError: a 4xx means
// the server rejected the request outright, so nothing was created and the
// exact same call is safe to retry. Any other error leaves whether the ACL
// was created unknown, and, since ACL names repeat, listing by name alone
// cannot tell a pre-existing ACL from one this create may have just made:
// CreatedAt is the only way to tell them apart.
func wrapAmbiguousACLCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list network ACLs by name in this VPC and compare createdAt, since a same-name ACL may already exist: %w", op, err)
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
// A 204 confirms the delete at once, with no wait. The DELETE is sent with
// Once true, overriding the transport's normal retry of an idempotent
// method on a retryable status (502, 503, 504), so this confirm, not a
// resend, decides the outcome of any attempt that fails. A DELETE sent
// into the ACL's own busy window after an earlier rules write (see
// aclBusyMessages) gets a 400 naming the ACL busy; DeleteNetworkACL maps
// that to ErrBusy the same way the rules and subnets PUT do. Confirmed
// live, a DELETE sent into the busy window after a subnets write
// (associate or disassociate) instead answers 500 and changes nothing,
// rather than the 400 "is being updated" any other write gets in that same
// window. DeleteNetworkACL cannot tell that 500 apart from any other by
// its status alone, so after any 5xx it never resends the DELETE; instead
// waitACLAbsentAfterDelete polls every network ACL, across every page,
// every 2 seconds for up to 60 seconds. The ACL absent from that list at
// any point means the delete did take effect and this call returns
// success; still listed at the bound, or a list error at any point,
// returns the original 500 unchanged, and waiting out the busy window
// before calling DeleteNetworkACL again succeeds.
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
		Once:      true,
	}, nil)
	if delErr == nil {
		return &DeleteNetworkACLOutput{}, nil
	}
	if isACLBusyErr(delErr) {
		return nil, wrapACLBusyErr(delErr)
	}
	if !isACLServerError(delErr) {
		return nil, delErr
	}
	if err := c.waitACLAbsentAfterDelete(ctx, in.NetworkACLID); err != nil {
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
