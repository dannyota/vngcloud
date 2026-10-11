# Testing

Run the checks the change requires and report their actual results. Local verification includes the race suite; CI does not replace `make check`.

## Test design

Write the failing behavior test before the fix. Inspect the existing code and tests first. Tests must be deterministic: inject clocks and randomness, use `httptest` servers, and never call the real API in unit tests. Never retry a flaky test into a pass.

Model and decoding changes need a decode test through the SDK using a sanitized raw response in `testdata/`, not decoded SDK output. Raw responses reveal unknown fields that decoding can silently drop. Follow [live data](live-data.md#sanitizing-fixtures) for sanitation and fixture provenance.

Every write API needs tests for its request body, success response, and each documented error status. Auth, credential, debug-output, and error tests enforce the [security rules](security.md#secure-defaults).

## Required checks

|Change|Check|
|-|-|
|Every commit, including docs|`make check`: `go test -race ./...`, `go vet ./...`, `golangci-lint run ./...`, and `bash scripts/check-lengths.sh`|
|Any Go change|`make check` and the contract's focused checks|
|Model or decoding change|SDK decode test using a sanitized raw fixture in `testdata/`|
|Public API change|Matching wiki page; breaking changes also update `RELEASE_NOTES.md`, per [documentation](documentation.md#public-surface-and-wiki)|
|Wiki change|Inspect `Page-Name.md` links; use `make wiki-preview` for publication layout|
|Dependency or Go version change|`make vuln`|
|Tool pins|`make tools-outdated`, under [tool policy](security.md#tools-and-scanners)|
|Local Semgrep check|`make semgrep`; CI runs the connected scan under [security](security.md#tools-and-scanners)|
|Document move or rewrite|`git diff --check`, `bash scripts/check-lengths.sh`, source-to-destination coverage, and local link/anchor inspection|
|Release|Green CI on the exact commit under [release](release.md#release-gates)|

CI also checks both live-tag vet passes, tidy no-op, SDK dependency boundaries, Windows/macOS vet, generated-doc freshness, wiki preview, vulnerability scans, and full-history gitleaks. Pushes that change code run connected Semgrep Pro Code, Supply Chain, and Secrets scans. Keep these gates. The manager checks exact-head results under [landing](workflow.md#landing-a-slice); a failed, skipped, cancelled, missing, or pending required run does not pass.

## Live verification

Live tests and helpers live in `livetest/`, separate from root unit tests. These compile-only and vet commands make no API calls:

```sh
go test -tags live -run '^$' ./livetest/
go test -tags livewrite -run '^$' ./livetest/
go vet -tags live ./...
go vet -tags livewrite ./...
```

Run `make live` or any other live verification only when the brief asks for live verification, including read-only runs. Only the manager performs authorized live verification under the [worktree credential rule](workflow.md#worktrees-and-parallel-work); workers report required live checks to the manager. `make live` calls the real API and needs `.env`; never read its values into context. Compilation is not live verification. Live writes also require the approvals, gates, serialization, and cleanup in [live data](live-data.md).
