package network

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
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

var (
	// ErrInUse means a delete was refused because the resource is still
	// referenced elsewhere: a route table named by a subnet's
	// routeTableUuid, found by a pre-delete read with ListSubnetsByVPC.
	// Nothing was sent.
	ErrInUse = errors.New("network: resource in use")

	// ErrDefaultResource means a write targeted a resource the server
	// manages and never lets a caller change or delete, such as a VPC's
	// main route table. Nothing was sent.
	ErrDefaultResource = errors.New("network: default resource")

	// ErrBusy means AddRoute or RemoveRoute read a route table that was not
	// ACTIVE and it did not reach ACTIVE within the pre-write bound.
	// Nothing was sent; the caller may run the same call again.
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
// GetRouteTable to find its VPC, then reads that VPC with GetVPC and sends
// nothing, returning ErrDefaultResource, when the table is that VPC's main
// route table (VPC.RouteTableID). It then lists the VPC's subnets with
// ListSubnetsByVPC and sends nothing, returning ErrInUse, when any subnet
// names the table (its RouteTableID, read from the subnet's own
// routeTableUuid).
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
	if vpc.VPC.RouteTableID == in.RouteTableID {
		return nil, fmt.Errorf("%w: %s: route table %s is the VPC's main route table", ErrDefaultResource, op, in.RouteTableID)
	}
	subnets, err := c.ListSubnetsByVPC(ctx, &ListSubnetsByVPCInput{VPCID: table.RouteTable.NetworkID})
	if err != nil {
		return nil, err
	}
	for _, subnet := range subnets.Items {
		if subnet.RouteTableID == in.RouteTableID {
			return nil, fmt.Errorf("%w: %s: route table %s is named by a subnet of its VPC", ErrInUse, op, in.RouteTableID)
		}
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

// routeEntry is one entry of the routes list AddRoute and RemoveRoute send:
// the two fields the API's replace body takes per route, per the design.
type routeEntry struct {
	DestinationCIDRBlock string `json:"destinationCidrBlock"`
	Target               string `json:"target"`
}

// routesReplaceBody is the PUT .../routes request body.
type routesReplaceBody struct {
	Routes []routeEntry `json:"routes"`
}

// routeEntriesOf builds the routes replace body from routes exactly as
// GetRouteTable read them, dropping every field but the two the API lets a
// caller resend.
//
// Which routingType, if any, marks a system route the server manages
// outside a caller's control, and whether such a route must still be
// resent on every replace, are not yet confirmed live; see the design's
// open questions. Until that is known, this SDK resends every route it
// read, so a replace never silently drops one the caller did not name.
func routeEntriesOf(routes []Route) []routeEntry {
	entries := make([]routeEntry, len(routes))
	for i, r := range routes {
		entries[i] = routeEntry{DestinationCIDRBlock: r.DestinationCIDRBlock, Target: r.Target}
	}
	return entries
}

// routesOf is routeEntriesOf's inverse, for building a RouteTable.Routes
// value from the entries a replace sent, when no confirming read is taken
// (NoWait).
func routesOf(entries []routeEntry) []Route {
	routes := make([]Route, len(entries))
	for i, e := range entries {
		routes[i] = Route{DestinationCIDRBlock: e.DestinationCIDRBlock, Target: e.Target}
	}
	return routes
}

// routesEqual reports whether routes, read back after a replace, name
// exactly the same (destination, target) pairs as entries, the list just
// sent, regardless of order.
func routesEqual(routes []Route, entries []routeEntry) bool {
	if len(routes) != len(entries) {
		return false
	}
	remaining := make(map[routeEntry]int, len(entries))
	for _, e := range entries {
		remaining[e]++
	}
	for _, r := range routes {
		e := routeEntry{DestinationCIDRBlock: r.DestinationCIDRBlock, Target: r.Target}
		if remaining[e] == 0 {
			return false
		}
		remaining[e]--
	}
	return true
}

// checkRouteDestinationCIDR checks cidr per AddRoute and RemoveRoute's doc
// comments, before any request: it must parse as a CIDR prefix with no
// host bits set.
func checkRouteDestinationCIDR(op, cidr string) error {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return fmt.Errorf("%w: %s: DestinationCIDR must be a CIDR prefix such as 10.251.200.0/24, got %q",
			core.ErrInvalidInput, op, cidr)
	}
	if prefix != prefix.Masked() {
		return fmt.Errorf("%w: %s: DestinationCIDR must have no host bits set, got %q",
			core.ErrInvalidInput, op, cidr)
	}
	return nil
}

// checkRouteTarget checks target per AddRoute's doc comment, before any
// request: it must parse as an IP address.
func checkRouteTarget(op, target string) error {
	if _, err := netip.ParseAddr(target); err != nil {
		return fmt.Errorf("%w: %s: Target must be an IP address such as 10.251.200.10, got %q",
			core.ErrInvalidInput, op, target)
	}
	return nil
}

// AddRouteInput adds one route to a route table.
type AddRouteInput struct {
	RouteTableID    string `vngcloud:"required"`
	DestinationCIDR string `vngcloud:"required"`
	Target          string `vngcloud:"required"`

	NoWait bool
}

type AddRouteOutput struct {
	RouteTable RouteTable
	Changed    bool
}

// AddRoute adds one route to a route table. DestinationCIDR must be a CIDR
// prefix with no host bits set; Target must be an IP address. Whether the
// server requires Target to be a live interface's address is not yet
// confirmed live.
//
// The API replaces a route table's whole route list on every write, so
// AddRoute is a read-merge write: it reads the table's current routes with
// GetRouteTable, waiting first for the table to reach ACTIVE within a
// pre-write bound (ErrBusy, nothing sent, if it does not), then sends back
// every route it read plus the one being added, never a caller-supplied
// whole list.
//
// A route already present for DestinationCIDR with the same Target makes
// AddRoute a no-op: Changed is false and nothing is sent. One present with
// a different Target fails with core.ErrInvalidInput naming that target,
// nothing sent; RemoveRoute the old route first.
//
// Without NoWait, AddRoute waits for the table to return to ACTIVE after
// its PUT, then confirms that a fresh read names exactly the routes just
// sent. A failure at either point returns an error wrapping ErrFailed (the
// table reached ERROR) or ErrNotSettled (the bound ran out, the confirm
// read did not match, or a read or sleep failed); the PUT itself is not
// resent on a rerun; AddRoute simply reads the table again.
func (c *Client) AddRoute(ctx context.Context, in *AddRouteInput) (*AddRouteOutput, error) {
	const op = "network.AddRoute"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RouteTableID", in.RouteTableID); err != nil {
		return nil, err
	}
	if err := checkRouteDestinationCIDR(op, in.DestinationCIDR); err != nil {
		return nil, err
	}
	if err := checkRouteTarget(op, in.Target); err != nil {
		return nil, err
	}

	table, err := c.waitRouteTablePreWriteActive(ctx, op, in.RouteTableID)
	if err != nil {
		return nil, err
	}

	entries := routeEntriesOf(table.Routes)
	for _, e := range entries {
		if e.DestinationCIDRBlock != in.DestinationCIDR {
			continue
		}
		if e.Target == in.Target {
			return &AddRouteOutput{RouteTable: *table, Changed: false}, nil
		}
		return nil, fmt.Errorf("%w: %s: route table %s already has a route to %s with target %s; remove it first",
			core.ErrInvalidInput, op, in.RouteTableID, in.DestinationCIDR, e.Target)
	}
	entries = append(entries, routeEntry{DestinationCIDRBlock: in.DestinationCIDR, Target: in.Target})

	updated, err := c.putRoutesAndConfirm(ctx, op, in.RouteTableID, entries, table, in.NoWait)
	if updated == nil {
		return nil, err
	}
	// The PUT was sent either way; Changed reflects that even when err wraps
	// ErrFailed or ErrNotSettled, so the caller's Output still holds the
	// last table a read returned, per the design's error table.
	return &AddRouteOutput{RouteTable: *updated, Changed: true}, err
}

// RemoveRouteInput removes one route, named by its destination, from a
// route table.
type RemoveRouteInput struct {
	RouteTableID    string `vngcloud:"required"`
	DestinationCIDR string `vngcloud:"required"`

	NoWait bool
}

type RemoveRouteOutput struct {
	RouteTable RouteTable
	Changed    bool
}

// RemoveRoute removes one route from a route table, named by its
// DestinationCIDR, which must be a CIDR prefix with no host bits set. It is
// the read-merge write AddRoute's doc comment describes, in reverse: it
// waits for the table to be ACTIVE (ErrBusy, nothing sent, past the
// pre-write bound), then sends back every route it read except the one
// removed.
//
// No route matching DestinationCIDR fails with core.ErrNotFound, nothing
// sent. Without NoWait, RemoveRoute waits and confirms exactly as AddRoute
// does; see its doc comment for the wait, the confirm, and rerun safety.
func (c *Client) RemoveRoute(ctx context.Context, in *RemoveRouteInput) (*RemoveRouteOutput, error) {
	const op = "network.RemoveRoute"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "RouteTableID", in.RouteTableID); err != nil {
		return nil, err
	}
	if err := checkRouteDestinationCIDR(op, in.DestinationCIDR); err != nil {
		return nil, err
	}

	table, err := c.waitRouteTablePreWriteActive(ctx, op, in.RouteTableID)
	if err != nil {
		return nil, err
	}

	entries := routeEntriesOf(table.Routes)
	idx := -1
	for i, e := range entries {
		if e.DestinationCIDRBlock == in.DestinationCIDR {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("%w: %s: route table %s has no route to %s", core.ErrNotFound, op, in.RouteTableID, in.DestinationCIDR)
	}
	entries = append(entries[:idx], entries[idx+1:]...)

	updated, err := c.putRoutesAndConfirm(ctx, op, in.RouteTableID, entries, table, in.NoWait)
	if updated == nil {
		return nil, err
	}
	return &RemoveRouteOutput{RouteTable: *updated, Changed: true}, err
}

// putRoutesAndConfirm sends entries as routeTableID's whole route list,
// then, unless noWait, waits for the table to return to ACTIVE and confirms
// that a fresh read names exactly entries. base is the pre-write read
// AddRoute or RemoveRoute already took; with noWait its Routes field is
// replaced with entries, mapped to Route values, and returned as is, since
// no read after the PUT is taken to build anything better.
func (c *Client) putRoutesAndConfirm(ctx context.Context, op, routeTableID string, entries []routeEntry, base *RouteTable, noWait bool) (*RouteTable, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.networkURL([]string{projectID, "route-table", routeTableID, "routes"}, nil),
		Body:      routesReplaceBody{Routes: entries},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	if noWait {
		fallback := *base
		fallback.Routes = routesOf(entries)
		return &fallback, nil
	}
	return c.waitRouteTableRoutesSettled(ctx, op, routeTableID, entries)
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

// waitRouteTablePreWriteActive is AddRoute and RemoveRoute's pre-write
// read: it reads routeTableID with GetRouteTable until its Status reaches
// routeTableStatusActive, within the shorter pollBound (60 seconds, the
// same bound the design gives this wait). Unlike the create and delete
// waits above, a read failure, including NotFound, stops the wait at once
// rather than tolerating it: the table this wait reads is an existing one
// AddRoute or RemoveRoute did not just create, so a NotFound here is a real
// error, not a race with the server making a brand new id readable.
//
// The design defines no failure status for this wait (an ERROR table still
// just keeps it polling until the bound), so past the bound it returns
// ErrBusy rather than ErrNotSettled: nothing has been sent yet, and the
// caller may simply run the same call again later.
func (c *Client) waitRouteTablePreWriteActive(ctx context.Context, op, routeTableID string) (*RouteTable, error) {
	var table *RouteTable
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
		func(ctx context.Context) (bool, error) {
			out, err := c.GetRouteTable(ctx, &GetRouteTableInput{RouteTableID: routeTableID})
			if err != nil {
				return true, err
			}
			table = &out.RouteTable
			return table.Status == routeTableStatusActive, nil
		},
		func() error {
			return fmt.Errorf("%w: %s: route table %s is not ACTIVE within %s; nothing sent",
				ErrBusy, op, routeTableID, pollBound)
		},
	)
	return table, err
}

// waitRouteTableRoutesSettled is AddRoute and RemoveRoute's post-write
// wait unless NoWait is set: it reads routeTableID with GetRouteTable,
// using the shorter pollBound (60 seconds), until its Status reaches
// routeTableStatusActive or routeTableStatusError, then confirms that the
// routes it just read are exactly sent, regardless of order. A mismatch
// means another writer changed the table between AddRoute or RemoveRoute's
// own read and this one; it returns an error wrapping ErrNotSettled, same
// as a bound timeout or a read or sleep failure, since the PUT already
// reached the server either way and is not resent by running the call
// again; RemoveRoute or AddRoute simply reads the table fresh instead.
func (c *Client) waitRouteTableRoutesSettled(ctx context.Context, op, routeTableID string, sent []routeEntry) (*RouteTable, error) {
	var table *RouteTable
	err := poll(ctx, c.now, c.sleep, pollInterval, pollBound,
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
			return fmt.Errorf("%w: %s: route table %s did not settle within %s; the write may have reached the server and is safe to run again",
				ErrNotSettled, op, routeTableID, pollBound)
		},
	)
	if err != nil {
		if !errors.Is(err, ErrFailed) && !errors.Is(err, ErrNotSettled) {
			err = fmt.Errorf("%w: %s: route table %s: %w", ErrNotSettled, op, routeTableID, err)
		}
		return table, err
	}
	if !routesEqual(table.Routes, sent) {
		return table, fmt.Errorf("%w: %s: route table %s: routes read after the write do not match what was sent; another writer may have changed the table",
			ErrNotSettled, op, routeTableID)
	}
	return table, nil
}
