# Limitations

GreenNode server behaviors this SDK cannot change, each with what the SDK
does about it. Most of these come from
[Network](Network.md) and [Network ACLs](Network-ACLs.md); the rest are
collected at the bottom, each with a link to where it is documented in
full.

## A network ACL's busy window

Associating or disassociating a subnet leaves the ACL busy for about 20
seconds, during which any other write to it fails. Unlike after a rules
write, `Status` stays `"ACTIVE"` the whole time, so nothing in a read marks
the window; the write itself gets a 400 naming the ACL "is being updated".
The SDK maps that to `network.ErrBusy`, which always means nothing was
sent, so waiting a few seconds and calling again is safe. See
[Rules](Network-ACLs.md#rules).

## Deleting a subnet a network ACL still holds

Deleting a subnet while a network ACL still lists it, instead of
disassociating it first, leaves that ACL permanently stuck: every later
write to it fails, its own delete fails too, and its VPC can never be
deleted. `DeleteSubnet` now refuses, sending nothing, whenever a network
ACL in the subnet's VPC still holds it; disassociate the subnet first. See
[Creating, renaming, and deleting subnets](Network.md#creating-renaming-and-deleting-subnets).

## A permanently stuck network ACL

Once a network ACL is wedged this way, no SDK call can recover it: every
write returns 400 "is being updated", and even `DeleteNetworkACL` returns
500 instead of the usual 500-on-read. Only GreenNode support can clear it.

## A VPC's zone is ignored

`CreateVPC` sends no `zoneId`; the server places every new VPC in the
region's first zone regardless of what a caller asks for, confirmed
through both the SDK and the GreenNode web console. The zone that matters
is the one a subnet names. See
[Creating, renaming, and deleting VPCs](Network.md#creating-renaming-and-deleting-vpcs).

## Subnet size limits

The GreenNode web console's own subnet form offers `/16`, `/18`, `/20`,
`/22`, `/24`, `/26`, and `/28`. The SDK does not restrict `CIDR` beyond
requiring no host bits; the server enforces any narrower limit.

## A deleted subnet lingers

`GetSubnet` keeps returning a deleted subnet with status `"DELETED"` for
minutes after `ListSubnetsByVPC` has already dropped it, and a VPC delete
can keep failing for that same stretch after its last subnet is gone.
`DeleteSubnet` and `DeleteVPC` both treat this as expected and safe to
retry. See [Waits](Network.md#waits).

## Reading a deleted network ACL

`GetNetworkACL` answers 500, not 404, once an ACL is deleted. This SDK
never treats a bare 5xx as not-found on its own; see
[Get, create, and delete](Network-ACLs.md#get-create-and-delete).

## A rules replace can drop a rule silently

Sending an ACL's rules list without one of its priority-0 pass-all rules
removes that rule; the same omission for a priority-2000 deny-all rule
leaves it in place instead. `AddNetworkACLRule` and `RemoveNetworkACLRule`
always resend every rule they read, so a caller never triggers this by
accident. See [Rules](Network-ACLs.md#rules).

## Other server quirks

- **vDNS**: a record write locks its zone for several seconds; any other
  record write started in that window fails, so the SDK waits for the zone
  to clear before sending one. `UpdateRecord`'s `PUT` also replaces only
  the fields it is given, not the whole record. See
  [Creating, updating, and deleting records](DNS.md#creating-updating-and-deleting-records).
- **Security groups**: the `Name` filter on `ListSecurityGroups` matches by
  substring, not exactly; a search for `"web"` also finds `"webhook"`. See
  [Reading groups and rules](Network.md#reading-groups-and-rules).
- **SSH keys**: `ImportSSHKey` accepts RSA public keys only; an ED25519 or
  ECDSA key is refused. See [SSH keys](Compute.md#ssh-keys).
- **Container registry**: a `Repository` has no status field, since no
  response carries one, and repository and user names follow their own
  rules the server enforces. See
  [design/vcr-writes.md](https://github.com/dannyota/vngcloud/blob/master/docs/design/vcr-writes.md).
- **IAM**: a policy's or group's `root` field carries the account's own
  numeric id, sent as a JSON number rather than a string; and every list
  (`iam-users`, `service-accounts`, `policies`) numbers its pages from 0,
  so page 1 of a one-item list comes back empty. See
  [design/iam-writes-api.md](https://github.com/dannyota/vngcloud/blob/master/docs/design/iam-writes-api.md#bodies-and-responses).
- **vMonitor**: pausing and resuming a check are one toggle call each, not
  a dedicated pause and resume pair, and neither is ever retried after an
  unconfirmed result. See [Pausing and resuming](Monitor.md#pausing-and-resuming).
