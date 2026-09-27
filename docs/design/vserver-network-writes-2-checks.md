# vServer Network Writes 2: Checks

Status: Accepted (2026-09-27), with
[vServer network writes 2](vserver-network-writes-2.md).

The unit tests, probes, live checks, and security review for
[vServer network writes 2](vserver-network-writes-2.md). Terms and
sentinels are defined there.

## Unit tests

Unit tests use `httptest` and an injected clock and sleep.

- Sanitized raw fixtures and decode tests in `testdata/network/` and
  `testdata/tagging/` for the DHCP set list, get, and create; the VPC after
  a set change; the virtual IP create and update; and the tag read and
  write, including a system tag.
- Request bodies: DHCP create with and without `MTU`, never `tags` or
  `zoneId`; the `PATCH` holds only `dhcpOptionId`; virtual IP create with
  and without `IPAddress` and `Description`; an update of only `Name`
  resends the read `description` and `mode`; a tag write holds every user
  tag read plus the change, with `resourceId` equal to the path ID.
- Shape refusals with no request: empty or non-IPv4 DNS server, empty DNS
  list, a set name with the system prefix, a bad virtual IP address, empty
  update, empty tag key.
- Guards, each sending no write: DHCP delete with a VPC; set on a VPC with
  `DNSStatus` `ENABLED` or `ENABLING`, or whose set is a system set; target
  set that is a system set, missing, or not `ACTIVE`; virtual IP delete
  with address pair IPs, with a listed pair, or with a public type; a tag
  write on a resource with a system tag.
- No-change outcomes with no write: set on a VPC that already has the
  target; tag with the same value; untag of an absent key.
- Confirms: a tag read after the write that differs is `ErrNotSettled`; a
  VPC read that never shows the target set reaches the bound and returns
  `ErrNotSettled`; `ERROR` returns `ErrFailed`.
- Statuses 200, 201, 204, 400, 404, 409, and 5xx; no create resend after a
  502; a create response without an ID fails with a message naming the
  list to check.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every operation
  that takes an ID, including `GetVirtualIPAddress` and
  `ListAddressPairsByVirtualIPAddress`.
- CLI golden tests for every command; `--yes` refusals for each command
  the CLI table marks; read-only refusal with no request; the
  `--dhcp-options-id` flag name.

## Probes the manager runs

The architect sent no write. The manager runs these on the test account in
`hcm-3` through a throwaway script, following
[live data](../../instructions/live-data.md), after the owner approves the
run naming the account, region, and resources. Every resource name starts
with `vngcloud-live-`, and the script deletes all it creates.

Before the probes, read-only: `get-balances`, `get-cost-overview`, and
`list-cost-resources` for the period, and `portal list-quota-used`. The run
needs one free VPC and one free subnet under the quota.

Setup: create a VPC and a `/24` subnet through the SDK (N2). Never enable
Private DNS on it.

### Cost and shape probe for DHCP sets

1. Create a set with `dnsServers` `10.166.12.196` and `10.166.12.197` and
   no `mtu`: status, envelope, `mtu`. Create the same name again. Create
   one with five servers. Record each status and message.
2. Get the set; get a made-up `dop-` ID: status.
3. Set it on the run's VPC: status, response, how long until the VPC's
   `dhcpOptionId` shows it, and the set's `associatedNetworks`. Send the
   same `PATCH` again: status.
4. Send the `PATCH` with `dhcpOptionId` `""`: status, and the VPC's set
   afterwards. This shows whether the API can clear a set.
5. Delete the set while attached: the server's refusal.
6. Create a second set and move the VPC to it: the first set's
   `associatedNetworks` afterwards.
7. Delete the run's subnet and VPC; then the second set's
   `associatedNetworks` and status.
8. Delete both sets: 204. Get after delete, and a repeat delete: status.

### Cost probe for private virtual IPs

On the run's subnet, before step 7 above:

1. Create a virtual IP with `mode` `Active/Passive` and no `ipAddress`:
   status, or the refusal and its message. A refusal that names payment,
   balance, or credit means the create is paid; stop and record it.
2. If it succeeds: the response `type`, `status`, and address. Read
   `get-balances` at once. Create a second one without `mode`: status.
3. Update the name only (resending `mode`), then change `mode`: status and
   the final `mode`.
4. Delete: 204. Get after delete, and a repeat delete: status.

The next day, `list-cost-resources` and `get-cost-overview` show no line
for a virtual IP, and `get-balances` is unchanged. Any line makes N6 paid.

### Type probe for tags

On the run's VPC and subnet, before step 7 above:

1. Read `GET tag/resource/{id}` for the VPC and subnet: `[]` expected.
2. Create a route table, a network ACL, a security group, a server group,
   and a second subnet, each with one tag in the create body's `tags`.
   Read each one's tags, then `GET tag` for the `resourceType` the server
   stored per resource. The survey's free resources are then covered.
3. If no stored type is found: send a tag `PUT` on the VPC with one tag
   and `resourceType` in turn `NETWORK`, `VPC`, `network`, and `vpc`, and
   on the subnet with `SUBNET` and `subnet`. Record each status and
   message, and read the tags after each accepted one.
4. With an accepted type: `PUT` two tags, then `PUT` one of them. The other
   gone means replace; still present means upsert. Then `PUT` an empty
   list: the tags afterwards.
5. `PUT` eleven tags: the quota refusal.
6. `PUT` an empty list on every tagged resource, then delete the resources
   from step 2.

A free resource type accepted in step 2 or 3 opens N7's gate. If none
is, N7's live check waits for credit, on a `SERVER` or `VOLUME`.

## Live checks

Each release's checks pass on the test account before its code merges,
through the release's live write test, following
[live data](../../instructions/live-data.md); each write run needs the
owner's approval. Every DHCP set change runs only on a VPC the same run
created; the test keeps that VPC's ID and never sends the `PATCH` to
another ID.

No two live tests that create a VPC run at the same time, since the quota
leaves one free VPC.

### N5 DHCP options sets

Read-only: `ListDHCPOptions` raw rows against the model, and the `name`
filter: exact or substring.

On a VPC the run creates:

1. `CreateDHCPOptions` with the default resolvers, then `GetDHCPOptions`.
2. `SetVPCDHCPOptions`: `Changed` true within the bound; again: `Changed`
   false, no `PATCH`.
3. `DeleteDHCPOptions` while attached: the SDK's `ErrInUse`, no request.
4. A second set; move the VPC to it; delete the first.
5. Delete the subnet and VPC, then the second set.
6. `portal list-quota-used` before and after.

### N6 private virtual IPs

Runs only once the gate passes, or once the owner adds credit.

On a subnet the run creates: create with each mode and with a given
address; create with the same address again; update the name only; delete;
the subnet delete refused while a virtual IP remains.

### N7 resource tags

Runs on the type the probe found free, or on a `SERVER` or `VOLUME` once
the owner adds credit: tag, tag again with the same value, change the
value, untag if shipped, and read after each. The resource ends with no
tags.

### Cleanup

The live write test first deletes leftovers whose names start with
`vngcloud-live-`: virtual IPs, then the subnets and VPCs, then DHCP sets,
as in [vServer network writes](vserver-network-writes-checks.md#cleanup). It
registers `t.Cleanup` as soon as each ID is known and deletes with its own
context. A DHCP set attached to the run's VPC is deleted after that VPC.
It never touches a resource without the prefix, and never a system set it
did not create.

## Security review

- Every write gets an adversarial review before its release. The review
  checks: no create resend after a 5xx; path ID checks on every call,
  reads included; each guard sends nothing; the `PATCH` never reaches a
  VPC with Private DNS or a system set; a tag write never drops a user tag
  the caller did not name and never sends a system tag changed; a public
  virtual IP is never deleted; `--yes` on every command the design's CLI
  table marks; read-only refusal of every write.
- A DHCP set change redirects DNS for every server in a VPC. The wiki shows
  it on a test VPC first and says how to restore the default resolvers.
- VPC names, CIDRs, addresses, DNS servers other than the documented
  defaults, tag keys and values, and IDs are account data. Fixtures use
  `<id>`, `<name>`, `<cidr>`, and `<ip>`.
