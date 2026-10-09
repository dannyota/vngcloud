# Release Notes

## v0.44.0 - Volume Attach and Detach

### Highlights

- New `volume.AttachVolume` and `DetachVolume`, with `vngcloud volume
  attach-volume` and `detach-volume`. Each reads the volume first and
  returns `Changed` false, sending nothing, when it is already in the
  requested state; the PUT then waits for `IN-USE` or `AVAILABLE`.
- `DetachVolume` refuses, before any request, a server's boot volume
  (`volume.ErrBootVolume`), a volume not attached to the named server, and
  a running server unless `AllowRunning` (`--allow-running`) is set
  (`volume.ErrServerRunning`); `detach-volume` needs `--yes`.
- Verified live on 2026-10-09: attach settled in 8 s and detach in 6 s on
  an `s2-general-1x2` server; every guard refused as designed.

## v0.43.0 - Server Writes

### Highlights

- New `compute.CreateServer`, `DeleteServer`, `StartServer`, `StopServer`,
  `RebootServer`, and `RenameServer`, with `vngcloud compute create-server`,
  `delete-server`, `start-server`, `stop-server`, `reboot-server`, and
  `rename-server`. A create quotes first and orders only at or under
  `MaxPrice` (`--max-price`); the order and each action are sent once. User
  data comes from `--user-data-file`, never argv, and never reaches the
  quote, logs, or errors.
- Waits: create to `ACTIVE`, stop to `STOPPED`, start and reboot to
  `ACTIVE`, delete to gone; `ERROR` is `compute.ErrFailed`, a timeout
  `compute.ErrNotSettled`, a status the action cannot start from
  `compute.ErrUnexpectedStatus`.
- `DeleteServer` always removes the boot volume with the server;
  `DeleteVolumes` (`--delete-volumes`) governs only attached data volumes,
  and `KeptVolumeIDs` lists the ones left behind. `delete-server`,
  `stop-server`, and `reboot-server` need `--yes`.
- Verified live on 2026-10-09: an `s2-general-1x2` server (347,800 VND a
  month) created in 42 s, stopped in 25 s, started in 15 s, rebooted in
  20 s, deleted in 20 s with a refund of the unused value.

## v0.42.0 - Volume Writes

### Highlights

- New `volume.CreateVolume`, `DeleteVolume`, and `QuoteResizeVolume`, with
  `vngcloud volume create-volume`, `delete-volume`, and
  `quote-resize-volume`. A create quotes first and orders only when the
  quote is at or under `MaxPrice` (`--max-price`, default 0, so a bare
  command orders nothing); the order is sent once and never retried.
- Waits: a create polls to `AVAILABLE` and a delete to gone; `ERROR` is
  `volume.ErrFailed`, a timeout `volume.ErrNotSettled` (do not repeat the
  write blind).
- `DeleteVolume` refuses an attached volume with `volume.ErrVolumeInUse`
  before any request; `delete-volume` needs `--yes`.
- New `vngcloud.ErrUnpriced`, CLI code `Unpriced`: a paid order whose quote
  is 0 is refused, since nothing in vServer is free.
- Verified live on 2026-10-09: a 10 GB SSD volume (32,000 VND a month)
  created in 15 s, deleted in 12 s, and the delete refunded the unused
  value to the wallet.

## v0.41.0 - vStorage Reads

### Highlights

- New `storage` package: `ListRegions`, `ListProjects`, `ListBuckets`, and
  `GetBucket`, with `vngcloud storage list-regions`, `list-projects`,
  `list-buckets`, and `get-bucket`. Reads only; buckets and keys come in
  later releases. `Region` names a vStorage region such as `HCM04`; left
  empty, `hcm-3` maps to `HCM04` and `han-1` to `HAN02`.
- The bucket commands take the vStorage project from the global
  `--project-id` flag only, never from the environment or profile, which
  hold the vServer project. A missing one exits 2 before any request.
- New `vngcloud.ErrUnpriced`, for a paid write whose quote is 0; no shipped
  write returns it yet.
- New [Storage](https://github.com/dannyota/vngcloud/wiki/Storage) and
  [CLI: Storage](https://github.com/dannyota/vngcloud/wiki/CLI-Storage)
  wiki pages.

## v0.40.0 - Resource Tags

### Highlights

- New `tagging` package: `ListResourceTags`, `TagResource`, and
  `UntagResource`, with `vngcloud tagging list-resource-tags`,
  `tag-resource`, and `untag-resource`. One tag API serves every resource
  type; `tagging.ResourceTypeVirtualIPAddress` is confirmed live and free,
  and VNG Cloud's own types `SERVER`, `VOLUME`, and `LOAD-BALANCER` are
  accepted as given.
- A write reads the resource's tags, sends the whole user tag list once
  with the one change applied, and reads again to confirm; it never sends
  a system tag, and refuses a `vng.` key with `tagging.ErrSystemTag`. The
  API has no conditional update, so keep tag writes on one resource to one
  writer at a time.
- New [Tagging](https://github.com/dannyota/vngcloud/wiki/Tagging) wiki
  page.

## v0.39.0 - Private Virtual IPs

### Highlights

- New `network.CreateVirtualIPAddress`, `UpdateVirtualIPAddress`, and
  `DeleteVirtualIPAddress`, with matching `vngcloud network` commands. A
  private virtual IP is free; `Mode` is required on create, and the
  address is the server's choice when left empty.
- `UpdateVirtualIPAddress` reads the virtual IP first and resends every
  field the caller left unset, since the API replaces all of them on each
  `PUT`. `DeleteVirtualIPAddress` refuses with `network.ErrInUse` while
  an address pair still binds the address, and with
  `vngcloud.ErrInvalidInput` for any virtual IP whose type is not
  `private`, so a public virtual IP is never deleted through it.
- New [Network Virtual IPs](https://github.com/dannyota/vngcloud/wiki/Network-VirtualIPs)
  wiki page.

## v0.38.0 - DHCP Options Sets

### Highlights

- New `network.ListDHCPOptions`, `GetDHCPOptions`, `CreateDHCPOptions`,
  `DeleteDHCPOptions`, `SetVPCDHCPOptions`, and `ClearVPCDHCPOptions`, with
  matching `vngcloud network` commands. A set holds up to four IPv4
  resolvers and an optional MTU; the SDK never adds a region's default
  resolvers on its own.
- `SetVPCDHCPOptions` and `ClearVPCDHCPOptions` read the VPC first and
  return `Changed` false, sending nothing, when the VPC already uses that
  set. `DeleteDHCPOptions` refuses with `network.ErrInUse`, naming the
  VPCs, while any VPC still uses the set.
- New [Network DHCP Options](https://github.com/dannyota/vngcloud/wiki/Network-DHCPOptions)
  wiki page.

### Behavior changes

`CreateDHCPOptions` refuses a name starting with `dhcp-option-dns-`, which
the API reserves for the set it creates when Private DNS is enabled, with
`vngcloud.ErrInvalidInput`.

## v0.37.0 - Network ACLs

### Highlights

- New `network.GetNetworkACL`, `CreateNetworkACL`, `DeleteNetworkACL`,
  `AddNetworkACLRule`, `RemoveNetworkACLRule`, `AssociateNetworkACLSubnet`,
  and `DisassociateNetworkACLSubnet`, with matching `vngcloud network`
  commands. Rule and subnet writes need `--yes`, re-read the ACL right
  before sending, and send each write once.
- `network.ErrBusy`, CLI code `ResourceBusy`, now also covers an ACL still
  settling an earlier write; nothing changed, so waiting and calling again
  is safe.
- New [Limitations](https://github.com/dannyota/vngcloud/wiki/Limitations)
  wiki page listing GreenNode server behaviors the SDK cannot change, such
  as the ACL busy window and overlapping VPC CIDRs.

### Behavior changes

- `network.DeleteSubnet` refuses with `ErrInUse`, sending nothing, while a
  network ACL still holds the subnet, since that delete leaves the ACL
  stuck for good. It also refuses when the subnet belongs to a VPC other
  than `VPCID`.
- `network.DeleteNetworkACL` sends the DELETE once and, after a 5xx,
  checks the ACL list for up to 60 seconds before reporting the result.

## v0.36.0 - Load Balancer Quotes

### Highlights

- New `loadbalancer.QuoteCreateLoadBalancer` and `QuoteResizeLoadBalancer`,
  with `vngcloud loadbalancer quote-create-load-balancer` and
  `quote-resize-load-balancer`. A quote orders nothing; prices are VND a
  month (400,000 for the smallest package).
- `loadbalancer.Pool` gains `ProgressStatus`.

### Behavior changes

Every load balancer, listener, pool, member, policy, and tag read refuses a
malformed ID before any request.

## v0.35.0 - Flavors and Create Quotes

### Highlights

- New `compute.ListFlavorZones`, `ListFlavors`, and `QuoteCreateServer`,
  and `volume.ListVolumesByServer` and `QuoteCreateVolume`, with matching
  commands. A quote orders nothing; prices are VND a month with VAT.
- `pricing.GetQuoteInput` gains `Action` (`create` when empty, or
  `resize`). `volume get-default-volume-type` gains `--zone-id`.
- New `vngcloud.ErrPriceAboveMax`, CLI code `PriceAboveMax`, for paid
  writes; `monitor.ErrPriceAboveMax` is the same value.
- A list Input field now makes a repeatable flag, such as
  `--security-group-id`.

### Behavior changes

`compute.GetServer`, `volume.GetVolume`, and `volume.ListSnapshots` refuse
a malformed ID before any request.

## v0.34.0 - IAM Group Writes

### Highlights

- New `iam.CreateGroup`, `UpdateGroup`, `DeleteGroup`, `AddUserToGroup`,
  `RemoveUserFromGroup`, and `AttachGroupPolicy`, `DetachGroupPolicy`,
  `AttachUserPolicy`, and `DetachUserPolicy`, with matching `vngcloud iam`
  commands.
- Guards refuse, sending nothing, any change to the caller's own rights,
  including a group the caller belongs to; any change to a group or user
  that holds IAM write rights; and any privileged policy. Only `iam` mode
  groups are changed: an identity provider group is refused.
- A group with members or policies is never deleted (`ResourceInUse`).
- A group create is sent once. Every write except `create-group` and
  `update-group` needs `--yes`.

## v0.33.0 - IAM Policy Writes

### Highlights

- New `iam.CreatePolicy`, `UpdatePolicy`, `DeletePolicy`, and
  `AttachServiceAccountPolicy` and `DetachServiceAccountPolicy`, with
  matching `vngcloud iam` commands.
- A policy document comes only from `--document-file` or
  `--cli-input-json`. It is checked for shape: known keys only, an effect
  of `allow` or `deny`, and non-empty actions and resources. An AWS-style
  document exits 2 before any request.
- Guards refuse, sending nothing, any policy that grants IAM write rights
  (including wildcards and any action pattern of unusual shape), any
  change to the caller's own rights, and any change to a principal that
  holds IAM write rights. A GreenNode-managed policy is never changed
  (`ManagedPolicy`); an attached policy is never deleted (`ResourceInUse`).
- A create is sent once. If a create or update landed but its confirming
  read failed, the CLI prints the policy ID and exits `NotSettled`.
- Every write except `create-policy` needs `--yes`.

## v0.32.0 - IAM Service Account Writes

### Highlights

- New `iam.CreateServiceAccount`, `UpdateServiceAccount`,
  `DeleteServiceAccount`, and `ResetServiceAccountSecret`, with matching
  `vngcloud iam` commands.
- The client secret is a `vngcloud.Secret`. `create-service-account` and
  `reset-service-account-secret` need `--secret-file`; the secret goes only
  to a new file at mode 0600. Create and reset are sent once.
- Guards refuse, sending nothing, any change to the caller's own rights
  (`SelfChange`) or to a service account that holds IAM write rights
  (`PrivilegedChange`), and every write from a service account caller.
  They fail closed on any unreadable or partial read.
- If a reset returns no secret, the old one is probably revoked: reset
  again. `delete-service-account` and `reset-service-account-secret` need
  `--yes`.
- The IAM write docs are on a new `IAM` wiki page.

## v0.31.0 - IAM Reads

### Highlights

- New `iam` package and `vngcloud iam` command group: `GetCallerIdentity`,
  `ListUsers`, `ListActions`, service account, policy, and group reads,
  policy attachments, and a user's groups and policies.
- The policies API lives on its own host; `Config` gains an `IAM`
  endpoint for it.
- Pages start at 0 on the IAM APIs.

Older releases are in [docs/release-notes-archive.md](docs/release-notes-archive.md).
