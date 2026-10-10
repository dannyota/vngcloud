# Dashboard service coverage

Add read coverage for main-dashboard services the SDK and CLI lack. Models follow `instructions/roles.md`; implementation and reviews run on Codex. CDN path purge stays deferred in `feat/cdn-purge-v061` (see its own plan there).

## Shipped (2026-10-10)

- v0.60.1: transport and login hardening, found by the reviews of the three reads below. Every request path enforces same-scheme, same-host redirects (also after a caller hook); login posts the TOTP code only to the sign-in origin; the credential a request sent is redacted from errors, envelope errors, debug paths, redirect hosts, and captures; transport errors expose fixed text and safe sentinels only. Four adversarial rounds; the last left only a crafted-encoding case (a credential inside nested JSON escape text in a string), accepted as low under the threat model that the server already holds the credential.
- v0.61.0: VKS cluster list, versions, and quota in `hcm-3` and `han-1`.
- v0.62.0: `volume` snapshot backends and policies in `hcm-3`.
- v0.63.0: `backup` Backup Center backends and policies in `hcm-3`.

Each was checked live through the SDK login, reviewed, and tagged on green CI.

## Remaining

| Design | State | Next |
|-|-|-|
| `docs/design/vks.md` | First release shipped | Held reads (cluster detail, node groups, nodes, events) need a populated cluster; kubeconfig needs its own design |
| `docs/design/server-services.md` | Snapshot policy reads shipped | Snapshot history and scheduled operations need populated evidence |
| `docs/design/backup-services.md` | First release shipped | Policy detail, server and destination inventory, points, and history need their evidence checks |
| `docs/design/network-services.md` | Draft | NAT and VPN models need populated evidence |
| `docs/design/database-services.md` | Draft | vDB models need populated evidence |

Read discovery never authorizes activation, purchase, or resource creation. A held read that needs a paid resource for evidence needs the owner's approval first. The discovery captures were deleted after the three first releases took their fixtures.
