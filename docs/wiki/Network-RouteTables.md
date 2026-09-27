# Network Route Tables

Route tables and routes for `danny.vn/vngcloud/network`. See
[Network](Network.md) for security groups, VPCs, subnets, and Private DNS.

This page assumes `cfg`, `ctx`, and `client := network.New(cfg)` from
[Network's Setup](Network.md#setup), and a VPC's `vpcID` from
[Creating, renaming, and deleting VPCs](Network.md#creating-renaming-and-deleting-vpcs).

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
