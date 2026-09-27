# Release Notes

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

## v0.30.0 - vCR User Writes

### Highlights

- New `containerregistry.ListPermissions`, `ListRepositoryUsers`,
  `CreateUser`, and `DeleteUser`, with matching `vngcloud containerregistry`
  commands. `list-users` now ships too.
- `create-user` needs `--secret-file`: the registry secret goes only to a
  new file at mode 0600 and prints as `[redacted]`. Permissions are given
  by action name: `Pull Images`, `Push Images`, or `All`. An expiry in days
  is optional.
- The server requires a user name of 6 to 14 characters. A name another
  user already has is refused before any create. The create is sent once.
- If the new user cannot be found after the create, the secret is still
  written and the command exits with `UserNotFound`. If the file write
  fails, the CLI deletes the new user. `delete-user` needs `--yes`.

### Behavior changes

`containerregistry.User` is now a typed struct instead of a map. Breaking.

## v0.29.0 - vCR Repository Writes

### Highlights

- New `containerregistry.GetRepository`, `CreateRepository`, and
  `DeleteRepository`, with matching `vngcloud containerregistry` commands.
- Repositories are always private; no flag makes one public.
- The server keeps the name as given and requires 6 to 20 characters of
  lowercase letters, digits, `_`, or `-`, starting with a letter or digit.
- A repository holding images is never deleted: the SDK refuses with
  `containerregistry.ErrRepositoryNotEmpty`, CLI code `RepositoryNotEmpty`,
  and also refuses when the image count is unknown. `delete-repository`
  needs `--yes`. A create is never resent; after a failure that may have
  landed, list repositories by the exact name before trying again.

### Behavior changes

`containerregistry.Repository` is now a typed struct instead of a map.
Breaking. A repository has no status field.

## v0.28.0 - vLB Certificate Writes

### Highlights

- New `loadbalancer.ImportCertificate` and `DeleteCertificate`, with
  `vngcloud loadbalancer import-certificate` and `delete-certificate`.
- The private key and passphrase are `vngcloud.Secret` values. The CLI
  reads the certificate, chain, key, and passphrase only from files
  (`--certificate-file`, `--certificate-chain-file`, `--private-key-file`,
  `--passphrase-file`), refuses non-regular files such as a FIFO, and never
  prints the key. Keep the key file at mode 0600.
- A failing import never shows the server's message, since it may quote
  the key: the error keeps only the status and code.
- A certificate used by a listener is never deleted: the SDK refuses with
  `loadbalancer.ErrCertificateInUse`, CLI code `ResourceInUse`.
  `delete-certificate` needs `--yes`. The import is never resent.
- Live checks imported RSA, ECDSA P-256, and passphrase-encrypted keys,
  and a `CA` certificate.

### Behavior changes

- `GetCertificate` refuses a malformed ID before any request.
- The CLI no longer makes a flag for any `vngcloud.Secret` Input field and
  refuses such a field in `--cli-input-json`.

## v0.27.0 - Route Table Writes

### Highlights

- New `network.GetRouteTable`, `CreateRouteTable`, `DeleteRouteTable`,
  `AddRoute`, and `RemoveRoute`, with matching `vngcloud network` commands.
- The first route table created in a VPC with none becomes its main table.
  A main table is never deleted while a subnet relies on it: the SDK
  refuses with `network.ErrDefaultResource`, CLI code `DefaultResource`.
- The server replaces a table's whole route list on each change. The SDK
  reads the list, adds or removes one route, and reads again just before
  sending, so it sends nothing if the table changed meanwhile. A write to a
  table that is still busy is refused with `network.ErrBusy`, CLI code
  `ResourceBusy`, and changed nothing.
- `add-route`, `remove-route`, and `delete-route-table` need `--yes`.

## v0.26.0 - VPC and Subnet Writes

### Highlights

- New `network.CreateVPC`, `UpdateVPC`, `DeleteVPC`, `CreateSubnet`,
  `UpdateSubnet`, `DeleteSubnet`, and `EnableVPCPrivateDNS`, and the
  `ListServersBySubnet` read, with matching `vngcloud network` commands.
- A subnet is created in a zone you name. Creates wait until the VPC or
  subnet is `ACTIVE`.
- A VPC with subnets, or a subnet with servers, is never deleted: the SDK
  refuses with `network.ErrInUse`, CLI code `ResourceInUse`. The server
  keeps a deleted subnet for several minutes; a VPC delete that it refuses
  meanwhile also reads as `ResourceInUse`.
- `delete-vpc`, `delete-subnet`, and `enable-vpc-private-dns` need `--yes`.
  Private DNS cannot be turned off again and takes about 6 minutes.
- Do not delete a subnet while a network ACL holds it; disassociate it in
  the console first. The server can leave the ACL stuck otherwise.

### Behavior changes

`GetVPC`, `GetSubnet`, and `ListSubnetsByVPC` refuse a malformed ID before
any request.

## v0.25.0 - Server Group Writes

### Highlights

- New `compute.GetServerGroup`, `CreateServerGroup`, `UpdateServerGroup`,
  and `DeleteServerGroup`, with matching `vngcloud compute` commands.
- A group with servers is never deleted: the SDK refuses with
  `compute.ErrServerGroupInUse`, CLI code `ServerGroupInUse`, and sends
  nothing. `delete-server-group` needs `--yes`.
- An update sends only the fields given and keeps the rest. If its
  confirming read fails, it returns `compute.ErrNotSettled`, CLI code
  `NotSettled`, and the update may be sent again.
- A missing group reads as `NotFound`.

## v0.24.0 - SSH Key Writes

### Highlights

- New `compute.GetSSHKey`, `ImportSSHKey`, `CreateSSHKey`, and
  `DeleteSSHKey`, with matching `vngcloud compute` commands.
- Import takes RSA public keys only; the server refuses `ssh-ed25519`.
- `create-ssh-key` needs `--secret-file`: the CLI writes the new private
  key to a new file with mode 0600 and prints it only as `[redacted]`. If
  that write fails, the CLI deletes the key and exits with
  `SecretFileFailed`.
- New `vngcloud.Secret` type: its value prints as `[redacted]` under
  `fmt`, `slog`, and JSON, except for `%p`, `gob`, and reflection.
- A missing key reads as `NotFound` on get and delete.

### Behavior changes

`compute.SSHKey` drops `PrivateKey`. Breaking.

## v0.23.0 - Security Group Writes

### Highlights

- New `network.CreateSecurityGroup`, `UpdateSecurityGroup`, and
  `DeleteSecurityGroup`, and `CreateSecurityGroupRule` and
  `DeleteSecurityGroupRule`, with matching `vngcloud network` commands.
- An ingress rule from `0.0.0.0/0` or `::/0` needs `--yes`, and so does
  every delete.
- A system group, or a group with servers, is never updated or deleted.
  A rule is deleted only when it belongs to the named group.
- Rules are checked before any request: direction, protocol, prefix,
  ports, and ether type. An `icmp` rule covers all ICMP and takes no
  ports.
- New CLI codes `SystemSecurityGroup` and `SecurityGroupInUse`.

### Behavior changes

`GetSecurityGroup`, `ListSecurityGroupRules`, and
`ListServersBySecurityGroup` refuse a malformed ID before any request.

## v0.22.0 - vMonitor Log Project Orders

### Highlights

- New `monitor.CreateLogProject` and `vngcloud monitor create-log-project`
  order a log project. It quotes first and refuses with
  `monitor.ErrPriceAboveMax`, CLI code `PriceAboveMax`, when the price is
  above `MaxPrice`, which defaults to 0. It also refuses a name already in
  use, a `NaN`, infinite, or negative `MaxPrice`, and a quote with no price.
  The order is never resent. It waits for the project to be `ACTIVE`.
- New `monitor.DeleteLogProject` and `vngcloud monitor delete-log-project`
  (`--yes`) delete a log project, with `Purge` to also remove it from trash.
- The Basic class allows 3 orders or recoveries per month.

### Fixes

- `ListLogProjects` returned no projects: it sent empty filters that the
  API treats as real ones.
- `LogProject.ProjectName` and `ProjectDescription` were always empty.

### Behavior changes

`monitor.LogProject` drops `Zone` and `UpdatedAt`, which the API never
sends. Breaking.

## v0.21.0 - vMonitor OTP Channels

### Highlights

- New `monitor.SendChannelOTP` and `vngcloud monitor send-channel-otp`
  send a one-time code to an Email, Slack, SMS, or Telegram address.
- `CreateChannel` and `UpdateChannel` now take `OTPRef` and `OTP`
  (`--otp-ref` and `--otp`), so every channel type can be written, not
  just `Webhook`. A wrong or expired code returns `monitor.ErrOTPRejected`,
  CLI code `OTPRejected`, and nothing is written.
- The code, its ref, and the address never appear in errors or
  `--debug` output. No request that carries a code is ever resent.
- SMS and Email past the free 20 each spend a paid package.

### Behavior changes

`CreateChannel` and `UpdateChannel` accept Email, Slack, SMS, and
Telegram channels, which they refused before. Setting `OTPRef` without
`OTP`, or either on a `Webhook`, now fails with `ErrInvalidInput`.

## v0.20.0 - vMonitor Log Projects and Alarm Reads

### Highlights

- New `monitor.ListLogProjects`, `GetLogProject`, and
  `ListLogProjectClasses`, with matching `vngcloud monitor` commands.
- New `monitor.QuoteCreateLogProject` and `vngcloud monitor
  quote-create-log-project` price a log project order without placing it.
- New `monitor.ListAlarms` and `GetAlarm`, with matching commands. A list
  needs `Kind`, `Metric` or `Log`. `GetAlarm` leaves `Kind` empty, since
  the API sends no field that names it.
- The test account has no log project or alarm, so those response shapes
  are inferred from the console's code and marked unverified.

### Behavior changes

None.

## v0.19.0 - vMonitor Check Alerting

### Highlights

- `CreateCheckInput` gains `Notifications`: the channel IDs alerted when a
  check goes into alarm, comes back up, or turns undetermined.
- New `monitor.UpdateCheck` and `vngcloud monitor update-check`. The API
  replaces the whole check, so the SDK reads it first and sends back every
  field left unset. `Notifications` replaces all three lists at once. An
  update never changes a check's paused or enabled status, always sends
  TLS verification on, and refuses to touch a check it cannot read or a
  check type other than API/HTTP.
- `--cli-input-json` now refuses an unknown key at any depth, before any
  request. A mistyped nested key, such as `InAlarm` for `In-alarm`, used
  to be dropped silently and could clear a check's alerts.

### Behavior changes

`--cli-input-json` input that held an unknown nested key now exits 2
instead of being accepted.

Notes for `v0.7.0` to `v0.18.0` are in
[docs/release-notes/v0.7-v0.18.md](docs/release-notes/v0.7-v0.18.md), and
notes for `v0.1.0` to `v0.6.0` are in
[docs/release-notes/v0.1-v0.6.md](docs/release-notes/v0.1-v0.6.md).
