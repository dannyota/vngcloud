# Resize credit fixture provenance

`GetQuoteServerResizeCredit.json` is the sanitized raw response body of one
`pricing.GetQuote` call (`POST v1/price`, `resourceType` `server`, `action`
`resize`, `resourceInfo` with `serverId`, `flavorId`, `period` 1, and `isPoc`
false) made through `compute.QuoteResizeServer` by the owner's test account in
region `hcm-3`. The quote downsized an existing server from `s2-general-2x4` to
`s2-general-1x2`; the server had volume encryption, so the response carries a
`CES` row. The call returned HTTP 200. No write was sent.

Sanitizing replaced the one identifier in the body, a flavor ID in the `CES`
row's `description`, with `<flavor-id>`. Every price, key, and other value is
unchanged, including fields the SDK does not decode (`originalCurrentPrice`,
`originalNewPrice`, and `artifactPrices`). The capture method, the evidence it
supports, and what it leaves unknown are in [vServer paid writes:
resize](../../docs/design/vserver-paid-writes-resize.md#evidence).
