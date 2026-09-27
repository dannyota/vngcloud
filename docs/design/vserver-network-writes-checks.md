# vServer Network Writes: Checks

Status: Accepted (2026-09-27), with
[vServer network writes](vserver-network-writes.md).

The unit tests, cost probes, and live checks for
[vServer network writes](vserver-network-writes.md). Terms and sentinels
are defined there.

## Unit tests

Unit tests use `httptest` and an injected clock and sleep.

- Sanitized raw fixtures and decode tests for each create, get, and
  replace response in `testdata/compute/` and `testdata/network/`,
  including the integer `serverGroupId`, the route table create map, and
  an ACL with default and user rules and two associated subnets.
- Request bodies: server group create with and without description;
  update of only `Name` resends the read description, and the reverse;
  VPC create with and without `ZoneID`; subnet create never sends
  `secondarySubnetRequests`; add and remove bodies for routes, rules, and
  subnets hold every entry read except the one removed, plus the one
  added.
- Shape refusals with no request: VPC or subnet CIDR with host bits, IPv6,
  or no prefix; route target that is not an address; ACL priority 0, port
  70000, min above max; empty server group update; subnet rename of a
  subnet with secondary subnets.
- Guards, each sending no write: server group with servers, and the
  server's `server group is in use` message at 400 and 409; VPC with a
  subnet, a server count, or a volume count; subnet with a server, an
  interface, or a virtual IP; main route table; route table named by a
  subnet; ACL with subnets; default ACL; default rule removal; associate
  of a subnet whose `GetSubnet` under the ACL's VPC is 404.
- Read-merge outcomes: add of a present route or rule is `Changed` false
  with no `PUT`; add with a conflicting target or rule is
  `ErrInvalidInput`; remove of an absent one is `NotFound`; a confirm read
  that differs is `ErrNotSettled`; default rules are never sent changed.
- Private DNS enable: `ENABLED` sends nothing; `ENABLING` waits without
  sending; `DISABLED` sends one `PATCH` and waits; an unknown status is
  `ErrUnexpectedStatus`; a 502 on the `PATCH` is not resent and returns
  `ErrNotSettled`; a 400 returns the `*APIError`.
- Waits, per row of the waits table: settled, `ERROR`, 404 then `ACTIVE`,
  delete by 404 and by `DELETED`, the bound, `NoWait`, poll spacing, and a
  cancelled context. The pre-write wait returns `ErrBusy` with no `PUT`.
- Statuses 200, 201, 202, 204, 400, 404, 409, and 5xx; no create resend
  after a 502; a create response without an ID fails with a message naming
  the list to check.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every operation
  that takes an ID, including `GetVPC`, `GetSubnet`, and
  `ListSubnetsByVPC`.
- CLI golden tests for every command; `--yes` refusals for each command
  the CLI table marks; read-only refusal with no request; the
  `--network-acl-id` flag name.

## Cost probes

One create and delete per pick on the test account in `hcm-3`, as the
survey approved. The manager runs them before the first release's code
merges, through the console or a throwaway script on the IAM vServer
gateway, following [live data](../../instructions/live-data.md): log only
statuses, field names, types, counts, and timings. Each write run needs
the owner's approval naming the account, region, and resources. Names are
`vngcloud-live-<8 hex>`.

Read-only, first:

1. `vngcloud billing list-cost-resources` and `get-cost-overview` for the
   current period, and `get-balances`: the baseline for the next-day
   check.
2. `vngcloud portal list-quota-used`: the rows for VPCs, subnets, route
   tables, network ACLs, and server groups, with limit and use. Stop if
   any has no free slot.
3. `vngcloud network list-vpcs`: CIDRs in use, so the probe picks a free
   `/16` (for example `10.251.0.0/16`), and any field that marks a default
   VPC.
4. `vngcloud compute list-server-group-policies`: the policy IDs.

Writes, in one session:

5. Server group: `POST serverGroups` with the first policy; `GET` it;
   `DELETE`; `GET` again for 404.
6. VPC: `POST networks` with the free `/16`; poll `GET` until `ACTIVE`,
   noting the time.
7. Subnet: `POST networks/{vpc}/subnets` with a `/24` inside it; poll
   until `ACTIVE`.
8. Route table: `POST route-table` with the probe VPC; poll until
   `ACTIVE`; `DELETE`; poll until 404.
9. Network ACL: `POST network-acl` with the probe VPC; `GET`; `DELETE`;
   `GET` for 404.
10. Private DNS: `PATCH networks/{vpc}/enableDns` once; poll `GET` every
    10 seconds until `dnsStatus` is `ENABLED`, noting the time.
11. Cleanup: `DELETE` the subnet and poll until 404; `DELETE` the VPC and
    poll until 404. If either delete fails, stop and report; the VPC
    holds nothing billable.

Next day:

12. `billing list-cost-resources` and `get-cost-overview`: no new line for
    a VPC, subnet, route table, ACL, vDNS, or server group; `get-balances`
    unchanged. Any new line stops the releases until the owner decides.

## Live checks

Each release's checks pass on the test account before its code merges.
The manager runs them through the release's live write test, with the
same logging rule and approval as the probes. Writes that change a whole
VPC (main route table routes, ACL association, Private DNS) run only on a
VPC the same run created; the test keeps that VPC's ID and never sends
such a write to another ID.

### N1 server groups

Read-only: `ListServerGroups` raw rows (`serverGroupId` type, `servers`),
a `GET` on one group when any exists, and whether the `name` filter
matches exactly or by substring.

1. Create a group with each policy: status, response shape, fields set.
   Create the same name again: status and message.
2. Update the name only, then the description only, with `serverGroupId`
   left out of the body: accepted or refused. Update with `description`
   `""`.
3. Delete: status. Repeat delete: status. `GET` after delete: 404.
4. `portal list-quota-used`: the server group row before and after.

Not checkable without a paid server: deleting a group with servers. The
pre-read covers it; the server's message is from VNG Cloud's SDK.

### N2 VPCs and subnets

Read-only: `ListVPCs` and `GetVPC` raw rows for default markers; the
`name` filter; `ListSubnetsByVPC` on a VPC with Private DNS `ENABLED`,
when one exists, for reserved subnets and how they are marked.

1. Create a VPC without `zoneId`: status, response shape, zone chosen,
   statuses and time to `ACTIVE`. Create the same name again, and the same
   CIDR under another name: status and message; delete any second VPC.
2. Rename the VPC; rename it to the same name.
3. Create a `/24` subnet and a `/28` subnet: status, shape, time to
   `ACTIVE`. Create an overlapping subnet: status and message. Rename one.
4. `ListServersBySubnet` on the new subnet: empty list shape.
5. Enable Private DNS: status, response shape, `dnsStatus` values, time to
   `ENABLED`. Then list subnets: reserved subnets, and how they are
   marked.
6. Delete one subnet: status, statuses until 404 or `DELETED`, time.
   Repeat delete: status.
7. On a second test VPC with one subnet, send the raw VPC `DELETE`
   without the SDK guard: refused or cascaded, status, and message.
8. Delete the first VPC, with Private DNS on and its remaining subnet
   deleted first: status, time until 404, and whether reserved subnets
   blocked it.
9. `portal list-quota-used`: VPC and subnet rows before and after.

### N3 route tables

Read-only: `ListRouteTables` raw rows for the main table of an existing
VPC: its routes, `routingType` values, and any default marker; each
subnet's `routeTableUuid`; the `name` filter.

On a VPC and `/24` subnet the run creates:

1. Create a route table: status, response keys, time to `ACTIVE`. Create
   the same name again.
2. Add a route to `10.251.200.0/24` with a target address inside the test
   subnet that no interface holds: accepted or refused, the new route's
   `routingType`, statuses from `UPDATING` to `ACTIVE`, time.
3. On the test VPC's main route table: whether it has system routes, and
   whether a `PUT` that leaves them out keeps them. Restore the table as
   read afterwards.
4. Remove the route: status and final list.
5. Delete the route table: status 202, time until 404. Repeat delete.

### N4 network ACLs

Read-only: `ListNetworkACLs` raw rows against the `ACL` model; a `GET` on
one ACL when any exists, for rule fields and default markers.

On a VPC and `/24` subnet the run creates:

1. Create an ACL: status, shape, `defaultAcl`. `GET`: the default rules,
   their count, `type`, `seqNumber`, `protocol`, `port`, `source`,
   `action`, and any field that marks them. Create the same name again.
2. Add an inbound `TCP` rule for port 443 from `203.0.113.0/24`, priority
   100, `allow`, with the default rules left out of the `PUT`: kept or
   dropped. If dropped, restore them and repeat with the default rules
   resent as read.
3. Add an `ANY` rule and an `ICMP` rule: how `port` is stored for each.
   Add a rule with a used priority: status and message.
4. Remove the user rules: final list, default rules unchanged.
5. Associate the test subnet: status, `subnetAssociationList`, the
   subnet's `interfaceAclPolicyUuid`. Associate again. Send the raw ACL
   `DELETE` while associated: status and message.
6. Disassociate: the subnet's ACL fields afterwards.
7. Delete the ACL: status. Repeat delete: status.

### Cleanup

The live write test first deletes leftovers whose names start with
`vngcloud-live-`: ACLs (after disassociating their subnets), route tables,
subnets, VPCs, then server groups. It registers `t.Cleanup` as soon as
each ID is known, and deletes in that order with its own context,
asserting none remain. If a create fails, it lists by exact name and
deletes a match. It never touches a resource without the prefix, a main
route table, or a default ACL. The VPC checks take more than 10 minutes,
so the live target's timeout is at least 20 minutes.
