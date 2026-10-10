# Network

`network` is `danny.vn/vngcloud/network`, with its own `New(cfg)`. It reads
VPCs, subnets, WAN IPs, interfaces, virtual IPs, route tables, peerings,
ACLs, interconnects, and endpoints; see [Services](Services.md#network) for
that full read list. This page covers security groups and their rules, VPCs,
subnets, and Private DNS, the network resources this SDK writes here. See
[Network Route Tables](Network-RouteTables.md) for route tables and routes,
[Network ACLs](Network-ACLs.md) for ACLs, their rules, and subnet associations,
[Network DHCP Options](Network-DHCPOptions.md) for DHCP options sets, and
[Network Virtual IPs](Network-VirtualIPs.md) for virtual IPs. Servers,
volumes, and floating IPs stay read-only.

See [Network NAT](Network-NAT.md) for Public NAT inventory.

If a VPC, subnet, route table, ACL, or security group is managed by
OpenTofu or Terraform, a write made here drifts from that state; keep such
a resource's writes in its own tool.

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

## Creating, renaming, and deleting VPCs

```go
created, err := client.CreateVPC(ctx, &network.CreateVPCInput{
	Name: "prod",
	CIDR: "10.20.0.0/16",
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.VPC.UUID)

renamed, err := client.UpdateVPC(ctx, &network.UpdateVPCInput{
	VPCID: created.VPC.UUID,
	Name:  "prod-vpc",
})
if err != nil {
	log.Fatal(err)
}
log.Println(renamed.VPC.Name)

if _, err := client.DeleteVPC(ctx, &network.DeleteVPCInput{
	VPCID: created.VPC.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`CIDR` must parse with `net/netip.ParsePrefix`, be IPv4, and have no host
bits: `10.20.1.0/16` is refused because bits beyond the prefix length are
set. The exact prefix length and which blocks are private stay on the
server, and so does overlap: a `CIDR` that overlaps any VPC already in the
project is refused with 400 "VPC is overlap with another." `CreateVPC`
never sends `zoneId`: live, the server ignores it and places every VPC in
the region's first zone, even one disabled for the account, so an input
the server ignores would tell the caller it chose a zone when it did not.
A subnet's own `ZoneID` is what matters.

`CreateVPC` is a `POST` and is never retried after an ambiguous failure,
for the same reason `CreateSecurityGroup` is not; list VPCs and match the
name exactly before creating it again. Without `NoWait`, it then waits for
`ACTIVE`; see [Waits](#waits) below.

`UpdateVPC` sends only `Name`; the API replaces the whole name on every
`PATCH`, which is marked idempotent and keeps the transport's normal
retries.

`DeleteVPC` deletes a VPC and its ACLs and route tables. It reads the VPC
and its subnets first and sends nothing when any server, volume, or subnet
is still attached (`network.ErrInUse`): subnets carry workloads and must be
deleted first, and ACLs and route tables hold no traffic once the subnets
are gone, so the server removes them along with the VPC. The server's own
refusal is the final guard: it keeps refusing for minutes after a subnet's
delete leaves the VPC's subnet list, so a `network.ErrInUse` here also
means "a rerun once that window passes is safe". `DELETE` is idempotent.
Without `NoWait`, it then waits for a 404; see [Waits](#waits) below.

## Creating, renaming, and deleting subnets

```go
created, err := client.CreateSubnet(ctx, &network.CreateSubnetInput{
	VPCID:  vpcID,
	ZoneID: zoneID,
	Name:   "web",
	CIDR:   "10.20.1.0/24",
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.Subnet.UUID)

if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{
	VPCID:    vpcID,
	SubnetID: created.Subnet.UUID,
}); err != nil {
	log.Fatal(err)
}

servers, err := client.ListServersBySubnet(ctx, &network.ListServersBySubnetInput{
	SubnetID: created.Subnet.UUID,
})
```

`ZoneID` names a zone enabled for the account; the SDK picks no default,
since a guess would place the subnet, and any server later created in it,
in a zone the caller did not choose. `portal.ListZones` finds an enabled
zone. `CIDR` follows the same rule as a VPC's; the SDK does not check that
it lies inside the VPC's own CIDR, the server does.

`CreateSubnet` is a `POST` and is never retried after an ambiguous
failure. Subnets have no list filter by name, and live, subnet names can
repeat, so list the VPC's subnets with `ListSubnetsByVPC` and match by
CIDR instead: an overlapping CIDR is refused with a 400, so a CIDR is
unique within a VPC and rerunning this same create with the same CIDR
cannot make a second subnet. Without `NoWait`, it then waits for `ACTIVE`;
see [Waits](#waits) below.

`UpdateSubnet` renames a subnet. It reads the subnet first and refuses one
that has any `SecondarySubnets`, with `vngcloud.ErrInvalidInput`, sending
nothing: the rename body has no field for them, and whether omitting it
would drop them is not yet confirmed live.

`DeleteSubnet` returns `vngcloud.IsNotFound(err) == true`, sending nothing, for
a subnet read with status `"DELETED"`: `GetSubnet` keeps returning a deleted
subnet for minutes after `ListSubnetsByVPC` has already dropped it. It also
sends nothing and returns `network.ErrInUse` when `ListServersBySubnet`,
`ListNetworkInterfaces`, or `ListVirtualIPAddresses` shows any item in the
subnet, or when a [network ACL still associates the
subnet](Network-ACLs.md#subnet-associations): deleting a held subnet instead of
disassociating it first can leave that ACL stuck for good, so this check fails
closed on any error reading the account's ACLs. A repeat `DELETE` on an
already-deleted subnet returns a 500, so after a 5xx or network error on the
`DELETE`, `DeleteSubnet` lists the VPC's subnets: an absent subnet means the
delete took effect. Without `NoWait`, it then waits for the subnet to leave that
list; see [Waits](#waits) below. Wait 30 seconds after disassociating a subnet
from a network ACL before deleting it; see [Limitations](Limitations.md).

## Enabling Private DNS

```go
enabled, err := client.EnableVPCPrivateDNS(ctx, &network.EnableVPCPrivateDNSInput{
	VPCID: vpcID,
})
if err != nil {
	log.Fatal(err)
}
log.Println(enabled.Changed)
```

`EnableVPCPrivateDNS` drives a VPC's Private DNS to `ENABLED`. There is no
matching disable call: the API has no call for it, and the SDK never
enables Private DNS as a side effect of any other write. Enabling changes
the VPC's DHCP options; a server already running only picks up the new
resolver after a DHCP renew.

It reads the VPC first. `dnsStatus` `"ENABLED"` returns at once with
`Changed` false, sending nothing. `"ENABLING"` sends nothing and waits, since
an earlier call already started it. `"DISABLED"` sends the enable `PATCH`
at most once: a resend would act on a status read that only grows staler.
Any other `dnsStatus` fails closed with `network.ErrUnexpectedStatus`,
sending nothing.

A `PATCH` failure that is a 4xx `*vngcloud.APIError` proves the server
never acted and is returned as is. Any other failure wraps
`network.ErrNotSettled` instead; the recovery is to call
`EnableVPCPrivateDNS` again, since it always reads first. Without `NoWait`,
it then waits for `dnsStatus` `"ENABLED"`, which took over 5 minutes in the
probe; see [Waits](#waits) below.

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

`CreateVPC` and `CreateSubnet` poll every 2 seconds for up to 3 minutes of
elapsed time, tolerating a 404, until the resource reaches `"ACTIVE"`.
`DeleteVPC` polls every 2 seconds for up to 3 minutes for a 404.
`DeleteSubnet` polls every 2 seconds for up to 3 minutes for the subnet's
absence from `ListSubnetsByVPC`, not for a 404 from `GetSubnet`, which
stays stale for minutes after the delete. `EnableVPCPrivateDNS` polls every
10 seconds for up to 10 minutes for `dnsStatus` `"ENABLED"`.

If a VPC or subnet reaches `"ERROR"` instead, the create or delete returns
an error wrapping `network.ErrFailed`. Once any of these bounds runs out,
or a read or a sleep fails, the error wraps `network.ErrNotSettled`: a
create must not be repeated, while a delete or `EnableVPCPrivateDNS` reads
first and so may be rerun. A create's Output still holds the last resource
a read returned, or, if none did, the one the write's own response carried.
`DeleteVPC` and `DeleteSubnet`'s Output is always empty: neither returns
the resource it deleted.

## Errors

```go
var ErrSecurityGroupInUse = errors.New("network: security group in use")
var ErrSystemGroup        = errors.New("network: system security group")
var ErrInUse              = errors.New("network: resource in use")
var ErrUnexpectedStatus   = errors.New("network: unexpected status")
var ErrNotSettled         = errors.New("network: write accepted but not settled")
var ErrFailed             = errors.New("network: write failed on the server")
```

`ErrSystemGroup` means `UpdateSecurityGroup` or `DeleteSecurityGroup`
refused a project's default group before sending anything.
`ErrSecurityGroupInUse` means `DeleteSecurityGroup` sent
nothing, or that a delete's own `DELETE` request was refused by the server;
see [Creating, updating, and deleting
groups](#creating-updating-and-deleting-groups) above. `ErrInUse` means
`DeleteVPC` or `DeleteSubnet` sent nothing because a pre-write read showed
the resource still holds something, or that the server's own refusal named
it in use; see [Creating, renaming, and deleting
VPCs](#creating-renaming-and-deleting-vpcs) and [Creating, renaming, and
deleting subnets](#creating-renaming-and-deleting-subnets) above.
`DeleteRouteTable` and `DeleteNetworkACL` return the same `ErrInUse`,
alongside their own `ErrDefaultResource` and `ErrBusy`; see [Network Route
Tables](Network-RouteTables.md) and [Network ACLs](Network-ACLs.md) for
those. `ErrUnexpectedStatus` means `EnableVPCPrivateDNS` read a `dnsStatus`
this SDK does not know how to act on. `ErrFailed` means a create or delete
reached `"ERROR"`. `ErrNotSettled` means a write was sent, and may have
reached the server, but no confirming read followed; see [Waits](#waits)
above for what to do next and for why the Output still holds the resource.
See the top of this page for the route table, ACL, and DHCP options set
calls split onto their own pages.
