# Public NAT

`network.Client.ListNATInstances` reads one page of Public NAT inventory.
It supports `hcm-3` and `han-1` through their regional vNetwork origins.
Use the same login and selected project as other SDK services.

```go
client := network.New(cfg)
out, err := client.ListNATInstances(ctx, &network.ListNATInstancesInput{
    Page: 1,
    Size: 10,
})
if err != nil {
    return err
}
for _, nat := range out.Items {
    fmt.Println(nat.NATName, nat.Status)
}
```

A nil input or zero `Page` and `Size` requests page 1 with size 10.
Positive values pass through unchanged. Negative values fail before any
request. Each call requests one page and preserves the server's `Page`,
`PageSize`, `TotalPage`, and `TotalItem`. Page advancement and a server size
cap have not been verified live. The SDK does not automatically fetch more
pages or expose search and sort inputs.

Set `ZoneID` to use an explicit zone. Otherwise, discovery must return one
zone whose dashboard or gateway matches the selected region's verified
origin. A missing, ambiguous, or conflicting mapping returns
`vngcloud.ErrInvalidConfig`. Discovery errors reach the caller. The SDK
never substitutes a region name for a zone UUID. Zone and project IDs must
pass path validation before resource requests.

HCM uses `https://hcm-3-vnetwork.console.greennode.ai`; HAN uses
`https://han-1-vnetwork.console.greennode.ai`. An `Endpoints.VNetwork`
override controls both zone discovery and the NAT request. Discovery cannot
replace the override or select an arbitrary credential destination.

`ListNATInstancesOutput` holds `[]NATInstance` and page metadata.
`NATInstance` includes identity, name, status, timestamps, project and zone
UUIDs, a `NATPackage`, and a `NATVPC`. `NATPackage` includes a `NATImage`
with a `NATPackageLimit`. CPU, memory, and disk limit integers retain the
API's values; their units are unverified.

`NATGatewayIP`, `PublicIP`, `DeletedAt`, and `BillingStatus` are `*string`.
Null stays distinct from an empty string. Status and timestamp fields stay
raw strings. Unknown status values are preserved. A provisioning status
does not prove traffic flow.

The models omit account identifiers, `visible`, `message`, `subnet`,
null-only fields, `elasticIps`, and `image.licenseKey`. They also omit
`monthlyPrice`: the API's zero is not a usable package price. Unknown fields
are discarded.

A successful response needs `success: true`, positive page and size,
nonnegative totals, and an array of rows with nonempty UUIDs. A verified
empty response may omit `data` only when both totals are explicitly zero.
`data: null`, missing metadata, wrong types, and malformed bodies return
`InvalidResponse`. NAT keeps the shared API errors, cancellation, and GET
retry policy. No NAT detail, rule, or write methods are exposed.

See [Network](Network.md) for other network operations and
[Errors](Errors.md) for shared error handling.
