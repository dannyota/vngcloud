package network

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// aclMaxUserPriority is the highest Priority AddNetworkACLRule accepts.
// GreenNode's docs describe user rule priorities running 1 to 32766; a
// rule at 0 or above this bound is one of the server's own default rules
// (see isDefaultACLRule), which a caller can name to remove but never add.
const aclMaxUserPriority = 32766

// checkACLRulePriority checks priority per AddNetworkACLRule's doc
// comment, before any request: it must be 1 to aclMaxUserPriority.
// CheckRequired already refuses 0 as a missing field on
// AddNetworkACLRuleInput, so this only ever catches a negative or
// too-large value.
func checkACLRulePriority(op string, priority int) error {
	if priority < 1 || priority > aclMaxUserPriority {
		return fmt.Errorf("%w: %s: Priority must be %d to %d, got %d",
			core.ErrInvalidInput, op, 1, aclMaxUserPriority, priority)
	}
	return nil
}

// checkACLRuleCIDR checks cidr per AddNetworkACLRule's doc comment, before
// any request: it must parse as a CIDR prefix with no host bits set.
func checkACLRuleCIDR(op, cidr string) error {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return fmt.Errorf("%w: %s: CIDR must be a CIDR prefix such as 203.0.113.0/24, got %q",
			core.ErrInvalidInput, op, cidr)
	}
	if prefix != prefix.Masked() {
		return fmt.Errorf("%w: %s: CIDR must have no host bits set, got %q",
			core.ErrInvalidInput, op, cidr)
	}
	return nil
}

// checkACLRulePorts checks portMin and portMax per AddNetworkACLRule's doc
// comment, before any request, and returns the pair to send: each must be 0
// to 65535, portMax 0 is replaced with portMin, and portMin must not be
// above the resulting portMax.
//
// Leaving both at their zero value, meaning "no ports given," would send
// port "0-0"; whether the server reads that as port 0 or as every port is
// an open live check, so this refuses that pair instead of guessing. A
// caller that wants every port sends it explicitly: PortRangeMin 0,
// PortRangeMax 65535.
func checkACLRulePorts(op string, portMin, portMax int) (int, int, error) {
	if portMin < 0 || portMin > 65535 {
		return 0, 0, fmt.Errorf("%w: %s: PortRangeMin must be 0 to 65535, got %d", core.ErrInvalidInput, op, portMin)
	}
	if portMax < 0 || portMax > 65535 {
		return 0, 0, fmt.Errorf("%w: %s: PortRangeMax must be 0 to 65535, got %d", core.ErrInvalidInput, op, portMax)
	}
	if portMin == 0 && portMax == 0 {
		return 0, 0, fmt.Errorf("%w: %s: PortRangeMin and PortRangeMax must not both be 0; for every port send PortRangeMin 0 and PortRangeMax 65535",
			core.ErrInvalidInput, op)
	}
	if portMax == 0 {
		portMax = portMin
	}
	if portMin > portMax {
		return 0, 0, fmt.Errorf("%w: %s: PortRangeMin %d is above PortRangeMax %d", core.ErrInvalidInput, op, portMin, portMax)
	}
	return portMin, portMax, nil
}

// aclRuleEntry is one entry of the rules list AddNetworkACLRule and
// RemoveNetworkACLRule send: every field the API's inferred replace body
// takes per rule.
type aclRuleEntry struct {
	Type                   string `json:"type"`
	SeqNumber              int    `json:"seqNumber"`
	Protocol               string `json:"protocol"`
	Port                   string `json:"port"`
	Source                 string `json:"source"`
	Action                 string `json:"action"`
	System                 bool   `json:"system"`
	InterfaceACLPolicyUUID string `json:"interfaceAclPolicyUuid"`
}

// aclRulesReplaceBody is the PUT .../rules request body, inferred from the
// design: the whole rule list, plus the ACL's own id repeated at the top.
type aclRulesReplaceBody struct {
	ACLID             string         `json:"aclId"`
	DetailACLRuleList []aclRuleEntry `json:"detailAclRuleList"`
}

// isDefaultACLRule reports whether rule is one of an ACL's default rules,
// which AddNetworkACLRule and RemoveNetworkACLRule must resend exactly as
// read and never let a caller remove or rewrite. A rule holding Priority 0
// or above aclMaxUserPriority is one no caller-supplied Priority can ever
// equal (checkACLRulePriority refuses both), so it must be the server's
// own; a rule that decodes System true is also treated as default, since
// GreenNode's docs describe default deny rules a caller cannot change.
func isDefaultACLRule(rule ACLRule) bool {
	return rule.Priority == 0 || rule.Priority > aclMaxUserPriority || rule.System
}

// aclRuleEntriesOf builds the rules replace body from rules exactly as
// GetNetworkACL read them, for the ACL identified by networkACLID.
//
// Whether the server needs a default rule resent on every replace, or
// drops it if left out, is a live check the design leaves open; until then
// this SDK always resends every rule it read, default or not, so a replace
// never silently drops one the caller did not name. Every entry's System
// is copied from the rule as GetNetworkACL read it, not recomputed from
// isDefaultACLRule: a default rule must go back exactly as read, and a
// rule the server marks System for a reason isDefaultACLRule does not
// capture must not have that flag silently cleared.
func aclRuleEntriesOf(networkACLID string, rules []ACLRule) []aclRuleEntry {
	entries := make([]aclRuleEntry, len(rules))
	for i, r := range rules {
		entries[i] = aclRuleEntry{
			Type: r.Direction, SeqNumber: r.Priority, Protocol: r.Protocol,
			Port: r.Port, Source: r.CIDR, Action: r.Action,
			System: r.System, InterfaceACLPolicyUUID: networkACLID,
		}
	}
	return entries
}

// aclRulesOf is aclRuleEntriesOf's inverse, for building an ACL.Rules value
// from the entries a replace sent, when no confirming read is taken
// (NoWait).
func aclRulesOf(entries []aclRuleEntry) []ACLRule {
	rules := make([]ACLRule, len(entries))
	for i, e := range entries {
		rules[i] = ACLRule{Direction: e.Type, Priority: e.SeqNumber, Protocol: e.Protocol, Port: e.Port, CIDR: e.Source, Action: e.Action}
	}
	return rules
}

// aclRuleKey is the fields aclRulesEqual compares: every one but System and
// InterfaceACLPolicyUUID, which the read model (ACLRule) never carries back.
type aclRuleKey struct {
	Type      string
	SeqNumber int
	Protocol  string
	Port      string
	Source    string
	Action    string
}

// aclRulesEqual reports whether rules, read back after a replace, name
// exactly the same rules as entries, the list just sent, regardless of
// order.
func aclRulesEqual(rules []ACLRule, entries []aclRuleEntry) bool {
	if len(rules) != len(entries) {
		return false
	}
	remaining := make(map[aclRuleKey]int, len(entries))
	for _, e := range entries {
		remaining[aclRuleKey{e.Type, e.SeqNumber, e.Protocol, e.Port, e.Source, e.Action}]++
	}
	for _, r := range rules {
		k := aclRuleKey{r.Direction, r.Priority, r.Protocol, r.Port, r.CIDR, r.Action}
		if remaining[k] == 0 {
			return false
		}
		remaining[k]--
	}
	return true
}

// AddNetworkACLRuleInput adds one rule to a network ACL.
//
// Direction, Protocol, and Action are sent to the server as given and
// checked only for shape, not against a fixed value set, so a value the
// server adds later never needs an SDK release; the wiki shows "inbound"
// and "outbound" for Direction and "ANY", "TCP", "UDP", and "ICMP" for
// Protocol, all confirmed live for at least one rule. Priority orders
// rules, lowest first, and must be 1 to 32766; a default rule the server
// owns holds a Priority outside that range, or a fixed one inside it with
// its System flag set, so a caller can never add or overwrite one. CIDR
// must be a CIDR prefix with no host bits set. PortRangeMin and
// PortRangeMax each range 0 to 65535; PortRangeMax left 0 sends the same
// value as PortRangeMin, and PortRangeMin must not be above the resulting
// PortRangeMax. Leaving both at 0 is refused: send PortRangeMin 0 and
// PortRangeMax 65535 for every port.
type AddNetworkACLRuleInput struct {
	NetworkACLID string `vngcloud:"required"`
	Direction    string `vngcloud:"required"`
	Priority     int    `vngcloud:"required"`
	Protocol     string `vngcloud:"required"`
	CIDR         string `vngcloud:"required"`
	Action       string `vngcloud:"required"`

	PortRangeMin int
	PortRangeMax int
	NoWait       bool
}

type AddNetworkACLRuleOutput struct {
	ACL     ACL
	Changed bool
}

// AddNetworkACLRule adds one rule to a network ACL. A rule is keyed by its
// Direction (case-insensitive) and Priority.
//
// The API replaces an ACL's whole rule list on every write, so
// AddNetworkACLRule is a read-merge write: it reads the ACL's current
// rules with GetNetworkACL, waiting first for the ACL to reach ACTIVE
// within a pre-write bound (ErrBusy, nothing sent, if it does not), then
// sends back every rule it read, default rules included and exactly as
// read, plus the one being added, never a caller-supplied whole list.
// Immediately before sending that write, it re-reads the ACL and refuses
// with ErrBusy, again sending nothing, if the rules no longer match the
// read this merge started from; see putACLRulesAndConfirm.
//
// A rule already present for the same key (Direction, Priority) with every
// other field equal makes AddNetworkACLRule a no-op: Changed is false and
// nothing is sent. The same key with any other field different fails with
// core.ErrInvalidInput, nothing sent.
//
// Without NoWait, AddNetworkACLRule waits for the ACL to return to ACTIVE
// after its PUT, then confirms that a fresh read names exactly the rules
// just sent. A failure at any of these points returns an error wrapping
// ErrFailed (the ACL reached ERROR), ErrBusy (the pre-write wait or the
// pre-PUT re-read above), or ErrNotSettled (the post-write bound ran out,
// the confirm read did not match, or a read or sleep failed); once the PUT
// itself is sent, it is not resent on a rerun; AddNetworkACLRule simply
// reads the ACL again from the start.
func (c *Client) AddNetworkACLRule(ctx context.Context, in *AddNetworkACLRuleInput) (*AddNetworkACLRuleOutput, error) {
	const op = "network.AddNetworkACLRule"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "NetworkACLID", in.NetworkACLID); err != nil {
		return nil, err
	}
	if err := checkACLRulePriority(op, in.Priority); err != nil {
		return nil, err
	}
	if err := checkACLRuleCIDR(op, in.CIDR); err != nil {
		return nil, err
	}
	portMin, portMax, err := checkACLRulePorts(op, in.PortRangeMin, in.PortRangeMax)
	if err != nil {
		return nil, err
	}

	acl, err := c.waitACLPreWriteActive(ctx, op, in.NetworkACLID)
	if err != nil {
		return nil, err
	}

	port := fmt.Sprintf("%d-%d", portMin, portMax)
	entries := aclRuleEntriesOf(in.NetworkACLID, acl.Rules)
	for _, e := range entries {
		if !strings.EqualFold(e.Type, in.Direction) || e.SeqNumber != in.Priority {
			continue
		}
		if e.Protocol == in.Protocol && e.Port == port && e.Source == in.CIDR && e.Action == in.Action {
			return &AddNetworkACLRuleOutput{ACL: *acl, Changed: false}, nil
		}
		return nil, fmt.Errorf("%w: %s: network ACL %s already has a %s rule at priority %d with different fields; remove it first",
			core.ErrInvalidInput, op, in.NetworkACLID, in.Direction, in.Priority)
	}
	entries = append(entries, aclRuleEntry{
		Type: in.Direction, SeqNumber: in.Priority, Protocol: in.Protocol,
		Port: port, Source: in.CIDR, Action: in.Action,
		System: false, InterfaceACLPolicyUUID: in.NetworkACLID,
	})

	updated, err := c.putACLRulesAndConfirm(ctx, op, in.NetworkACLID, entries, acl, in.NoWait)
	if updated == nil {
		return nil, err
	}
	// The PUT was sent either way; Changed reflects that even when err wraps
	// ErrFailed or ErrNotSettled, so the caller's Output still holds the
	// last ACL a read returned, per the design's error table.
	return &AddNetworkACLRuleOutput{ACL: *updated, Changed: true}, err
}

// RemoveNetworkACLRuleInput removes one rule, named by its Direction
// (case-insensitive) and Priority, from a network ACL.
//
// Priority is not tagged required: 0 is CheckRequired's zero value, but it
// is also one marker for a default rule (see ACLRule's doc comment and
// isDefaultACLRule), so a caller must be able to pass it, and any value
// above 32766, to name such a rule and reach RemoveNetworkACLRule's
// ErrDefaultResource refusal, rather than being stopped earlier by a shape
// check meant only for a rule a caller could add. A negative value, which
// no rule can ever hold, is still refused before any request.
type RemoveNetworkACLRuleInput struct {
	NetworkACLID string `vngcloud:"required"`
	Direction    string `vngcloud:"required"`
	Priority     int

	NoWait bool
}

type RemoveNetworkACLRuleOutput struct {
	ACL     ACL
	Changed bool
}

// RemoveNetworkACLRule removes one rule from a network ACL, named by its
// Direction (case-insensitive) and Priority. It is the read-merge write
// AddNetworkACLRule's doc comment describes, in reverse: it waits for the
// ACL to be ACTIVE (ErrBusy, nothing sent, past the pre-write bound), then
// sends back every rule it read, default rules included and exactly as
// read, except the one removed. As for AddNetworkACLRule, it also re-reads
// the ACL immediately before sending that write and refuses with ErrBusy,
// again sending nothing, if the rules have changed since the first read;
// see putACLRulesAndConfirm.
//
// No rule matching the key fails with core.ErrNotFound, nothing sent. A
// rule that matches but is one of the ACL's default rules (see
// isDefaultACLRule) fails with ErrDefaultResource, nothing sent: default
// rules never change or get removed. Without NoWait, RemoveNetworkACLRule
// waits and confirms exactly as AddNetworkACLRule does; see its doc
// comment for the wait, the confirm, and rerun safety.
func (c *Client) RemoveNetworkACLRule(ctx context.Context, in *RemoveNetworkACLRuleInput) (*RemoveNetworkACLRuleOutput, error) {
	const op = "network.RemoveNetworkACLRule"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "NetworkACLID", in.NetworkACLID); err != nil {
		return nil, err
	}
	if in.Priority < 0 {
		return nil, fmt.Errorf("%w: %s: Priority must not be negative, got %d", core.ErrInvalidInput, op, in.Priority)
	}

	acl, err := c.waitACLPreWriteActive(ctx, op, in.NetworkACLID)
	if err != nil {
		return nil, err
	}

	idx := -1
	for i, r := range acl.Rules {
		if strings.EqualFold(r.Direction, in.Direction) && r.Priority == in.Priority {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil, fmt.Errorf("%w: %s: network ACL %s has no %s rule at priority %d", core.ErrNotFound, op, in.NetworkACLID, in.Direction, in.Priority)
	}
	if isDefaultACLRule(acl.Rules[idx]) {
		return nil, fmt.Errorf("%w: %s: network ACL %s's %s rule at priority %d is a default rule and cannot be removed",
			ErrDefaultResource, op, in.NetworkACLID, in.Direction, in.Priority)
	}
	entries := aclRuleEntriesOf(in.NetworkACLID, acl.Rules)
	entries = append(entries[:idx], entries[idx+1:]...)

	updated, err := c.putACLRulesAndConfirm(ctx, op, in.NetworkACLID, entries, acl, in.NoWait)
	if updated == nil {
		return nil, err
	}
	return &RemoveNetworkACLRuleOutput{ACL: *updated, Changed: true}, err
}

// putACLRulesAndConfirm sends entries as networkACLID's whole rule list,
// then, unless noWait, waits for the ACL to return to ACTIVE and confirms
// that a fresh read names exactly entries. base is the pre-write read
// AddNetworkACLRule or RemoveNetworkACLRule already took; with noWait its
// Rules field is replaced with entries, mapped to ACLRule values, and
// returned as is, since no read after the PUT is taken to build anything
// better.
//
// Immediately before sending the PUT, it re-reads the ACL and compares
// those rules to base.Rules. A mismatch means some other writer changed
// the ACL since AddNetworkACLRule or RemoveNetworkACLRule's own read, so
// entries, built from that now-stale read, would silently overwrite the
// change; this sends nothing and returns an error wrapping ErrBusy
// instead. This narrows the race but does not close it: a writer that
// changes the ACL between this re-read and the PUT actually reaching the
// server can still be overwritten by it.
func (c *Client) putACLRulesAndConfirm(ctx context.Context, op, networkACLID string, entries []aclRuleEntry, base *ACL, noWait bool) (*ACL, error) {
	recheck, err := c.GetNetworkACL(ctx, &GetNetworkACLInput{NetworkACLID: networkACLID})
	if err != nil {
		return nil, err
	}
	if !aclRulesEqual(recheck.ACL.Rules, aclRuleEntriesOf(networkACLID, base.Rules)) {
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
		URL:       c.networkURL([]string{projectID, "network-acl", networkACLID, "rules"}, nil),
		Body:      aclRulesReplaceBody{ACLID: networkACLID, DetailACLRuleList: entries},
		OK:        []int{200},
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, err
	}
	if noWait {
		fallback := *base
		fallback.Rules = aclRulesOf(entries)
		return &fallback, nil
	}
	return c.waitACLRulesSettled(ctx, op, networkACLID, entries)
}

// waitACLRulesSettled is AddNetworkACLRule and RemoveNetworkACLRule's
// post-write wait unless NoWait is set: it reads networkACLID with
// GetNetworkACL, using the shorter pollBound, until its Status reaches
// aclStatusActive or aclStatusError, then confirms that the rules it just
// read are exactly sent, regardless of order.
//
// putACLRulesAndConfirm already re-reads the ACL immediately before the
// PUT and refuses with ErrBusy, sending nothing, if that read did not
// match the caller's own first read, so a mismatch found here instead
// means a writer changed the ACL after the PUT itself, in the window
// between it and this confirming read; that window is not covered by any
// check. Either that mismatch or a bound timeout or a read or sleep
// failure returns an error wrapping ErrNotSettled, since the PUT already
// reached the server either way and is not resent by running the call
// again.
func (c *Client) waitACLRulesSettled(ctx context.Context, op, networkACLID string, sent []aclRuleEntry) (*ACL, error) {
	acl, err := c.waitACLSettled(ctx, op, networkACLID)
	if err != nil {
		return acl, err
	}
	if !aclRulesEqual(acl.Rules, sent) {
		return acl, fmt.Errorf("%w: %s: network ACL %s: rules read after the write do not match what was sent; another writer may have changed the ACL",
			ErrNotSettled, op, networkACLID)
	}
	return acl, nil
}
