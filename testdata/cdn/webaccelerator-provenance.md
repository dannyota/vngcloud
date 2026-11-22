# Web Accelerator detail fixtures

`webaccelerator-detail.json`, `webaccelerator-detail-deploying.json`, and
`webaccelerator-detail-disabled.json` retain the sanitized raw response
shapes recorded in [vCDN CLI](../../docs/design/cdn-cli.md#live-checks).
Their `cdnId` uses the synthetic value `cdn-1` so SDK reads can verify the
requested identity. No new capture supplies these fixtures.

Malformed identity and missing-status cases in the package tests are
synthetic adversarial responses, separate from these raw fixtures.
