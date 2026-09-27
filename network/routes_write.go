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

// canonicalCIDR returns cidr's canonical net/netip.Prefix string, so
// spellings that name the same prefix, such as 2001:DB8::/32 and
// 2001:0db8::/32 both against 2001:db8::/32, compare equal. A value that
// does not parse as a prefix is returned unchanged, so a comparison against
// it simply falls back to a raw string match.
func canonicalCIDR(cidr string) string {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return cidr
	}
	return prefix.String()
}

// canonicalRouteEntry returns e with its destination passed through
// canonicalCIDR, for comparisons that must treat equivalent spellings of
// the same prefix as the same route.
func canonicalRouteEntry(e routeEntry) routeEntry {
	e.DestinationCIDRBlock = canonicalCIDR(e.DestinationCIDRBlock)
	return e
}

// routesEqual reports whether routes, read back after a replace, name
// exactly the same (destination, target) pairs as entries, the list just
// sent, regardless of order or of equivalent spellings of the same
// destination prefix.
func routesEqual(routes []Route, entries []routeEntry) bool {
	if len(routes) != len(entries) {
		return false
	}
	remaining := make(map[routeEntry]int, len(entries))
	for _, e := range entries {
		remaining[canonicalRouteEntry(e)]++
	}
	for _, r := range routes {
		e := canonicalRouteEntry(routeEntry{DestinationCIDRBlock: r.DestinationCIDRBlock, Target: r.Target})
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
// request: it must parse as an IP address with no zone. A zone such as
// "fe80::1%eth0" names an interface on the caller's own machine, which
// means nothing to the server this address is sent to.
func checkRouteTarget(op, target string) error {
	addr, err := netip.ParseAddr(target)
	if err != nil {
		return fmt.Errorf("%w: %s: Target must be an IP address such as 10.251.200.10, got %q",
			core.ErrInvalidInput, op, target)
	}
	if addr.Zone() != "" {
		return fmt.Errorf("%w: %s: Target must not have a zone, got %q",
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
// prefix with no host bits set; Target must be an IP address with no zone.
// Whether the server requires Target to be a live interface's address is
// not yet confirmed live.
//
// The API replaces a route table's whole route list on every write, so
// AddRoute is a read-merge write: it reads the table's current routes with
// GetRouteTable, waiting first for the table to reach ACTIVE within a
// pre-write bound (ErrBusy, nothing sent, if it does not), then sends back
// every route it read plus the one being added, never a caller-supplied
// whole list. Immediately before sending that write, it re-reads the table
// and refuses with ErrBusy, again sending nothing, if the routes no longer
// match the read this merge started from; see putRoutesAndConfirm.
//
// A route already present for DestinationCIDR (compared as a parsed CIDR
// prefix, so equivalent spellings match) with the same Target makes
// AddRoute a no-op: Changed is false and nothing is sent. One present with
// a different Target fails with core.ErrInvalidInput naming that target,
// nothing sent; RemoveRoute the old route first.
//
// Without NoWait, AddRoute waits for the table to return to ACTIVE after
// its PUT, then confirms that a fresh read names exactly the routes just
// sent. A failure at any of these points returns an error wrapping
// ErrFailed (the table reached ERROR), ErrBusy (the pre-write wait or the
// pre-PUT re-read above), or ErrNotSettled (the post-write bound ran out,
// the confirm read did not match, or a read or sleep failed). Once the PUT
// itself is sent, it is not resent on a rerun; AddRoute simply reads the
// table again from the start.
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
	wantCIDR := canonicalCIDR(in.DestinationCIDR)
	for _, e := range entries {
		if canonicalCIDR(e.DestinationCIDRBlock) != wantCIDR {
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
// removed. As for AddRoute, it also re-reads the table immediately before
// sending that write and refuses with ErrBusy, again sending nothing, if
// the routes have changed since the first read; see putRoutesAndConfirm.
//
// DestinationCIDR is compared as a parsed CIDR prefix, so equivalent
// spellings match. No route matching it fails with core.ErrNotFound,
// nothing sent. More than one route matching it fails with
// core.ErrInvalidInput naming the count, nothing sent, rather than guessing
// which one to drop. Without NoWait, RemoveRoute waits and confirms exactly
// as AddRoute does; see its doc comment for the wait, the confirm, and
// rerun safety.
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
	wantCIDR := canonicalCIDR(in.DestinationCIDR)
	var matches []int
	for i, e := range entries {
		if canonicalCIDR(e.DestinationCIDRBlock) == wantCIDR {
			matches = append(matches, i)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w: %s: route table %s has no route to %s", core.ErrNotFound, op, in.RouteTableID, in.DestinationCIDR)
	case 1:
		i := matches[0]
		entries = append(entries[:i], entries[i+1:]...)
	default:
		return nil, fmt.Errorf("%w: %s: route table %s has %d routes to %s; remove is refused rather than guessing which to drop",
			core.ErrInvalidInput, op, in.RouteTableID, len(matches), in.DestinationCIDR)
	}

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
//
// Immediately before sending the PUT, it re-reads the table and compares
// those routes to base.Routes. A mismatch means some other writer changed
// the table since AddRoute or RemoveRoute's own read, so entries, built
// from that now-stale read, would silently overwrite the change; this
// sends nothing and returns an error wrapping ErrBusy instead. This narrows
// the race but does not close it: a writer that changes the table between
// this re-read and the PUT actually reaching the server can still be
// overwritten by it.
func (c *Client) putRoutesAndConfirm(ctx context.Context, op, routeTableID string, entries []routeEntry, base *RouteTable, noWait bool) (*RouteTable, error) {
	recheck, err := c.GetRouteTable(ctx, &GetRouteTableInput{RouteTableID: routeTableID})
	if err != nil {
		return nil, err
	}
	if !routesEqual(recheck.RouteTable.Routes, routeEntriesOf(base.Routes)) {
		return nil, fmt.Errorf("%w: %s: route table %s changed since it was read; nothing sent, run the call again",
			ErrBusy, op, routeTableID)
	}

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

// waitRouteTablePreWriteActive is AddRoute and RemoveRoute's pre-write
// read: it reads routeTableID with GetRouteTable until its Status reaches
// routeTableStatusActive, within the shorter pollBound (60 seconds, the
// same bound the design gives this wait). Unlike the create and delete
// waits in route_tables_write.go, a read failure, including NotFound, stops
// the wait at once rather than tolerating it: the table this wait reads is
// an existing one AddRoute or RemoveRoute did not just create, so a
// NotFound here is a real error, not a race with the server making a brand
// new id readable.
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
// routes it just read are exactly sent, regardless of order.
//
// AddRoute and RemoveRoute already re-read the table immediately before
// the PUT and refused with ErrBusy, sending nothing, if that read did not
// match their own first read (see putRoutesAndConfirm), so a mismatch found
// here instead means a writer changed the table after the PUT itself, in
// the window between it and this confirming read; that window is not
// covered by any check. Either that mismatch or a bound timeout or a read
// or sleep failure returns an error wrapping ErrNotSettled, since the PUT
// already reached the server either way and is not resent by running the
// call again; RemoveRoute or AddRoute simply reads the table fresh instead.
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
