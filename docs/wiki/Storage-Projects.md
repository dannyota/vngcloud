# Storage: Projects

Use the [Storage client](Storage.md#setup) for these calls.

## Project pricing

`ListProjectTypes` reads the regional catalog, monthly purchase type, and
quota configuration. It quotes each active monthly offer at its minimum
quota. Each call reads fresh prices and configuration.
Both pricing methods validate both regions before authentication or requests,
even with endpoint overrides. Config must use `hcm-3` or `han-1`; explicit
vStorage names must match `HCM04` or `HAN02`, ignoring case. Price requests
refuse redirects and retain retries for transient failures.

```go
types, err := client.ListProjectTypes(ctx, nil)
if err != nil {
	log.Fatal(err)
}
log.Println(len(types.Items))

quote, err := client.QuoteCreateProject(ctx, &storage.CreateProjectInput{
	Type:    "Gold",
	QuotaGB: 30,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("monthly %.0f, total %.0f %s", quote.MonthlyPrice,
	quote.TotalPrice, quote.Currency)
```

Use the exact catalog `Name`, such as `Gold` or `Instant-Archive-2`.
`ProjectType` preserves descriptions, SKU metadata, billable resources, storage
policy, and `AllowPeriod`. Offers include purchase type, price key, nullable
period, quota limits in GB, quoted quota, monthly price, and currency.
`StepQuotaGB` is null because the API supplies no step. Unknown purchases
remain visible without a quote; their `QuotedQuotaGB` stays zero.

`QuoteCreateProject` places no order. `QuotaGB` must be a positive integer
within configured limits. Disabled, ambiguous, unknown, or non-monthly types
return `vngcloud.ErrInvalidInput`. Missing or malformed configuration returns
`*vngcloud.APIError` before pricing.

Quotes ignore `Name`, `MaxPrice`, and `NoWait`; Input has no `Period`.
`MonthlyPrice` and `TotalPrice` equal `OptimumPrice` for one month in VND.
Output preserves original price, discounts, and property prices with nullable
names and descriptions. Zero or negative prices return `vngcloud.ErrUnpriced`.
Missing, null, malformed, or non-finite prices return `*vngcloud.APIError`.

## Project purchase and delete

Purchase and delete are pending the paid live check in
[vStorage projects][project-design].

Set `Name` and `MaxPrice` on `CreateProjectInput`, then pass the input to
`CreateProject`. Use the exact catalog type and integer GB quota.
Create checks fresh configuration, quota, duplicate names, and project count.
It quotes immediately before one auto order for one month with renewal off.
Default `MaxPrice: 0` buys nothing. The cap protects the quote, not the
server's debit, because the API has no price lock. Output holds `Project`,
`OrderID`, `MonthlyPrice`, and `TotalPrice`. Unverified order ID fields leave
`OrderID` empty. `Project` stays nil until a read confirms identity. Success
requires exact name, region, type, quota, active status 1, and renewal off.

An echoed project identity supports readiness polling every two seconds for
120 seconds. Checkout or unclassified responses get one complete project
read. No new exact-name project returns `storage.ErrPaymentRequired`;
failed or incomplete reads, unconfirmed projects, and lost or malformed
responses return `storage.ErrNotSettled`. Inspect pending orders and billing
before retrying. Neither refusal nor absence proves that no money moved.
Server refusals retain `*vngcloud.APIError` and its sentinel. `ErrFailed` is
reserved for proven terminal failures; no terminal status is assumed.
`NoWait` skips readiness polling only. Renewal true always fails.

`DeleteProject` takes `Region`, `ProjectID`, and `NoWait`. It requires the ID
in a complete list, or returns `vngcloud.ErrNotFound`. Any bucket returns
`storage.ErrProjectNotEmpty`; missing or incomplete lists also stop deletion.
Stop bucket writers first: the API has no concurrent-write precondition.
Delete sends `{}` once, requires an HTTP 200 success envelope, and polls
complete lists every two seconds for 60 seconds unless `NoWait` is set.
Unconfirmed removal returns `ErrNotSettled`. Inspect projects and billing
before retrying. Removal leaves free trash for normal expiry; refunds vary.
Both writes use `Once`, with no retries, resends, or redirects. The SDK never
sends manual orders, pays separately, purges, or exposes payment URLs.

[project-design]: https://github.com/dannyota/vngcloud/blob/master/docs/design/storage-projects.md
