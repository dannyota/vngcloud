package loadbalancer

import (
	"context"
	"fmt"
)

func (c *Client) mutatePoolMembers(ctx context.Context, op, lbID, poolID string, noWait bool, merge func([]poolMemberEntry) ([]poolMemberEntry, bool, error)) (*Pool, bool, error) {
	deadline := c.now().Add(preWriteBound)
	preCtx, cancel := context.WithTimeout(ctx, preWriteBound)
	defer cancel()
	for attempt := 0; ; attempt++ {
		pool, err := c.poolPreWritePool(preCtx, op, lbID, poolID)
		if err != nil {
			return nil, false, err
		}
		members, err := c.ListPoolMembers(preCtx, &ListPoolMembersInput{LoadBalancerID: lbID, PoolID: poolID})
		if err != nil {
			return nil, false, err
		}
		if err := checkMemberReadsAgree(op, poolID, pool, members.Items); err != nil {
			return nil, false, err
		}
		entries, changed, err := merge(poolMemberEntriesOf(members.Items))
		if err != nil {
			return nil, false, err
		}
		if !changed {
			return pool, false, nil
		}
		if err := preCtx.Err(); err != nil {
			return nil, false, err
		}
		if !c.now().Before(deadline) {
			return nil, false, fmt.Errorf("%w: %s: pool is not ready; nothing sent", ErrBusy, op)
		}
		settled, err := c.sendPoolMembersAndConfirm(ctx, op, lbID, poolID, entries, noWait)
		if attempt == 0 && isBusyRefusal(err) {
			continue
		}
		if settled == nil {
			settled = pool
			if noWait && err == nil {
				existing := make(map[poolMemberKey]PoolMember, len(members.Items))
				for _, member := range members.Items {
					existing[poolMemberKeyOf(member.Address, member.ProtocolPort)] = member
				}
				settled.Members = make([]PoolMember, len(entries))
				for i, entry := range entries {
					member := existing[poolMemberKeyOf(entry.Address, entry.Port)]
					member.Address, member.ProtocolPort, member.MonitorPort = entry.Address, entry.Port, entry.MonitorPort
					member.Weight, member.Backup, member.Name = entry.Weight, entry.Backup, entry.Name
					settled.Members[i] = member
				}
			}
		}
		return settled, true, err
	}
}
