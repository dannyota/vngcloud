# CDN path purge implementation plan

> **For agentic workers:** Use superpowers:subagent-driven-development. The manager owns Git and releases.

**Goal:** Ship `cdn purge-paths` with the accepted SDK contract and tests as v0.61.0.
**Architecture:** `cdn.Client.PurgePaths` validates the input and posts `{cdnDomain,type:"URI",patterns}` through the existing vCDN transport. The CLI uses the public SDK and takes paths through JSON input.
**Tech stack:** Go standard-library SDK and the existing CLI framework.
**Spec:** `docs/design/cdn-writes.md`, `docs/design/cdn-api.md`, `docs/design/cdn-cli.md`, ADR 0002.

**State:** Deferred by the owner on 2026-10-10 for Claude to resume. Do not request a test CDN or run further purge work until resumed. The owner redirected current work toward gaps in main-dashboard service coverage. v0.61.0 is a proposed version, not reserved; check the latest tag before release.

## Constraints

- Start with `git status --short`. Preserve other edits. No worker Git mutations, live writes, paid resources, secrets in output, or new dependencies.
- Sol medium implements; independent Sol xhigh reviews. At most three workers with disjoint file ownership.
- Keep the basic example read-only as its README requires. The gated live write test demonstrates purge and records its raw response under ignored output; never add an automatic purge to the read example.
- Plain exact writing, no em dashes, no plan or task IDs in code, tests, comments, or living docs.

## Review focus

- Empty or wildcard paths must send no request; reject only the input shape the design specifies.
- A successful purge can consume daily quota, so never resend after a 5xx, envelope failure, or ambiguous network failure.
- Cooldown code 202 must match only `ErrPurgeCooldown`; other code 202 responses remain invalid input.
- A read-only CLI profile must fail before any API request, including JSON input.
- API key echoes and account data must not appear in CLI output, debug logs, committed fixtures, or reports.

## SDK

Owned paths: new `cdn/purge.go`, `cdn/purge_test.go`, `cdn/vcdn.go`, relevant CDN tests, new sanitized `testdata/cdn/purge-*.json`, `live_cdn_test.go`, `docs/wiki/CDN.md`.
Interface: `PurgePathsInput{CDNDomain string, Paths []string}` with both fields required; `PurgePathsOutput{}`; `func (*Client) PurgePaths(context.Context, *PurgePathsInput) (*PurgePathsOutput, error)`; exported sentinel `ErrPurgeCooldown`.

- [x] Write failing request, validation, error classification, retry, and secret-redaction tests. Include HTTP 200, 400, 401, 403, 404, 500, and failed envelopes.
- [x] Implement the method and the cooldown envelope row before general code 202 handling. POST uses the existing non-idempotent retry rule, not `Once` or `Idempotent`.
- [x] Add one purge to the gated live test before its final delete, with raw and SDK output capture under ignored output paths. Keep logs to counts, statuses, and codes.
- [ ] Run the live purge and add its sanitized raw fixture and decode test. Never purge twice to test cooldown live.
- [x] Update SDK docs, run focused tests and `make check`, and report exact paths and results.

## CLI

Owned paths: `internal/cli/svc_cdn.go`, new `internal/cli/svc_cdn_purge_test.go`, `internal/cli/errors.go`, `internal/cli/gendocs_notes_cdn.go`, generated `docs/wiki/CLI-CDN.md`, and CDN CLI goldens if needed.
Consumes the SDK interface above after manager inspection.

- [x] Write failing CLI tests for JSON paths, no paths flag, no `--yes` requirement, read-only rejection with no HTTP, `{}` success, and `PurgeCooldown` exit 1 versus invalid input exit 2.
- [x] Register the write and error mapping, add a short recipe with the 30-second cooldown and daily quota, and regenerate CLI docs.
- [x] Run focused tests and `make check`, and report exact paths and results.

## Manager verification and release

- [ ] Obtain a free disposable portal CDN under an owner-controlled domain, with no DNS pointing to it. Existing session access determines whether the owner must perform the portal step.
- [ ] Inspect changes, run `make check`, compile/vet live-tag tests, and run `VNGCLOUD_LIVE_WRITE=1 VNGCLOUD_LIVE_CDN=1 go test -tags livewrite -count=1 -timeout 60m -run '^TestLiveWriteCDN$' .` with output kept private. Confirm one purge and deletion.
- [x] Have Sol xhigh adversarially review the write path and CLI behavior. No actionable code findings. Review the captured fixture separately once the live run provides it.
- [ ] Run required checks, commit exact paths with signing, fast-forward master, push, and wait for CI and Semgrep on the exact commit before signing and pushing v0.61.0.
- [ ] Update the remaining work record and remove this completed plan. Preserve existing account resources and other worktrees.

## Current evidence

- Manager inspected the SDK, CLI, docs, tests, and live capture changes. `make check`, `go test -tags livewrite -run '^$' .`, `go vet -tags livewrite .`, and `git diff --check` passed. CLI help shows `--cdn-domain` and `--cli-input-json`, with no `--paths` flag.
- The independent Sol xhigh review found no actionable code issues. It confirmed validation, retry limits, cooldown classification, key redaction, CLI behavior, and ignored capture paths. The reviewer explicitly withheld release readiness pending live proof and a sanitized fixture.
- A read-only live check on 2026-10-10 returned zero CDNs. The portal session is signed out. The owner has been asked to create the disposable CDN; the live write check and captured fixture remain pending.
