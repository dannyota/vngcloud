# Short-term work

Remove each item in the commit that completes it. Keep completed checkboxes
in the relevant plan and durable rules in the linked authority. A listed
item does not approve implementation or a live run; unresolved evidence,
permission, cost, and resource gates still apply.

## Owner

- [ ] Owner rotates the vCDN API key. [Credential rules](instructions/security.md#secure-defaults).

## Backlog

These are additive and ship in `1.x`.

- [ ] Implement CDN certificate import, enable, disable, and delete after populated evidence and a scoped test CDN; keep API CDN create deferred. [Contract](docs/design/cdn-writes.md#certificates).
- [ ] Obtain scoped approval and disposable project or CDN prerequisites before another paid storage or CDN live run. [Live-run rules](instructions/live-data.md), [storage contract](docs/design/storage-projects.md), and [CDN contract](docs/design/cdn-writes.md).
- [ ] Qualify held VKS cluster detail, node groups, nodes, and events with populated evidence, and define a separate kubeconfig contract. [Evidence gate](docs/design/vks.md#evidence-and-release-gate).
- [ ] Qualify snapshot history and scheduled operations with populated evidence. [Contract](docs/design/server-services.md).
- [ ] Qualify Backup Center policy detail, server and destination inventory, restore points, and history with populated evidence. [Contract](docs/design/backup-services.md#scope).
- [ ] Qualify remaining NAT detail and rule reads and network parity reads; NAT and VPN lists are implemented. [Contract](docs/design/network-services.md#scope-and-evidence).
- [ ] Complete gated vDB engine designs and populated models beyond MySQL 8.0. [Contract](docs/design/database-services.md).
- [ ] Specify manual one-period renewal orders in a later slice; renewal settings do not place orders. [Contract](docs/design/auto-renew.md).
- [ ] Keep root-checkout NAT outside the approved IAM write slice; obtain separate approval for any future contract. [Auth boundary](docs/design/network-writes-nat.md#purchase-and-auth).
- [ ] Add flags to set a check's In-alarm, Up, and Undetermined channel lists on `create-check` and `update-check`; today only `--cli-input-json` attaches channels. [Check notifications](docs/design/monitor-alerts.md#check-notifications).
- [ ] Add a read of a check's per-location test results, so a user can see why one location alarms while another stays OK. [vMonitor](docs/design/monitor.md).
- [ ] Discover and add a channel test send, so a user can prove delivery without breaking a check. [Channels](docs/design/monitor-alerts.md#channels).
- [ ] Add vMonitor dashboard list, get, and delete for user dashboards; GreenNode creates one dashboard per resource and the console does not allow deleting those. [vMonitor](docs/design/monitor.md).
- [ ] Quote a replacement or upgrade of an existing notification quota, and point `pricing get-quote` help at the vMonitor quote commands. [Quote contract](docs/design/monitor-quotas.md#quote-and-price-contract).
- [ ] Record API evidence under `docs/design/` for the wire models whose type comment says no design document records it (`git grep -n "No design document records"`), then cite that record in the comment. [Go rules](instructions/go.md#formatting-and-comments).
- [ ] Consider release binaries with checksums and provenance attestations for CI installs; pinned `go install` with the Go checksum database is the supported path today. [Unattended CI](docs/design/cli.md).
- [ ] Ratchet non-test Go to 600 lines after the billing history and floating IP slices land, then to 500, one pull request each. Extend `scripts/test/check-lengths-test.sh` to the new limit in each step. [File length](instructions/documentation.md#file-length).
- [ ] Add an auto-renew read and toggle for endpoint billing records, like the vStorage project toggle; the endpoint create request for renewal off is not reflected in billing. [Auto-renew](docs/design/auto-renew.md).
