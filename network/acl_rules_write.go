package network

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// aclMaxUserPriority is the highest Priority AddNetworkACLRule accepts,
// confirmed live: 1999 works, and 2500 returns the server's own 400 "The
// priority is too big." aclDefaultRulePriority, one past it, is the
// seqNumber a new ACL's own deny-all rules hold; a rule at that priority or
// above is one of the server's own default rules (see isDefaultACLRule),
// which a caller can name to remove but never add.
const (
	aclMaxUserPriority     = 1999
	aclDefaultRulePriority = 2000
)

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

// Protocol values checkACLRuleProtocol accepts, and the exact spelling
// each sends to the server, confirmed live: "ANY" only in uppercase, and
// "tcp", "udp", and "icmp" only in lowercase. The server's own 400
// "Invalid protocol." punishes any other spelling, including "TCP" or
// "any"; checkACLRuleProtocol accepts a caller's protocol
// case-insensitively and always sends the server's own spelling, so that
// mismatch never reaches it.
const (
	aclProtocolAny  = "ANY"
	aclProtocolTCP  = "tcp"
	aclProtocolUDP  = "udp"
	aclProtocolICMP = "icmp"
)

// aclProtocolSpellings maps a protocol's upper-cased form to the exact
// spelling checkACLRuleProtocol sends to the server.
var aclProtocolSpellings = map[string]string{
	"ANY":  aclProtocolAny,
	"TCP":  aclProtocolTCP,
	"UDP":  aclProtocolUDP,
	"ICMP": aclProtocolICMP,
}

// checkACLRuleProtocol checks protocol per AddNetworkACLRule's doc
// comment, before any request, and returns the exact spelling to send:
// "ANY", "tcp", "udp", or "icmp", matched case-insensitively against
// protocol. Any other value is refused.
func checkACLRuleProtocol(op, protocol string) (string, error) {
	if spelling, ok := aclProtocolSpellings[strings.ToUpper(protocol)]; ok {
		return spelling, nil
	}
	return "", fmt.Errorf("%w: %s: Protocol must be one of ANY, tcp, udp, icmp (case-insensitive), got %q",
		core.ErrInvalidInput, op, protocol)
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
// comment, before any request, and returns the port string to send.
// protocol is the spelling checkACLRuleProtocol already returned.
//
// portMin and portMax must each be 0 to 65535, portMax left 0 defaults to
// portMin (so a single port such as 443 sets only portMin), and portMin
// must not be above the resulting portMax. Confirmed live, "ANY" carries
// no ports of its own and requires the full range, portMin 0 and portMax
// 65535; "icmp" accepts that same full range, or portMin 0 and portMax 0
// together, the server's own "every ICMP" port value. "tcp" and "udp" need
// an explicit port or range: portMin and portMax left at 0 and 0 together
// is refused, since that pair is never live-verified to mean a single port
// rather than every port, unlike "ANY" and "icmp"'s own explicit full-range
// encodings above.
//
// The returned string is portMin itself when portMin equals portMax
// (confirmed live: a single port sends "22"), or "portMin-portMax"
// otherwise (a range sends "53-54", every port "0-65535").
func checkACLRulePorts(op, protocol string, portMin, portMax int) (string, error) {
	if portMin < 0 || portMin > 65535 {
		return "", fmt.Errorf("%w: %s: PortRangeMin must be 0 to 65535, got %d", core.ErrInvalidInput, op, portMin)
	}
	if portMax < 0 || portMax > 65535 {
		return "", fmt.Errorf("%w: %s: PortRangeMax must be 0 to 65535, got %d", core.ErrInvalidInput, op, portMax)
	}
	if portMax == 0 {
		portMax = portMin
	}
	if portMin > portMax {
		return "", fmt.Errorf("%w: %s: PortRangeMin %d is above PortRangeMax %d", core.ErrInvalidInput, op, portMin, portMax)
	}
	switch protocol {
	case aclProtocolAny:
		if portMin != 0 || portMax != 65535 {
			return "", fmt.Errorf("%w: %s: protocol ANY requires PortRangeMin 0 and PortRangeMax 65535, got %d-%d",
				core.ErrInvalidInput, op, portMin, portMax)
		}
	case aclProtocolICMP:
		if portMin != 0 || (portMax != 65535 && portMax != 0) {
			return "", fmt.Errorf("%w: %s: protocol icmp requires PortRangeMin 0 and PortRangeMax 65535, or both 0, got %d-%d",
				core.ErrInvalidInput, op, portMin, portMax)
		}
	case aclProtocolTCP, aclProtocolUDP:
		if portMin == 0 && portMax == 0 {
			return "", fmt.Errorf("%w: %s: protocol %s needs an explicit PortRangeMin (and, for a range, PortRangeMax); leaving both at 0 may mean every port",
				core.ErrInvalidInput, op, protocol)
		}
	}
	if portMin == portMax {
		return strconv.Itoa(portMin), nil
	}
	return fmt.Sprintf("%d-%d", portMin, portMax), nil
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
// read and never let a caller remove or rewrite. Confirmed live, a new
// ACL's inbound and outbound deny-all rules hold Priority (seqNumber)
// aclDefaultRulePriority (2000); no caller-supplied Priority can ever equal
// or exceed that (checkACLRulePriority refuses it), so a rule at or above
// it must be the server's own. A rule that decodes System true is also
// treated as default.
//
// A new ACL's inbound and outbound pass-all rules at Priority 0 are not
// default by this marker: confirmed live, they are ordinary rules that
// exist by default but the server does not protect, and a caller may
// remove them like any other rule.
func isDefaultACLRule(rule ACLRule) bool {
	return rule.Priority >= aclDefaultRulePriority || rule.System
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
// Direction and Action are sent to the server as given and checked only
// for shape, not against a fixed value set, so a value the server adds
// later never needs an SDK release; the wiki shows "inbound" and
// "outbound" for Direction. Protocol is checked case-insensitively against
// the server's own accepted values, "ANY", "tcp", "udp", and "icmp", and
// sent in that exact spelling regardless of the case given; any other
// value is refused with core.ErrInvalidInput before any request, since the
// server's own refusal for a wrong spelling, such as "TCP" or "any", never
// names which spelling it wanted. Priority orders rules, lowest first, and
// must be 1 to 1999 (aclMaxUserPriority); a default rule the server owns
// holds a Priority of 2000 or above, or a fixed one inside the user range
// with its System flag set, so a caller can never add or overwrite one.
// CIDR must be a CIDR prefix with no host bits set.
//
// PortRangeMin and PortRangeMax each range 0 to 65535; PortRangeMax left 0
// sends the same value as PortRangeMin, and PortRangeMin must not be above
// the resulting PortRangeMax. Protocol "ANY" requires the full range,
// PortRangeMin 0 and PortRangeMax 65535; "icmp" accepts that same full
// range, or PortRangeMin 0 and PortRangeMax 0 together; either other
// pairing for these two protocols is refused. "tcp" and "udp" need an
// explicit port or range: PortRangeMin and PortRangeMax left at 0 and 0
// together is refused, since that pairing is never live-verified to mean a
// single port rather than every port.
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
// nothing is sent. CIDR is compared as a parsed prefix (so equivalent
// spellings, such as an IPv6 address in upper and lower case, match) and
// Action is compared case-folded; every other field is compared as given.
// The same key with any other field different fails with
// core.ErrInvalidInput, nothing sent. CIDR is sent to the server in its
// canonical parsed form, so a rerun and the post-write confirm compare
// like for like.
//
// The PUT itself is sent with Once true (see putACLRulesAndConfirm): never
// retried by the transport, so a retry can never land in the ACL's own busy
// window. Without NoWait, AddNetworkACLRule then waits for the ACL to
// return to ACTIVE, and confirms that a fresh read names exactly the rules
// just sent. A failure at any of these points returns an error wrapping
// ErrFailed (the ACL reached ERROR), ErrBusy (the pre-write wait, the
// pre-PUT re-read, or a busy 400 on the PUT's own single attempt), or
// ErrNotSettled (a 5xx, a network error, or a timeout on the PUT that may
// have reached the server; the post-write bound ran out; the confirm read
// did not match; or a read or sleep failed); once the PUT itself is sent,
// it is not resent on a rerun; AddNetworkACLRule simply reads the ACL again
// from the start.
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
	protocol, err := checkACLRuleProtocol(op, in.Protocol)
	if err != nil {
		return nil, err
	}
	port, err := checkACLRulePorts(op, protocol, in.PortRangeMin, in.PortRangeMax)
	if err != nil {
		return nil, err
	}

	acl, err := c.waitACLPreWriteActive(ctx, op, in.NetworkACLID)
	if err != nil {
		return nil, err
	}

	wantCIDR := canonicalCIDR(in.CIDR)
	entries := aclRuleEntriesOf(in.NetworkACLID, acl.Rules)
	for _, e := range entries {
		if !strings.EqualFold(e.Type, in.Direction) || e.SeqNumber != in.Priority {
			continue
		}
		if e.Protocol == protocol && e.Port == port && canonicalCIDR(e.Source) == wantCIDR && strings.EqualFold(e.Action, in.Action) {
			return &AddNetworkACLRuleOutput{ACL: *acl, Changed: false}, nil
		}
		return nil, fmt.Errorf("%w: %s: network ACL %s already has a %s rule at priority %d with different fields; remove it first",
			core.ErrInvalidInput, op, in.NetworkACLID, in.Direction, in.Priority)
	}
	entries = append(entries, aclRuleEntry{
		Type: in.Direction, SeqNumber: in.Priority, Protocol: protocol,
		Port: port, Source: wantCIDR, Action: in.Action,
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
// Priority is not tagged required: 0 is CheckRequired's zero value, and,
// confirmed live, also names the ordinary priority-0 pass-all rule a new
// ACL starts with, which a caller may remove. A caller must also be able
// to pass a Priority of aclDefaultRulePriority (2000) or above, to name a
// default rule and reach RemoveNetworkACLRule's ErrDefaultResource
// refusal, rather than being stopped earlier by a shape check meant only
// for a rule a caller could add (see ACLRule's doc comment and
// isDefaultACLRule). A negative value, which no rule can ever hold, is
// still refused before any request.
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
//
// The PUT is sent with Once true: never retried by the transport after a
// 5xx or a network error, so a retry can never land in the ACL's own busy
// window, confirmed live at roughly 18 seconds after an earlier write.
// wrapACLPutFailure classifies the PUT's own failure: a busy 400 there (the
// server naming the ACL busy) wraps ErrBusy, since that single attempt was
// rejected outright and nothing changed; any other 4xx is returned as is; a
// 5xx, a network error, or a timeout may have reached the server, so it
// wraps ErrNotSettled instead, since whether the ACL changed is unknown.
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
		Once:      true,
	}
	if err := c.c.DoJSON(ctx, req, nil); err != nil {
		return nil, wrapACLPutFailure(op, networkACLID, err)
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
