# Network ACLs

Network ACLs are part of the [Network](Network.md) package,
`danny.vn/vngcloud/network`; this page covers creating, deleting, and
associating them, in the shape [Route tables and
routes](Network.md#route-tables-and-routes) uses for its own writes. The
rest of this page assumes `cfg`, `ctx`, and `client := network.New(cfg)`
from [Network's setup](Network.md#setup).

```go
acl, err := client.CreateNetworkACL(ctx, &network.CreateNetworkACLInput{
	VPCID: vpcID,
	Name:  "public",
})
if err != nil {
	log.Fatal(err)
}
log.Println(acl.ACL.UUID)

added, err := client.AddNetworkACLRule(ctx, &network.AddNetworkACLRuleInput{
	NetworkACLID: acl.ACL.UUID,
	Direction:    "inbound",
	Priority:     100,
	Protocol:     "TCP",
	CIDR:         "203.0.113.0/24",
	Action:       "pass",
	PortRangeMin: 443,
})
if err != nil {
	log.Fatal(err)
}
log.Println(added.Changed)

associated, err := client.AssociateNetworkACLSubnet(ctx, &network.AssociateNetworkACLSubnetInput{
	NetworkACLID: acl.ACL.UUID,
	SubnetID:     subnetID,
})
if err != nil {
	log.Fatal(err)
}
log.Println(associated.ACL.SubnetIDs)

if _, err := client.DisassociateNetworkACLSubnet(ctx, &network.DisassociateNetworkACLSubnetInput{
	NetworkACLID: acl.ACL.UUID,
	SubnetID:     subnetID,
}); err != nil {
	log.Fatal(err)
}

if _, err := client.RemoveNetworkACLRule(ctx, &network.RemoveNetworkACLRuleInput{
	NetworkACLID: acl.ACL.UUID,
	Direction:    "inbound",
	Priority:     100,
}); err != nil {
	log.Fatal(err)
}

if _, err := client.DeleteNetworkACL(ctx, &network.DeleteNetworkACLInput{
	NetworkACLID: acl.ACL.UUID,
}); err != nil {
	log.Fatal(err)
}
```

## The ACL model

`ACL` gains `DefaultACL`, `VPCID`, `Rules` (`[]network.ACLRule`), and
`SubnetIDs` over the fields `ListNetworkACLs` already returned; a value
read through `ListNetworkACLs` leaves the new fields at their zero value,
and one read through `GetNetworkACL` or `CreateNetworkACL` leaves the
older `NetworkID` and `SubnetID` fields empty, since the two calls return
different shapes. `ACLRule` holds `UUID`, `Direction`, `Priority`,
`Protocol`, `Port`, `CIDR`, and `Action`. `Direction`, `Protocol`, and
`Action` are sent to the server exactly as given and checked only for
shape; the reference and a live read show `"inbound"` and `"outbound"` for
`Direction`, and `"ANY"`, `"TCP"`, `"UDP"`, and `"ICMP"` for `Protocol`.

## Get, create, and delete

`GetNetworkACL` reads an ACL's rules and associated subnets. Confirmed
live, a deleted ACL's `GET` returns 500, not 404, so this SDK never treats
a bare 5xx there as not found on its own: after a 5xx, it lists network
ACLs once and checks whether the id is still listed. Absent, the call
returns `vngcloud.IsNotFound(err) == true`; listed, or if the list call
itself fails, it returns the original 5xx. `DeleteNetworkACL` uses the same
list confirm after a 5xx on its own `DELETE`: absent means the delete
already took effect and the call succeeds; listed, or a failed list,
returns the original error. A plain 404 on either call, for an id that was
never valid, still becomes not-found through the shared transport with no
list call.

`CreateNetworkACL` makes an ACL that is already `"ACTIVE"` and holds the
server's default rules; it takes no wait, since its response is final.
`DeleteNetworkACL` reads the ACL first and sends nothing when it is a
project's default ACL (`network.ErrDefaultResource`) or still has an
associated subnet (`network.ErrInUse`); disassociate every subnet first. A
`204` confirms the delete; there is no wait either.

## Rules

The API replaces an ACL's whole rule list on every write, so
`AddNetworkACLRule` and `RemoveNetworkACLRule` are read-merge writes, in
the shape `AddRoute` and `RemoveRoute` use: each reads the ACL's current
rules, waits for it to be `"ACTIVE"` first (`network.ErrBusy`, nothing
sent, past a 60-second bound), then sends back every rule it read,
including the server's own default rules, plus one change. A rule is
keyed by `Direction` (case-insensitive) and `Priority`. `AddNetworkACLRule`
of a rule already present with every other field equal is a no-op:
`Changed` is `false` and nothing is sent. The same key with any other field
different fails with `vngcloud.ErrInvalidInput`; remove it first.
`RemoveNetworkACLRule` of a key with no match returns
`vngcloud.IsNotFound(err) == true`, sending nothing.

A default rule never changes. The server's own default rules are the ones
this SDK has seen with `Priority` (its `seqNumber`) `0`, one lower than
`AddNetworkACLRule` ever accepts (`Priority` must be at least 1), so a
caller can never add a rule sharing that priority. `RemoveNetworkACLRule`
on a rule at `Priority` `0` fails with `network.ErrDefaultResource`,
sending nothing; naming a default rule this way, to confirm it is
protected, is safe. Whether the full default rule list uses only priority
`0`, and whether the server needs a default rule resent on a replace or
drops it if left out, are still live checks; until they are confirmed, this
SDK always resends every rule it read, so a replace never silently drops
one the caller did not name.

`PortRangeMin` and `PortRangeMax` each range 0 to 65535; `PortRangeMax`
left 0 sends the same value as `PortRangeMin`. `CIDR` must be a CIDR
prefix with no host bits set. The SDK never defaults `Action`, `Protocol`,
or `CIDR`.

## Subnet associations

`AssociateNetworkACLSubnet` and `DisassociateNetworkACLSubnet` are the same
kind of read-merge write, over an ACL's subnet list instead of its rules.
A subnet belongs to at most one ACL, so associating one already associated
with a different ACL moves it there, and its rules apply to that subnet's
traffic at once; test on a non-production VPC first. `AssociateNetworkACLSubnet`
of a subnet already in the list is a no-op, sending nothing, including no
read of the subnet itself. Otherwise it reads the subnet with `GetSubnet`
under the ACL's own VPC first, so a subnet of a different VPC is never
sent: a `404` there becomes `vngcloud.IsNotFound(err) == true`.
`DisassociateNetworkACLSubnet` of a subnet not in the list is also a no-op,
unlike `RemoveNetworkACLRule`'s not-found for an absent rule; what a subnet
falls back to once disassociated is not yet confirmed live.

## Waits

Without `NoWait`, every ACL write above waits for the ACL to return to
`"ACTIVE"` and confirms that a fresh read matches what was sent, exactly as
`AddRoute` and `RemoveRoute` do; see [Route tables and
routes](Network.md#route-tables-and-routes) for that wait and its
`network.ErrNotSettled` failure mode, which network ACL writes share.
