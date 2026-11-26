# Network Bandwidth Packages

`network.Client.ListBandwidthPackages` lists the vServer bandwidth packages
your project can choose in the configured region. It works in `hcm-3` and
`han-1` and reads the catalog of your own project, so the list can differ
between accounts and regions.

```go
client := network.New(cfg)
out, err := client.ListBandwidthPackages(ctx, nil)
if err != nil {
    return err
}
for _, p := range out.Items {
    fmt.Println(p.Name, p.Type, p.Bandwidth)
}
```

```sh
vngcloud network list-bandwidth-packages --output table
```

Each `BandwidthPackage` has `ID`, `Name`, `Type`, `Description`, and
`Bandwidth`. `Type` is the provider's label, such as `Domestic` or
`International`. `Bandwidth` is the provider's whole-number value, which the
console shows in Mbps; the SDK does not convert it.

The catalog carries no price. Use [Billing and Pricing](Billing-and-Pricing.md)
for quotes. The read never buys, attaches, or changes bandwidth.

A response that is not a complete, successful catalog returns
`InvalidResponse` with no output. An oversized body returns an error that
matches `vngcloud.ErrBodyTooLarge`.
