# Encrypted Volumes

Status: Accepted (2026-10-10).

This design lets `volume.CreateVolume` and `compute.CreateServer` ask for
encrypted disks, and lets their quotes price the encryption. It extends
[vServer paid writes](vserver-paid-writes.md): the price guard, waits,
duplicate-name checks, and retries there apply unchanged. Calls, bodies,
and prices are in [vServer paid writes: API](vserver-paid-writes-api.md).

## What is known

- `volume.ListEncryptionTypes` calls
  `GET v1/{projectId}/volumes/encryption_types`, a project-level read with
  no zone. On the test account it returns the strings
  `aes-xts-plain64_128` and `aes-xts-plain64_256` (live). The SDK maps each
  string to an `EncryptionType` whose `ID`, `Name`, and `Value` are that
  string. The type ID this design sends is that string.
- The vServer reference lists `encryptionType` on the volume create body,
  and on the server create lists `encryptionVolume` (a required bool) plus
  encryption type fields whose key names this repository has not recorded.
- The server quote with `encryptionVolume` true and no type key added a
  `CES` line of 85,140 VND to 347,800 for `s2-general-1x2`, giving
  432,940 (live, 2026-09-28). 85,140 is 30% of the flavor's 283,800, so
  server encryption is priced on the flavor, not the disk size.
- The volume quote has never been sent with an encryption key, so whether
  an encrypted volume costs more than 32,000 VND for 10 GB is unknown.
- A volume read (`GetUnderlyingVolume`) carries `encryptionType`, null for
  a plain volume (fixture).
- VNG Cloud's SDK maps the attach error `cannot attach encryption volume`.
  When an encrypted volume can be attached, and to which servers, is
  unknown. This matters for an encrypted data volume added after create.

## Body keys

| Write | Key | Value | Source |
|-|-|-|-|
| Volume create | `encryptionType` | The type ID | Reference; confirm in check 1 |
| Volume create | `encryptionVolume` | true | Only if check 1 shows the console sends it |
| Server create | `encryptionVolume` | true when either disk is encrypted, else false | Reference, live quote |
| Server create | root disk type key | The root type ID | Name from check 1 |
| Server create | data disk type key | The data type ID | Name from check 1 |

- The SDK sends no encryption key when no type ID is set. The volume body
  keeps its current keys exactly; the server body keeps `encryptionVolume`
  false.
- Check 1 settles every key name before the SDK is written. Until then
  the root and data disk keys are written here as `rootDiskEncryptionType`
  and `dataDiskEncryptionType`, the names VNG Cloud's Terraform provider
  is believed to use; this is unverified. If the console sends other
  names, the SDK uses the console's and this table is amended.
- If the console sends only `encryptionVolume` with no type key for the
  server, the server fields collapse to one bool; see
  [owner decisions](#owner-decisions).

## SDK

### Volumes

`volume.CreateVolumeInput` gains:

| Field | Rule |
|-|-|
| `EncryptionTypeID` | Optional. A type ID from `ListEncryptionTypes`. Checked with `core.CheckPathID` |

- `QuoteCreateVolume` takes the same Input, so it gains the field. When
  set, the quote body carries the volume body's encryption keys with the
  same values, so the guard prices encryption.
- The volume quote body is priced-only
  ([quote requests](vserver-paid-writes-api.md#quote-requests)); the
  encryption keys join it because they can change the price. The drift
  test sets `EncryptionTypeID` and checks each encryption key in the quote
  equals the one in the create body.
- `Volume` and `UnderlyingVolume` keep their fields. The CLI's
  `create-volume` output shows what the create returns; a caller confirms
  encryption with `volume get-underlying-volume`.

### Servers

`compute.CreateServerInput` gains:

| Field | Rule |
|-|-|
| `RootDiskEncryptionTypeID` | Optional. Encrypts the root disk. Checked with `core.CheckPathID` |
| `DataDiskEncryptionTypeID` | Optional. Encrypts the data disk; refused without `DataDiskSize` and `DataDiskTypeID` |

- The names follow `RootDiskTypeID` and `DataDiskTypeID`, so the flags
  read as one family.
- `encryptionVolume` is true when either field is set.
- `QuoteCreateServer` takes the same Input. Its quote body sends
  `encryptionVolume` as today, now from the fields, plus each type key
  that check 1 shows the console's price request sends. The drift test
  sets both fields and compares every encryption key.
- `DataDiskEncryptionTypeID` is the supported way to get an encrypted data
  volume on a server until check 6 shows `AttachVolume` accepts an
  encrypted volume.

### Validation

- A type ID gets `core.CheckPathID`, like every body ID: empty after
  trimming, `.`, `..`, `/`, `?`, and control characters are
  `ErrInvalidInput` before the quote. Both live IDs pass.
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
```

- The `CLI-Volume` and `CLI-Compute` wiki pages name
  `list-encryption-types` as the source of the ID, state the price
  effect measured in the live checks, and state the attach rule.
- `--yes` and `--max-price` rules do not change: the price guard already
  covers the higher price.

## Security

- Encryption is opt-in. The default bodies stay as today, so no existing
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

Free, before the SDK change:

1. In the `hcm-3` console, fill the create-volume form with encryption on
   and the create-server form with root and data disk encryption on, and
   capture each `POST v1/price` request body. Stop before ordering.
   Record every key that carries encryption and its value.

Free, after the SDK change:

2. `QuoteCreateVolume`, 10 GB SSD in `HCM03-1C`, with
   `aes-xts-plain64_256` and with `aes-xts-plain64_128`: `OptimumPrice`
   against 32,000 and the `propertiesPrice` lines.
3. `QuoteCreateServer`, `s2-general-1x2`, Ubuntu 24.04, 20 GB SSD root,
   with `RootDiskEncryptionTypeID`: `OptimumPrice` against 347,800 and
   432,940, and a `CES` line. Then with a 10 GB data disk and
   `DataDiskEncryptionTypeID` only.

Paid, cleaned up, on the test account (a few hundred VND after refunds):

4. `CreateVolume`, 10 GB SSD, `aes-xts-plain64_256`, name
   `vngcloud-live-<8 hex>`, `MaxPrice` equal to check 2's quote: status,
   time to `AVAILABLE`, and `GetUnderlyingVolume` reading
   `encryptionType` equal to the ID. A balance drop matching the quote.
5. `DeleteVolume`: status, time, and the balance back to its start
   (refund on delete).

Paid, needs owner approval (about 433,000 VND charged, refunded to the
minute on delete; cap 500,000):

6. `CreateServer` as in check 3 with both type IDs: time to `ACTIVE`, and
   `encryptionType` on the boot and data volumes. Create a 10 GB encrypted
   volume as in check 4 and `AttachVolume` it to the server: record
   success or the refusal. Stop the server, detach, and delete everything
   as in the [cleanup](vserver-paid-writes-checks.md#cleanup).

The unit tests add: each body with and without each field, the drift test
with both fields, `CheckPathID` refusals for the new fields, and
`DataDiskEncryptionTypeID` without a data disk refused. CLI golden tests
cover the new flags on the quote and create commands.

## Release

| Release | Content | Tag |
|-|-|-|
| E1 | `CreateVolumeInput.EncryptionTypeID`, `CreateServerInput.RootDiskEncryptionTypeID` and `DataDiskEncryptionTypeID`, their quote keys and drift test; CLI flags; wiki | One minor tag after checks 1 to 5 pass, and check 6 if approved |

E1 breaks no caller: every new field is optional and its zero value sends
the current body.

## Owner decisions

All four are approved as recommended, including the paid server check with a
500,000 VND cap.

1. Scope. Options: volume and server together in E1; volume only, server
   later. Approved: together, if the owner approves check 6.
   Otherwise ship the volume fields alone and leave the server fields for
   a later tag, since a server create with encryption is unverified.
2. Server field shape. Options: one ID per disk
   (`RootDiskEncryptionTypeID`, `DataDiskEncryptionTypeID`); one ID for
   both; a bool. Approved: one ID per disk, matching the per-disk type
   fields. If check 1 shows the console sends only `encryptionVolume`,
   use one bool, `EncryptVolumes`, instead.
3. Type validation. Options: path check only, leaving an unknown ID to
   the server; check against `ListEncryptionTypes` first. Approved:
   path check only.
4. Check 6. Options: approve one paid server run with encryption; skip
   it. Approved: approve. It is the only way to learn the server's
   type keys work and whether an encrypted volume attaches.
