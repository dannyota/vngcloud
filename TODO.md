# Short-term work

Remove each item in the commit that completes it. Keep completed checkboxes
in the relevant plan and durable rules in the linked authority. A listed
item does not approve implementation or a live run; unresolved evidence,
permission, cost, and resource gates still apply.

- [ ] Implement CDN certificate import, enable, disable, and delete after populated evidence and a scoped test CDN; keep API CDN create deferred. [Contract](docs/design/cdn-writes.md#certificates).
- [ ] Owner resolves the stuck VPC through GreenNode support or a quota increase and obtains a free HCM VPC before database evidence work; do not clean up unidentified existing resources. [Evidence gate](docs/design/database-services.md#availability-and-evidence-limits).
- [ ] Obtain scoped approval and disposable project or CDN prerequisites before another paid storage or CDN live run. [Live-run rules](instructions/live-data.md), [storage contract](docs/design/storage-projects.md), and [CDN contract](docs/design/cdn-writes.md).
- [ ] Qualify held VKS cluster detail, node groups, nodes, and events with populated evidence, and define a separate kubeconfig contract. [Evidence gate](docs/design/vks.md#evidence-and-release-gate).
- [ ] Qualify snapshot history and scheduled operations with populated evidence. [Contract](docs/design/server-services.md).
- [ ] Qualify Backup Center policy detail, server and destination inventory, restore points, and history with populated evidence. [Contract](docs/design/backup-services.md#scope).
- [ ] Qualify remaining NAT detail and rule reads and network parity reads; NAT and VPN lists are implemented. [Contract](docs/design/network-services.md#scope-and-evidence).
- [ ] Complete gated vDB engine designs and populated models after the owner provides a free HCM VPC. [Contract](docs/design/database-services.md).
- [ ] Complete the approved VPN create live probe before implementation that depends on its body and secret contract. [Evidence gates](docs/design/network-writes.md#evidence-gates-and-release-order).
- [ ] Specify manual one-period renewal orders in a later slice; renewal settings do not place orders. [Contract](docs/design/auto-renew.md).
- [ ] Keep root-checkout NAT outside the approved IAM write slice; obtain separate approval for any future contract. [Auth boundary](docs/design/network-writes-nat.md#purchase-and-auth).
- [ ] Owner rotates the vCDN API key. [Credential rules](instructions/security.md#secure-defaults).
- [ ] Bound the IAM user login page and token responses, which `internal/iamuser/login.go` reads with `io.ReadAll`; a hostile or broken sign-in host can exhaust memory. [Security rules](instructions/security.md).
- [ ] Address the remaining scheduler sensitivity in `TestRedactionBoundedHTTPErrorCost`'s elapsed-time assertion without weakening the bounded-work guarantee. Credential-key normalization now has deterministic allocation regressions. [Test rules](instructions/testing.md#test-design).
- [ ] Add flags to set a check's In-alarm, Up, and Undetermined channel lists on `create-check` and `update-check`; today only `--cli-input-json` attaches channels. [Check notifications](docs/design/monitor-alerts.md#check-notifications).
- [ ] Document Telegram channel addresses in the wiki: a negative chat ID needs `--address=-<id>`, and a group upgraded to a supergroup gets a new `-100` ID, so the channel must be re-verified with a new OTP. [Channels](docs/design/monitor-alerts.md#channels).
- [ ] Add a read of a check's per-location test results, so a user can see why one location alarms while another stays OK. [vMonitor](docs/design/monitor.md).
- [ ] Discover and add a channel test send, so a user can prove delivery without breaking a check. [Channels](docs/design/monitor-alerts.md#channels).
- [ ] Add vMonitor dashboard list, get, and delete for user dashboards; GreenNode creates one dashboard per resource and the console does not allow deleting those. [vMonitor](docs/design/monitor.md).
- [ ] Quote a replacement or upgrade of an existing notification quota, and point `pricing get-quote` help at the vMonitor quote commands. [Quote contract](docs/design/monitor-quotas.md#quote-and-price-contract).
- [ ] Show a server's public address in one field or command; a floating IP sits in `InternalInterfaces[].FloatingIP` while `ExternalInterfaces` is empty. [Server reads](docs/design/server-services.md).
- [ ] State each list command's output root (for example `Items`) in help and the wiki so `--query` expressions work the first time, and note that `get-server-console-log` returns only the current boot. [CLI](docs/design/cli.md).
- [ ] Record API evidence under `docs/design/` for the wire models whose type comment says no design document records it (`git grep -n "No design document records"`), then cite that record in the comment. [Go rules](instructions/go.md#formatting-and-comments).
- [ ] Check `compute.Flavor`'s `ZoneID` and `FlavorZoneID` against live `ListFlavors` rows: the rows carry values in a different form from the zone and flavor zone IDs the SDK uses in requests, so code that compares them fails. [Server reads](docs/design/server-services.md).
- [ ] Consider release binaries with checksums and provenance attestations for CI installs; pinned `go install` with the Go checksum database is the supported path today. [Unattended CI](docs/design/cli.md).
