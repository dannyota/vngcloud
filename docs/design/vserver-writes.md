# vServer Free Writes Design

Status: Accepted (2026-09-27).

This design adds the vServer writes that cost nothing: security groups and
their rules in `network`, and SSH keys in `compute`. The test account has no
credit, so these are the first vServer writes that can be verified live.
Servers, volumes, and floating IPs stay read-only.

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md). The private key of a
created SSH key follows the secret rules of [vStorage](storage.md#secrets)
and [CLI secret files](cli.md#secret-files).

## Source

The shapes below come from three public sources, none checked live yet:

- The vServer API reference on `docs.api.greennode.ai`
  (`service-docs/vserver.html`), tags `Secgroup`, `Secgroup-Rule`, and
  `SSH key`.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`, package
  `services/network/v2`), which has security group and rule writes and
  matches errors by message text.
- VNG Cloud's Terraform provider (`vngcloud/terraform-provider-vngcloud`,
  `client/vserver` and `resource/vserver`), whose generated client has
  every call below and whose resources show the waits.

The reference documents the public gateway
(`https://hcm-3.api.vngcloud.vn/vserver/vserver-gateway/`). The SDK's reads
use the IAM console gateway (`VServer` endpoint,
`.../vserver/iam-vserver-gateway/`) with the same `v2/{projectId}/...`
paths; that the write paths exist there is a
[live check](vserver-writes-checks.md#live-checks).
The reference marks a `portal-user-id` header required; the reads work
without it, and so must the writes.

Every shape marked "inferred" is taken from these sources and must be
confirmed by the live checks before code merges.

### Calls

| Call | Method and path under `v2/{projectId}` | Success |
|-|-|-|
| Create group | `POST /secgroups` | 201 |
| Update group | `PUT /secgroups/{secgroupId}` | 200 |
| Delete group | `DELETE /secgroups/{secgroupId}` | 204 |
| Create rule | `POST /secgroups/{secgroupId}/secgroupRules` | 201 |
| Delete rule | `DELETE /secgroups/{secgroupId}/secgroupRules/{ruleId}` | 204 |
| List rules (exists) | `GET /secgroups/{secgroupId}/secGroupRules` | 200 |
| Get key | `GET /sshKeys/{sshKeyId}` | 200 |
| Create key | `POST /sshKeys` | 201 |
| Import key | `POST /sshKeys/import` | 201 |
| Delete key | `DELETE /sshKeys/{sshKeyId}` | 204 |

The rule list path spells `secGroupRules`; the write paths spell
`secgroupRules`. Both come from the reference and both SDKs.

### Bodies (inferred)

- Group create and update: `name` (required), `description`. Both also
  take `tags` and `zoneId`, which the SDK does not send. Terraform sends
  both fields on every update, so the design treats update as a full
  replace.
- Rule create: `direction`, `etherType`, `protocol`, `portRangeMin`,
  `portRangeMax`, `remoteIpPrefix` (all required), `description`, and
  `securityGroupId`. The reference omits `securityGroupId`; both SDKs send
  it, so this SDK sends it too.
- Key create: `name`. The response holds `privateKey`.
- Key import: `name`, `pubKey`.

### Responses (inferred)

Create responses do not match the read models:

- Group create returns `id` as an integer, the group ID as `uuid`, and the
  name as `secgroupName`. Reads return `id` as the string ID and `name`.
- Rule create returns `uuid`, `secgroupUuid`, and an integer `ruleId`. The
  rule list returns string IDs; the existing fixture has a string `ruleId`.
- Key create and import return `data` with `id`, `name`, `pubKey`,
  `privateKey` (create only), `status`, and `createdAt`.

So each create decodes into a private response type and maps it to the
public model; it never decodes into the model, where an integer `id`
would fail.

### Server rules (from the product docs)

- A new group holds default egress rules that allow all outbound traffic,
  and no ingress rule, so all inbound traffic is denied. Rules only allow;
  there is no deny rule. Groups are stateful.
- Each project has a system `default` group (`isSystem`) with allow-all
  egress and ingress on SSH, RDP, HTTP, HTTPS, and ICMP from anywhere.
- Group names are unique per project: a repeat name fails with
  `name of security group already exist`.
- A duplicate rule fails with `SecurityGroupRuleExists`.
- Deleting a group in use fails with `SecurityGroupInUse`. The status is
  unknown; Neutron uses 409.
- Quotas exist for groups and rules (`exceeded secgroup quota`,
  `exceeded secgroup_rule quota`).
- SSH keys: import takes RSA only (`ssh-ed25519` gets 400 "Invalid public
  key", seen live). A lost private key cannot be recovered.

### Waits (inferred)

Terraform waits after a group create until `status` leaves `CREATING` and
reaches `ACTIVE`. It does not wait after a rule create, a key write, or a
group delete. Its rule delete re-sends the delete for up to 3 minutes on
any error, which suggests a rule delete can fail while its group is busy.

### Cost

No vServer pricing page or calculator item names security groups, rules,
or SSH keys; the calculator prices servers, volumes, and IPs. The design
treats all three as free, so ADR 0002 rule 8 (quote) does not apply. None
is flagged as possibly billed. On an account with no credit a paid create
fails, so a live run would show a surprise charge as an error. The
next day's bill must show no vServer line.

## Non-goals

- Server, volume, and floating IP writes, and attaching a group to a
  server. Each costs money or touches a paid resource.
- Rule update. The API changes only a rule's description; delete and
  create cover it.
- `tags` and `zoneId` on creates, until someone needs them.
- Finding a created resource by listing inside the SDK.
- Editing groups that OpenTofu manages. The wiki warns that a CLI change
  to such a group drifts from its state.

## Security groups and rules

All methods live in `network`, next to the existing group and rule reads.

```go
func (c *Client) CreateSecurityGroup(ctx context.Context, in *CreateSecurityGroupInput) (*CreateSecurityGroupOutput, error)
func (c *Client) UpdateSecurityGroup(ctx context.Context, in *UpdateSecurityGroupInput) (*UpdateSecurityGroupOutput, error)
func (c *Client) DeleteSecurityGroup(ctx context.Context, in *DeleteSecurityGroupInput) (*DeleteSecurityGroupOutput, error)
func (c *Client) CreateSecurityGroupRule(ctx context.Context, in *CreateSecurityGroupRuleInput) (*CreateSecurityGroupRuleOutput, error)
func (c *Client) DeleteSecurityGroupRule(ctx context.Context, in *DeleteSecurityGroupRuleInput) (*DeleteSecurityGroupRuleOutput, error)

var (
	ErrSecurityGroupInUse = errors.New("network: security group in use")
	ErrSystemGroup        = errors.New("network: system security group")
	ErrNotSettled         = errors.New("network: write accepted but not settled")
	ErrFailed             = errors.New("network: write failed on the server")
)
```

"(r)" marks `vngcloud:"required"`.

| Operation | Input | Output |
|-|-|-|
| `CreateSecurityGroup` | `Name` (r), `Description`, `NoWait` | `{SecurityGroup}` |
| `UpdateSecurityGroup` | `SecurityGroupID` (r), `Name` *string, `Description` *string | `{SecurityGroup}` |
| `DeleteSecurityGroup` | `SecurityGroupID` (r) | `{}` |
| `CreateSecurityGroupRule` | `SecurityGroupID` (r), `Direction` (r), `EtherType`, `Protocol` (r), `PortRangeMin`, `PortRangeMax`, `RemoteIPPrefix` (r), `Description` | `{SecurityGroupRule}` |
| `DeleteSecurityGroupRule` | `SecurityGroupID` (r), `SecurityGroupRuleID` (r) | `{}` |

### Create and update a group

- `CreateSecurityGroup` sends `name` and `description`, then waits (see
  [Group wait](#group-wait)). The Output is the settled read, or with
  `NoWait` the mapped create response.
- `UpdateSecurityGroup` follows ADR 0002 rule 3. An update with no
  non-nil field is `ErrInvalidInput`, and nothing is sent. The API replaces
  both fields, so the SDK reads the group, sets each non-nil field on what
  it read, and sends both. A nil `Description` resends the current one.
  The last writer wins; the API has no condition field.
- Update refuses a group whose read shows `IsSystem` or `System`, with
  `ErrSystemGroup`, before sending. Renaming the default group is never
  intended.
- The update response is taken from a read after the write, since its
  shape is not verified.

### Rules

`CreateSecurityGroupRule` sends every field it has. Per ADR 0002 rule 5,
the SDK checks shape, not value sets: `Direction`, `Protocol`, and
`EtherType` go to the server as given, so a new protocol never needs an SDK
release. The shape checks, all before any request, each `ErrInvalidInput`:

| Field | Check |
|-|-|
| `RemoteIPPrefix` | Parses with `netip.ParsePrefix`. A bare address is refused; a single host is written `/32` or `/128` |
| `EtherType` | Empty means the prefix's family: `IPv4` or `IPv6`. When given as `IPv4` or `IPv6`, it must match the prefix family |
| `PortRangeMin`, `PortRangeMax` | Each 0 to 65535. `PortRangeMax` 0 means equal to `PortRangeMin`. Min above max is refused |
| Ports for `tcp` or `udp` (any case) | `PortRangeMin` at least 1. All ports must be written `1` to `65535` |
| `Description` | Sent as given |

Safe defaults:

- `RemoteIPPrefix` is required. The SDK never defaults it to
  `0.0.0.0/0`; opening to the world must be written out.
- `Protocol` is required. The SDK never defaults it to `any`.
- A new group admits no inbound traffic until a rule allows it. The SDK
  keeps the server's default egress rules; `delete-security-group-rule`
  removes them.

How the API encodes "all ports" for `any` and the ICMP type and code for
`icmp` is a live check. Until it is known, the wiki shows only `tcp` and
`udp` rules with explicit ports.

### Delete

- `DeleteSecurityGroup` reads the group first. It returns `ErrSystemGroup`
  for a system group, and `ErrSecurityGroupInUse` when
  `ListServersBySecurityGroup` returns any server, and sends nothing.
  The server's own refusal is the final guard, because a group can be in
  use by more than servers: an error whose message contains
  `securitygroupinuse` (case-insensitive) also wraps
  `ErrSecurityGroupInUse`, whatever its status.
- `DeleteSecurityGroupRule` lists the group's rules first and returns
  `NotFound`, sending nothing, when the rule is not in that group. VNG
  Cloud's SDK sends the literal `undefined` as the group ID in this path,
  which suggests the server ignores it; without the check, a wrong
  group ID could delete a rule from another group.
- Group delete is taken as synchronous: the Output is `{}` after the 204.
  The live checks decide whether it needs a wait.

### Group wait

Per ADR 0002 rule 7, `CreateSecurityGroup` waits unless `NoWait` is set.
It polls `GetSecurityGroup` every 2 seconds, honours `ctx`, and stops
after 60 seconds, with an injected clock, as in [vDNS](dns.md#waits).

| Status | Result |
|-|-|
| `ACTIVE` | Settled; the Output holds the read |
| `ERROR` | Output and an error wrapping `ErrFailed` |
| `CREATING` or unknown | Keep polling |
| Bound reached | Output and an error wrapping `ErrNotSettled`, saying the group exists and the create must not be repeated |

A 404 during the wait keeps polling, since a new group may not be readable
at once. No other write waits unless the live checks show it must.

## SSH keys

All methods live in `compute`, next to `ListSSHKeys`.

```go
func (c *Client) GetSSHKey(ctx context.Context, in *GetSSHKeyInput) (*GetSSHKeyOutput, error)
func (c *Client) ImportSSHKey(ctx context.Context, in *ImportSSHKeyInput) (*ImportSSHKeyOutput, error)
func (c *Client) CreateSSHKey(ctx context.Context, in *CreateSSHKeyInput) (*CreateSSHKeyOutput, error)
func (c *Client) DeleteSSHKey(ctx context.Context, in *DeleteSSHKeyInput) (*DeleteSSHKeyOutput, error)
```

| Operation | Input | Output |
|-|-|-|
| `GetSSHKey` | `SSHKeyID` (r) | `{SSHKey}` |
| `ImportSSHKey` | `Name` (r), `PublicKey` (r) | `{SSHKey}` |
| `CreateSSHKey` | `Name` (r) | `{SSHKey; PrivateKey vngcloud.Secret}` |
| `DeleteSSHKey` | `SSHKeyID` (r) | `{}` |

- `SSHKey` drops its `PrivateKey` field, so no read model can hold a
  private key. The field was empty on every read so far. Breaking.
- `ImportSSHKey` checks shape before any request: `PublicKey` is one line
  after trimming, and it must not contain `PRIVATE KEY`. The second check
  stops a caller who passes the private key file by mistake from sending
  it. The error never quotes the value. Key type and size stay on the
  server.
- `CreateSSHKey` sets `transport.Request.Sensitive`, so the response
  capture hook never sees the response and a decode error never quotes
  its body. The private key is decoded straight into `vngcloud.Secret`,
  whose `String`, `Format`, `LogValue`, `MarshalJSON`, and `MarshalText`
  give `[redacted]`; only `Reveal()` returns it. A 201 without an ID or
  a private key is an `*APIError`; the message says a key may exist and
  names `list-ssh-keys`.
- `vngcloud.Secret` and `Sensitive` are defined in
  [vStorage](storage.md#secrets). Whichever of the two features ships
  first builds them to that contract.
- Key writes take no wait unless the live checks show a status other than
  final on the create response.

The wiki recommends `import-ssh-key` with a key made by `ssh-keygen`: the
private key then never leaves the owner's machine. With `create-ssh-key`,
GreenNode generated the key and saw it.

## Identifiers and retries

- Every operation that puts an ID in a path checks it with
  `core.CheckPathID` (`^[A-Za-z0-9-]+$`) before any request, including
  `GetSecurityGroup`, `ListServersBySecurityGroup`,
  `ListSecurityGroupRules`, and `GetSSHKey`, which today send any value.
- Creates are `POST`: the transport retries only after a 429 or a failed
  dial (ADR 0002 rule 2). After a 5xx or a network error the resource may
  exist, and the error says how to check:
  - A group: `list-security-groups --name <name>`, then match the name
    exactly. Names are unique, so a rerun with the same name fails rather
    than making a second group.
  - A rule: `list-security-group-rules`. A rerun of the same rule is
    refused as a duplicate, so no second rule appears.
  - An imported key: `list-ssh-keys --name <name>`, match exactly.
  - A created key: the same list; a key found that way has lost its
    private key, so delete it.
- Update and delete keep the transport's retries. A retried delete that
  finds the resource gone returns `NotFound`.
- The CLI never retries a write.

## CLI

| Command | Kind | Needs `--yes` | Release |
|-|-|-|-|
| `network create-security-group` | Write | No | V1 |
| `network update-security-group` | Write | No | V1 |
| `network delete-security-group` | Write, destructive | Yes | V1 |
| `network create-security-group-rule` | Write | Yes when world-open ingress | V1 |
| `network delete-security-group-rule` | Write, destructive | Yes | V1 |
| `compute get-ssh-key` | Read | No | V2 |
| `compute import-ssh-key` | Write | No | V2 |
| `compute create-ssh-key` | Write | No | V2 |
| `compute delete-ssh-key` | Write, destructive | Yes | V2 |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- All Input fields are scalars, so every field is a flag by the usual
  reflection: `--name`, `--description`, `--security-group-id`,
  `--security-group-rule-id`, `--direction`, `--ether-type`, `--protocol`,
  `--port-range-min`, `--port-range-max`, `--remote-ip-prefix`,
  `--ssh-key-id`, `--public-key`, `--no-wait`.
- World-open ingress means `Direction` `ingress` and a prefix of length 0
  (`0.0.0.0/0` or `::/0`). Its create needs `--yes`: an open port is
  exposed to every scanner while it lasts, as a public bucket's objects
  are ([vStorage](storage.md)). A prefix of length 0 is checked after
  parsing, so `0.0.0.0/00` cannot slip past.
- `--public-key` is not a secret and may come from argv:
  `--public-key "$(cat ~/.ssh/id_rsa.pub)"`.

```sh
vngcloud network create-security-group --name web
vngcloud network create-security-group-rule --security-group-id <id> \
  --direction ingress --protocol tcp --port-range-min 443 \
  --remote-ip-prefix 0.0.0.0/0 --yes
```

### create-ssh-key

`compute create-ssh-key` needs `--secret-file <path>` and cannot print the
private key. It follows [CLI secret files](cli.md#secret-files) and the
steps of [create-s3-key](storage-cli.md#create-s3-key):

1. Before any request, the parent directory must exist and nothing may
   exist at the path, symlinks included; otherwise it exits 2.
2. After the create, it opens the path with
   `O_CREATE|O_EXCL|O_NOFOLLOW` and mode 0600, writes the private key as
   returned with one trailing newline, syncs, and closes. `ssh -i` reads
   the file as it is.
3. If the write fails, it removes any partial file, deletes the new key,
   and exits 1 with `SecretFileFailed`. If that delete fails, the error
   names the key ID so a person can delete it.
4. Stdout gets the key without the secret: `PrivateKey` prints as
   `[redacted]`, and a `SecretFile` field names the path.

No flag prints the private key: stdout reaches transcripts and CI logs.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, bad prefix, port, or public key shape, empty update | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| World-open ingress without `--yes` | No request | `InvalidUsage`, 2 |
| `--secret-file` exists or its directory is missing | No request | `InvalidUsage`, 2 |
| Unknown group, rule, or key; rule not in the named group | `NotFound` | `NotFound`, 4 |
| Update or delete of a system group | `ErrSystemGroup`, no request | `SystemSecurityGroup`, 1 |
| Delete of a group with servers, or the server's in-use refusal | `ErrSecurityGroupInUse` | `SecurityGroupInUse`, 1 |
| Group `ERROR` after create | `ErrFailed`, with Output | `WriteFailed`, 1 |
| Group not `ACTIVE` within the wait | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| Duplicate name or rule, quota, bad key | The server's `*APIError` | 1 |
| Private key file write failed after create | Key deleted | `SecretFileFailed`, 1 |
| 5xx or network error on a create | The error; the message names the list to check | 1 |

The CLI error list in [CLI](cli.md#errors-and-exit-codes) gains
`SystemSecurityGroup` and `SecurityGroupInUse`. `WriteFailed`,
`NotSettled`, and `SecretFileFailed` keep their meaning; `NotSettled`
prints the Output on stdout, as for vDNS.

## Security

- Every write gets an adversarial review before its release. The review
  checks: no create retry after a 5xx; path ID checks on every call,
  reads included; the rule-in-group check before a rule delete; the
  system and in-use checks send nothing; an empty update sends nothing;
  `--yes` on deletes and world-open ingress; read-only refusal of every
  write; the private key never reaches stdout, stderr, `--debug`, an
  error, a response capture, or a fixture; `--secret-file` refuses
  existing paths and symlinks and creates mode 0600; the orphan key is
  deleted after a failed write; `SSHKey` has no private key field; and an
  import refuses private key text.
- A rule is an access grant. The wiki shows narrow prefixes first and
  explains the `--yes` on world-open ingress.
- Group names, prefixes, public keys, and IDs are account data. Fixtures
  use `<id>`, `<name>`, `<cidr>`, `<public-key>`, and `<secret>`.

## Testing and live checks

Unit tests and the live checks that must pass before code merges are in
[vServer free writes: checks](vserver-writes-checks.md).

## Releases

| Release | Content |
|-|-|
| V1 | `network` group create, update, and delete with the group wait; rule create and delete with the rule-in-group check; path ID checks on the group and rule reads; CLI commands and world-open `--yes` |
| V2 | `compute` `GetSSHKey`, `ImportSSHKey`, `CreateSSHKey`, `DeleteSSHKey`; `SSHKey.PrivateKey` removed (breaking); `vngcloud.Secret`, `Sensitive`, and `--secret-file` if vStorage has not shipped them; CLI commands |

Each is numbered when it ships. V1 changes no existing method, except that
the group and rule reads reject a malformed ID. V2 breaks callers that
read `SSHKey.PrivateKey`. The `Network` and `Compute` wiki pages gain the
writes, the retry advice, and the OpenTofu drift warning.

## Owner decisions

The owner approved each recommendation below.

1. Package ownership. Settled by [SDK and CLI](sdk-and-cli.md): groups and
   rules in `network`, SSH keys in `compute`.
2. Release split. Options: one release; groups and rules (V1) then keys
   (V2). Recommend V1 then V2: keys carry the secret handling, the
   riskiest part.
3. Group update. Options: include (name and description, read-merge);
   leave out. Recommend include: it is free and small.
4. Rule update (description only). Recommend leave out; delete and create
   cover it.
5. World-open ingress. Options: `--yes` for ingress from a length-0
   prefix; no guard. Recommend `--yes`.
6. Default egress. Options: keep the server's allow-all egress; delete it
   after create; add a flag. Recommend keep: it matches the console and
   AWS, and `delete-security-group-rule` removes it.
7. Group delete guards. Options: pre-read refusing system groups and
   groups with servers, plus the server refusal; server refusal only.
   Recommend the pre-read.
8. Rule delete guard. Options: list the group's rules first and refuse a
   rule not in it; trust the path. Recommend the list.
9. Server-generated keys. Options: ship `create-ssh-key` with
   `--secret-file`; ship import only. Recommend both, with the wiki
   preferring import.
10. `SSHKey.PrivateKey`. Options: remove it (breaking); change its type to
    `vngcloud.Secret` (also breaking). Recommend remove.
11. `vngcloud.Secret` ownership if V2 ships before vStorage's key release.
    Recommend V2 builds it to the [vStorage](storage.md#secrets) contract.
12. Port rules. Recommend `tcp` and `udp` need a port of at least 1, so
    "all ports" is written `1` to `65535`, and the SDK never defaults a
    protocol or prefix.

## Open questions

- Whether the IAM gateway accepts these writes.
- The encoding of "all ports" and ICMP type and code.
- The status of the in-use refusal.
- Whether a rule delete ignores the group ID in its path.
