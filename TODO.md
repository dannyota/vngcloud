# Short-term work

Remove each item in the commit that completes it. Keep completed checkboxes
in the relevant plan and durable rules in the linked authority. A listed
item does not approve implementation or a live run; unresolved evidence,
permission, cost, and resource gates still apply.

## Backlog

These are additive and ship in `1.x`.

- [ ] Implement CDN certificate import, enable, disable, and delete after populated evidence and a scoped test CDN; keep API CDN create deferred. [Contract](docs/design/cdn-writes.md#certificates).
- [ ] Obtain scoped approval and disposable project or CDN prerequisites before another paid storage or CDN live run. [Live-run rules](instructions/live-data.md), [storage contract](docs/design/storage-projects.md), and [CDN contract](docs/design/cdn-writes.md).
- [ ] Qualify held VKS cluster detail, node groups, nodes, and events with populated evidence, and define a separate kubeconfig contract. [Evidence gate](docs/design/vks.md#evidence-and-release-gate).
- [ ] Qualify snapshot history and scheduled operations with populated evidence. [Contract](docs/design/server-services.md).
- [ ] Qualify Backup Center server and destination inventory, restore points, and history with populated evidence. [Contract](docs/design/backup-services.md#scope).
- [ ] Qualify NAT user inbound rules, `hcm-3` NAT detail shapes, and network parity reads with populated evidence. [NAT reads](docs/design/network-nat-reads.md) and [parity inventory](docs/design/network-parity-inventory.md).
- [ ] Complete gated vDB engine designs and populated models beyond MySQL 8.0. [Contract](docs/design/database-services.md).
- [ ] Specify manual one-period renewal orders in a later slice; renewal settings do not place orders. [Contract](docs/design/auto-renew.md).
- [ ] Live-verify Public NAT create and delete in `hcm-3` once the test account's VPCs list an enabled AZ (today they list only the disabled HCM03-1A); the SDK already allows it per account. [HCM live check](docs/design/network-nat-hcm-checks.md).
- [ ] Keep root-checkout NAT outside the approved IAM write slice; obtain separate approval for any future contract. [Auth boundary](docs/design/network-writes-nat.md#purchase-and-auth).
- [ ] Add a read of a check's individual run results per location; no stored-result GET has been found, and the console shows them from a new test run. [Diagnostics](docs/design/monitor-console-reads.md).
- [ ] Discover and add a channel test send, so a user can prove delivery without breaking a check. [Channels](docs/design/monitor-alerts.md#channels).
- [ ] Add vMonitor dashboard delete for custom dashboards once the provider is shown to refuse deleting generated dashboards. [Dashboards](docs/design/monitor-console-reads.md#dashboards).
- [ ] Quote an upgrade of an existing Email or SMS notification quota after observing the resize quote on an active quota. [Resize quote](docs/design/monitor-console-reads.md#existing-notification-quota-resize-quote).
- [ ] Consider release binaries with checksums and provenance attestations for CI installs; pinned `go install` with the Go checksum database is the supported path today. [Unattended CI](docs/design/cli.md).
- [ ] Design bandwidth package reads when a consumer needs them; the `han-1` and `hcm-3` console configurations (`vserver/assets/config/vserver-portal.json`) both set `BANDWIDTH_GATE_WAY=/vserver/bandwidth-api` on the regional host. [Pricing discovery](docs/design/pricing-discovery-api.md).
- [ ] Verify the HAN endpoint subnet picker on a VPC with an eligible subnet before offering endpoint quote or create in `han-1`; endpoint billing evidence also gates any HAN create. [Endpoint API](docs/design/network-endpoints-api.md).
- [ ] Verify service-account server scope with a scoped profile and one approved server before claiming it. [Service-account auth](docs/design/service-account-auth.md).
- [ ] Add an auto-renew read and toggle for endpoint billing records, like the vStorage project toggle; the endpoint create request for renewal off is not reflected in billing. [Auto-renew](docs/design/auto-renew.md).
