# Dashboard service coverage

Add read coverage for main-dashboard services the SDK and CLI lack. Models follow `instructions/roles.md`. At most three workers, on disjoint file sets, in service worktrees; shared plumbing files merge in release order. CDN path purge stays deferred in `feat/cdn-purge-v061` (see its own plan there).

## Designs

| Design | State | Worktree |
|-|-|-|
| `docs/design/vks.md` | Accepted for the first release (2026-10-10, owner) | `vks-reads` |
| `docs/design/server-services.md` | Accepted for snapshot backend and policy reads (2026-10-10, owner) | `server-services` |
| `docs/design/backup-services.md` | Accepted for the first release (2026-10-10, owner) | `backup-services` |
| `docs/design/network-services.md` | Draft; NAT and VPN models need populated evidence | none |
| `docs/design/database-services.md` | Draft; vDB models need populated evidence | none |

Held reads (VKS details and node groups, snapshot history, scheduled operations, Backup Center inventory, details, and history, NAT, VPN, vDB) need their evidence gates before a later design approval. Read discovery never authorizes activation, purchase, or resource creation.

## Evidence

Read-only Playwright discovery on 2026-10-10 with `scripts/browser-login.js`. Private captures are in the coordination worktree under `examples/basic/output/raw/discovery/` (mode 0600, ignored). Never stage them. Tracked fixtures replace names, IDs, and account quota and usage counts with synthetic values. Delete the discovery captures once the three first releases have their fixtures.

## Release order

Each row is one tag. Versions are assigned at tag time from the latest tag.

| Release | SDK | CLI | Review | Live read | Tag |
|-|-|-|-|-|-|
| VKS inventory | pending | pending | pending | pending | pending |
| Snapshot backend and policy reads | pending | pending | pending | pending | pending |
| Backup Center backend and policy lists | pending | pending | pending | pending | pending |

Per release: an sdk worker builds the package, endpoint plumbing, fixtures, example, live read test, and SDK wiki; the manager inspects the SDK and runs `make check`; a cli worker adds the commands and CLI wiki; one review covers the release, including token routing to the new host; the manager runs the live read, merges in release order, pushes, and tags on green CI.

## Shared files

Every release touches the endpoint plumbing (`internal/core/config.go`, `internal/core/client.go`, `internal/endpoints/`, `internal/routes/routes.go`), the example dispatcher, the CLI root and service titles, and the wiki sidebar. Each worktree edits them for its own product only; the manager resolves the additive conflicts when merging in release order.
