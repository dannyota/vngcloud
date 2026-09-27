# vServer Network Writes Design

Status: Accepted (2026-09-27).

This design adds the free vServer writes the
[free writes survey](free-writes-survey.md) picked: server groups in
`compute`, and VPCs, subnets, VPC Private DNS enable, route tables and
routes, and network ACLs in `network`. They ship as four releases, server
groups first.

It follows [vServer free writes](vserver-writes.md) for the gateway,
create retries, delete guards, and waits, and
[ADR 0002](../adr/0002-write-api-conventions.md) for every write. Tests,
cost probes, and live checks are in
[vServer network writes: checks](vserver-network-writes-checks.md).

## Source

The calls, bodies, responses, server rules, and cost evidence are in
[vServer network writes: API](vserver-network-writes-api.md). They come
from public sources only, and every shape is inferred until its live
check passes.

## Non-goals

- Tags and `zoneId` on writes other than VPC create.
- Secondary subnets, DHCP options sets, virtual IPs, peering, and
  interconnects.
- Disabling Private DNS: the API has no call for it.
- Associating a subnet with a route table: the reference has no call for
  it.
- A routes or rules replace that takes a whole list from the caller; see
  [Owner decisions](#owner-decisions).
- Writes to resources that OpenTofu manages. The wiki warns about drift.

## Server groups

All methods live in `compute`, next to the existing server group reads.

| Operation | Input | Output |
|-|-|-|
| `GetServerGroup` | `ServerGroupID` (r) | `{ServerGroup}` |
| `CreateServerGroup` | `Name` (r), `PolicyID` (r), `Description` | `{ServerGroup}` |
| `UpdateServerGroup` | `ServerGroupID` (r), `Name` *string, `Description` *string | `{ServerGroup}` |
| `DeleteServerGroup` | `ServerGroupID` (r) | `{}` |

"(r)" marks `vngcloud:"required"`.

- Create sends `name`, `policyId`, and `description`. The SDK checks only
  that `PolicyID` is present and a path-safe ID; the server checks it
  exists. The wiki points to `list-server-group-policies`.
- Update is the read-merge of security group update: an update with no
  non-nil field is `ErrInvalidInput` with nothing sent; otherwise the SDK
  reads the group, applies the non-nil fields, and sends `name` and
  `description`. The Output is a read after the write.
- Delete lists server groups, finds the ID, and returns
  `ErrServerGroupInUse`, sending nothing, when it has servers. The get
  response has no `servers`, so the list is the only source. An API error
  whose message contains `server group is in use` (case-insensitive) also
  wraps `ErrServerGroupInUse`, whatever its status.
- No wait: create returns the mapped response.

```go
var ErrServerGroupInUse = errors.New("compute: server group in use")
```

## VPCs and subnets

All methods live in `network`.

| Operation | Input | Output |
|-|-|-|
| `CreateVPC` | `Name` (r), `CIDR` (r), `ZoneID`, `NoWait` | `{VPC}` |
| `UpdateVPC` | `VPCID` (r), `Name` (r) | `{VPC}` |
| `DeleteVPC` | `VPCID` (r), `NoWait` | `{}` |
| `EnableVPCPrivateDNS` | `VPCID` (r), `NoWait` | `{VPC; Changed bool}` |
| `CreateSubnet` | `VPCID` (r), `Name` (r), `CIDR` (r), `NoWait` | `{Subnet}` |
| `UpdateSubnet` | `VPCID` (r), `SubnetID` (r), `Name` (r) | `{Subnet}` |
| `DeleteSubnet` | `VPCID` (r), `SubnetID` (r), `NoWait` | `{}` |
| `ListServersBySubnet` | `SubnetID` (r) | `Items []compute.Server` |

- `CIDR` must parse with `netip.ParsePrefix`, be IPv4, and have no host
  bits (`10.20.1.0/16` is refused). Prefix length and the private blocks
  stay on the server (ADR 0002 rule 5). The SDK does not check that a
  subnet lies inside its VPC; the server does.
- `ZoneID` is sent only when set; without it the server picks the zone,
  which the live check records.
- `UpdateVPC` and `UpdateSubnet` send only `name`, so they need no
  pointer. Each `PATCH` is marked idempotent: sending the same name twice
  is harmless. The Output is a read after the write.
- `UpdateSubnet` reads the subnet first and refuses one that has secondary
  subnets, with `ErrInvalidInput`, sending nothing: the body has a
  `secondarySubnetRequests` list, and whether leaving it out drops them is
  unknown.

### VPC delete guard

`DeleteVPC` reads first and sends nothing when:

- `GetVPC` shows `ServerCount` or `VolumeCount` above 0, or
  `ListSubnetsByVPC` returns any subnet a user made: `ErrInUse`. The
  message names the count and says to delete subnets first. Subnets that
  Private DNS reserved do not count, if the live check shows they appear
  in the list and can be told apart; otherwise the design is amended.
- The live read shows a default or system marker on the VPC:
  `ErrDefaultResource`.

The VPC's ACLs and route tables go with it; the wiki and the `--yes`
help say so. The server's refusal is the final guard.

### Subnet delete guard

`DeleteSubnet` sends nothing and returns `ErrInUse` when
`ListServersBySubnet`, `ListNetworkInterfaces`, or `ListVirtualIPAddresses`
shows any item in the subnet. The last two filter by subnet ID in the SDK.
The server's refusal is the final guard.

### Private DNS enable

`EnableVPCPrivateDNS` sets the target state once, in the manner of
[ADR 0003](../adr/0003-toggle-writes.md), because the call is one-way and
its effect is slow:

1. Read the VPC. `ENABLED`: return with `Changed` false, sending nothing.
   `ENABLING`: send nothing and wait. `DISABLED`: go on. Any other value:
   `ErrUnexpectedStatus`, sending nothing.
2. Send the `PATCH` with `Once`: no retry after any status or error.
3. Wait for `dnsStatus` `ENABLED` (see [Waits](#waits)). `Changed` is true.

A failure after the send that is not a 4xx returns `ErrNotSettled`: the
recovery is to run the same call again, since it reads first. Enabling
changes the VPC's DHCP options; servers pick up the new resolver only
after a DHCP renew, which the wiki says. The SDK never enables Private DNS
as a side effect.

This replaces decision 8 of [vDNS](dns.md#owner-decisions) once this
design is accepted; vDNS then links here instead of naming a console step.

## Route tables and routes

All methods live in `network`.

| Operation | Input | Output |
|-|-|-|
| `GetRouteTable` | `RouteTableID` (r) | `{RouteTable}` |
| `CreateRouteTable` | `VPCID` (r), `Name` (r), `NoWait` | `{RouteTable}` |
| `DeleteRouteTable` | `RouteTableID` (r), `NoWait` | `{}` |
| `AddRoute` | `RouteTableID` (r), `DestinationCIDR` (r), `Target` (r), `NoWait` | `{RouteTable; Changed bool}` |
| `RemoveRoute` | `RouteTableID` (r), `DestinationCIDR` (r), `NoWait` | `{RouteTable; Changed bool}` |

- `DestinationCIDR` must parse as a prefix with no host bits. `Target`
  must parse with `netip.ParseAddr`; the docs show an address. Whether
  the server needs the target to be a live interface is a live check.
- `DeleteRouteTable` sends nothing and returns `ErrDefaultResource` for
  the VPC's main route table (`VPC.RouteTableID`), and `ErrInUse` when any
  subnet of the VPC names the table.

### Routes replace

`PUT .../routes` replaces the whole list, so `AddRoute` and `RemoveRoute`
are read-merge writes:

1. Read the table. If its status is not `ACTIVE`, poll until it is, within
   the pre-write bound; past it, `ErrBusy`, nothing sent.
2. Build the list from the user routes read (`destinationCidrBlock` and
   `target` only), then apply the change. Which `routingType` marks a
   user route, and whether system routes must be resent, are live checks;
   the rule is to send only what the server lets a user change.
3. Compare, then send or stop:
   - Add, route already present with the same target: `Changed` false,
     nothing sent. Same destination, other target: `ErrInvalidInput`
     naming the current target, nothing sent. Remove the old route first.
   - Remove, no route with that destination: `NotFound`, nothing sent.
4. Send the list, then wait for `ACTIVE`, and confirm that the user routes
   read equal the list sent. A mismatch is `ErrNotSettled`, whose message
   says another writer may have changed the table.

The API has no condition field, so two writers can lose an update between
steps 1 and 4; the confirm read in step 4 reports it. A rerun is safe:
each operation reads first. The `PUT` keeps the transport's retries,
since resending the same list is idempotent.

## Network ACLs

All methods live in `network`. The existing `ACL` model gains
`DefaultACL` (`defaultAcl`), `VPCID` (`interfaceNetworkUuid`), `Rules`
(`aclPolicyRules`), and `SubnetIDs` (`subnetAssociationList`); a new
`ACLRule` model holds `UUID`, `Direction` (`type`), `Priority`
(`seqNumber`), `Protocol`, `Port`, `CIDR` (`source`), and `Action`. The
fields the list fixture has today stay, so nothing breaks.

| Operation | Input | Output |
|-|-|-|
| `GetNetworkACL` | `NetworkACLID` (r) | `{ACL}` |
| `CreateNetworkACL` | `VPCID` (r), `Name` (r) | `{ACL}` |
| `DeleteNetworkACL` | `NetworkACLID` (r) | `{}` |
| `AddNetworkACLRule` | `NetworkACLID` (r), `Direction` (r), `Priority` (r), `Protocol` (r), `PortRangeMin`, `PortRangeMax`, `CIDR` (r), `Action` (r) | `{ACL; Changed bool}` |
| `RemoveNetworkACLRule` | `NetworkACLID` (r), `Direction` (r), `Priority` (r) | `{ACL; Changed bool}` |
| `AssociateNetworkACLSubnet` | `NetworkACLID` (r), `SubnetID` (r) | `{ACL; Changed bool}` |
| `DisassociateNetworkACLSubnet` | `NetworkACLID` (r), `SubnetID` (r) | `{ACL; Changed bool}` |

Shape checks, all `ErrInvalidInput` before any request: `CIDR` parses as a
prefix with no host bits; `Priority` is at least 1; ports are 0 to 65535
with `PortRangeMax` 0 meaning equal to `PortRangeMin`, and min not above
max. `Direction`, `Protocol`, and `Action` go to the server as given
(ADR 0002 rule 5). The SDK never defaults `Action`, `Protocol`, or `CIDR`.
How `port` encodes a range, all ports, and ICMP is a live check; until
then the wiki shows `TCP` and `UDP` rules with explicit ports.

### Rules replace

`PUT .../rules` replaces the whole list. Add and remove follow the steps of
[Routes replace](#routes-replace), with these differences:

- A rule is keyed by direction (case-insensitive) and priority. Add with
  the same key and the same fields: `Changed` false. Same key, other
  fields: `ErrInvalidInput`, nothing sent.
- Default rules never change. `RemoveNetworkACLRule` on a default rule
  returns `ErrDefaultResource`, nothing sent. The list sent leaves default
  rules out when the live check shows the server keeps them; otherwise it
  resends each exactly as read. The marker that identifies a default rule
  (a `system` field or a fixed priority) comes from the live check, and
  the design is amended with it before code.
- The confirm read checks the user rules and that every default rule is
  unchanged.

### Subnet association

`PUT .../subnets` replaces the ACL's subnet list. Associate and
disassociate read the ACL, apply the change to `SubnetIDs`, send the whole
list, and confirm by reading. Before sending, associate reads the subnet
with `GetSubnet` under the ACL's VPC; a 404 there is `NotFound`, so a
subnet of another VPC is never sent. Associate of a subnet already in the
list, or disassociate of one not in it, returns `Changed` false and sends
nothing.

Association is the call that can cut traffic: the ACL's rules apply to the
subnet at once, and a new ACL denies everything its rules do not allow.
Associating moves a subnet away from its current ACL; the wiki says so.
What a subnet falls back to after disassociate is a live check.

### ACL delete

`DeleteNetworkACL` reads first and sends nothing when the ACL has subnets
(`ErrInUse`) or is a default ACL (`ErrDefaultResource`).

## Waits

Per ADR 0002 rule 7, each asynchronous write waits unless `NoWait` is set.
The `network` poll helper takes its interval and bound per wait instead of
the fixed ones it has today, keeps the injected clock and sleep, and
honours `ctx`.

| Write | Settled | Failed | Poll | Bound |
|-|-|-|-|-|
| VPC, subnet, route table create | `ACTIVE` | `ERROR` | 2 s | 3 min |
| Routes, ACL rules, ACL subnets replace | `ACTIVE` and the confirm read | `ERROR` | 2 s | 60 s |
| VPC, subnet, route table delete | 404, or status `DELETED` | `ERROR` | 2 s | 3 min |
| Private DNS enable | `dnsStatus` `ENABLED` | none known | 10 s | 10 min |
| Pre-write, before a replace | `ACTIVE` | none | 2 s | 60 s |

A 404 during a create wait keeps polling. `ERROR` returns the Output and
an error wrapping `ErrFailed`. The bound returns the Output and an error
wrapping `ErrNotSettled`, whose message says the write was accepted and
must not be repeated (a create) or can be rerun (the read-first writes).
The live checks record real times; a bound changes only by amending this
table. ACL create and delete, and server group writes, have no wait
unless the live checks show one is needed.

## Identifiers and retries

- Every operation that puts an ID in a path checks it with
  `core.CheckPathID` before any request, including the reads this design
  uses: `GetVPC`, `GetSubnet`, and `ListSubnetsByVPC`, which send any
  value today.
- Creates are `POST` and are never resent after a 5xx or network error
  (ADR 0002 rule 2). The error names the list to check, matching the name
  exactly, because list filters may match substrings as security groups
  do: `list-server-groups --name`, `list-vpcs --name`,
  `list-subnets-by-vpc`, `list-route-tables --name`, or
  `list-network-acls --name`. Whether each name is unique is a live check;
  where it is not, the message also says a rerun can make a second one.
- Updates, replaces, and deletes keep the transport's retries, except the
  Private DNS `PATCH` (`Once`). A retried delete that finds the resource
  gone returns `NotFound`.
- The CLI never retries a write.

## CLI

| Command | Kind | `--yes` | Release |
|-|-|-|-|
| `compute get-server-group` | Read | No | N1 |
| `compute create-server-group`, `update-server-group` | Write | No | N1 |
| `compute delete-server-group` | Write, destructive | Yes | N1 |
| `network create-vpc`, `update-vpc`, `create-subnet`, `update-subnet` | Write | No | N2 |
| `network delete-vpc`, `delete-subnet` | Write, destructive | Yes | N2 |
| `network enable-vpc-private-dns` | Write, one-way | Yes | N2 |
| `network list-servers-by-subnet` | Read | No | N2 |
| `network get-route-table` | Read | No | N3 |
| `network create-route-table` | Write | No | N3 |
| `network delete-route-table` | Write, destructive | Yes | N3 |
| `network add-route`, `remove-route` | Write, changes traffic | Yes | N3 |
| `network get-network-acl` | Read | No | N4 |
| `network create-network-acl` | Write | No | N4 |
| `network delete-network-acl` | Write, destructive | Yes | N4 |
| `network add-network-acl-rule`, `remove-network-acl-rule` | Write, changes traffic | Yes | N4 |
| `network associate-network-acl-subnet`, `disassociate-network-acl-subnet` | Write, changes traffic | Yes | N4 |

- Private DNS enable needs `--yes` because no command undoes it
  (ADR 0002 rule 6).
- Route, rule, and association writes need `--yes` every time: each can
  redirect or cut traffic for every server in a subnet, and the CLI cannot
  tell cheaply whether the table or ACL is in use.
- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- Every field is a scalar flag. The rename table gains
  `NetworkACLID` as `network-acl-id`, since `kebab` would give
  `network-aclid`.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, bad CIDR, address, port, or priority, empty update, conflicting route or rule, subnet with secondary subnets | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Missing `--yes` | No request | `InvalidUsage`, 2 |
| Unknown resource; route, rule, or subnet not found | `NotFound` | `NotFound`, 4 |
| Server group with servers | `compute.ErrServerGroupInUse` | `ServerGroupInUse`, 1 |
| VPC, subnet, route table, or ACL in use | `ErrInUse` | `ResourceInUse`, 1 |
| Main route table, default ACL, default rule, default VPC | `ErrDefaultResource`, no request | `DefaultResource`, 1 |
| Not `ACTIVE` within the pre-write bound | `ErrBusy`, no request | `ResourceBusy`, 1 |
| Unknown `dnsStatus` before enable | `ErrUnexpectedStatus`, no request | `UnexpectedStatus`, 1 |
| `ERROR` after a write | `ErrFailed`, with Output | `WriteFailed`, 1 |
| Bound reached, or confirm read differs | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| Duplicate name, quota, CIDR overlap, bad policy | The server's `*APIError` | 1 |
| 5xx or network error on a create | The error; the message names the list | 1 |

New sentinels in `network`: `ErrInUse`, `ErrDefaultResource`, `ErrBusy`,
and `ErrUnexpectedStatus`. `ErrSecurityGroupInUse` and `ErrSystemGroup`
keep their meaning. The CLI list in
[CLI](cli.md#errors-and-exit-codes) gains `ServerGroupInUse`,
`ResourceInUse`, `DefaultResource`, and `ResourceBusy`; `UnexpectedStatus`,
`WriteFailed`, and `NotSettled` keep their meaning.

## Security

- Every write gets an adversarial review before its release. The review
  checks: no create resend after a 5xx; the Private DNS `PATCH` sent at
  most once; path ID checks on every call, reads included; each guard
  sends nothing; read-merge never drops a route, rule, or subnet the
  caller did not name; default rules are never sent changed; associate
  never sends a subnet outside the ACL's VPC; `--yes` on every command the
  table marks; read-only refusal of every write.
- Route, rule, and association writes change who can reach what. The wiki
  shows them on a test VPC first, and explains each `--yes`.
- VPC names, CIDRs, targets, and IDs are account data. Fixtures use
  `<id>`, `<name>`, `<cidr>`, and `<ip>`.

## Releases

| Release | Content |
|-|-|
| N1 | `compute` `GetServerGroup`, `CreateServerGroup`, `UpdateServerGroup`, `DeleteServerGroup`, `ErrServerGroupInUse`; CLI commands |
| N2 | `network` VPC and subnet writes, `EnableVPCPrivateDNS`, `ListServersBySubnet`, the per-wait poll bounds, `ErrInUse`, `ErrDefaultResource`, `ErrUnexpectedStatus`, path ID checks on `GetVPC`, `GetSubnet`, and `ListSubnetsByVPC`; CLI commands |
| N3 | `network` `GetRouteTable`, route table create and delete, `AddRoute`, `RemoveRoute`, `ErrBusy`; CLI commands |
| N4 | `network` `GetNetworkACL`, ACL create and delete, rule add and remove, subnet associate and disassociate, the `ACL` fields and `ACLRule`; CLI commands |

Each is numbered when it ships, in this order: N3 and N4 need N2's VPC for
their live tests. No release breaks callers; the reads above only start
rejecting a malformed ID. `network.go` is near the 700-line limit, so each
release adds its own files. The `Compute` and `Network` wiki pages gain
the writes, the retry advice, the `--yes` reasons, and the OpenTofu drift
warning. Each release's cost probe and live checks pass before its code
merges.

## Owner decisions

The owner approved every recommendation below on 2026-09-27.

1. Release split. Options: four releases N1 to N4 in the order above; one
   release. Recommend four, as the survey approved.
2. VPC delete with children. Options: refuse while user subnets, servers,
   or volumes remain, and let ACLs and route tables go with the VPC;
   also refuse while any ACL or custom route table remains; cascade
   everything behind `--yes`. Recommend the first: subnets carry
   workloads, and the others hold no traffic once the subnets are gone.
3. Private DNS enable. Options: ship `EnableVPCPrivateDNS` with `--yes`
   and replace vDNS decision 8; keep the console step. Recommend ship, as
   the survey approved; it lets vDNS live tests use their own VPC.
4. Full-list writes. Options: add and remove only (read-merge); also a
   replace that takes the whole routes or rules list. Recommend add and
   remove only: an empty list from a script would wipe a table, and a
   rerun of add or remove is safe.
5. `--yes` on route, rule, and association writes. Options: always; only
   when the table or ACL is in use, which needs reads in the CLI guard.
   Recommend always.
6. Subnet rename with secondary subnets. Options: refuse; resend them as
   read. Recommend refuse until a live check shows the body's effect.
7. `ZoneID` on VPC create. Options: optional input; never sent. Recommend
   optional: a VPC is zonal, and the server's default zone may not be the
   one the servers use.
8. Route table create with routes. Options: no routes on create; accept
   them. Recommend none: `add-route` covers it with one code path.
9. New error codes. Recommend the four codes in [Errors](#errors) rather
   than reusing `SecurityGroupInUse` or `SystemSecurityGroup`, whose names
   would mislead.
10. Cost probes. Options: one session before N1 that probes all four
    picks, sharing one probe VPC; one probe per release. Recommend one
    session, since three picks need a VPC, with the next-day bill read per
    service line.

## Open questions

- Whether the IAM gateway accepts each write.
- The default or system markers on VPCs, route tables, routes, ACLs, and
  ACL rules, and whether Private DNS subnets appear in the subnet list.
- Which routes and rules the replace calls must resend.
- The `port` encoding for ACL rules, and the `type` and `action` values.
- What a subnet uses after ACL disassociate.
- Whether names and VPC CIDRs must be unique, and each create's status.
- Real wait times.
