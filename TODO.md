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
- [ ] Type `portal.Zone` so `portal list-zones` prints the reviewed field names instead of raw lowercase map keys. [CLI read contract](docs/design/cli-reads.md).
- [ ] Complete the approved VPN create live probe before implementation that depends on its body and secret contract. [Evidence gates](docs/design/network-writes.md#evidence-gates-and-release-order).
- [ ] Specify manual one-period renewal orders in a later slice; renewal settings do not place orders. [Contract](docs/design/auto-renew.md).
- [ ] Keep root-checkout NAT outside the approved IAM write slice; obtain separate approval for any future contract. [Auth boundary](docs/design/network-writes-nat.md#purchase-and-auth).
- [ ] Owner rotates the vCDN API key. [Credential rules](instructions/security.md#secure-defaults).
