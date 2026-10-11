# Pricing discovery fixture provenance

These fixtures are sanitized raw response bodies from read-only catalog GETs
and price-quote POSTs made by the owner's test account in region hcm-3. They
support the [pricing discovery design](../../docs/design/pricing-discovery.md);
the [API evidence](../../docs/design/pricing-discovery-api.md) records what the
probes showed. No order, create, or other write was sent.

## Sanitation

- Every resource and catalog identifier is replaced by a stable synthetic ID
  that is consistent across all fixtures: `flav-<n>` for flavors, `img-<n>`
  for images, `vtype-<n>` for volume types, `fz-<n>` for flavor zones, and
  `region-<n>` for storage regions. Zone names such as `HCM03-1C` are kept.
- Storage hosts become `<hostname>`, IP ranges become `<ip>`, the account URL
  becomes `<internal-url>`, the user mapping becomes `<account>`, and image
  license keys become `<secret>`.
- Flavor-zone names and descriptions in the product list are replaced with
  `Flavor zone <n>`.
- Success envelopes whose message field the capture withheld record it as
  `null`, matching the observed type in the sanitized call summaries.
- Field presence, nullability, wrappers, types, enum strings, prices, and
  array order are otherwise unchanged. Arrays are not trimmed.

## Files

Route templates use `{V}` for the vServer gateway, `{B}` for the price route
behind `pricing.GetQuote`, and `{Storage}` for the vStorage origin. Request
bodies use the same synthetic IDs as the fixtures.

|Fixture|Request|Synthetic request body|Status|
|-|-|-|-|
|`portal/discovery_zones.json`|GET `{V}/v1/{projectId}/zones`|`none`|200|
|`compute/discovery_product_zones.json`|GET `{V}/v1/{projectId}/flavor_zones/product`|`none`|200|
|`compute/discovery_platform_codes.json`|GET `{V}/v1/{projectId}/flavor_zones/codes`|`none`|200|
|`compute/discovery_families.json`|GET `{V}/v1/{projectId}/flavor_zones/families`|`none`|200|
|`compute/discovery_custom_zones_empty.json`|GET `{V}/v1/{projectId}/flavor_zones/customs`|`none`|200|
|`compute/discovery_custom_flavors_empty.json`|GET `{V}/v1/{projectId}/flavors/customs`|`none`|200|
|`compute/discovery_family_flavors.json`|GET `{V}/v1/{projectId}/flavors/families/{family}/platforms/{code}`|`none`|200|
|`compute/discovery_family_flavors_empty.json`|GET `{V}/v1/{projectId}/flavors/families/{family}/platforms/{code}`|`none`|200|
|`compute/discovery_legacy_flavors.json`|GET `{V}/v1/{projectId}/{flavorZoneId}/flavors`|`none`|200|
|`compute/discovery_os_images.json`|GET `{V}/v1/{projectId}/images/os`|`none`|200|
|`compute/discovery_quote_server_s2.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"encryptionVolume":false,"flavorId":"flav-1","imageId":"img-1","isPoc":false,"period":1,"rootDiskSize":20,"rootDiskTypeId":"vtype-1","zoneId":"HCM03-1C"},"resourceType":"server"}`|200|
|`compute/discovery_quote_server_s.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"encryptionVolume":false,"flavorId":"flav-34","imageId":"img-1","isPoc":false,"period":1,"rootDiskSize":20,"rootDiskTypeId":"vtype-1","zoneId":"HCM03-1C"},"resourceType":"server"}`|200|
|`volume/discovery_types.json`|GET `{V}/v1/{projectId}/{typeZoneId}/volume_types`|`none`|200|
|`volume/discovery_encryption_types.json`|GET `{V}/v1/{projectId}/volumes/encryption_types`|`none`|200|
|`volume/discovery_quote_plain.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"isPoc":false,"period":1,"size":1,"volumeTypeId":"vtype-1","zoneId":"HCM03-1C"},"resourceType":"volume"}`|200|
|`volume/discovery_quote_encrypted.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"encryptionType":"aes-xts-plain64_128","isPoc":false,"period":1,"size":1,"volumeTypeId":"vtype-1","zoneId":"HCM03-1C"},"resourceType":"volume"}`|200|
|`storage/discovery_regions.json`|GET `{Storage}/internal/v1/regions`|`none`|200|
|`storage/discovery_purchase_types.json`|GET `{Storage}/internal/v1/billing/purchase_types`|`none`|200|
|`storage/discovery_project_types.json`|GET `{Storage}/internal/v1/billing/project_types`|`none`|200|
|`storage/discovery_config_min_quota.json`|GET `{Storage}/billing-api/v1/configurations`|`none`|200|
|`storage/discovery_config_max_quota.json`|GET `{Storage}/billing-api/v1/configurations`|`none`|200|
|`storage/discovery_config_max_projects.json`|GET `{Storage}/billing-api/v1/configurations`|`none`|200|
|`storage/discovery_config_iam_checkout.json`|GET `{Storage}/billing-api/v1/configurations`|`none`|200|
|`storage/discovery_quote_gold.json`|POST `{Storage}/billing-api/v2/price`|`{"action":"create","resourceInfo":{"projectType":1,"purchaseTypeId":4,"quota":30},"resourceType":"object_storage"}`|200|
|`storage/discovery_quote_archive.json`|POST `{Storage}/billing-api/v2/price`|`{"action":"create","resourceInfo":{"projectType":7,"purchaseTypeId":4,"quota":30},"resourceType":"object_storage"}`|200|
|`network/discovery_quote_public.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"type":"public"},"resourceType":"public-vip"}`|200|
|`network/discovery_quote_public_vm.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"isBuyMorePo":false,"isPoc":false,"type":"public-vm"},"resourceType":"public-vip"}`|200|
|`network/discovery_quote_public_mkp.json`|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"isBuyMorePo":false,"isPoc":false,"type":"public-mkp"},"resourceType":"public-vip"}`|200|

Three more quotes were observed with the same response shapes as saved
fixtures and are not saved separately:

|Request|Synthetic request body|Status|Same shape as|
|-|-|-|-|
|POST `{B}/v1/price`|`{"action":"create","resourceInfo":{"encryptionType":"aes-xts-plain64_256","isPoc":false,"period":1,"size":1,"volumeTypeId":"vtype-1","zoneId":"HCM03-1C"},"resourceType":"volume"}`|200|`volume/discovery_quote_encrypted.json`, with the same amount as the plain and `_128` quotes|
|POST `{Storage}/billing-api/v2/price`|`{"action":"create","resourceInfo":{"projectType":1,"purchaseTypeId":4,"quota":31},"resourceType":"object_storage"}`|200|`storage/discovery_quote_gold.json`, at 1000 VND per GB|
|POST `{Storage}/billing-api/v2/price`|`{"action":"create","resourceInfo":{"projectType":7,"purchaseTypeId":4,"quota":31},"resourceType":"object_storage"}`|200|`storage/discovery_quote_archive.json`, at 530 VND per GB|

Both returned encryption keys, `aes-xts-plain64_128` and
`aes-xts-plain64_256`, therefore have an observed quote.

Two shapes reuse existing sanitized fixtures because the probes did not
capture them: the uncached project list uses
[list_projects.json](../project/list_projects.json), and the unfiltered volume
type-zone parent uses [list_volume_type_zones.json][type-zones].
The probes observed that `volume_type_zones?zoneId=` returns an empty list.

The compute quotes split into `INSTANCE TYPE` and `ROOT DISK` lines. The
`discovery_quote_server_s.json` quote uses a code-s flavor that is not in
`discovery_family_flavors.json`, which holds the code-s2 rows only.

[type-zones]: ../volume/list_volume_type_zones.json
