# Setup estimate fixture provenance

These fixtures are sanitized raw response bodies from read-only price-quote
POSTs made by the owner's test account in region hcm-3, zone HCM-1B. They
support the [setup cost estimate design](../../docs/design/cost-estimate.md)
and its [evidence](../../docs/design/cost-estimate-api.md). A throwaway probe
sent exactly four server quotes and one public-VIP quote with fixed bodies
and a hard send cap. No order, create, or other write was sent.

## Sanitation

- The root and data volume type identifier inside each disk description is
  replaced by `vtype-1`, matching the
  [pricing discovery fixtures](discovery-provenance.md).
- No other identifier, account value, or host appears in the bodies.
  Field presence, nullability, wrappers, types, labels, prices, and array
  order are otherwise unchanged.
- The plain server quote matched
  [`compute/discovery_quote_server_s2.json`](../compute/discovery_quote_server_s2.json)
  exactly after the same replacement, so that fixture remains the plain
  baseline and no new plain file was added.

## Files

Each request went to POST `{B}/v1/price` and returned HTTP 200 with
`application/json`. Request bodies use synthetic selectors: `flav-1` is
`s2-general-1x2`, `img-1` is an unlicensed Ubuntu image, and `vtype-1` is the
SSD 3000 IOPS volume type. Encrypted cases set only `encryptionVolume`; no
encryption type identifier enters the quote body.

| Fixture | `resourceInfo` beyond the shared server fields |
|-|-|
| `estimate_quote_server_data.json` | `"encryptionVolume":false,"dataDiskSize":1,"dataDiskTypeId":"vtype-1"` |
| `estimate_quote_server_encrypted_root.json` | `"encryptionVolume":true` |
| `estimate_quote_server_encrypted_data.json` | `"encryptionVolume":true,"dataDiskSize":1,"dataDiskTypeId":"vtype-1"` |
| `estimate_quote_public_flagged.json` | Public VIP body, below |

Shared server fields: `"zoneId":"HCM-1B","flavorId":"flav-1","imageId":"img-1",
"rootDiskSize":20,"rootDiskTypeId":"vtype-1","period":1,"isPoc":false`, with
`"resourceType":"server","action":"create"`.

Public VIP body:
`{"resourceType":"public-vip","action":"create","resourceInfo":{"type":"public","isBuyMorePo":false,"isPoc":false}}`.

## Observations

- The server quote accepts the paired `dataDiskSize` and `dataDiskTypeId`
  spelling and returns a separate `DATA DISK` row.
- Encryption adds a separate `CES` row described as `Enabled`. Its amount in
  these captures is one observation, not a universal rate.
- Top-level `optimumPrice` equals the sum of the property rows in every case.
