# Storage: Projects

Use the [Storage client](Storage.md#setup) for these calls.

## Project pricing

Prices are VND totals that include VAT, with no API VAT breakdown; see
[Billing and Pricing](Billing-and-Pricing.md#price-quotes).

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
Duplicate JSON keys in the catalog or price response return
`*vngcloud.APIError`.

## Project purchase and delete

Purchase and delete were verified live on 2026-10-10 in `HCM04`: one order
charged the quoted price, the project was active at once, and delete
refunded the full amount. See [vStorage projects][project-design].

Set `Name` and `MaxPrice` on `CreateProjectInput`, then pass the input to
`CreateProject`. Use the exact catalog type and integer GB quota. Create checks
fresh configuration, quota, duplicate names, and project count. It quotes
immediately before one auto order for one month with renewal off. Default
`MaxPrice: 0` buys nothing. The cap protects the quote, not the server's debit,
because the API has no price lock. Output holds `Project`, `OrderID`,
`MonthlyPrice`, and `TotalPrice`. Unverified order ID fields leave `OrderID`
empty. `Project` stays nil until a read confirms identity. Ready success
requires exact name, region, type, quota, active status 1, and renewal off. With
`NoWait`, a matching pending project with renewal explicitly off returns partial
output with `Project` set.

An echoed project identity supports readiness polling every two seconds for 120
seconds. A non-empty checkout redirect gets one complete project read. No new
exact-name project returns `storage.ErrPaymentRequired`; failed or incomplete
reads, unconfirmed projects, and lost or malformed responses return
`storage.ErrNotSettled`. Unknown order data always returns `ErrNotSettled`, even
if a later read could find a matching project. Inspect pending orders and
billing before retrying. Neither refusal nor absence proves that no money moved.
An HTTP 200 or 4xx refusal with `success: false` and a numeric code from 400 to
499, 112, or 114 retains plain `*vngcloud.APIError` and its sentinel. Other
codes, unclassified 4xx, and all 5xx order responses return `ErrNotSettled` with
the `*vngcloud.APIError` in the error chain. `ErrFailed` is reserved for proven
terminal failures; no terminal status is assumed. `NoWait` skips readiness
polling only. Renewal true always fails. Each confirmation read uses the
remaining deadline; late reads cannot confirm success.

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

## Auto-renew

`GetProjectAutoRenew` joins a complete regional project list with
[billing resources](Billing-and-Pricing.md#prepaid-resources). Required
`ProjectID` selects one exact project; `Region` defaults to the configured
storage region. Missing projects return `vngcloud.ErrNotFound`. Duplicate,
incomplete, conflicting, or missing billing rows return `*vngcloud.APIError`.

```go
renewal, err := client.GetProjectAutoRenew(ctx,
    &storage.GetProjectAutoRenewInput{ProjectID: "project-1"})
if err != nil {
    log.Fatal(err)
}
log.Println(renewal.State.EndTime, renewal.State.PriceStatus)
```

`State` reports identity, end time in UTC, renewal type, nullable `Enabled`,
and configured `PeriodMonths`. MANUAL exposes false and null months.
AUTO-RENEW requires matching positive periods in both lists. Unknown states
expose null `Enabled` and block writes. The billing end timestamp is the
current term's end, not a promised charge time.

The read prices the exact catalog type and positive integral GB quota with
a fresh monthly create quote, including VAT. Preview `PeriodMonths` accepts
1, 3, 6, or 12. Omission uses the configured enabled period or one month
when off. `QuotePeriodMonths` and `QuotedRenewalCharge` describe the preview;
`NextCharge` always estimates the configured enabled period. When off,
`NextCharge` is null. Billing `Cost` never supplies a price.

Pricing failure alone returns state successfully with null prices,
`PriceStatus: "Unavailable"`, and a safe `PriceErrorCode`. Cancellation or
failed state reads remain errors. `NextCharge` uses today's create price;
future tariffs, renewal discounts, and payment success are unverified.

```go
changed, err := client.PutProjectAutoRenew(ctx,
    &storage.PutProjectAutoRenewInput{
        ProjectID: "project-1",
        Enabled:   vngcloud.Ptr(true),
        MaxPrice:  30000,
    })
if err != nil {
    log.Fatal(err)
}
log.Println(changed.Changed)
```

`Enabled` is required. Enable defaults to one month when off; omission
keeps the current period when enabled. An explicit different period updates
the setting. Allowed periods are 1, 3, 6, and 12. The renewal menu is
separate from purchase `AllowPeriod`. Only prepaid ordinary object-storage
projects using the active Normal monthly purchase are supported. Enable
and period changes require an active future term, no renewal in progress,
and `MaxPrice` at least the fresh monthly quote times target months.
Equality passes; default zero authorizes no change that enables spending.
An already matching setting returns `Changed: false` without PUT or new
price consent. A no-op enable still offers a fresh price when available.

The cap is local consent to today's estimate. It is not sent to the server,
saved for later charges, or a lifetime limit. Auto-renew can charge
repeatedly until disabled. The setting places no order and does not extend
the current term. Another actor can race the read and write; no conditional
update exists.

Disable uses `Enabled: vngcloud.Ptr(false)`, rejects `PeriodMonths`, and
needs no quote, catalog, active term, or price cap. It works during a
pricing outage. Disable output leaves prices unavailable; a later read can
price the project. Disabling does not cancel a charge already in progress.

Each actual PUT resolves fresh account identity internally, sends one
resource with its observed channel, and uses no retries or redirects.
Accepted writes confirm both lists immediately, then every two seconds for
at most 30 seconds. `Changed: true` means a sent request was confirmed.
Outputs retain the last valid observation on later errors; nil `State`
means no valid join exists.

An explicit rejection returns `*vngcloud.APIError` with code
`AutoRenewRejected` and a safe error count. The SDK never publishes error
entries or account identity. Lost, malformed, or unrecognized replies and
failed confirmation return `storage.ErrNotSettled` with the underlying
error. An inconsistent rejection also matches `ErrNotSettled`; observing
the target never erases a rejected reply. Known HTTP refusals and failed
dials retain their errors. Read `GetProjectAutoRenew`, reconcile both lists,
and make a new deliberate setting if needed. The SDK never rolls back or
resends automatically.
