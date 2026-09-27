# Network Virtual IPs

Private virtual IP writes for `danny.vn/vngcloud/network`. See
[Network](Network.md) for security groups, VPCs, subnets, and Private DNS,
and [Services](Services.md#network) for the read-only virtual IP list and
its address pairs.

This page assumes `cfg`, `ctx`, and `client := network.New(cfg)` from
[Network's Setup](Network.md#setup), and a subnet's `subnetID` from
[Creating, renaming, and deleting
subnets](Network.md#creating-renaming-and-deleting-subnets).

## Creating, updating, and deleting virtual IPs

```go
created, err := client.CreateVirtualIPAddress(ctx, &network.CreateVirtualIPAddressInput{
	SubnetID: subnetID,
	Name:     "lb-vip",
	Mode:     network.VirtualIPModeActivePassive,
})
if err != nil {
	log.Fatal(err)
}
log.Println(created.VirtualIPAddress.UUID)

updated, err := client.UpdateVirtualIPAddress(ctx, &network.UpdateVirtualIPAddressInput{
	VirtualIPAddressID: created.VirtualIPAddress.UUID,
	Description:        vngcloud.Ptr("front door failover pair"),
})
if err != nil {
	log.Fatal(err)
}
log.Println(updated.VirtualIPAddress.Description)

if _, err := client.DeleteVirtualIPAddress(ctx, &network.DeleteVirtualIPAddressInput{
	VirtualIPAddressID: created.VirtualIPAddress.UUID,
}); err != nil {
	log.Fatal(err)
}
```

`Mode` is required on every create; `network.VirtualIPModeActiveActive`
(`"Active/Active"`) and `network.VirtualIPModeActivePassive`
(`"Active/Passive"`) name the documented values, but the SDK sends `Mode`
as given and does not check it against them, so a value the server adds
later never needs an SDK release. `IPAddress`, when set, must parse with
`net/netip.ParseAddr` as an IPv4 address; left empty, the server chooses
it. Whether an address lies in the subnet, and whether it is already in
use, are the server's own checks. `CreateVirtualIPAddress` never sends
`tags`, `zoneId`, or a public type; a public virtual IP has its own create
call, not covered here.

`CreateVirtualIPAddress` is a `POST` and is never retried after a failure
that may already have reached the server, for the reason
`CreateSecurityGroup` is not (see [Network](Network.md)): list virtual IPs
with `ListVirtualIPAddresses` and match the name exactly, or the address
if one was given, since the server keeps an address unique within a
subnet, before creating it again. If the create response is not already
`"ACTIVE"`, it then waits; see [Waits](#waits) below.

`UpdateVirtualIPAddress` changes `Name`, `Description`, `Mode`, or any
combination; at least one must be set, or the call fails with
`vngcloud.ErrInvalidInput` and sends nothing. The API replaces every field
on each `PUT` and requires `Mode` on every call, so the SDK reads the
virtual IP first and resends whichever field the caller left `nil`
unchanged. The `PUT` is marked idempotent. Whether the server actually
lets `Mode` change on an existing virtual IP is not yet confirmed live.

`DeleteVirtualIPAddress` reads the virtual IP first and sends nothing when
any address pair is still attached, checked both by the read's own
`AddressPairIPs` and by `ListAddressPairsByVirtualIPAddress`
(`network.ErrInUse`): a pair binds the address to a server interface, and
deleting it would move traffic. It also sends nothing for a virtual IP
whose `Type` is not the private type (`vngcloud.ErrInvalidInput`), so a
public virtual IP, which has its own delete call and price, is never
deleted through this one; the exact `Type` a private virtual IP carries is
not yet confirmed live, so today this refuses every virtual IP until that
is recorded. `DELETE` is idempotent; a retry that finds the virtual IP
already gone returns `vngcloud.IsNotFound(err) == true`. Deleting a subnet
that still holds a virtual IP is refused; see [Creating, renaming, and
deleting
subnets](Network.md#creating-renaming-and-deleting-subnets).

## Waits

`CreateVirtualIPAddress` skips its wait entirely when the create response
is already `"ACTIVE"`. Otherwise, without `NoWait`, it polls
`GetVirtualIPAddress` every 2 seconds for up to 60 seconds of elapsed
time, tolerating a 404, until the virtual IP reaches `"ACTIVE"`. If it
reaches `"ERROR"` instead, the create returns an error wrapping
`network.ErrFailed`; once the bound runs out, or a read or the wait's own
sleep fails, it wraps `network.ErrNotSettled` instead, and the create must
not be repeated. Either way the Output still holds the last virtual IP a
read returned, or, if none did, the one the create response itself
carried.

`UpdateVirtualIPAddress` takes no wait of its own: it sends the `PUT`,
then always reads the virtual IP once more and returns that read as the
Output. If that confirm read fails, the write has already succeeded: the
error wraps `network.ErrNotSettled`, and the Output falls back to the
`PUT`'s own response instead. `DeleteVirtualIPAddress` takes no wait; the
delete is treated as synchronous.

## Errors

`CreateVirtualIPAddress`, `UpdateVirtualIPAddress`, and
`DeleteVirtualIPAddress` share `network.ErrInUse`, `network.ErrFailed`,
and `network.ErrNotSettled` with the rest of `network`'s writes; see
[Network's Errors](Network.md#errors). They add no sentinel of their own.
