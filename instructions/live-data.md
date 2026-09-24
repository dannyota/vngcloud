# Live data

Rules for anything that touches the real VNG Cloud API or its output. Everyone who runs `make live`, the basic example, or a browser capture reads this file.

- Treat IP addresses, hostnames, project IDs, resource IDs, account names, emails, internal URLs, tokens, cookies, and certificates from live runs as sensitive.
- Never read `.env` or `examples/basic/config.*.json` values into the conversation. Never print live output in chat, and never paste it into web searches or external tools.
- Live output stays under `examples/basic/output/`, which is git-ignored: `raw/<service>/<resource>.json` holds the unmodified response body under a `body` field, and `sdk/<service>/<resource>.json` holds the decoded SDK model.
- Browser captures (Playwright MCP) stay under ignored paths and are deleted when the discovery work ends.
- Live write calls need an explicit owner approval per run, naming the account, region, and resources. Clean up what the run created.

## Adding or fixing an API

1. Add the SDK method and a call in `examples/basic/`.
2. Run the example and compare `raw/` against `sdk/` for the resource. Fix any field the model drops or mistypes.
3. Copy a sanitized raw response to `testdata/<service>/<operation>.json`.
4. Add a test that decodes the fixture through the SDK.

Test against raw fixtures, not SDK output: decoding drops unknown fields silently, and only the raw response shows it.

## Sanitizing fixtures

Replace every sensitive value before a file enters `testdata/`: `<project-id>`, `<server-id>` or `<id>` for resource IDs, `<account>` for names and emails, `<hostname>`, `<internal-url>`, `<ip>`, and `<secret>` for tokens, keys, and certificates. Keep enum, status, and type strings only when a test needs them. Short synthetic IDs such as `vpc-1` are fine.
