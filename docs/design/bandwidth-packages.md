# vServer bandwidth packages

Proposed. List offers for the caller's vServer project in a selected region.

## SDK and CLI

Use `network.Client.ListBandwidthPackages(ctx, *ListBandwidthPackagesInput)`
and `vngcloud network list-bandwidth-packages --region <region>`. Bandwidth is
under the console's Network menu, and its packages apply to public IPs across
resource types, including load balancers. `network` owns this surface.

Use an empty input and the configured project (`--project-id`). Support every
account with access in each mapped region.

| Region | Fixed HTTPS origin |
|-|-|
| `han-1` | `https://han-1.console.greennode.ai` |
| `hcm-3` | `https://hcm-3.console.greennode.ai` |

Select the literal origin before project discovery or authentication. Return
`ErrInvalidConfig` for an unmapped region. If the project is unset, discover it
with `GET /vserver/iam-vserver-gateway/v1/projects` on that same fixed origin.
Reject missing, ambiguous or unsafe project IDs. Both requests use the fixed
origin regardless of `EndpointOverrides.VServer`, with no fallback.

GET `/vserver/bandwidth-api/v1/projects/{project}/bandwidth/package-config`
uses an empty query, with no body or zone lookup. [Hanoi config][han-config] ·
[HCM config][hcm-config] · [Earlier routing sketch][prior-route]

`ListBandwidthPackagesOutput.Items` holds `[]BandwidthPackage` in response
order. Map `data[].uuid`, `name`, `type`, and `description` to `ID`, `Name`,
`Type`, and `Description` strings. Preserve unfamiliar `Type` values. Map
`bandwidth` to `Bandwidth`, proposed as `int64` if the probe confirms whole
numbers in range. Publish no unit conversion. Both consoles display the raw
field as Mbps, and the earlier HCM response had no unit key.
[Hanoi bundle][han] · [HCM bundle][hcm] · [HCM read][prior-read]

Both consoles populate the catalog picker from those five fields. The create
summary fills `price` from the separate regional billing `POST
/vserver/iam-billing-gateway/v1/price`, with `resourceType: "bandwidth"`,
`action: "create"`, `period: 1`, `isPoc: false`, `isBuyMorePo: false`, and
`domConfigId`/`intConfigId` set to the selections or null. It matches
`propertiesPrice` lines named `DOMESTIC PACKAGE` and `INTERNATIONAL PACKAGE`
and displays `monthlyPrice`, `optimumPrice`, and `discountPercent`. The HCM
catalog contained no `price`. Keep price out of the catalog model.
Showing account prices requires a separately qualified read-only quote contract.
[Hanoi bundle][han] · [HCM bundle][hcm] · [HCM read][prior-read]

## Related console reads

Under the same project bandwidth prefix, both bundles use these GETs.

| Suffix | Console use and query |
|-|-|
| `/packages` | Purchased packages. `name`, `page`, `size` |
| `/packages/{id}` | Detail with `domConfig` and `intConfig`. No query |
| `/ip` | IP inventory with package association. `type`, `ip`, `page`, `size` |
| `/packages/{id}/ip` | Package members. `ip`, `page`, `size` |
| `/packages/{id}/usage` | Usage by `trafficType`, `startTimestamp`, `endTimestamp` |

The IP picker reads `packageId`. The bundles show no dedicated server or IP
bandwidth GET. Exclude purchased inventory, usage, purchase, resize and attach.
[Hanoi bundle][han] · [HCM bundle][hcm]

## Response handling and release gate

Keep TLS verification on. Refuse redirects and cookies, suppress captures,
and cap each response at 4 MiB, including errors. Use `DoDiscoveryJSONStatus`
for the catalog and `DoProjectDiscoveryJSONStatus` for project discovery with
one credential context. On every attempt, `guardDiscoveryResponse` checks raw
bytes and decoded JSON strings for all bearers sent by the operation before
status handling or retry. Reject reflected scope before use or caching.
Reflection or invalid success returns `InvalidResponse` with safe status and
fixed text. [Guard][guard]

Require HTTP 200 and exactly one JSON object with a non-null `data` array.
Accept `[]`. If present, `success` must be JSON `true` and `code` must be the
JSON integer `200`. Require `error`, `errors`, `errorCode`, `errorMsg` and
`errorMessage` to be absent or null. A conflicting marker invalidates the
whole response even with `data`. These marker rules are provisional until the
release probe. Reject missing, null or wrong-type required row fields,
duplicate keys or IDs, case aliases, trailing JSON, and incomplete paging.
Ignore other metadata after these checks.

Before release, the manager uses IAM and each region's project to run this
command in both mapped regions. Each run must exit 0 and print a positive count.

```sh
vngcloud network list-bandwidth-packages --region "$region" \
  --project-id "$project" --query 'length(Items)' --output json
```

Ship the SDK and wiki with decoder, routing, reflection and CLI tests. Include
`data` with a conflicting error marker, an overridden vServer host receiving
zero requests, and a reflected bearer in a 401 yielding `InvalidResponse`
after exactly one send.

[han]: https://han-1.console.greennode.ai/vserver/161.d6d061e98608e934.js
[hcm]: https://hcm-3.console.greennode.ai/vserver/161.be4056dfe6576d1f.js
[han-config]: https://han-1.console.greennode.ai/vserver/assets/config/vserver-portal.json
[hcm-config]: https://hcm-3.console.greennode.ai/vserver/assets/config/vserver-portal.json
[prior-route]: pricing-discovery-api.md#bandwidth-endpoint-contract
[prior-read]: pricing-discovery-api.md#storage-and-network-results
[guard]: ../../internal/transport/discovery.go
