# Encrypted Volumes

Status: Accepted (2026-10-10). Live checks passed on 2026-10-10.

This design lets `volume.CreateVolume` and `compute.CreateServer` ask for
encrypted disks, and lets their quotes price the encryption. It extends
[vServer paid writes](vserver-paid-writes.md): the price guard, waits,
duplicate-name checks, and retries there apply unchanged. Calls, bodies,
and prices are in [vServer paid writes: API](vserver-paid-writes-api.md).

## What is known

- `volume.ListEncryptionTypes` calls
  `GET v1/{projectId}/volumes/encryption_types`, a project-level read with
  no zone. On the test account it returns
  `[{"key","displayKey"}]` objects with keys `aes-xts-plain64_128` and
  `aes-xts-plain64_256` (live). The SDK fills `ID`, `Name`, and `Value`
  of each `EncryptionType` from `key`. It also accepts a list of plain
  strings. The type ID the SDK sends is that key.
- The console's server price request carries `encryptionVolume: true` and
  no type key. The type keys `rootDiskEncryptionType` and
  `dataDiskEncryptionType` appear only on the server order body (console
  bundle; confirmed live by check 6).
- The console's volume price request carries no encryption key.
- Server encryption adds a `CES` quote line priced on the flavor, not the
  disk size: 85,140 VND, 30% of the 283,800 `s2-general-1x2` flavor, with
  either type (live).
- An encrypted volume costs the same as a plain one: 10 GB SSD quotes
  32,000 VND with either type (live).
- `GetVolume` and `ListVolumes` rows carry `encryptionType`, null for a
  plain volume. `GetUnderlyingVolume` does not carry it (live).
- An encrypted volume attaches only to a server whose disks are encrypted.
  Attaching one to a server with plain disks fails with HTTP 400
  `BadRequest` and a message containing `cannot attach encryption volume`
  (live). See [live results](#live-results).

## Body keys

| Write | Key | Value |
|-|-|-|
| Volume create | `encryptionType` | The type ID |
| Volume quote | `encryptionType` | The type ID |
| Server create | `encryptionVolume` | true when either disk is encrypted, else false |
| Server create | `rootDiskEncryptionType` | The root type ID |
| Server create | `dataDiskEncryptionType` | The data type ID |
| Server quote | `encryptionVolume` | As on the create |

- The SDK sends no encryption key when no type ID is set. The volume body
  keeps its plain keys exactly; the server body keeps `encryptionVolume`
  false.
- The volume quote sends `encryptionType` although the console's does not,
  so the quote and the price guard share one body. The key does not change
  the price (live).
- The server quote sends only `encryptionVolume`, as the console does. The
  type keys go on the create only.

## SDK

### Volumes

`volume.CreateVolumeInput` gains:

| Field | Rule |
|-|-|
| `EncryptionTypeID` | Optional. A type ID from `ListEncryptionTypes`. Checked with `core.CheckTypeID` |

- `QuoteCreateVolume` takes the same Input, so it gains the field. When
  set, the quote body carries `encryptionType` with the create's value.
- The volume quote body is priced-only
  ([quote requests](vserver-paid-writes-api.md#quote-requests)). The drift
  test sets `EncryptionTypeID` and checks `encryptionType` in the quote
  equals the one in the create body.
- A caller confirms encryption with `volume get-volume`, which reads
  `encryptionType`. `get-underlying-volume` does not show it.

### Servers

`compute.CreateServerInput` gains:

| Field | Rule |
|-|-|
| `RootDiskEncryptionTypeID` | Optional. Encrypts the root disk. Checked with `core.CheckTypeID` |
| `DataDiskEncryptionTypeID` | Optional. Encrypts the data disk; refused without `DataDiskSize` and `DataDiskTypeID` |

- The names follow `RootDiskTypeID` and `DataDiskTypeID`, so the flags
  read as one family.
- `encryptionVolume` is true when either field is set.
- `QuoteCreateServer` takes the same Input. Its quote body sends
  `encryptionVolume` from the fields and no type key. The drift test sets
  both fields and checks `encryptionVolume` in the quote equals the
  create's.
- `DataDiskEncryptionTypeID` and a separate encrypted volume attached
  later both give an encrypted data volume on a server with encrypted
  disks (live).

### Identifiers

- A type ID gets `core.CheckTypeID`, pattern `^[A-Za-z0-9_-]+$`.
  `core.CheckPathID` refuses `_`, which every live type ID holds.
  `CheckTypeID` still refuses an empty value, `.`, `..`, `/`, `?`, spaces,
  and control characters as `ErrInvalidInput` before the quote. Both live
  IDs pass.
- The SDK does not check the ID against `ListEncryptionTypes`. The server
  refuses an unknown type, and that `*APIError` reaches the caller. A list
  read before every create would add a request and could go stale against
  the server's own check.
- `DataDiskEncryptionTypeID` without a data disk is `ErrInvalidInput`, in
  the same check as half a data disk, on the quote and the create.

### Errors

No new sentinel. A refused type, an encryption quota, or an attach refusal
is the server's `*APIError`, exit 1.

## CLI

The CLI's flag reflection turns the fields into flags. No new command.

| Command | New flags |
|-|-|
| `volume create-volume`, `quote-create-volume` | `--encryption-type-id` |
| `compute create-server`, `quote-create-server` | `--root-disk-encryption-type-id`, `--data-disk-encryption-type-id` |

```sh
vngcloud volume list-encryption-types
vngcloud volume quote-create-volume --zone-id <zone> --size 10 \
  --volume-type-id <type> --encryption-type-id aes-xts-plain64_256
vngcloud volume create-volume --name <name> --zone-id <zone> --size 10 \
  --volume-type-id <type> --encryption-type-id aes-xts-plain64_256 \
  --max-price <vnd>
vngcloud volume get-volume --volume-id <id>
```

- The `CLI-Volume` and `CLI-Compute` wiki pages name
  `list-encryption-types` as the source of the ID, state the price
  effect in [live results](#live-results), and name `get-volume` as the
  confirm step.
- `--yes` and `--max-price` rules do not change: the price guard already
  covers the higher price.

## Security

- Encryption is opt-in. The default bodies stay as before, so no existing
  caller pays more.
- The type ID is not secret. The SDK sends no key material; the server
  holds the keys (`encryptionKeyId` on the volume read). Nothing in this
  design logs or prints a key.
- The quote prices encryption before the order, so `MaxPrice` bounds it.
  The drift test keeps the guard's priced keys equal to the create's.
- The release gets the adversarial review that every paid write gets
  ([checks](vserver-paid-writes-checks.md#security-review)), focused on
  the drift test and on no encryption key reaching a body when unset.

## Live checks

Per [live data](../../instructions/live-data.md); log statuses, key names,
and prices only.

1. Console capture, free: the `POST v1/price` bodies for an encrypted
   volume and an encrypted server, stopping before the order.
2. `QuoteCreateVolume`, 10 GB SSD in `HCM03-1C`, with each type.
3. `QuoteCreateServer`, `s2-general-1x2`, Ubuntu 24.04, 20 GB SSD root,
   with `RootDiskEncryptionTypeID`; then with a 10 GB data disk and
   `DataDiskEncryptionTypeID` only.
4. `CreateVolume`, 10 GB SSD, `aes-xts-plain64_256`, name
   `vngcloud-live-<8 hex>`, `MaxPrice` equal to check 2's quote; read
   `encryptionType` back with `GetVolume`.
5. `DeleteVolume`, then wait for the refund.
6. `CreateServer` as in check 3 with both type IDs. Create an encrypted
   volume as in check 4, attach it, stop the server, detach, and delete
   everything as in the
   [cleanup](vserver-paid-writes-checks.md#cleanup).

Checks 4 and 5 run in a live test gated by `VNGCLOUD_LIVE_WRITE=1`,
`VNGCLOUD_LIVE_PAID_ENCRYPTED_VOLUME=1`, and `VNGCLOUD_LIVE_MAX_VND`.
Check 6 runs in one gated by `VNGCLOUD_LIVE_WRITE=1`,
`VNGCLOUD_LIVE_PAID_ENCRYPTED_SERVER=1`, and `VNGCLOUD_LIVE_MAX_VND`.
`VNGCLOUD_LIVE_MAX_VND` is the `MaxPrice` cap. After cleanup each test
waits up to 5 minutes for the balance to return within 1,000 VND of its
start, and fails otherwise.

The unit tests cover each body with and without each field, the drift
test with both fields, `CheckTypeID` refusals for the new fields, and
`DataDiskEncryptionTypeID` without a data disk refused. CLI golden tests
cover the new flags on the quote and create commands.

## Live results

All on 2026-10-10, test account, `hcm-3`, zone `HCM03-1C`, VND a month
with VAT. Everything was deleted and refunded.

| Check | Result |
|-|-|
| 2 | 10 GB SSD: 32,000 with either type, the same as plain |
| 3 | Root encrypted: 432,940 against 347,800, a `CES` line, either type |
| 3 | 10 GB data disk encrypted only: 464,940 against 379,800 |
| 4 | Charged 32,000; `AVAILABLE` in 22 s; `GetVolume` read `encryptionType` |
| 5 | Deleted in 8 s; refund posted within 5 minutes |
| 6 | 20 GB root (256), 20 GB data disk (128): quoted and ordered at 496,940 |
| 6 | `ACTIVE` in 2m10s; both volumes read their `encryptionType` |
| 6 | A separate encrypted volume attached, `IN-USE` in 17 s |
| 6 | It detached after a server stop |
| 7 | 10 GB `aes-xts-plain64_256` volume, plain-disk server: attach refused |

Rule: an encrypted volume attaches to a server with encrypted root and data
disks (check 6) and not to a server with plain disks (check 7). On a plain
server `AttachVolume` answers HTTP 400 `BadRequest` with a message
containing `cannot attach encryption volume`.

After cleanup the balance was 24 VND below its start for checks 2 to 6 and
8 VND below for check 7, after its refund.

## Open items

- `ListVolumesByServer` returned 0 rows for the check 6 server, which held
  2 volumes. The cause is unknown and unrelated to encryption. The decoder
  now reads the `volumes` key, as VNG Cloud's SDK does. A second paid run
  is capturing the raw body to confirm the shape.

## Release

| Release | Content | Tag |
|-|-|-|
| E1 | `CreateVolumeInput.EncryptionTypeID`, `CreateServerInput.RootDiskEncryptionTypeID` and `DataDiskEncryptionTypeID`, `core.CheckTypeID`, their quote keys and drift tests; CLI flags; wiki | Ships as v0.59.0 |

E1 breaks no caller: every new field is optional and its zero value sends
the plain body.

## Owner decisions

All four are approved as recommended, including the paid server check with
a 500,000 VND cap.

1. Scope: volume and server together in E1.
2. Server field shape: one ID per disk (`RootDiskEncryptionTypeID`,
   `DataDiskEncryptionTypeID`), matching the per-disk type fields. The
   order body takes a type key per disk, so the bool fallback is not
   needed.
3. Type validation: shape check only, leaving an unknown ID to the
   server.
4. Check 6: one paid server run with encryption, approved and run.
