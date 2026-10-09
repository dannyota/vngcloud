package loadbalancer

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// poolMemberEntry is one entry of the members list AddPoolMember,
// UpdatePoolMember, and RemovePoolMember send: every field the API's
// replace body takes per member (no member id).
type poolMemberEntry struct {
	Address     string `json:"ipAddress"`
	Port        int    `json:"port"`
	Backup      bool   `json:"backup"`
	Weight      int    `json:"weight"`
	Name        string `json:"name,omitempty"`
	MonitorPort int    `json:"monitorPort,omitempty"`
}

// poolMembersReplaceBody is the PUT .../members request body.
type poolMembersReplaceBody struct {
	Members []poolMemberEntry `json:"members"`
}

// canonicalIPv4 returns address's canonical net/netip.Addr string, so
// equivalent spellings of the same address compare equal. A value that does
// not parse as an address is returned unchanged.
func canonicalIPv4(address string) string {
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return address
	}
	return addr.String()
}

// poolMemberKey identifies a member by address and port, the two fields the
// members PUT carries no other identifier for.
type poolMemberKey struct {
	address string
	port    int
}

func poolMemberKeyOf(address string, port int) poolMemberKey {
	return poolMemberKey{address: canonicalIPv4(address), port: port}
}

// effectiveMonitorPort returns the health check port a member is sent with:
// monitorPort, or the traffic port when monitorPort is 0, because the API
// refuses monitorPort 0.
func effectiveMonitorPort(port, monitorPort int) int {
	if monitorPort == 0 {
		return port
	}
	return monitorPort
}

// poolMemberEntriesOf builds the members replace body from members exactly
// as ListPoolMembers read them, dropping every field but the ones the API
// lets a caller resend, so a replace never silently drops a member the
// caller did not name.
func poolMemberEntriesOf(members []PoolMember) []poolMemberEntry {
	entries := make([]poolMemberEntry, len(members))
	for i, m := range members {
		entries[i] = poolMemberEntry{Address: m.Address, Port: m.ProtocolPort, Backup: m.Backup, Weight: m.Weight, Name: m.Name, MonitorPort: effectiveMonitorPort(m.ProtocolPort, m.MonitorPort)}
	}
	return entries
}

// findPoolMemberEntry returns the index of the entry in entries whose
// address and port match key, or -1.
func findPoolMemberEntry(entries []poolMemberEntry, key poolMemberKey) int {
	for i, e := range entries {
		if poolMemberKeyOf(e.Address, e.Port) == key {
			return i
		}
	}
	return -1
}

// poolMemberFieldsEqual reports whether a and b carry the same Backup,
// Weight, Name, and MonitorPort; it ignores Address and Port, which the
// caller has already matched as the same key.
func poolMemberFieldsEqual(a, b poolMemberEntry) bool {
	return a.Backup == b.Backup && a.Weight == b.Weight && a.Name == b.Name && a.MonitorPort == b.MonitorPort
}

// poolMembersEqual reports whether members, read back after a replace, name
// exactly the same entries as sent, regardless of order.
func poolMembersEqual(members []PoolMember, sent []poolMemberEntry) bool {
	if len(members) != len(sent) {
		return false
	}
	remaining := make(map[poolMemberEntry]int, len(sent))
	for _, e := range sent {
		e.Address = canonicalIPv4(e.Address)
		remaining[e]++
	}
	for _, m := range members {
		e := poolMemberEntry{Address: canonicalIPv4(m.Address), Port: m.ProtocolPort, Backup: m.Backup, Weight: m.Weight, Name: m.Name, MonitorPort: effectiveMonitorPort(m.ProtocolPort, m.MonitorPort)}
		if remaining[e] == 0 {
			return false
		}
		remaining[e]--
	}
	return true
}

// checkPoolMemberAddress returns core.ErrInvalidInput unless address parses
// as an IPv4 address.
func checkPoolMemberAddress(op, address string) error {
	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is4() {
		return fmt.Errorf("%w: %s: Address must be an IPv4 address, got %q", core.ErrInvalidInput, op, address)
	}
	return nil
}

// checkPoolMemberPort returns core.ErrInvalidInput unless port is 1 to
// 65535.
func checkPoolMemberPort(op, field string, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("%w: %s: %s must be 1 to 65535, got %d", core.ErrInvalidInput, op, field, port)
	}
	return nil
}

// checkPoolMemberMonitorPort returns core.ErrInvalidInput unless
// monitorPort is 0 (unset) or 1 to 65535.
func checkPoolMemberMonitorPort(op string, monitorPort int) error {
	if monitorPort == 0 {
		return nil
	}
	return checkPoolMemberPort(op, "MonitorPort", monitorPort)
}

// sendPoolMembersAndConfirm sends entries as poolID's whole member list,
// then, unless noWait, waits for the pool to settle (CREATED, load balancer
// not busy) and confirms that a fresh ListPoolMembers names exactly entries.
// A mismatch there wraps ErrNotSettled: the write already reached the
// server, and another writer may have changed the pool since. The returned
// Pool is nil when noWait is set or no confirming read ever completed; the
// caller falls back to the pool it already read before sending.
func (c *Client) sendPoolMembersAndConfirm(ctx context.Context, op, lbID, poolID string, entries []poolMemberEntry, noWait bool) (*Pool, error) {
	projectID, err := c.c.RequireProjectID(ctx)
	if err != nil {
		return nil, err
	}
	req := transport.Request{
		Operation: op,
		Method:    http.MethodPut,
		URL:       c.lbURL([]string{projectID, "loadBalancers", lbID, "pools", poolID, "members"}, nil),
		Body:      poolMembersReplaceBody{Members: entries},
		OK:        httpStatusOKWrite,
	}
	if err := sendWithBusyResend(ctx, poolBusyWaiter(c, op, lbID, poolID), func() error {
		return c.c.DoJSON(ctx, req, nil)
	}); err != nil {
		return nil, err
	}
	if noWait {
		return nil, nil
	}

	var pool *Pool
	if err := c.waitChildSettled(ctx, op, lbID, "pool", poolID, func(ctx context.Context) (string, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: lbID, PoolID: poolID})
		if err != nil {
			return "", err
		}
		pool = &out.Pool
		return pool.ProgressStatus, nil
	}); err != nil {
		return pool, err
	}

	membersOut, err := c.ListPoolMembers(ctx, &ListPoolMembersInput{LoadBalancerID: lbID, PoolID: poolID})
	if err != nil {
		return pool, fmt.Errorf("%w: %s: pool %s: %w", ErrNotSettled, op, poolID, err)
	}
	if !poolMembersEqual(membersOut.Items, entries) {
		return pool, fmt.Errorf("%w: %s: pool %s: members read after the write do not match what was sent; another writer may have changed the pool",
			ErrNotSettled, op, poolID)
	}
	return pool, nil
}

// poolMemberKeySetOf builds the set of poolMemberKey (address and port) for
// members.
func poolMemberKeySetOf(members []PoolMember) map[poolMemberKey]bool {
	set := make(map[poolMemberKey]bool, len(members))
	for _, m := range members {
		set[poolMemberKeyOf(m.Address, m.ProtocolPort)] = true
	}
	return set
}

// checkNoDuplicateMemberKey returns ErrBusy when members names the same
// Address and Port twice. The members PUT carries no member id, so
// findPoolMemberEntry and poolMembersEqual both assume Address and Port
// uniquely name one member; a read that violates that would make an update
// silently target only the first match, or a members replace read back as
// mismatched even though it succeeded. label names which read produced
// members, for the error message. Nothing has been sent by the time this
// runs, so a retry is always safe.
func checkNoDuplicateMemberKey(op, poolID, label string, members []PoolMember) error {
	seen := make(map[poolMemberKey]bool, len(members))
	for _, m := range members {
		key := poolMemberKeyOf(m.Address, m.ProtocolPort)
		if seen[key] {
			return fmt.Errorf("%w: %s: pool %s: %s names more than one member at %s:%d; nothing sent, retry",
				ErrBusy, op, poolID, label, m.Address, m.ProtocolPort)
		}
		seen[key] = true
	}
	return nil
}

// checkMemberReadsAgree refuses to proceed when either the pre-write GetPool
// read's own embedded Members, or a fresh ListPoolMembers read, names the
// same Address and Port twice (checkNoDuplicateMemberKey), or when the two
// reads do not name the same set of members, keyed by Address and Port. The
// two reads happen moments apart; server lag or another process's write
// between them can make GetPool's embedded list and ListPoolMembers disagree
// by any number of members, not just down to zero, and every write in this
// file would otherwise resend whichever list it read as the pool's whole
// set, silently dropping or fabricating the members only the other read
// named. A duplicate key within a single read can leave the two reads'
// key sets equal in size and content even though one of them double-counts
// an address and port, so the set comparison alone would not catch it.
//
// GetPool omitting the members field entirely leaves pool.Members nil,
// distinct from the server naming an empty list; there is then nothing to
// compare against, so the set comparison is skipped and the write proceeds
// from the ListPoolMembers read alone, as it always did before this check
// existed. The duplicate check on that ListPoolMembers read still runs.
func checkMemberReadsAgree(op, poolID string, pool *Pool, members []PoolMember) error {
	if err := checkNoDuplicateMemberKey(op, poolID, "the pool read", pool.Members); err != nil {
		return err
	}
	if err := checkNoDuplicateMemberKey(op, poolID, "ListPoolMembers", members); err != nil {
		return err
	}
	if pool.Members == nil {
		return nil
	}
	want := poolMemberKeySetOf(pool.Members)
	got := poolMemberKeySetOf(members)
	agree := len(want) == len(got)
	if agree {
		for k := range want {
			if !got[k] {
				agree = false
				break
			}
		}
	}
	if agree {
		return nil
	}
	return fmt.Errorf("%w: %s: pool %s: the pool read named %d member(s) and ListPoolMembers named %d; the two reads disagree, nothing sent, retry",
		ErrBusy, op, poolID, len(pool.Members), len(members))
}

// poolPreWritePool waits, within the pre-write bound, until the load
// balancer and the pool are both not busy, and returns the pool a read
// found once ready. It is shared by AddPoolMember, UpdatePoolMember, and
// RemovePoolMember, none of which have a child other than the pool itself.
func (c *Client) poolPreWritePool(ctx context.Context, op, lbID, poolID string) (*Pool, error) {
	return waitPreWriteReady(c, ctx, op, lbID, "pool", poolID, func(ctx context.Context) (*Pool, string, error) {
		out, err := c.GetPool(ctx, &GetPoolInput{LoadBalancerID: lbID, PoolID: poolID})
		if err != nil {
			return nil, "", err
		}
		return &out.Pool, out.Pool.ProgressStatus, nil
	})
}

// AddPoolMemberInput adds one member to a pool.
type AddPoolMemberInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
	Address        string `vngcloud:"required"`
	Port           int    `vngcloud:"required"`

	Name        string
	Weight      int
	MonitorPort int
	Backup      bool

	NoWait bool
}

type AddPoolMemberOutput struct {
	Pool    Pool
	Changed bool
}

// AddPoolMember adds one member to a pool, identified by Address (must
// parse as IPv4) and Port (1 to 65535); MonitorPort, when set, must also be
// 1 to 65535, and 0 sends Port, because the API refuses monitorPort 0.
// Weight 0 sends 1.
//
// The members PUT replaces the whole list, so AddPoolMember is a
// read-merge write: it waits, within the pre-write bound, until the load
// balancer and the pool are both not busy (ErrBusy, nothing sent, past that
// bound), reads the pool's members with ListPoolMembers, and sends back
// every member it read plus the one being added, never a caller-supplied
// whole list.
//
// A member already present for Address and Port with the same Name, Weight,
// MonitorPort, and Backup makes AddPoolMember a no-op: Changed is false and
// nothing is sent. One present with any different field fails with
// core.ErrInvalidInput naming update-pool-member, nothing sent.
//
// Without NoWait, AddPoolMember waits for the pool to settle after the PUT,
// then confirms that a fresh read names exactly the members just sent. A
// failure at any of these points returns an error wrapping ErrFailed (the
// pool or load balancer reached ERROR) or ErrNotSettled (the post-write
// bound ran out, the confirm read did not match, or a read or sleep
// failed). Once the PUT is sent, it is not resent on a rerun; AddPoolMember
// simply reads the pool's members again from the start.
func (c *Client) AddPoolMember(ctx context.Context, in *AddPoolMemberInput) (*AddPoolMemberOutput, error) {
	const op = "loadbalancer.AddPoolMember"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PoolID", in.PoolID); err != nil {
		return nil, err
	}
	if err := checkPoolMemberAddress(op, in.Address); err != nil {
		return nil, err
	}
	if err := checkPoolMemberPort(op, "Port", in.Port); err != nil {
		return nil, err
	}
	if err := checkPoolMemberMonitorPort(op, in.MonitorPort); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	pool, err := c.poolPreWritePool(ctx, op, in.LoadBalancerID, in.PoolID)
	if err != nil {
		return nil, err
	}

	membersOut, err := c.ListPoolMembers(ctx, &ListPoolMembersInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
	if err != nil {
		return nil, err
	}
	if err := checkMemberReadsAgree(op, in.PoolID, pool, membersOut.Items); err != nil {
		return nil, err
	}
	entries := poolMemberEntriesOf(membersOut.Items)

	weight := in.Weight
	if weight == 0 {
		weight = 1
	}
	wanted := poolMemberEntry{Address: in.Address, Port: in.Port, Backup: in.Backup, Weight: weight, Name: in.Name, MonitorPort: effectiveMonitorPort(in.Port, in.MonitorPort)}
	key := poolMemberKeyOf(in.Address, in.Port)
	if i := findPoolMemberEntry(entries, key); i >= 0 {
		if poolMemberFieldsEqual(entries[i], wanted) {
			return &AddPoolMemberOutput{Pool: *pool, Changed: false}, nil
		}
		return nil, fmt.Errorf("%w: %s: pool %s already has a member at %s:%d; use update-pool-member to change it",
			core.ErrInvalidInput, op, in.PoolID, in.Address, in.Port)
	}
	entries = append(entries, wanted)

	settled, err := c.sendPoolMembersAndConfirm(ctx, op, in.LoadBalancerID, in.PoolID, entries, in.NoWait)
	if settled == nil {
		settled = pool
	}
	return &AddPoolMemberOutput{Pool: *settled, Changed: true}, err
}

// UpdatePoolMemberInput changes a member's Name, Weight, MonitorPort, or
// Backup; a nil field keeps its current value. Address and Port identify
// the member and cannot themselves be changed: remove it and add it again
// under a new address or port.
type UpdatePoolMemberInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
	Address        string `vngcloud:"required"`
	Port           int    `vngcloud:"required"`

	Name        *string
	Weight      *int
	MonitorPort *int
	Backup      *bool

	NoWait bool
}

type UpdatePoolMemberOutput struct {
	Pool    Pool
	Changed bool
}

// UpdatePoolMember changes one member of a pool, identified by Address and
// Port; at least one of Name, Weight, MonitorPort, and Backup must be set,
// checked before any request (core.ErrInvalidInput). No member matching
// Address and Port fails with core.ErrNotFound, nothing sent.
//
// UpdatePoolMember is the same read-merge write AddPoolMember's doc comment
// describes: it waits for the load balancer and the pool to be ready, reads
// every member, applies the set fields to the matching one, and sends back
// the whole list. A Weight of 0 sends 1, and a MonitorPort of 0 sends Port. When the result has no field
// different from what was read, Changed is false and nothing is sent.
// Otherwise, without NoWait, it waits and confirms exactly as AddPoolMember
// does; see its doc comment for the wait, the confirm, and rerun safety.
func (c *Client) UpdatePoolMember(ctx context.Context, in *UpdatePoolMemberInput) (*UpdatePoolMemberOutput, error) {
	const op = "loadbalancer.UpdatePoolMember"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PoolID", in.PoolID); err != nil {
		return nil, err
	}
	if err := checkPoolMemberAddress(op, in.Address); err != nil {
		return nil, err
	}
	if err := checkPoolMemberPort(op, "Port", in.Port); err != nil {
		return nil, err
	}
	if in.MonitorPort != nil {
		if err := checkPoolMemberMonitorPort(op, *in.MonitorPort); err != nil {
			return nil, err
		}
	}
	if in.Name == nil && in.Weight == nil && in.MonitorPort == nil && in.Backup == nil {
		return nil, fmt.Errorf("%w: %s requires at least one field to change", core.ErrInvalidInput, op)
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	pool, err := c.poolPreWritePool(ctx, op, in.LoadBalancerID, in.PoolID)
	if err != nil {
		return nil, err
	}

	membersOut, err := c.ListPoolMembers(ctx, &ListPoolMembersInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
	if err != nil {
		return nil, err
	}
	if err := checkMemberReadsAgree(op, in.PoolID, pool, membersOut.Items); err != nil {
		return nil, err
	}
	entries := poolMemberEntriesOf(membersOut.Items)
	key := poolMemberKeyOf(in.Address, in.Port)
	i := findPoolMemberEntry(entries, key)
	if i < 0 {
		return nil, fmt.Errorf("%w: %s: pool %s has no member at %s:%d", core.ErrNotFound, op, in.PoolID, in.Address, in.Port)
	}

	updated := entries[i]
	if in.Name != nil {
		updated.Name = *in.Name
	}
	if in.Weight != nil {
		w := *in.Weight
		if w == 0 {
			w = 1
		}
		updated.Weight = w
	}
	if in.MonitorPort != nil {
		updated.MonitorPort = effectiveMonitorPort(updated.Port, *in.MonitorPort)
	}
	if in.Backup != nil {
		updated.Backup = *in.Backup
	}
	if poolMemberFieldsEqual(entries[i], updated) {
		return &UpdatePoolMemberOutput{Pool: *pool, Changed: false}, nil
	}
	entries[i] = updated

	settled, err := c.sendPoolMembersAndConfirm(ctx, op, in.LoadBalancerID, in.PoolID, entries, in.NoWait)
	if settled == nil {
		settled = pool
	}
	return &UpdatePoolMemberOutput{Pool: *settled, Changed: true}, err
}

// RemovePoolMemberInput identifies the member to remove by Address and
// Port.
type RemovePoolMemberInput struct {
	LoadBalancerID string `vngcloud:"required"`
	PoolID         string `vngcloud:"required"`
	Address        string `vngcloud:"required"`
	Port           int    `vngcloud:"required"`

	NoWait bool
}

type RemovePoolMemberOutput struct {
	Pool    Pool
	Changed bool
}

// RemovePoolMember removes one member from a pool, identified by Address
// and Port. No member matching them fails with core.ErrNotFound, nothing
// sent. It is the read-merge write AddPoolMember's doc comment describes,
// in reverse: it waits, reads every member, and sends back every one except
// the match. Without NoWait, it waits and confirms exactly as AddPoolMember
// does.
func (c *Client) RemovePoolMember(ctx context.Context, in *RemovePoolMemberInput) (*RemovePoolMemberOutput, error) {
	const op = "loadbalancer.RemovePoolMember"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "LoadBalancerID", in.LoadBalancerID); err != nil {
		return nil, err
	}
	if err := core.CheckPathID(op, "PoolID", in.PoolID); err != nil {
		return nil, err
	}
	if err := checkPoolMemberAddress(op, in.Address); err != nil {
		return nil, err
	}
	if err := checkPoolMemberPort(op, "Port", in.Port); err != nil {
		return nil, err
	}

	unlock, err := c.lockLoadBalancer(ctx, in.LoadBalancerID)
	if err != nil {
		return nil, err
	}
	defer unlock()

	pool, err := c.poolPreWritePool(ctx, op, in.LoadBalancerID, in.PoolID)
	if err != nil {
		return nil, err
	}

	membersOut, err := c.ListPoolMembers(ctx, &ListPoolMembersInput{LoadBalancerID: in.LoadBalancerID, PoolID: in.PoolID})
	if err != nil {
		return nil, err
	}
	if err := checkMemberReadsAgree(op, in.PoolID, pool, membersOut.Items); err != nil {
		return nil, err
	}
	entries := poolMemberEntriesOf(membersOut.Items)
	key := poolMemberKeyOf(in.Address, in.Port)
	i := findPoolMemberEntry(entries, key)
	if i < 0 {
		return nil, fmt.Errorf("%w: %s: pool %s has no member at %s:%d", core.ErrNotFound, op, in.PoolID, in.Address, in.Port)
	}
	entries = append(entries[:i], entries[i+1:]...)

	settled, err := c.sendPoolMembersAndConfirm(ctx, op, in.LoadBalancerID, in.PoolID, entries, in.NoWait)
	if settled == nil {
		settled = pool
	}
	return &RemovePoolMemberOutput{Pool: *settled, Changed: true}, err
}
