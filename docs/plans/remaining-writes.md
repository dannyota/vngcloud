# Remaining writes

Continue the accepted designs. Release one feature per tag. Do not run paid or uncleanable writes without the owner's approval.

## Authorities

- [vStorage](../design/storage.md) owns the storage releases S2 to S6.
- [Verification](../../instructions/verification.md) and [live data](../../instructions/live-data.md) govern checks and releases.

## Shipped

On 2026-10-09: DHCP options sets (v0.38.0), private virtual IPs (v0.39.0), resource tags (v0.40.0), vStorage S1 reads (v0.41.0), the paid vServer writes P2 to P5 (v0.42.0 to v0.45.0), the paid load balancer writes L2 to L6 (v0.46.0 to v0.50.0), and the vMonitor log alarm writes (v0.51.0, checked on a Pro log project bought and deleted the same day for 637 VND net). Every paid write was checked live on the test account with 2,000,000 VND of credit; deletes refunded the unused value and the day cost about 574 VND net. Both paid designs' checks docs hold the results. v0.51.1 (same day) made every vStorage call send the `region` header, so the S1 reads find the project. v0.52.0 (same day) shipped S2: bucket create and delete with the delete wait, checked live on the test project. v0.52.1 decodes the IAM accounts API errors wrapper into the error code. v0.53.0 shipped S3: S3 keys on the vStorage console API with `create-s3-key --secret-file`, checked live including the ten-key limit and an S3 listing. v0.54.0 shipped S4: keys bound to service accounts through the console attach calls and an explicit principal write, checked live.

## Work order

| Branch | State | Remaining work | Release gate |
|-|-|-|-|
| vCDN | C1 (v0.56.0); C1b and C2 in v0.60.0; C3 path purge in v0.69.0 | C4: certificate import, enable, disable, and delete. Create stays deferred because the API refuses valid bodies | C4 uses a self-signed test certificate and a portal-made test CDN; paid or uncleanable writes still need owner approval |

## Open on the account

- vStorage project purchase works for the IAM user once the project name is filled: the console form shows Billing method (Pay monthly only; no pay-as-you-go on this account), project type (Gold or Instant Archive), and a 30 GB minimum. The `users/details?generated=true` code 114 error is unrelated and harmless. The create flow is `POST billing-api/v2/price` then `POST internal/v2/orders` (`resourceType: object_storage`, `action: create`, `paymentType: manual`, `resourceInfo: {projectName, purchaseTypeId: 4, projectType: 1, quota, archivePeriod: 0, billingTimeType: block}`), which redirects to the payment console checkout; untick Auto-renew there. The console lists projects with `GET internal/v1/projects?reload=false&load_all=true`; the server fills results only when the request carries the console's `region: <region id>` header, which v0.51.1 added.
- The VPC quota is 2 and one slot is held by `stuck-acl-vpc-*`, which the server cannot delete (needs a support ticket). Live tests that need a VPC borrow an existing one through `VNGCLOUD_LIVE_NETWORK_VPC_ID` when the quota is full.
- vStorage shipped through v0.55.0; the test project was deleted on 2026-10-09 and the unused value was refunded at once (balance 1,968,789 to 1,998,684 VND). A later vStorage live run needs a new project (console purchase, 30,000 VND, refunded on delete).
- vCDN C1b and C2 were checked on a portal-made test CDN and the CDN was deleted. The corrected live write test passed on 2026-10-10. Each later CDN write run needs another portal-made test CDN; API create stays deferred.
- The budget `vngcloud-live-cap` (1,000,000 VND a month, alert at 50%) stays on the account for later paid runs.

## Encrypted volumes (requested by aboutme, 2026-10-10)

State: SDK and CLI done, reviewed, pushed (8673504); tags as v0.59.0 on green CI. The gitleaks allowlist names the encryption type enum values (a generic-api-key false positive on the fixture's type names). Found and fixed on the way: `volume.ListVolumesByServer` never read the server's `volumes` envelope key (0 rows); the fixture now comes from a live capture. The extra paid check for aboutme ran: an encrypted volume does not attach to a server with plain disks (400 `cannot attach encryption volume`); it attaches to a server created with encrypted disks. A dns test that raced its own cancel was made deterministic.

`volume create-volume` and `compute create-server` cannot ask for an encrypted volume: the bodies send `encryptionVolume: false` and `CreateVolumeInput` has no encryption field, although `volume list-encryption-types` lists `aes-xts-plain64` 128 and 256. aboutme's production shape needs an encrypted data volume. Work: architect designs `EncryptionTypeID` on the volume create and quote (and the server root disk), the sdk adds it with the quote pricing it (live: an encrypted boot disk added about 30% to the flavor price on 2026-09-28; measure the data volume), the cli adds `--encryption-type-id`; one paid live check (create and delete an encrypted 10 GB volume, refund on delete).

## CLI usability (from the first external agent run, 2026-10-10)

An agent set up the CLI read-only and quoted servers end to end. Design: `docs/design/cli-usability.md`. U1 (items 1 and 3's note) shipped as v0.57.0; items 5 to 7 as v0.56.1; U2 (item 2) as v0.58.0. Item 4 stays a doc note. This section is done. Its findings, in order of value:

1. ID discovery for a quote takes five calls (zones, flavor zones, flavors, volume type zones, volume types) with same-named flavor zones that return no flavors. Wanted: `compute list-flavors --zone-id <zone> [--name <flavor>]` fanning out over the zone's flavor zones client side, `volume list-volume-types --zone-id <zone> [--iops N]`, and a wiki page listing the IDs `create-server` needs and where each comes from. Architect decides the shape.
2. `quote-create-server` requires `--vpc-id`, `--subnet-id`, `--security-group-id`, `--ssh-key-id`, `--name` although the gateway ignores them for pricing; placeholders work. Architect decides: optional on the quote command, or a documented placeholder.
3. Withdrawn by its author: every list prints `{"Items": [...]}`; the null came from a `--query` without the `Items` prefix. Remaining task: one line in the CLI wiki saying every list returns `{"Items": [...]}` so a `--query` starts with `Items[...]` (cli, with U1).
4. Quote output shows the root disk's type ID where its size belongs (gateway text); the CLI could render the size from its own input. Low.
5. `portal list-zones` reports `HCM03-1A` `IsEnabled=false`, "Contact to enable", yet quotes and creates work there: a wiki note.
6. Setup recipe for a read-only agent profile in the Configuration wiki page (`configure set read_only 1`, the credential keys, `VNGCLOUD_REGION` has no default), and `configure list` should print the file path when values are empty.
7. `--version` should work beside `vngcloud version`.

## Verification state

Local `make check` passed on master on 2026-10-09 with Go 1.27.2. No release is approved solely by local tests.
