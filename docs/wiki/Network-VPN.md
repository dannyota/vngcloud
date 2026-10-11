# Site-to-site VPN

`network.Client.ListVPNConnections` reads one page of VPN inventory with
inline sites, tunnels, and safe phase configuration. It supports `hcm-3`
and `han-1` using the same login and selected project as other SDK services.

```go
client := network.New(cfg)
out, err := client.ListVPNConnections(ctx, &network.ListVPNConnectionsInput{
    Page: 1,
    Size: 10,
})
if err != nil {
    return err
}
for _, vpn := range out.Items {
    fmt.Println(vpn.VPNName, vpn.Status, len(vpn.VPNSites))
}
```

A nil input or zero `Page` and `Size` requests page 1 with size 10.
Positive values pass through unchanged. Negative values fail before any
request. Each call requests one page and preserves the server's `Page`,
`PageSize`, `TotalPage`, and `TotalItem`. Page advancement and a server size
cap have not been verified live. The SDK does not fetch more pages or expose
search and sort inputs.

HCM uses `https://hcm-3-vnetwork.console.greennode.ai`; HAN uses
`https://han-1-vnetwork.console.greennode.ai`. An `Endpoints.VNetwork`
override controls the resource request. VPN paths use the selected project
without a zone segment or zone discovery. Project IDs pass path validation.
Cached discovery endpoints cannot replace the fixed regional origins or an
override. Other regions return `vngcloud.ErrInvalidConfig`.

`ListVPNConnectionsOutput` holds `[]VPNConnection` and page metadata.
Each connection includes identity, name, status, billing status, timestamps,
package and zone UUIDs, local network CIDR, and typed `VPNSubnet`, `VPNVPC`,
`VPNProject`, and `VPNPackage` objects. `LocalGatewayIP` and `VPNGatewayIP`
are `*string`: null stays distinct from an empty string.

`VPNSites` holds `[]VPNSite`, each with `[]VPNTunnel` and
`[]VPNPhase1Config`. Each tunnel includes `[]VPNPhase2Config`. Phase
configuration includes algorithm, hash, DH group, and IKE lifetime. Lifetime
values stay strings. Status and timestamp fields stay raw strings. Unknown
status values are preserved. Provisioning and ACTIVE statuses do not prove
VPN connectivity or tunnel health.

The API returns plaintext keys, so every VPN resource request is sensitive.
The SDK omits `preShareKey` from all models and suppresses raw response
captures. It withholds server error messages and codes, including failure
envelopes with HTTP 200. Errors retain the operation, HTTP status,
retryability, and safe shared sentinels. Invalid success bodies return a
fixed `InvalidResponse` error without a decode cause. Debug logs and example
output never include the omitted keys.

Models also omit `portalUserId`, `monthlyPrice`, null-only fields, outer
phase lifetime and phase status fields, `elasticIps`, and unknown fields.
No credential reveal, detail, child-list, or write method is exposed.

A successful response needs `success: true`, positive page and size,
nonnegative totals, and an array of rows with nonempty UUIDs. An empty
response may omit `data` only when both totals are explicitly zero.
`data: null`, missing metadata, wrong types, duplicate JSON keys, field name
case aliases, and malformed bodies return `InvalidResponse`. Cancellation
and the shared GET retry policy remain available.

See [Network](Network.md) and [Errors](Errors.md).
