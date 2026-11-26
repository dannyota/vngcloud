# vServer bandwidth packages

Proposed. List offers for the caller's vServer project in a selected region.

## SDK and CLI

Use `network.Client.ListBandwidthPackages(ctx, *ListBandwidthPackagesInput)`
and `vngcloud network list-bandwidth-packages --region <region>`. Bandwidth is
under the console's Network menu, and its packages apply to public IPs across
resource types, including load balancers. `network` owns this surface.

Use an empty input and the configured project (`--project-id`), or discover
one project with the guarded regional vServer read. Reject missing, ambiguous
or unsafe project IDs. Support every account with access in each mapped region.

| Region | Fixed bandwidth base |
|-|-|
| `han-1` | `https://han-1.console.greennode.ai/vserver/bandwidth-api` |
| `hcm-3` | `https://hcm-3.console.greennode.ai/vserver/bandwidth-api` |

Append `/v1/projects/{project}/bandwidth/package-config`. Send one logical
GET with an empty query and no body or zone lookup. Select the base before
project discovery or authentication. An unmapped region returns
`ErrInvalidConfig`. Use literal regional bases, with no endpoint override,
URL interpolation, or fallback. This proposal replaces the earlier routing
sketch for this operation if approved. [Hanoi config][han-config] ·
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

Follow [NAT read handling][nat]. Keep TLS verification on. Refuse redirects
and cookies, suppress captures, and cap each catalog and project-discovery
response at 4 MiB, including error responses. On each attempt check raw bytes
and decoded JSON strings for credentials sent during the operation before
interpreting the response, retrying, caching scope, or returning output. Reject
reflected scope before using it. Return safe status and sentinel errors with
fixed text. A reflection or invalid success body yields `InvalidResponse` and
no output or server text.

Require one HTTP 200 JSON object with a successful observed envelope and an
explicit `data` array. Preserve `[]`. Reject missing, null or wrong-type
required fields, duplicate keys or package IDs, case aliases of known keys,
trailing JSON and evidence of more pages. Ignore unprojected keys after checks.
Required keys and kinds follow the console code and the recorded HCM read.

The shipped read is its own probe. Before release, the manager runs the built
CLI read-only in `hcm-3` and `han-1` with the caller's IAM login and each
region's project. Both pass when the command exits 0 with a nonempty list;
a strict-decoding `InvalidResponse` means the contract needs revising before
release, and its output shows no response values. Ship the SDK and wiki with
synthetic fixtures and negative decoder, routing, reflection and CLI tests.

[han]: https://han-1.console.greennode.ai/vserver/161.d6d061e98608e934.js
[hcm]: https://hcm-3.console.greennode.ai/vserver/161.be4056dfe6576d1f.js
[han-config]: https://han-1.console.greennode.ai/vserver/assets/config/vserver-portal.json
[hcm-config]: https://hcm-3.console.greennode.ai/vserver/assets/config/vserver-portal.json
[prior-route]: pricing-discovery-api.md#bandwidth-endpoint-contract
[prior-read]: pricing-discovery-api.md#storage-and-network-results
[nat]: ../../network/nat_detail.go
