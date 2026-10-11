# Dashboard service coverage

Completed dashboard reads stay checked below. [Short-term work](../../TODO.md) owns the remaining evidence and prerequisite actions. Follow [model routing](../../AGENTS.md#ai-agent-definitions-and-routing), [workflow duties](../../instructions/workflow.md#duties), and the linked domain contracts.

## Completed work

- [x] Harden transport and login against credential leaks and foreign-origin redirects under the [security rules and redaction limitation](../../instructions/security.md#secure-defaults).
- [x] Add VKS cluster lists, versions, and quota under the [VKS contract](../design/vks.md).
- [x] Add snapshot backends and policies under the [server service contract](../design/server-services.md#snapshot-policy-reads).
- [x] Add Backup Center backends and policies under the [backup contract](../design/backup-services.md).
- [x] Add safe server console-log reads under the [server API contract](../design/server-services-api.md).
- [x] Add public NAT and site-to-site VPN lists under the [network read contract](../design/network-services.md).
- [x] Add CDN path purge under the [CDN write contract](../design/cdn-writes.md#purge).

Held VKS, snapshot history, scheduled operation, Backup Center, and network parity reads retain their domain evidence gates. [Database inventory](../design/database-services.md) also requires populated models and owner-provided prerequisites. Existing list implementations do not qualify held detail or rule reads.

Read discovery never authorizes activation, purchase, or resource creation. Evidence work that needs a paid resource requires scoped owner approval under the [live-data rules](../../instructions/live-data.md). The manager alone performs authorized live verification under the [testing rules](../../instructions/testing.md#live-verification). [Release notes](../../RELEASE_NOTES.md) retain versioned facts; these checkboxes do not assert a new live check or release approval.
