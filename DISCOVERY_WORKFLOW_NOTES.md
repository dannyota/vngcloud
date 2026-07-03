# SDK Discovery Workflow Notes

Local notes for future agents improving this SDK. Do not paste live captures,
credentials, account details, project IDs, resource names, IP addresses,
hostnames, internal URLs, cookies, or tokens into this file.

## Goal

Build a read-only Go SDK for VNG Cloud IAM User workflows.

The SDK should make Hotpot-style inventory ingestion easier than the previous
SDKs:

- Smaller build surface.
- Read-only by design.
- IAM User login first.
- Multi-region config.
- Optional project IDs through SDK discovery.
- More complete resource coverage.
- Better data detail through typed models and preserved raw fields where needed.

## Discovery Method

Use three sources together:

| Source | Use |
|--------|-----|
| Live console with Playwright MCP | Discover IAM User browser flows, routes, query parameters, pagination behavior, and detail calls. |
| Official SDK | Find service-account-oriented resource coverage and existing model intent. |
| Previous SDK | Understand Hotpot's current ingestion needs and resource naming. |

The live console is the source of truth for IAM User support. The official SDK is
useful, but many routes target service account flows or contain model gaps.

### GreenNode Domain Migration (July 2026)

All VNG Cloud domains migrated to `greennode.ai` (verified live 2026-07-03,
regions hcm-3 and han-1). Key facts:

- Default gateway pattern: `{region}.console.greennode.ai`.
- IAM login flow unchanged at `signin.greennode.ai` with the same OAuth
  clientId.
- Root-account SSO is now CAS at `sso.greennode.ai/cas` (not used by this
  SDK).
- vDNS API moved host and gained a path prefix:
  `vdns.console.greennode.ai/vdns-api/` (the old `vdns.api.vngcloud.vn`
  host does not have a `greennode.ai` equivalent without the prefix).
- Legacy `*.vngcloud.vn` hosts 301-redirect to the new domains.

Live verification (2026-07-03, `make live`): full pass across hcm-3 and han-1
for all read services. Headless IAM login worked with password + computed TOTP
and no captcha. The token endpoint DOES return a `refreshToken` alongside the
access token — a refresh-token grant is feasible follow-up work to avoid full
re-login (and TOTP) on token expiry.

## Playwright MCP Workflow

1. Start from the console login page.
2. Login with local ignored example config credentials.
3. If captcha is present, ask the user to complete the captcha manually.
4. Use computed TOTP when the account enforces OTP.
5. Navigate manually through each resource page.
6. Switch regions through the UI and observe how the console changes host and route.
7. Click resource rows to capture detail APIs, not only list APIs.
8. Inspect network requests after each page or click.
9. Keep raw request and response captures local-only under ignored output paths.
10. Convert only sanitized payloads into committed fixtures.

Do not rely only on scripted navigation. Some console pages lazy-load data or call
detail APIs only after user interaction.

## Smoke Output Workflow

Use the basic example as the live smoke test:

```bash
go run ./examples/basic
```

Expected local output layout:

```text
examples/basic/output/raw/<domain>/<resource>.json
examples/basic/output/sdk/<domain>/<resource>.json
```

Rules:

- `raw/` stores unmodified HTTP response bodies inside a wrapper when needed.
- `sdk/` stores SDK-parsed public model output.
- Both paths are ignored and may contain sensitive local data.
- Never print raw output into chat.
- Never commit raw output.
- Before creating `testdata/`, replace all sensitive values with placeholders.

## Implementation Pattern

Keep the public API simple:

```go
client.Compute.ListServers(ctx, opts)
client.Compute.GetServer(ctx, id)
```

Project IDs should remain optional:

- Users provide IAM User credentials and regions.
- SDK discovers project IDs per region where possible.
- If a project ID is provided, use it.
- If future VNG Cloud behavior allows multiple projects per region, preserve the
  model shape needed to return multiple project IDs.

## Resource Coverage Strategy

For each resource area:

1. Add the SDK read method.
2. Add the basic example call.
3. Capture raw output.
4. Capture SDK output.
5. Compare raw vs SDK output locally.
6. Add sanitized fixtures.
7. Add decode/model tests.
8. Update public feature docs.

Prefer typed models for stable resources. For portal-like or changing metadata,
use map-backed models or raw fields to avoid dropping data silently.

## Pagination Notes

Do not copy low defaults blindly from previous SDKs.

- Test each resource type against the live console behavior.
- Use high page-size defaults only where the server accepts them reliably.
- If a large page size causes slow responses or failures, lower it for that
  resource and document the reason in code.
- Stop pagination based on server metadata, not a guessed fixed count.

## Auth Notes

Supported first:

- IAM User with root email, username, password, and optional TOTP.
- IAM User without OTP when the account does not enforce OTP.

Do not claim root-user SDK support unless a non-browser API can pass login
without captcha. Browser captcha can be completed manually for discovery, but it
is not a usable SDK auth flow.

## Naming Notes

Use clear public service names:

- `Compute`
- `Network`
- `Volume`
- `LoadBalancer`
- `GlobalLoadBalancer`
- `DNS`
- `ContainerRegistry`
- `Portal`

Avoid product abbreviations in public method names when the expanded name is
clearer.

## Safety Rules

- Do not commit live config files.
- Do not commit smoke output.
- Do not commit account names, resource names, IP addresses, hostnames, project
  IDs, internal URLs, cookies, tokens, certificates, or raw API paths collected
  from private sessions.
- Do not use external search with copied private payloads.
- Do not add co-author commit trailers.

## Handoff For Future Agents

Before adding new SDK methods:

1. Read `README.md`, `FEATURES.md`, and `GUIDES.md`.
2. Run `go test ./...`.
3. Run `go run ./examples/basic` only when local ignored config is available.
4. Use Playwright MCP to verify IAM User routes before porting official SDK
   service-account URLs.
5. Leave local raw captures under `examples/basic/output/`.
6. Commit only source, docs, and sanitized fixtures.
