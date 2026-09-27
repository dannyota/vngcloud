# Network DHCP Options

DHCP options sets for `danny.vn/vngcloud/network`. See [Network](Network.md)
for security groups, VPCs, subnets, and Private DNS.

This page assumes `cfg`, `ctx`, and `client := network.New(cfg)` from
[Network's Setup](Network.md#setup), and a VPC's `vpcID` from
[Creating, renaming, and deleting VPCs](Network.md#creating-renaming-and-deleting-vpcs).

## Creating, reading, and deleting sets

```go
created, err := client.CreateDHCPOptions(ctx, &network.CreateDHCPOptionsInput{
	Name:       "corp",
	DNSServers: []string{"10.166.12.196", "10.166.12.197"},
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.DHCPOptions.UUID)

set, err := client.GetDHCPOptions(ctx, &network.GetDHCPOptionsInput{
	DHCPOptionsID: created.DHCPOptions.UUID,
})

if _, err := client.DeleteDHCPOptions(ctx, &network.DeleteDHCPOptionsInput{
	DHCPOptionsID: created.DHCPOptions.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`DNSServers` must hold at least one address, and each must parse as IPv4
(`net/netip.ParseAddr`); the server enforces the four-address limit and a
quota of 10 sets per account. The SDK never adds a region's own default
resolvers on the caller's behalf; in `hcm-3` they are `10.166.12.196` and
`10.166.12.197`, and in `han-1` `10.236.10.196` and `10.236.10.197`. Leaving
them off a set risks some platform services failing to resolve, so include
them alongside any custom resolver unless a caller has a specific reason
not to. `MTU` is sent only when set; the server's own default is 1450.
`Name` must not start with `dhcp-option-dns-`, which the API reserves for
the set it creates when Private DNS is enabled on a VPC (see
[Enabling Private DNS](Network.md#enabling-private-dns)); the server gives
no other way to tell such a set apart, so the SDK refuses that prefix on
create with `vngcloud.ErrInvalidInput` so a caller's own set can never be
mistaken for one.

`CreateDHCPOptions` is a `POST` and is never retried after an ambiguous
failure, for the same reason `CreateSecurityGroup` is not; list sets with
`ListDHCPOptions` and match the name exactly before creating it again. It
has no post-create wait: the create response is the Output.

`DeleteDHCPOptions` reads the set first and sends nothing when it is still
attached to any VPC (`network.ErrInUse`, naming the VPCs): a set must be
detached from every VPC before delete. A set enabling Private DNS created,
once its VPC is deleted and the set is left behind unattached, deletes like
any other set; the SDK adds no extra guard for its reserved name once it
has no VPC left. `DELETE` is idempotent, and a retry that finds the set
already gone returns `vngcloud.IsNotFound(err) == true`.

## Setting a VPC's DHCP options

A change here redirects DNS resolution for every server in the VPC. Try it
on a VPC used for testing before running it against one carrying production
traffic.

```go
result, err := client.SetVPCDHCPOptions(ctx, &network.SetVPCDHCPOptionsInput{
	VPCID:         vpcID,
	DHCPOptionsID: created.DHCPOptions.UUID,
})
if err != nil {
	log.Fatal(err)
}
log.Println(result.Changed)
```

`SetVPCDHCPOptions` moves a VPC onto a set. There is no call to clear a
VPC's set, so this is one-way: a VPC can move to another set but never back
to having none. It reads the VPC first; if its current set already matches,
it returns at once with `Changed` false, sending nothing.

To restore a VPC's default resolvers, create a set with the region's
documented defaults (see above) and move the VPC to it with
`SetVPCDHCPOptions`; there is no call that clears a set or restores the
defaults directly.

It refuses, with `network.ErrDefaultResource` and nothing sent, a VPC whose
Private DNS is enabled or enabling, or whose current set is already one
Private DNS created: replacing that set would cut every server in the VPC
off from its private zone lookups, and there is no call to put it back.
Whether enabling Private DNS on a VPC that already carries a caller-made set
replaces that set, rather than refusing or leaving it alone, is unverified;
until it is checked live, treat the two as unsafe to combine in either
order.

It then reads the target set: a set that does not exist returns
`vngcloud.IsNotFound(err) == true`; one Private DNS created returns
`network.ErrDefaultResource`; one not yet `"ACTIVE"` returns
`network.ErrBusy`. Each of these sends nothing.

A `PATCH` failure that is a 4xx error is returned as is, since the server
never acted on it; any other failure, such as a 5xx or a network error,
wraps a hint that the change may already be in place and that `GetVPC`
shows the VPC's current set. The `PATCH`'s own response is not decoded; on
success, `SetVPCDHCPOptions` instead waits for a follow-up `GetVPC` to show
the target set, polling every 2 seconds for up to 60 seconds of elapsed
time. `Changed` is `true` once the `PATCH` is sent, whatever the wait's own
outcome: reaching `"ERROR"` returns an error wrapping `network.ErrFailed`,
and the bound running out, or a read or a sleep failing, wraps
`network.ErrNotSettled`; either way the Output still holds the last VPC a
read returned. Existing servers keep their old resolvers until a DHCP renew
or reboot (`dhclient`, `ipconfig /renew`); only a new server, or one
renewed, picks up the change.
