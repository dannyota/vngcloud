# Response integrity fixture provenance

`GetBudgetExactAmountSynthetic.json` is synthetic adversarial data for SDK
decoding. Its amount exceeds float64's exact integer range while remaining
inside int64. It establishes no accepted API limit or live response shape.
The other numeric, credential, duplicate-key, and missing-data variants live
in loopback tests, separate from raw response evidence.

Existing operation fixtures still exercise ordinary SDK decoding. The captured
cost-series evidence remains in
[billing design](../../docs/design/billing.md#cost-overview-evidence).
No live call or private capture was used for these regressions.
