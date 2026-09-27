# vServer Network Writes: Checks

Status: Accepted (2026-09-27), with
[vServer network writes](vserver-network-writes.md).

The unit tests, cost probes, live checks, and security review for
[vServer network writes](vserver-network-writes.md). Terms and sentinels
are defined there.

## Unit tests

Unit tests use `httptest` and an injected clock and sleep.

- Sanitized raw fixtures and decode tests for each create, get, and
  replace response in `testdata/compute/` and `testdata/network/`,
  including the integer `serverGroupId`, the route table create map, and
  an ACL with default and user rules and two associated subnets.
- Request bodies: server group create with and without description;
  update of only `Name` resends the read description, and the reverse,
  and always sends `serverGroupId` equal to the path ID; VPC create never
  sends `zoneId`; subnet create sends `zoneId` and never
  `secondarySubnetRequests`; add and remove bodies for routes, rules, and
  subnets hold every entry read except the one removed, plus the one
  added.
- Missing resources: server group get with `data` null is `NotFound`;
  subnet delete of a `DELETED` subnet sends nothing and is `NotFound`;
  ACL get 500 with the ID absent from the list is `NotFound`, and with
  the ID listed, or a failed list, returns the 500; a bare 500 on any
  other get is never `NotFound`.
- Delete confirms: subnet `DELETE` 500 with the subnet absent from the
  VPC's list succeeds and waits, and with it listed returns the 500; ACL
  `DELETE` 500 confirms the same way; VPC `DELETE` 400 `contains the
  subnet` with an empty list is `ErrInUse` and is not resent.
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
  VPC and route table delete by 404, subnet delete by list absence while
  `GetSubnet` still returns `DELETED`, the bound, `NoWait`, poll spacing,
  and a cancelled context. The pre-write wait returns `ErrBusy` with no `PUT`.
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

The manager ran one create and delete per pick on the test account in
`hcm-3` on 2026-09-27, through a throwaway script, following
[live data](../../instructions/live-data.md), and deleted every probe
resource. The results are in
[vServer network writes: API](vserver-network-writes-api.md). The read-only
baseline (balances, the period's cost, and the quota rows) came first.

Still to run, the next day: `billing list-cost-resources` and
`get-cost-overview` show no new line for a VPC, subnet, route table, ACL,
vDNS, or server group, and `get-balances` is unchanged. Any new line stops
the releases until the owner decides.

## Live checks

Each release's checks pass on the test account before its code merges.
The manager runs them through the release's live write test, following
[live data](../../instructions/live-data.md); each write run needs the
owner's approval naming the account, region, and resources. Writes that
change a whole VPC (main route table routes, ACL association, Private DNS)
run only on a VPC the same run created; the test keeps that VPC's ID and
never sends such a write to another ID.

The account's VPC quota leaves one free VPC, so a run creates at most one
VPC, and no two live tests that create a VPC (N2, N3, N4, or vDNS) run at
the same time.

### N1 server groups

Read-only: `ListServerGroups` raw rows (`serverGroupId` type, `servers`),
a `GET` on one group when any exists, and whether the `name` filter
matches exactly or by substring.

1. Create a group with each policy: status, response shape, fields set.
   Create the same name again: 400 and its message.
2. Update the name only, then the description only. Update with
   `description` `""`.
3. Delete: 204. Repeat delete: status. `GET` after delete: `data` null.
4. `portal list-quota-used`: the server group row before and after.

Not checkable without a paid server: deleting a group with servers. The
pre-read covers it; the server's message is from VNG Cloud's SDK.

### N2 VPCs and subnets

Read-only: `ListVPCs` and `GetVPC` raw rows for default markers; the
`name` filter; `portal list-zones` for the enabled zone.

1. Create a VPC: 200, `ACTIVE`. A second VPC create hits the quota, so
   duplicate VPC names and CIDRs stay unchecked.
2. Rename the VPC; rename it to the same name.
3. Create a `/24` subnet and a `/28` subnet in the enabled zone: status,
   shape, time to `ACTIVE`. Create an overlapping subnet, and one with the
   same name: status and message. Rename one.
4. `ListServersBySubnet` on the new subnet: empty list shape.
5. Enable Private DNS: `ENABLING`, then `ENABLED` within the bound; the
   subnet list is unchanged.
6. Delete one subnet: 200; `GetSubnet` status and the time until it
   leaves the list. Repeat delete: 500, and the SDK's list confirm.
7. Delete the VPC through the SDK with the other subnet deleted first:
   the `ErrInUse` refusals, then the time until the delete succeeds and
   until 404. Whether a `DELETED` subnet still counts against the subnet
   quota.
8. `portal list-quota-used`: VPC and subnet rows before and after.

### N3 route tables

Read-only: `ListRouteTables` raw rows for the main table of an existing
VPC: its routes, `routingType` values, and any default marker; each
subnet's `routeTableUuid`; the `name` filter.

On a VPC and `/24` subnet the run creates:

1. Create a route table: 202, `ACTIVE`, no routes. Create the same name
   again.
2. Add a route to `10.251.200.0/24` with a target address inside the test
   subnet that no interface holds: accepted or refused, the new route's
   `routingType`, statuses from `UPDATING` to `ACTIVE`, time.
3. Whether the new table became the VPC's main table: a VPC created with
   none gets one assigned automatically, and this run's own VPC has none
   beforehand. When it did, whether that table carries any routes right
   after becoming main.
4. Remove the route: status and final list.
5. Delete the route table: 202, then 404. Repeat delete: status.

### N4 network ACLs

Read-only: `ListNetworkACLs` raw rows against the `ACL` model; a `GET` on
one ACL when any exists, for rule fields and default markers.

On a VPC and `/24` subnet the run creates:

1. Create an ACL: 201, `ACTIVE`, `defaultAcl` false. `GET`: every default
   rule, their count, `type`, `seqNumber`, `protocol`, `port`, `source`,
   `action`, and any field that marks them. Create the same name again.
2. Add an inbound `TCP` rule for port 443 from `203.0.113.0/24`, priority
   100, action `pass` (the value the default rule uses), with the default
   rules left out of the `PUT`: kept or dropped. If dropped, restore them
   and repeat with the default rules resent as read.
3. Add an `ANY` rule and an `ICMP` rule: how `port` is stored for each.
   Add a rule with a used priority: status and message.
4. Remove the user rules: final list, default rules unchanged.
5. Associate the test subnet: status, `subnetAssociationList`, the
   subnet's `interfaceAclPolicyUuid`. Associate again. Try
   `DeleteNetworkACL` while associated: the SDK's own `ErrInUse` refusal,
   or the server's; confirm the ACL still exists and still lists the
   subnet.
6. Disassociate: the subnet's ACL fields afterwards.
7. Delete the ACL: 204, and `GetNetworkACL` afterwards is `NotFound`
   through the list confirm. Repeat delete: status.

### Cleanup

The live write test first deletes leftovers whose names start with
`vngcloud-live-`: ACLs (after disassociating their subnets), route tables,
subnets, VPCs, then server groups. It registers `t.Cleanup` as soon as
each ID is known, and deletes in that order with its own context,
asserting none remain. If a create fails, it lists by exact name and
deletes a match. It never touches a resource without the prefix, a main
route table, or a default ACL. A VPC delete refused with `ErrInUse` after
its subnets are gone is retried every 30 seconds for up to 20 minutes,
since the server holds deleted subnets for minutes. Private DNS takes
about 6 minutes and that hold about 11, so the live target's timeout is
at least 40 minutes.

## Security review

- Every write gets an adversarial review before its release. The review
  checks: no create resend after a 5xx; the Private DNS `PATCH` sent at
  most once; path ID checks on every call, reads included; each guard
  sends nothing; read-merge never drops a route, rule, or subnet the
  caller did not name; default rules are never sent changed; associate
  never sends a subnet outside the ACL's VPC; a 500 becomes `NotFound` or
  a delete success only through the list confirm; `--yes` on every
  command the design's CLI table marks; read-only refusal of every write.
- Route, rule, and association writes change who can reach what. The wiki
  shows them on a test VPC first, and explains each `--yes`.
- VPC names, CIDRs, targets, and IDs are account data. Fixtures use
  `<id>`, `<name>`, `<cidr>`, and `<ip>`.
