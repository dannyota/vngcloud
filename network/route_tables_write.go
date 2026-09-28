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

// Status values GetRouteTable and the route table waits observe. A status
// this SDK does not recognize keeps a wait polling rather than treating it
// as settled.
const (
	routeTableStatusActive = "ACTIVE"
	routeTableStatusError  = "ERROR"
)

// routeTableWaitBound is the create and delete wait's bound: 3 minutes, per
// the design's waits table. AddRoute and RemoveRoute's pre-write and
// post-write waits reuse pollBound (60 seconds), the same bound the design
// gives both of them.
const routeTableWaitBound = 3 * time.Minute

// ErrInUse is defined in vpcs_write.go; a route table delete returns it,
// nothing sent, when a pre-delete read with ListSubnetsByVPC finds the
// table still named by a subnet's routeTableUuid.

var (
	// ErrDefaultResource means a write targeted a resource the SDK itself
	// refuses to change or delete while some other part of the account
	// still relies on it: a VPC's main route table while a subnet names no
	// route table of its own, a network ACL that is a project's default ACL
	// (acls_write.go), or one of an ACL's own default rules, priority 2000
	// or above or decoded System true (acl_rules_write.go). The server
	// itself allows deleting a main route table once no subnet depends on
	// it, and does not protect a new ACL's priority-0 pass-all rules the
	// same way it protects the priority-2000 ones; these refusals are the
	// SDK's own guard, not a limit the server enforces everywhere. Nothing
	// was sent.
	ErrDefaultResource = errors.New("network: default resource")

	// ErrBusy means a route table or network ACL write found the resource
	// not ready, or refused to send a stale merge. For AddRoute and
	// RemoveRoute it means the table was not ACTIVE within the pre-write
	// bound, or a re-read taken immediately before the write found the
	// table's routes had already changed since the call's own earlier
	// read; both send nothing. AddNetworkACLRule, RemoveNetworkACLRule,
	// AssociateNetworkACLSubnet, and DisassociateNetworkACLSubnet
	// (acl_rules_write.go, acl_subnets_write.go) return it the same way for
	// an ACL, and DeleteNetworkACL too (acls_write.go); each of those also
	// wraps ErrBusy when the server's own 400 names the ACL still busy
	// settling an earlier write (see aclBusyMessages in acls_write.go). The
	// rules and subnets PUT are sent once and never retried, so that busy
	// 400 is always a clean refusal on a single attempt that reached the
	// server; DeleteNetworkACL's DELETE keeps the transport's normal
	// retries instead, and wraps the same busy 400 whichever attempt gets
	// it. Either way that single reply reached the server and was refused
	// outright, so nothing changed, even though a request did go out; every
	// other case here sends nothing at all.
	ErrBusy = errors.New("network: resource busy")
)

// GetRouteTableInput identifies the route table to read.
type GetRouteTableInput struct {
	RouteTableID string `vngcloud:"required"`
}

type GetRouteTableOutput struct {
	RouteTable RouteTable
}

// GetRouteTable reads a route table, including its routes. Confirmed live,
// a deleted route table's get returns 404, which the shared transport maps
// to core.ErrNotFound; GetRouteTable adds no further handling for that.
func (c *Client) GetRouteTable(ctx context.Context, in *GetRouteTableInput) (*GetRouteTableOutput, error) {
	const op = "network.GetRouteTable"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RouteTableID", in.RouteTableID); err != nil {
		return nil, err
	}
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data RouteTable `json:"data"`
	}
	if err := c.c.DoJSON(ctx, transport.Request{
		Operation: op,
		Method:    http.MethodGet,
		URL:       c.networkURL([]string{projectID, "route-table", in.RouteTableID}, nil),
		OK:        []int{200},
	}, &resp); err != nil {
		return nil, err
	}
	return &GetRouteTableOutput{RouteTable: resp.Data}, nil
}

// createRouteTableBody is CreateRouteTable's request body. The API also
// takes an optional routes list and tags and zoneId; the SDK sends none of
// them, per the design's decision to ship a new table with no routes.
type createRouteTableBody struct {
	Name      string `json:"name"`
	NetworkID string `json:"networkId"`
}

// createRouteTableResponse is Create's response, confirmed live: a flat
// object with only the new table's id, unlike the wrapped "data" envelope
// most other creates in this package return.
type createRouteTableResponse struct {
	UUID string `json:"uuid"`
}

// CreateRouteTableInput creates an empty route table in a VPC. Route tables
// are created with no routes; add one afterward with AddRoute.
type CreateRouteTableInput struct {
	VPCID string `vngcloud:"required"`
	Name  string `vngcloud:"required"`

	NoWait bool
}

type CreateRouteTableOutput struct {
	RouteTable RouteTable
}

// CreateRouteTable creates an empty route table in a VPC. Whether route
// table names are unique per VPC or per project is not yet confirmed live;
// a duplicate name fails with the server's own message either way.
//
// It is a POST and is never retried after a failure that may have already
// reached the server: after any error that is not a 4xx *core.APIError or
// core.ErrInvalidInput, the table may exist, and the caller lists route
// tables by Name and matches it exactly (the list's own name filter may
// match by substring) before creating it again, rather than retrying blind.
//
// Without NoWait, CreateRouteTable then waits for the new table to reach
// ACTIVE, confirmed live at about 5 seconds. If the table reaches ERROR
// instead, or the wait's bound runs out, or a read or a sleep in that wait
// fails, such as from a canceled ctx, the returned error wraps ErrFailed or
// ErrNotSettled and the Output still holds the table: the last one a read
// returned, or, if none did, the table the create response itself named.
func (c *Client) CreateRouteTable(ctx context.Context, in *CreateRouteTableInput) (*CreateRouteTableOutput, error) {
	const op = "network.CreateRouteTable"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}

	var resp createRouteTableResponse
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPost,
		URL:       c.networkURL([]string{projectID, "route-table"}, nil),
		Body:      createRouteTableBody{Name: in.Name, NetworkID: in.VPCID},
		OK:        []int{202},
	}
	status, err := c.c.DoJSONStatus(ctx, req, &resp)
	if err != nil {
		return nil, wrapAmbiguousRouteTableCreateErr(op, err)
	}
	if resp.UUID == "" {
		return nil, &core.APIError{Operation: op, StatusCode: status,
			Message: "create response had no id; the table may exist, list route tables and match the name exactly before creating it again"}
	}
	table := RouteTable{UUID: resp.UUID, Name: in.Name, NetworkID: in.VPCID}
	if in.NoWait {
		return &CreateRouteTableOutput{RouteTable: table}, nil
	}

	settled, waitErr := c.waitRouteTableActive(ctx, op, resp.UUID)
	if settled == nil {
		// The create already succeeded; no read after it ever came back, so
		// fall back to the create response itself, which at least carries
		// the new table's id, rather than losing it to a nil Output.
		settled = &table
	}
	return &CreateRouteTableOutput{RouteTable: *settled}, waitErr
}

// wrapAmbiguousRouteTableCreateErr wraps err, from the create POST op just
// sent, with a hint to list route tables before creating again, unless err
// is already a 4xx *core.APIError: a 4xx means the server rejected the
// request outright, so nothing was created and the exact same call is safe
// to retry. Any other error leaves whether the table was created unknown.
func wrapAmbiguousRouteTableCreateErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var apiErr *core.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		return err
	}
	return fmt.Errorf("%s: create may have already reached the server; list route tables and match the name exactly before creating it again: %w", op, err)
}

// DeleteRouteTableInput identifies the route table to delete.
type DeleteRouteTableInput struct {
	RouteTableID string `vngcloud:"required"`

	NoWait bool
}

type DeleteRouteTableOutput struct{}

// DeleteRouteTable deletes a route table. It reads the table first with
// GetRouteTable to find its VPC, then reads that VPC with GetVPC and lists
// the VPC's subnets with ListSubnetsByVPC.
//
// It sends nothing and returns ErrInUse when any subnet names the table
// (its RouteTableID, read from the subnet's own routeTableUuid), whether or
// not the table is the VPC's main route table.
//
// Otherwise, when the table is the VPC's main route table (VPC.RouteTableID)
// and some subnet of the VPC names no route table at all (an empty
// RouteTableID, so that subnet relies on the main one), DeleteRouteTable
// sends nothing and returns ErrDefaultResource. A main table with no subnet
// in that state, including one with no subnets at all, deletes normally: a
// live probe confirmed the server itself allows deleting a VPC's main route
// table once no subnet depends on it, clearing VPC.RouteTableID back to "".
//
// The DELETE itself is asynchronous, confirmed live at 202 then a 404 on
// GetRouteTable about 5 seconds later. Without NoWait, DeleteRouteTable
// waits for that 404. DELETE is idempotent and keeps the transport's normal
// retries; a retry that finds the table already gone returns NotFound, as
// does a repeat call to DeleteRouteTable itself, since its own guard reads
// run first.
func (c *Client) DeleteRouteTable(ctx context.Context, in *DeleteRouteTableInput) (*DeleteRouteTableOutput, error) {
	const op = "network.DeleteRouteTable"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RouteTableID", in.RouteTableID); err != nil {
		return nil, err
	}

	table, err := c.GetRouteTable(ctx, &GetRouteTableInput{RouteTableID: in.RouteTableID})
	if err != nil {
		return nil, err
	}
	vpc, err := c.GetVPC(ctx, &GetVPCInput{VPCID: table.RouteTable.NetworkID})
	if err != nil {
		return nil, err
	}
	subnets, err := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: table.RouteTable.NetworkID})
	if err != nil {
		return nil, err
	}
	var reliesOnMain bool
	for _, subnet := range subnets.Items {
		if subnet.RouteTableID == in.RouteTableID {
			return nil, fmt.Errorf("%w: %s: route table %s is named by a subnet of its VPC", ErrInUse, op, in.RouteTableID)
		}
		if subnet.RouteTableID == "" {
			reliesOnMain = true
		}
	}
	if vpc.VPC.RouteTableID == in.RouteTableID && reliesOnMain {
		return nil, fmt.Errorf("%w: %s: route table %s is the VPC's main route table and a subnet names no route table, so it relies on this one",
			ErrDefaultResource, op, in.RouteTableID)
	}

	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodDelete,
		URL:       c.networkURL([]string{projectID, "route-table", in.RouteTableID}, nil),
		OK:        []int{202},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	if in.NoWait {
		return &DeleteRouteTableOutput{}, nil
	}
	if err := c.waitRouteTableDeleted(ctx, op, in.RouteTableID); err != nil {
		return &DeleteRouteTableOutput{}, err
	}
	return &DeleteRouteTableOutput{}, nil
}

// waitRouteTableActive is CreateRouteTable's post-create wait unless NoWait
// is set: it reads routeTableID with GetRouteTable until its Status reaches
// routeTableStatusActive or routeTableStatusError; any other status keeps
// it polling. A 404 during the wait also keeps polling, since a table just
// created may not be readable yet; any other read failure stops the wait
// and is returned as is.
//
// It returns the last table a read returned alongside the outcome: nil
// error once ACTIVE, an error wrapping ErrFailed on ERROR, or an error
// wrapping ErrNotSettled once the bound runs out or a read or a sleep
// fails. The returned table is nil only when no read ever succeeded, in
// which case the caller falls back to the create response itself.
func (c *Client) waitRouteTableActive(ctx context.Context, op, routeTableID string) (*RouteTable, error) {
	var table *RouteTable
	err := poll(ctx, c.now, c.sleep, pollInterval, routeTableWaitBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetRouteTable(ctx, &GetRouteTableInput{RouteTableID: routeTableID})
			if err != nil {
				if core.IsNotFound(err) {
					return false, nil
				}
				return true, err
			}
			table = &out.RouteTable
			switch table.Status {
			case routeTableStatusActive:
				return true, nil
			case routeTableStatusError:
				return true, fmt.Errorf("%w: %s: route table %s is ERROR", ErrFailed, op, routeTableID)
			default:
				return false, nil
			}
		},
		func() error {
			return fmt.Errorf("%w: %s: route table %s did not reach ACTIVE within %s; the table exists and this create must not be repeated",
				ErrNotSettled, op, routeTableID, routeTableWaitBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: route table %s: %w", ErrNotSettled, op, routeTableID, err)
	}
	return table, err
}

// waitRouteTableDeleted is DeleteRouteTable's post-delete wait unless
// NoWait is set: it reads routeTableID with GetRouteTable until that read
// returns NotFound, which the design and a live probe confirm the server
// gives about 5 seconds after the DELETE. A read that instead shows the
// table reached ERROR stops the wait with ErrFailed; any other read
// failure also stops it and is returned as is.
func (c *Client) waitRouteTableDeleted(ctx context.Context, op, routeTableID string) error {
	err := poll(ctx, c.now, c.sleep, pollInterval, routeTableWaitBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetRouteTable(ctx, &GetRouteTableInput{RouteTableID: routeTableID})
			if err != nil {
				if core.IsNotFound(err) {
					return true, nil
				}
				return true, err
			}
			if out.RouteTable.Status == routeTableStatusError {
				return true, fmt.Errorf("%w: %s: route table %s is ERROR", ErrFailed, op, routeTableID)
			}
			return false, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: route table %s still exists after %s; the delete may still complete and this call is safe to run again",
				ErrNotSettled, op, routeTableID, routeTableWaitBound)
		},
	)
	if err != nil && !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
		err = fmt.Errorf("%w: %s: route table %s: %w", ErrNotSettled, op, routeTableID, err)
	}
	return err
}
