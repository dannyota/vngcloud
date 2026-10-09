# Network ACLs

Network ACLs are part of the [Network](Network.md) package,
`danny.vn/vngcloud/network`; this page covers creating, deleting, and
associating them, in the shape [Route tables and
routes](Network-RouteTables.md#route-tables-and-routes) uses for its own
writes. The rest of this page assumes `cfg`, `ctx`, and
`client := network.New(cfg)` from [Network's setup](Network.md#setup).

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
	Protocol:     "tcp",
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
`Protocol`, `Port`, `CIDR`, `Action`, and `System`. `Direction` and `Action`
are sent to the server exactly as given and checked only for shape; the
reference and a live read show `"inbound"` and `"outbound"` for
`Direction`. `Protocol` is checked case-insensitively and always sent in
the server's own spelling, confirmed live: `"ANY"` only in uppercase, and
`"tcp"`, `"udp"`, and `"icmp"` only in lowercase; any other spelling, such
as `"TCP"` or `"any"`, fails on the server with `"Invalid protocol."`, not
just this SDK's own check. `System`, decoded from the server's own `system`
field when present, marks a rule the server owns; see [Rules](#rules) for
how it and `Priority` together identify a default rule.

## Get, create, and delete

`GetNetworkACL` reads an ACL's rules and associated subnets. Confirmed
live, a deleted ACL's `GET` returns 500, not 404, so this SDK never treats
a bare 5xx there as not found on its own: after a 5xx, it lists network
ACLs once and checks whether the id is still listed. Absent, the call
returns `vngcloud.IsNotFound(err) == true`; listed, or if the list call
itself fails, it returns the original 5xx. A plain 404 on either call, for
an id that was never valid, still becomes not-found through the shared
transport with no list call.

`CreateNetworkACL` makes an ACL that is already `"ACTIVE"` and holds four
default rules, confirmed live: an inbound and an outbound pass-all rule at
`Priority` `0`, and an inbound and an outbound deny-all rule at `Priority`
`2000`; it takes no wait, since its response is final. ACL names are not
unique: creating a second ACL with the same `Name` in the same VPC
succeeds, confirmed live, so a create that fails ambiguously (any error
that is not a 4xx or `vngcloud.ErrInvalidInput`) hints at listing network
ACLs by name in the VPC and comparing `CreatedAt`, since a same-name ACL
may already exist for a reason having nothing to do with that failed call.

`DeleteNetworkACL` reads the ACL first and sends nothing when it is a
project's default ACL (`network.ErrDefaultResource`) or still has an
associated subnet (`network.ErrInUse`); disassociate every subnet first. A
`204` confirms the delete at once, with no wait. The `DELETE` is sent once
and never resent by the transport. A `DELETE` sent into the ACL's busy
window after a rules write (see [Rules](#rules)) gets a `400` naming the
ACL busy, which this SDK maps to `network.ErrBusy` the same way the rules
and subnets `PUT` do. Confirmed live, a `DELETE` sent into the busy window
after a subnets write (associate or disassociate) instead answers 500 and
changes nothing, rather than the 400 that window gives any other write.
`DeleteNetworkACL` cannot tell that 500 apart from any other by its status
alone, so after any 5xx it polls every network ACL instead of resending
the `DELETE`, across every page, every 2 seconds for up to 60 seconds: the
id absent at any point means the delete did take effect and the call
succeeds; still listed at the bound, or a list error at any point, returns
the original 500, and waiting out the busy window before calling again
succeeds.

## Rules

The API replaces an ACL's whole rule list on every write, so
`AddNetworkACLRule` and `RemoveNetworkACLRule` are read-merge writes, in
the shape `AddRoute` and `RemoveRoute` use: each reads the ACL's current
rules, waits for it to be `"ACTIVE"` first (`network.ErrBusy`, nothing
sent, past a 60-second bound), then sends back every rule it read,
including the server's own default rules and exactly as read, plus one
change. Immediately before sending that write, each also re-reads the ACL
and refuses with `network.ErrBusy`, again sending nothing, if the rules no
longer match the first read: some other writer changed the ACL in
between. This narrows the race between the read and the write, but does
not close it: a writer that changes the ACL between that final read and
the moment the `PUT` reaches the server can still be overwritten by it.

Confirmed live, an ACL stays busy for a while after a rules or subnets write
settles, and a write sent into that window gets a `400` this SDK maps to
`network.ErrBusy`. A rules write's window is roughly 18 to 23 seconds and
shows in `Status`, which leaves `"ACTIVE"` for that stretch; the busy `400`
then names the ACL `"is busy doing something"`. A subnets write
(`AssociateNetworkACLSubnet` or `DisassociateNetworkACLSubnet`) leaves the
ACL busy for about 20 seconds too, but `Status` reads `"ACTIVE"` throughout,
so nothing in a read marks the window; the busy `400` instead says the ACL
`"is being updated"`. Either message maps to `network.ErrBusy`. The rules
and subnets `PUT` are each sent once and never retried by the transport, so
a retry can never land in that window: a busy `400` on that single attempt
maps to `network.ErrBusy`, since it was rejected outright and changed
nothing, while a `5xx`, a network error, or a timeout maps to
`network.ErrNotSettled` instead, since that attempt may already have
reached the server. `DeleteNetworkACL`'s own `DELETE` is sent once too, and
in a rules write's busy window gets that same busy `400`, mapped to
`network.ErrBusy` the same way; in a subnets write's busy window it instead
answers 500, since that window gives a `DELETE` no busy `400` at all (see
[Get, create, and delete](#get-create-and-delete)).

Because a subnets write's busy window never shows in `Status`, a call that
already reported success can still leave the very next write to that ACL,
of any kind, returning `network.ErrBusy` for about 20 seconds afterward.
`network.ErrBusy` always means nothing was sent, so waiting a few seconds
and calling again is safe; this SDK never retries such a call
automatically, since only the caller knows whether that next write is
itself safe to repeat.

A rule is keyed by `Direction` (case-insensitive) and `Priority`.
`AddNetworkACLRule` of a rule already present with every other field equal
is a no-op: `Changed` is `false` and nothing is sent. The same key with any
other field different fails with `vngcloud.ErrInvalidInput`; remove it
first. `RemoveNetworkACLRule` of a key with no match returns
`vngcloud.IsNotFound(err) == true`, sending nothing.

A default rule never changes or gets removed. The server's own default
rules are the ones this SDK sees with `Priority` (`seqNumber`) `2000` or
above, one past the range `AddNetworkACLRule` accepts (`Priority` must be 1
to 1999), or with a decoded `System` field of `true`. `RemoveNetworkACLRule`
on a rule matching either fails with `network.ErrDefaultResource`, sending
nothing; naming a default rule this way, to confirm it is protected, is
safe. Every rule this SDK resends, default or not, carries its `System`
value exactly as read, never recomputed, so a default rule the server
marks for a reason beyond `Priority` keeps that marking. This SDK always
resends every rule it read on a replace, default or not, so a replace
never silently drops one the caller did not name; the server's own
behavior otherwise, confirmed live, is to keep a `Priority`-2000-or-above
rule left out of a replace, but to remove a `Priority`-0 rule left out.

A new ACL carries four default rules, confirmed live: an inbound and an
outbound pass-all rule at `Priority` `0`, and an inbound and an outbound
deny-all rule at `Priority` `2000`, each with `Protocol` `"ANY"`, `Port`
`"0-65535"`, and `CIDR` `"0.0.0.0/0"`. Despite the name "default rule" used
above, the two `Priority`-0 rules are ordinary rules by this SDK's own
marker: a caller may remove them like any other rule, and the two
`Priority`-2000 rules are the only ones actually protected. It has not been
shown live that a user rule with `Action` `"deny"` (or similar) takes
effect while a pass-all rule is still in the ACL's rule list; do not rely
on a deny rule alone to block traffic before removing the matching
`Priority`-0 pass-all rule.

`PortRangeMin` and `PortRangeMax` each range 0 to 65535; `PortRangeMax`
left 0 sends the same value as `PortRangeMin`, and `PortRangeMin` must not
be above the resulting `PortRangeMax`. The port string this SDK sends,
confirmed live, is `PortRangeMin` itself when the two are equal (a single
port such as `443` sends `"443"`), or `"PortRangeMin-PortRangeMax"`
otherwise (a range sends `"53-54"`, every port `"0-65535"`).

`Protocol` `"ANY"` carries no ports of its own and requires the full range,
`PortRangeMin` `0` and `PortRangeMax` `65535`; any other pair is refused
with `vngcloud.ErrInvalidInput`. `"icmp"` accepts that same full range, or
`PortRangeMin` `0` and `PortRangeMax` `0` together, the server's own "every
ICMP" port value (sent as `"0"`); any other pair is refused the same way.
`"tcp"` and `"udp"` need an explicit port or range: `PortRangeMin` and
`PortRangeMax` left at `0` and `0` together is refused with
`vngcloud.ErrInvalidInput` before any request, since that pairing is never
live-verified to mean a single port rather than every port. A caller
wanting every tcp or udp port sends `PortRangeMin` `0` and `PortRangeMax`
`65535` explicitly. `CIDR` must be a CIDR prefix with no host bits set. The
SDK never defaults `Action`, `Protocol`, or `CIDR`.

## Subnet associations

`AssociateNetworkACLSubnet` and `DisassociateNetworkACLSubnet` are the same
kind of read-merge write, over an ACL's subnet list instead of its rules,
including the same pre-PUT recheck described above. A subnet belongs to at
most one ACL, so associating one already associated with a different ACL
moves it there, and its rules apply to that subnet's traffic at once; test
on a non-production VPC first. `AssociateNetworkACLSubnet`'s output holds
`PreviousNetworkACLID`, the id of the ACL the subnet moved from, if any;
it is set only when `Changed` is `true`. `AssociateNetworkACLSubnet` of a
subnet already in the list is a no-op, sending nothing, including no read
of the subnet itself, so `PreviousNetworkACLID` is left empty. Otherwise it
reads the subnet with `GetSubnet` under the ACL's own VPC first, so a
subnet of a different VPC is never sent: a `404` there becomes
`vngcloud.IsNotFound(err) == true`. `DisassociateNetworkACLSubnet` of a
subnet not in the list is also a no-op, unlike `RemoveNetworkACLRule`'s
not-found for an absent rule; what a subnet falls back to once
disassociated is not yet confirmed live.

## Waits

Without `NoWait`, every ACL write above waits for the ACL to return to
`"ACTIVE"` and confirms that a fresh read matches what was sent, exactly as
`AddRoute` and `RemoveRoute` do; see [Route tables and
routes](Network-RouteTables.md#route-tables-and-routes) for that wait and its
`network.ErrNotSettled` failure mode, which network ACL writes share.
