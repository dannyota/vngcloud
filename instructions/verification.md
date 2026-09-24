# Verification

Code rules and the checks each change needs. Implementers, the reviewer, and the manager read this file.

- `go.mod` requires the Go version `.tool-versions` pins; local work and CI use that version. Tool versions follow the rule in AGENTS.md "Security first". Follow Google Go style with `gofmt` and `goimports`.
- Keep the public API in one package. A new public type, option, or method in `internal/*` gets a re-export in `vngcloud.go`.
- Tests are deterministic: inject clocks and randomness, and use `httptest` servers, never the real API. Never retry a flaky test into a pass.
- `golangci-lint` stays at 0 issues. A justified `//nolint:<linter>` needs a reason on the same line.
- Semgrep runs in CI; `make semgrep` is the offline version. Suppress a verified false positive with `// nosemgrep: <full-rule-id>` on the flagged line, using the full ID from `semgrep --json` (short IDs do not match), and put the reason in a comment beside it.
- Write APIs need tests for the request body, the success response, and each documented error status.

| Change | Check |
|-|-|
| Any Go change | `make check` (tests, vet, lint, lengths) |
| Model or decoding change | A decode test on a sanitized raw fixture in `testdata/`, per [live-data](live-data.md) |
| Public API change | `vngcloud.go` re-export and the matching `docs/wiki/` page |
| Wiki page change | Links between pages use `Page-Name.md` so they work in the repo and the wiki |
| Dependency or Go version change | `make vuln` |
| Release | Green GitHub CI on the exact commit |

`make live` runs against the real API and needs `.env`. Run it only when a brief asks for live verification.
