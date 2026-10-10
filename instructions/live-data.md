# Live data

Rules for anything that touches the real GreenNode API or its output. Everyone who runs `make live`, the basic example, or a browser capture reads this file.

- Treat IP addresses, hostnames, project IDs, resource IDs, account names, emails, internal URLs, tokens, cookies, and certificates from live runs as sensitive.
- Never read `.env` or `examples/basic/config.*.json` values into the conversation. Never print live output in chat, and never paste it into web searches or external tools.
- Live tests live in `livetest/`; their path helper finds the module root for `.env`, captures, and fixtures.
- Live output stays under `examples/basic/output/`, which is git-ignored: `raw/<service>/<resource>.json` holds the unmodified response body under a `body` field, and `sdk/<service>/<resource>.json` holds the decoded SDK model.
- Browser captures (Playwright MCP) stay under ignored paths and are deleted when the discovery work ends.
- To sign the Playwright MCP browser in, run `make browser-creds` in the background, then `browser_run_code_unsafe` with `filename: scripts/browser-login.js`. The helper serves the `.env` values once over 127.0.0.1 and exits, and the script clears the form and returns only the origin, so credentials stay out of tool output. Page snapshots after login still show account data; treat them as live output. Add `.playwright-mcp/` to `.git/info/exclude` in each clone.
- Live write calls need owner approval per run, naming the account, region, and resources, unless the owner gave standing approval for free, cleanable writes on the test account. Paid or uncleanable writes always need approval. Clean up what the run created.
- Run one live write test with `VNGCLOUD_LIVE_WRITE=1 VNGCLOUD_LIVE_<TEST>=1 go test -tags livewrite -count=1 -timeout 60m -run '^TestLiveWrite<Name>$' ./livetest/`; each test names its own gate variable. Only one test that creates a VPC runs at a time.
- A live test creates its own parent resources, deletes children before parents, and stops with a message naming the leftovers rather than delete a parent a child still holds.
- Throwaway live programs go under a git-ignored path and are deleted after use.

## Adding or fixing an API

1. Add the SDK method and a call in `examples/basic/`.
2. Run the example and compare `raw/` against `sdk/` for the resource. Fix any field the model drops or mistypes.
3. Copy a sanitized raw response to `testdata/<service>/<operation>.json`.
4. Add a test that decodes the fixture through the SDK.

Test against raw fixtures, not SDK output: decoding drops unknown fields silently, and only the raw response shows it.

## Sanitizing fixtures

Public documentation pages, such as the GreenNode CDN IP range FAQ, are not account data: their fixtures keep the published values and may be trimmed.

Replace every sensitive value before a file enters `testdata/`: `<project-id>`, `<server-id>` or `<id>` for resource IDs, `<account>` for names and emails, `<hostname>`, `<internal-url>`, `<ip>`, and `<secret>` for tokens, keys, and certificates. Keep enum, status, and type strings only when a test needs them. Short synthetic IDs such as `vpc-1` are fine.
- Live tests and hand probes on the shared vStorage test project never run at the same time: each live write test sweeps leftover `vngcloud-live-` resources, so a concurrent run loses its buckets or keys. Hand probes name resources `vngcloud-probe-<8 hex>` and clean up after themselves.
