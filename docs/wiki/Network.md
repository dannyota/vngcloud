# Network

`network` is `danny.vn/vngcloud/network`, with its own `New(cfg)`. It reads
VPCs, subnets, WAN IPs, interfaces, virtual IPs, route tables, peerings,
ACLs, interconnects, and endpoints; see the [Network section of
Services](Services.md#network) for that full read list. This page covers
security groups and their rules, route tables and routes, and network ACLs,
their rules, and subnet associations. Servers, volumes, and floating IPs
stay read-only.

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := network.New(cfg)
	_ = ctx
	_ = client
}
```

The rest of this page assumes `cfg` and `ctx` from this setup, plus
`client := network.New(cfg)`.

## Reading groups and rules

```go
groups, err := client.ListSecurityGroups(ctx, nil)              // Name, Page, Size
group, err := client.GetSecurityGroup(ctx, in)                  // SecurityGroupID (required)
servers, err := client.ListServersBySecurityGroup(ctx, in)      // SecurityGroupID (required)
rules, err := client.ListSecurityGroupRules(ctx, in)             // SecurityGroupID (required)
allRules, err := client.ListAllSecurityGroupRules(ctx, nil)
```

`SecurityGroup.System` is `true` for a project's default group, which
allows all outbound traffic and inbound SSH, RDP, HTTP, HTTPS, and ICMP
from anywhere. `ListSecurityGroups`' own `Name` filter matches by
substring, not exactly: a search for `"web"` also finds `"webhook"`. Any
code that must find one group by name lists and scans for an exact match
itself.

## Creating, updating, and deleting groups

```go
created, err := client.CreateSecurityGroup(ctx, &network.CreateSecurityGroupInput{
	Name:        "web",
	Description: "web tier",
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.SecurityGroup.ID)

updated, err := client.UpdateSecurityGroup(ctx, &network.UpdateSecurityGroupInput{
	SecurityGroupID: created.SecurityGroup.ID,
	Description:     vngcloud.Ptr("web tier, edge servers"),
})
if err != nil {
	log.Fatal(err)
}
log.Println(updated.SecurityGroup.Description)

if _, err := client.DeleteSecurityGroup(ctx, &network.DeleteSecurityGroupInput{
	SecurityGroupID: created.SecurityGroup.ID,
}); err != nil {
	log.Fatal(err)
}
```

A new group holds the server's default egress rules (allow all outbound
traffic) and no ingress rule, so it admits no inbound traffic until a rule
allows it. Group names are unique per project; a repeat name fails with the
server's own message.

`CreateSecurityGroup` is a `POST` and is never retried after a failure that
may already have reached the server: after any error that is not a 4xx
`*vngcloud.APIError` or `vngcloud.ErrInvalidInput`, the group may exist.
List security groups and match the name exactly (the list's own filter
matches by substring) before creating it again, rather than retrying blind.
Without `NoWait`, it then waits for the group to reach `ACTIVE`; see
[Waits](#waits) below.

`UpdateSecurityGroup` changes `Name`, `Description`, or both; at least one
must be set, or the call fails with `vngcloud.ErrInvalidInput` and sends
nothing. The API takes a full replacement body, so the SDK reads the group
first and resends whichever field the caller left `nil` unchanged. It
refuses a system group with `network.ErrSystemGroup`, before sending
anything: renaming or redescribing a project's default group is never
intended.

`DeleteSecurityGroup` reads the group first too, and sends nothing for a
system group (`network.ErrSystemGroup`) or one with any server attached
(`network.ErrSecurityGroupInUse`, found with `ListServersBySecurityGroup`).
A group can be in use by more than servers, so the server's own refusal is
the final guard: an error whose message contains `"securitygroupinuse"`
also wraps `network.ErrSecurityGroupInUse`, whatever its HTTP status.
`DELETE` is idempotent; a retry that finds the group already gone returns
`vngcloud.IsNotFound(err) == true`.

If a group is managed by OpenTofu or Terraform, a write made here drifts
from that state; keep such a group's writes in its own tool.

## Creating and deleting rules

```go
rule, err := client.CreateSecurityGroupRule(ctx, &network.CreateSecurityGroupRuleInput{
	SecurityGroupID: groupID,
	Direction:       "ingress",
	Protocol:        "tcp",
	RemoteIPPrefix:  "203.0.113.0/24",
	PortRangeMin:    443,
})
if err != nil {
	log.Fatal(err)
}
log.Println(rule.SecurityGroupRule.ID)

if _, err := client.DeleteSecurityGroupRule(ctx, &network.DeleteSecurityGroupRuleInput{
	SecurityGroupID:     groupID,
	SecurityGroupRuleID: rule.SecurityGroupRule.ID,
}); err != nil {
	log.Fatal(err)
}
```

There is no rule update; the API only changes a rule's description, so a
change is a `DeleteSecurityGroupRule` followed by a `CreateSecurityGroupRule`.

`RemoteIPPrefix` is required and must parse as a CIDR prefix
(`net/netip.ParsePrefix`): a bare address with no prefix length is
refused, so a single host is written `/32` or `/128`. `EtherType` left
empty is derived from `RemoteIPPrefix`'s address family (`"IPv4"` or
`"IPv6"`); given explicitly, it must match that family. `PortRangeMin` and
`PortRangeMax` each range 0 to 65535; `PortRangeMax` left 0 sends the same
value as `PortRangeMin`, so a single port needs only `PortRangeMin`, and
`PortRangeMin` must not be above `PortRangeMax`. For `Protocol` `"tcp"` or
`"udp"` (in any case), `PortRangeMin` must be at least 1: 0 is not a valid
tcp or udp port, and "all ports" is written `PortRangeMin: 1,
PortRangeMax: 65535`. For `Protocol` `"icmp"` (in any case), `PortRangeMin`
and `PortRangeMax` must both stay 0: a live check showed icmp stored with
a port range of 0/0 and its type/code encoding is not confirmed, so the
SDK fails closed and an icmp rule covers all ICMP types and codes.

The SDK never defaults `Protocol` or `RemoteIPPrefix`; both must be set,
and `RemoteIPPrefix` is never defaulted to `0.0.0.0/0`. Opening a port to
the world must be written out:

```go
// Open to every scanner on the internet while this rule exists.
client.CreateSecurityGroupRule(ctx, &network.CreateSecurityGroupRuleInput{
	SecurityGroupID: groupID,
	Direction:       "ingress",
	Protocol:        "tcp",
	RemoteIPPrefix:  "0.0.0.0/0",
	PortRangeMin:    443,
})
```

`Direction` must be exactly `"ingress"` or `"egress"`, the lowercase
strings the API itself stores; anything else, including different case or
surrounding whitespace, is refused with `vngcloud.ErrInvalidInput` before
any request. `Protocol` is sent to the server exactly as given and checked
only for shape, not against a fixed value set, so a protocol the server
adds later never needs an SDK release. A duplicate rule fails with the
server's own `SecurityGroupRuleExists` message.

`CreateSecurityGroupRule` is a `POST` and is never retried after an
ambiguous failure, for the same reason `CreateSecurityGroup` is not; list
the group's rules with `ListSecurityGroupRules` before creating it again.

`DeleteSecurityGroupRule` lists the group's rules first and returns
`vngcloud.IsNotFound(err) == true`, sending nothing, when no rule in that
list has the given `SecurityGroupRuleID`: some servers accept any group id
in this path and silently ignore it, so without this check a wrong
`SecurityGroupID` could delete a rule that belongs to a different group.
`DELETE` is otherwise idempotent, and a retry that finds the rule already
gone returns the same not-found result.

## Waits

Confirmed live, both `CreateSecurityGroup` and `CreateSecurityGroupRule`
return a group or rule that is already `"ACTIVE"`; neither write is
actually asynchronous. `CreateSecurityGroup` still runs its post-create
wait: without `NoWait`, it polls `GetSecurityGroup` every 2 seconds for up
to 60 seconds of elapsed time, tolerating a 404 (a group just created may
not be readable at once), until the group reaches `"ACTIVE"`, which in
practice settles on that wait's first read. `UpdateSecurityGroup` sends one
confirm read after its `PUT`; `DeleteSecurityGroup` sends no read after its
`DELETE`; rule writes take no wait at all.

If the group reaches `"ERROR"`, `CreateSecurityGroup` returns an error
wrapping `network.ErrFailed`. Once the bound runs out, or a read or the
wait's own sleep fails, such as from a canceled `ctx`, it returns an error
wrapping `network.ErrNotSettled`: the group may have been created and the
SDK could not confirm it, so do not send the same create again. Either way
the returned Output still holds the last group a read returned, or, if
none did, the one the create response itself carried, so the caller keeps
its id to check on it later:

```go
created, err := client.CreateSecurityGroup(ctx, in)
switch {
case errors.Is(err, network.ErrFailed):
	log.Printf("group %s failed: %s", created.SecurityGroup.ID, created.SecurityGroup.Status)
case errors.Is(err, network.ErrNotSettled):
	log.Printf("group %s may exist; check it later, do not retry", created.SecurityGroup.ID)
case err != nil:
	log.Fatal(err)
}
```

With `NoWait` set, `CreateSecurityGroup` returns the mapped create response
at once, without polling.

`UpdateSecurityGroup` reads the group once more after its `PUT` succeeds,
to build the Output from a shape the SDK trusts rather than the `PUT`
response itself. If that confirm read fails, the write has already
succeeded: the error wraps `network.ErrNotSettled`, and the Output falls
back to the fields the `PUT` itself sent.

## Errors

```go
var ErrSecurityGroupInUse = errors.New("network: security group in use")
var ErrSystemGroup        = errors.New("network: system security group")
var ErrNotSettled         = errors.New("network: write accepted but not settled")
var ErrFailed             = errors.New("network: write failed on the server")
```

`ErrSystemGroup` and `ErrSecurityGroupInUse` mean a delete or update sent
nothing, or that a delete's own `DELETE` request was refused by the
server; see [Creating, updating, and deleting groups](#creating-updating-and-deleting-groups)
above. `ErrFailed` means `CreateSecurityGroup` reached `"ERROR"`.
`ErrNotSettled` means `CreateSecurityGroup` or `UpdateSecurityGroup` was
sent and may have reached the server, but no confirming read followed; a
create must not be sent again with the same input, while an update's `PUT`
is idempotent and may be repeated. See [Waits](#waits) above for why the
Output still holds the group.

## Route tables and routes

```go
table, err := client.CreateRouteTable(ctx, &network.CreateRouteTableInput{
	VPCID: vpcID,
	Name:  "public",
})
if err != nil {
	log.Fatal(err)
}
log.Println(table.RouteTable.UUID)

added, err := client.AddRoute(ctx, &network.AddRouteInput{
	RouteTableID:    table.RouteTable.UUID,
	DestinationCIDR: "10.251.200.0/24",
	Target:          "10.251.200.10",
})
if err != nil {
	log.Fatal(err)
}
log.Println(added.Changed)

if _, err := client.RemoveRoute(ctx, &network.RemoveRouteInput{
	RouteTableID:    table.RouteTable.UUID,
	DestinationCIDR: "10.251.200.0/24",
}); err != nil {
	log.Fatal(err)
}

if _, err := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{
	RouteTableID: table.RouteTable.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`CreateRouteTable` makes an empty table in a VPC; a route table is never
created with routes, so add one afterward with `AddRoute`. It is a `POST`
and is never retried after an ambiguous failure, for the reason
`CreateSecurityGroup` is not; list route tables with `ListRouteTables` and
match the name exactly (the list's own filter may match by substring)
before creating it again. Without `NoWait`, it waits for the table to reach
`"ACTIVE"`, confirmed live at about 5 seconds.

A VPC created with no main route table gets one assigned automatically: the
first route table ever created in it becomes its main table
(`network.GetVPCOutput.VPC.RouteTableID`), confirmed live. A subnet with no
route table of its own (an empty `routeTableUuid`) relies on that main
table.

`DeleteRouteTable` reads the table, its VPC, and the VPC's subnets first.
It sends nothing and returns `network.ErrInUse` when a subnet still names
the table, and it sends nothing and returns `network.ErrDefaultResource`
when the table is the VPC's main table and some subnet relies on it, since
deleting it would leave that subnet with no route table at all. A main
table with no subnet relying on it, including one with no subnets in its
VPC, deletes normally: the server clears `VPC.RouteTableID` back to `""`.
Deleting a VPC deletes its route tables too. `DELETE` is asynchronous,
confirmed live at 202 then a 404 about 5 seconds later; without `NoWait`,
`DeleteRouteTable` waits for that 404.

The API replaces a route table's whole route list on every write, so
`AddRoute` and `RemoveRoute` are read-merge writes: each reads the table's
current routes, waits for the table to be `"ACTIVE"` first
(`network.ErrBusy`, nothing sent, past a 60-second bound), then sends back
every route it read plus one change. Neither ever takes a caller-supplied
whole list, since an empty one from a script could wipe a table.
Immediately before sending that write, each also re-reads the table and
refuses with `network.ErrBusy`, again sending nothing, if the routes no
longer match the first read: some other writer changed the table in
between. This narrows the race between the read and the write, but does
not close it: a writer that changes the table between that final read and
the moment the `PUT` reaches the server can still be overwritten by it.

A destination is compared as a parsed CIDR prefix, so equivalent spellings
of the same prefix, such as `2001:DB8::/32` and `2001:0db8::/32`, are the
same route. `AddRoute` of a route already present with the same `Target` is
a no-op: `Changed` is `false` and nothing is sent. One present with a
different `Target` fails with `vngcloud.ErrInvalidInput` naming that
target; remove the old route first. `RemoveRoute` of a destination with no
matching route returns `vngcloud.IsNotFound(err) == true`, sending nothing;
one matching more than one route fails with `vngcloud.ErrInvalidInput`
naming the count, sending nothing, rather than guessing which to drop.
`Target` must be an IP address with no zone; whether the server requires it
to belong to a live interface is not yet confirmed live.

Without `NoWait`, both wait for the table to return to `"ACTIVE"` after
their `PUT`, then confirm that a fresh read names exactly the routes just
sent. Either wait failing, or the confirm read not matching, returns an
error wrapping `network.ErrNotSettled`. The transport itself may still
retry the `PUT` request on its own after a transient failure such as a
5xx, since `PUT` is idempotent; what never happens is `AddRoute` or
`RemoveRoute` resending a previous call's already-computed route list.
Running the same call again simply reads the table fresh and starts over.

Which `routingType` marks a route the server manages outside a caller's
control, if any, is not yet confirmed live. Until that is known, a replace
resends every route this SDK read, so it never silently drops one the
caller did not name.

## Network ACLs

See [Network ACLs](Network-ACLs.md) for `GetNetworkACL`, `CreateNetworkACL`,
`DeleteNetworkACL`, `AddNetworkACLRule`, `RemoveNetworkACLRule`,
`AssociateNetworkACLSubnet`, and `DisassociateNetworkACLSubnet`.
