# Go

The module remains `danny.vn/vngcloud`. Follow the reviewed [SDK](../docs/design/sdk-and-cli.md) and [CLI](../docs/design/cli.md) contracts.

## Toolchain and package boundaries

`go.mod` requires the Go version pinned in `.tool-versions`; local work and CI use that version. Follow [security](security.md#tools-and-scanners) for tool updates and mirrored pins.

Each service is a public root package. Root `vngcloud` holds only shared config, auth, errors, and helpers and never imports a service package. Service types live in their service package. `vngcloud.go` re-exports only shared config, auth, error, and helper names. Private implementation lives under `internal/`.

The CLI calls the API only through the public SDK, rooted at `danny.vn/vngcloud` and its public service packages. Report a missing SDK method for a separately briefed SDK change instead of bypassing the SDK or editing outside owned paths.

The SDK remains standard-library only. The CLI and tooling have standing approval for dependencies that meet the [vetting criteria](security.md#dependencies). Record the reason in the approved design or active plan before relying on a dependency.

## Formatting and comments

Every exported symbol, every exported field of an operation Input type, and every exported field of the SDK's client configuration types has a doc comment; wire model types that mirror API responses cite a `docs/design/` document recording their fields, response, or observations, or state that no design document records their evidence, and their fields carry comments only where the meaning is verified.

Follow Google Go style and the repository's `golangci-lint` configuration. `make fmt` uses `golangci-lint fmt` with `gofmt` and `gci`; import groups are standard library, other imports, then `danny.vn/vngcloud`. Do not add a separate `goimports` dependency.

Keep `golangci-lint`, including gosec, at zero issues. A justified `//nolint:<linter>` needs its reason on the same line. [Security](security.md#tools-and-scanners) owns the scanner and suppression rules.

Non-test Go files stay at or under 700 lines, with the existing generated-file and fixture exemptions; [file length](documentation.md#file-length) sets the ratchet and split rules.

## Code rules

Apply these to new Go code. Leave existing declarations as they are during bounded edits and mechanical moves, such as adding an entry to an existing operation table; do not sweep unrelated code.

- Wrap an error with `%w` once, at the boundary where callers need the cause, and test causes with `errors.Is` or `errors.As`. Never put tokens, secrets, or response bodies in error text.
- Pass `context.Context` first to work that accepts it and propagate cancellation.
- Keep no package-level mutable runtime state. Inject clients, clocks, entropy, caches, and locks. Only immutable sentinel errors, compiled regular expressions, and embedded assets live at package level; write new read-only lookups as switch functions.
- Define small interfaces at the consumer, only for a real dependency, and return concrete types.
- Use named table-driven subtests for repeated cases; keep distinct scenarios and concurrency tests separate.
- `cmd/vngcloud/main.go` only parses, wires, and owns lifecycle; command construction lives under `internal/cli/`.

A comment explains a constraint the code cannot express; do not narrate the code. Use a design or ADR citation or state the invariant, never a plan, task, or finding ID. Follow [documentation](documentation.md#writing-rules) for plain wording and current-state claims.

Follow [testing](testing.md) for test-first development, deterministic tests, fixtures, and required checks.
