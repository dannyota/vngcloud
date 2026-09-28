# vServer Network Writes 2 Design

Status: Accepted (2026-09-27). The owner approved every recommendation
under [Owner decisions](#owner-decisions). Amended 2026-09-28 after a
console check showed that a VPC's DHCP options set can be cleared: the
owner approved the clear call and [decision 10](#owner-decisions)
(`--yes` on every call) the same day.

This design adds the three writes the
[free writes survey](free-writes-survey.md) left after
[vServer network writes](vserver-network-writes.md): DHCP options sets and
private virtual IPs in `network`, and resource tags in a new `tagging`
package. They ship as three releases, N5 to N7, and each has a gate that
must pass before its live check and tag.

It follows [vServer network writes](vserver-network-writes.md) for the
gateway, create retries, delete guards, waits, and live test VPC, and
[ADR 0002](../adr/0002-write-api-conventions.md) for every write. The calls,
server rules, and cost evidence are in
[vServer network writes 2: API](vserver-network-writes-2-api.md). Tests,
probes, live checks, and the security review are in
[vServer network writes 2: checks](vserver-network-writes-2-checks.md).

## Non-goals

- DHCP options set update: the API has no call for it.
- Public virtual IPs (`public-vips`), which cost 120,000 VND a month.
- Address pairs, which need a server interface and so a paid server.
- Tags on create bodies (`tags` in VPC, subnet, and other creates).
- The tag search reads (`tag`, `tag/tag-key`, `tag/{tagId}/resource-types`).
- Load balancer tags, which the vLB gateway serves.

## Gates

| Release | Gate | If the gate fails |
|-|-|-|
| N5 DHCP options sets | Next-day bill after the probe shows no line | Stop; owner decides |
| N6 private virtual IPs | Cost probe shows a create is free | Build and unit-test; live check and tag wait for credit and [decision 5](#owner-decisions) |
| N7 resource tags | Type probe shows a free resource type accepted | Build and unit-test; live check and tag wait for credit |

The probes are in
[checks](vserver-network-writes-2-checks.md#probes-the-manager-runs). A gate
that fails does not block the next release.

## DHCP options sets

All methods live in `network`. A new `DHCPOptions` model holds `UUID`
(`uuid`), `Name`, `Status`, `DNSServers` (`dnsServers`), `MTU`, `VPCIDs`
(`associatedNetworks`), `CreatedAt`, and `UpdatedAt`.

| Operation | Input | Output |
|-|-|-|
| `ListDHCPOptions` | `Name`, `Page`, `Size` | `PagedList[DHCPOptions]` |
| `GetDHCPOptions` | `DHCPOptionsID` (r) | `{DHCPOptions}` |
| `CreateDHCPOptions` | `Name` (r), `DNSServers` []string (r), `MTU` *int | `{DHCPOptions}` |
| `DeleteDHCPOptions` | `DHCPOptionsID` (r) | `{}` |
| `SetVPCDHCPOptions` | `VPCID` (r), `DHCPOptionsID` (r) | `{VPC; Changed bool}` |
| `ClearVPCDHCPOptions` | `VPCID` (r) | `{VPC; Changed bool}` |

"(r)" marks `vngcloud:"required"`.

- `DNSServers` needs at least one entry, and each must parse with
  `netip.ParseAddr` as IPv4; otherwise `ErrInvalidInput`, nothing sent. The
  four-server limit stays on the server (ADR 0002 rule 5). The SDK never
  adds the region's default resolvers; the wiki names them.
- Create sends `mtu` only when `MTU` is non-nil; the server's default is
  1450 (live). It never sends `tags` or `zoneId`.
- `GetDHCPOptions` decodes the set at the top level (live).

### System sets

Enabling VPC Private DNS creates a set named `dhcp-option-dns-<n>` and
attaches it to the VPC (live). Deleting that VPC leaves the set behind,
unattached. The response has no system marker, so the SDK treats a set as
a system set when its name starts with `dhcp-option-dns-`, and create
refuses that prefix with `ErrInvalidInput`, so a user set never looks like
one.

### Delete

`DeleteDHCPOptions` reads the set first. When `VPCIDs` is not empty it
returns `ErrInUse`, naming the VPCs, and sends nothing; the product docs
say a set must be detached from every VPC before delete. An unattached
system set is deleted like any other ([decision 3](#owner-decisions)).

The server's refusal is the final guard. How a get and a repeat delete
answer after a delete is a live check; until it is recorded, a 404 maps to
`NotFound` and a 5xx is returned as is.

### Set on a VPC

`PATCH networks/{vpcId}/updateDhcpOption` takes `dhcpOptionId` and replaces
the VPC's set; the same `PATCH` with body `{}` clears it (live). The write
is reversible: a VPC can move to another set, back to a previous set while
that set exists, or to no set with `ClearVPCDHCPOptions`.
`SetVPCDHCPOptions`:

1. Reads the VPC. When `DHCPOptionID` already equals the target: `Changed`
   false, nothing sent.
2. Refuses a VPC whose `DNSStatus` is not `DISABLED`, or whose current set
   is a system set, with `ErrDefaultResource`, nothing sent: replacing the
   Private DNS set would cut the VPC off from its private zones
   ([decision 2](#owner-decisions)).
3. Reads the target set: a 404 is `NotFound`; a system set is
   `ErrDefaultResource`; a status other than `ACTIVE` is `ErrBusy`. Nothing
   is sent in each case.
4. Sends the `PATCH`, marked idempotent: sending the same ID twice is
   harmless.
5. Confirms by reading the VPC until `DHCPOptionID` equals the target (see
   [Waits](#waits)). `Changed` is true.

`ClearVPCDHCPOptions`:

1. Reads the VPC. When it has no set: `Changed` false, nothing sent.
2. Refuses a VPC whose `DNSStatus` is not `DISABLED`, or whose current set
   is a system set, with `ErrDefaultResource`, nothing sent, for the reason
   in step 2 above.
3. Sends the `PATCH` with body `{}`, marked idempotent.
4. Confirms by reading the VPC until `DHCPOptionID` is empty (see
   [Waits](#waits)). `Changed` is true.

Servers keep their old resolvers until a DHCP renew or reboot; the wiki
says so. Whether enabling Private DNS on a VPC with a user set replaces it
is a live check.

## Private virtual IPs

All methods live in `network`, beside the existing virtual IP reads.

| Operation | Input | Output |
|-|-|-|
| `CreateVirtualIPAddress` | `SubnetID` (r), `Name` (r), `Mode` (r), `IPAddress`, `Description` | `{VirtualIPAddress}` |
| `UpdateVirtualIPAddress` | `VirtualIPAddressID` (r), `Name` *string, `Description` *string, `Mode` *string | `{VirtualIPAddress}` |
| `DeleteVirtualIPAddress` | `VirtualIPAddressID` (r) | `{}` |

- `Mode` is sent as given. The constants `VirtualIPModeActiveActive`
  (`Active/Active`) and `VirtualIPModeActivePassive` (`Active/Passive`)
  name the documented values. The SDK picks no default
  ([decision 4](#owner-decisions)).
- `IPAddress`, when set, must parse as an IPv4 address. Whether it lies in
  the subnet is the server's check.
- `CreateVirtualIPAddress` never sends `tags`, `zoneId`, or a public type.
- Update is a read-merge: an update with no non-nil field is
  `ErrInvalidInput` with nothing sent; otherwise the SDK reads the virtual
  IP, applies the non-nil fields, and sends `name`, `description`, and
  `mode`, because the server requires `mode`. The Output is a read after
  the write. Whether the server lets `mode` change is a live check.
- The `VirtualIPAddress` model is unchanged, except that the create and
  update responses decode into a private type mapped to it, as the other
  creates do.

### Virtual IP delete

`DeleteVirtualIPAddress` reads first and sends nothing when:

- `AddressPairIPs` is not empty, or `ListAddressPairsByVirtualIPAddress`
  returns any pair: `ErrInUse`. A pair binds the address to a server
  interface, and deleting it moves traffic.
- `Type` is not the private type the cost probe records: `ErrInvalidInput`,
  so a public virtual IP, which has its own delete call and price, is never
  deleted through this call.

The subnet delete guard already refuses a subnet that holds a virtual IP.
How a get and a repeat delete answer after a delete is a live check.

## Resource tags

A new package `tagging` holds one tag API for every resource type
([decision 6](#owner-decisions)). It uses the vServer gateway, as
`network` does.

| Operation | Input | Output |
|-|-|-|
| `ListResourceTags` | `ResourceID` (r) | `Items []Tag` |
| `TagResource` | `ResourceID` (r), `ResourceType` (r), `Key` (r), `Value` | `{Tags []Tag; Previous *string; Changed bool}` |
| `UntagResource` | `ResourceID` (r), `ResourceType` (r), `Key` (r) | `{Tags []Tag; Previous *string; Changed bool}` |

`Tag` holds `Key`, `Value`, `SystemTag`, and `CreatedAt`. `Previous` is the
key's value before the write, or nil when it had none, so a caller can undo
either write.

- `ResourceType` is sent as given. Constants exist only for types a live
  check accepted; the type probe decides which. VNG Cloud's Go SDK names
  `SERVER` and `VOLUME` for this gateway.
- An empty `Key` is `ErrInvalidInput`. Key and value limits stay on the
  server (ADR 0002 rule 5), including the quota of 10 tags per resource.

### Tag writes

`PUT tag/resource/{resourceId}` takes `resourceId`, `resourceType`, and
`tagRequestList`. The survey read it as a full replace; VNG Cloud's Go SDK
says it upserts by key and leaves unlisted keys alone. The design is safe
under both readings:

1. Read the tags. If any is a system tag: `ErrSystemTag`, nothing sent
   ([decision 8](#owner-decisions)).
2. Build the list from every user tag read, then apply the change.
   - Tag: key present with the same value: `Changed` false, nothing sent.
     Key present with another value: the value is replaced.
   - Untag: key absent: `Changed` false, nothing sent.
3. Send the whole list with `resourceId` set to the path ID. The `PUT`
   keeps the transport's retries, since resending the same list is
   idempotent.
4. Confirm that the user tags read equal the list sent. A mismatch is
   `ErrNotSettled`, whose message says another writer may have changed the
   tags.

Under upsert semantics, step 3 of untag leaves the key in place and step 4
reports it. So `UntagResource` ships only when the type probe shows that a
`PUT` drops unlisted keys, which is
[decision 7](#owner-decisions).

## Waits

Per ADR 0002 rule 7, only asynchronous writes wait. They use the `network`
poll helper with the injected clock and sleep, and honour `ctx`.

| Write | Settled | Failed | Poll | Bound |
|-|-|-|-|-|
| `SetVPCDHCPOptions` | VPC `DHCPOptionID` equals the target | VPC `ERROR` | 2 s | 60 s |
| `ClearVPCDHCPOptions` | VPC `DHCPOptionID` empty | VPC `ERROR` | 2 s | 60 s |
| Virtual IP create, when the response status is not `ACTIVE` | `ACTIVE` | `ERROR` | 2 s | 60 s |

DHCP set create and delete, virtual IP update and delete, and tag writes
have no wait unless their live check shows an intermediate status; a new
row then amends this table. The bound returns the Output and an error
wrapping `ErrNotSettled`. No write in this design takes `NoWait`, since
each bound is short.

## Identifiers and retries

- Every operation checks each path ID with `core.CheckPathID` before any
  request, including `GetVirtualIPAddress` and
  `ListAddressPairsByVirtualIPAddress`, which send any value today.
- Creates are `POST` and are never resent after a 5xx or network error
  (ADR 0002 rule 2). The error names the list to check by exact match:
  `list-dhcp-options --name`, or `list-virtual-ip-addresses --name`. A
  virtual IP created with `IPAddress` is matched by address, which the
  server keeps unique in a subnet, so a rerun with the same address cannot
  make a second one.
- The DHCP `PATCH`, the virtual IP `PUT`, the tag `PUT`, and the deletes
  keep the transport's retries. A retried delete that finds the resource
  gone returns `NotFound`.
- The CLI never retries a write.

## CLI

| Command | Kind | `--yes` | Release |
|-|-|-|-|
| `network list-dhcp-options`, `get-dhcp-options` | Read | No | N5 |
| `network create-dhcp-options` | Write | No | N5 |
| `network delete-dhcp-options` | Write, destructive | Yes | N5 |
| `network set-vpc-dhcp-options` | Write, changes DNS | Yes | N5 |
| `network clear-vpc-dhcp-options` | Write, changes DNS | Yes | N5 |
| `network create-virtual-ip-address`, `update-virtual-ip-address` | Write | No | N6 |
| `network delete-virtual-ip-address` | Write, destructive | Yes | N6 |
| `tagging list-resource-tags` | Read | No | N7 |
| `tagging tag-resource`, `untag-resource` | Write | No | N7 |

- `set-vpc-dhcp-options` and `clear-vpc-dhcp-options` need `--yes` on every
  call: each changes DNS for every server in the VPC on its next DHCP
  renew ([decision 10](#owner-decisions)).
- Tag writes need no `--yes`: each is undone by the other, and the Output
  gives `Previous` ([decision 9](#owner-decisions)).
- `DNSServers` is a list, so `create-dhcp-options` takes it through
  `--cli-input-json`, as [CLI](cli.md#operation-table) sets out.
- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- `DHCPOptionsID` becomes `--dhcp-options-id` by the kebab rule; no rename
  is needed.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, bad address, empty DNS list, reserved set name, empty update, empty key, public virtual IP | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Missing `--yes` | No request | `InvalidUsage`, 2 |
| Unknown VPC, set, virtual IP, or resource | `NotFound` | `NotFound`, 4 |
| Set attached to a VPC; virtual IP with address pairs | `network.ErrInUse` | `ResourceInUse`, 1 |
| Set on a Private DNS VPC, or to or from a system set | `network.ErrDefaultResource`, no request | `DefaultResource`, 1 |
| Clear on a Private DNS VPC, or of a system set | `network.ErrDefaultResource`, no request | `DefaultResource`, 1 |
| Target set not `ACTIVE` | `network.ErrBusy`, no request | `ResourceBusy`, 1 |
| Resource with a system tag | `tagging.ErrSystemTag`, no request | `SystemTag`, 1 |
| `ERROR` after a write | `network.ErrFailed`, with Output | `WriteFailed`, 1 |
| Bound reached, or confirm read differs | `ErrNotSettled` of the package, with Output | `NotSettled`, 1 |
| Set limit, tag quota, used address, unknown resource type, payment refused | The server's `*APIError` | 1 |
| 5xx or network error on a create | The error; the message names the list | 1 |

`network` gains no sentinel. `tagging` defines `ErrSystemTag` and
`ErrNotSettled`. The CLI list in
[CLI](cli.md#errors-and-exit-codes) gains `SystemTag`; `NotSettled` maps
both packages' `ErrNotSettled`.

## Releases

| Release | Content |
|-|-|
| N5 | `network` `DHCPOptions`, `ListDHCPOptions`, `GetDHCPOptions`, `CreateDHCPOptions`, `DeleteDHCPOptions`, `SetVPCDHCPOptions`, `ClearVPCDHCPOptions`; CLI commands |
| N6 | `network` `CreateVirtualIPAddress`, `UpdateVirtualIPAddress`, `DeleteVirtualIPAddress`, the mode constants, path ID checks on the virtual IP reads; CLI commands |
| N7 | `tagging` package, `ListResourceTags`, `TagResource`, `UntagResource` if decision 7 allows, `ErrSystemTag`, `ErrNotSettled`, the verified type constants; the `tagging` CLI group |

Each is numbered when it ships, in this order where the gates allow. No
release breaks callers; the virtual IP reads only start rejecting a
malformed ID. Each release adds its own files. The `Network` wiki page
gains N5 and N6 with the retry advice, the `--yes` reasons, the default
resolvers, and the DHCP renew note; N7 adds a `Tagging` page. A release's
live checks pass before its code merges.

## Owner decisions

1. Release split and order. Options: three releases N5 to N7 in the order
   above, each on its own gate; one release. Recommend three: DHCP needs
   no gate but the bill, while N6 and N7 may wait for credit.
2. Setting a set on a VPC with Private DNS. Options: refuse; allow behind
   `--yes`. Recommend refuse: the swap silently breaks private zone
   lookups, and the API cannot swap back to the system set cleanly.
3. System sets left by deleted Private DNS VPCs. Options: let
   `DeleteDHCPOptions` delete an unattached system set, like any set;
   refuse every system set. Recommend allow: an unattached one serves no
   VPC, and the probes already left two behind. The manager deletes them
   after this decision.
4. Virtual IP `Mode`. Options: required with no default; optional, sent
   only when set, as Terraform does. Recommend required: the reference
   marks it required, and the SDK does not guess a failover model.
5. A paid private virtual IP. The pricing API has no private type
   (`public-vip` ignores `type`; live), so ADR 0002 rule 8 cannot be met.
   Options: a new ADR that lets a paid create ship without a quote when
   the pricing API has no type, if the wiki states the billed price and
   the create needs `--yes`; hold N6 until a quote type exists; drop N6.
   Recommend the ADR, only if the probe shows a charge.
6. Tag package. Options: a `tagging` package for every type; tag methods
   in each service package. Recommend `tagging`: one call serves every
   type, and per-service methods would repeat the read-merge.
7. `UntagResource` under upsert semantics. Options: ship it only if the
   probe shows a `PUT` drops unlisted keys; ship `TagResource` alone
   otherwise, and look for a removal call in a later design. Recommend
   that rule.
8. Resources with system tags. Options: refuse any tag write; resend the
   system tags as read. Recommend refuse until a live check on a resource
   with a system tag shows the `PUT`'s effect. The test account has none.
9. `--yes` on tag writes. Options: none; always. Recommend none.
10. `--yes` on `set-vpc-dhcp-options` and `clear-vpc-dhcp-options` now that
    both are reversible. Options: every call; none. Recommend every call:
    each changes DNS for every server in the VPC, and an agent should not
    do that by default.

## Open questions

- The next day's bill after the DHCP, virtual IP, and tag probes.
- Whether the private virtual IP create is billed, and the `type` value of
  a private virtual IP.
- Which tag resource types the server accepts, and whether a tag `PUT`
  replaces or upserts.
- Whether unattached system sets count against the limit of 10 sets.
- Whether enabling Private DNS replaces a user set, and what a VPC's DNS is
  with no set.
- Get and repeat delete answers after a DHCP set or virtual IP delete.
- Whether a virtual IP's `mode` can change, and whether create is
  asynchronous.
