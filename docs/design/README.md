# Design

Intended design for the SDK and CLI. A design is approved by the owner before
code starts. Decisions with lasting trade-offs also get an
[ADR](../adr/README.md).

| Design | Status |
|-|-|
| [SDK and CLI](sdk-and-cli.md) | Approved |
| [CLI](cli.md) | Approved |
| [Billing and pricing](billing.md) | Approved |
| [CDN IP ranges](cdn.md) | Approved |
| [vCDN API](cdn-api.md) | Accepted |
| [vCDN CLI and releases](cdn-cli.md) | Accepted |
| [vCDN writes](cdn-writes.md) | Accepted |
| [vMonitor](monitor.md) | Accepted |
| [vMonitor Alerts](monitor-alerts.md) | Accepted |
| [vMonitor Log Alarms](monitor-log-alarms.md) | Accepted |
| [vDNS](dns.md) | Accepted |
| [vStorage](storage.md) | Accepted |
| [vStorage workload access](storage-workload-access.md) | Existing-command guidance; new endpoint contracts remain proposed |
| [vStorage object transfers](storage-objects.md) | Implemented; native SDK and CLI PUT/GET; HAN02 create-only PUT gated |
| [Object operations][object-ops] | ListObjects, HeadObject, DeleteObject, and CopyObject implemented |
| [vStorage projects](storage-projects.md) | Accepted |
| [Prepaid resource auto-renew](auto-renew.md) | Accepted |
| [CLI reads](cli-reads.md) | Accepted |
| [vServer free writes](vserver-writes.md) | Accepted |
| [Free writes survey](free-writes-survey.md) | Accepted |
| [vServer network writes](vserver-network-writes.md) | Accepted |
| [vLB certificates](lb-certificates.md) | Accepted |
| [vCR writes](vcr-writes.md) | Accepted |
| [vLB writes](lb-writes.md) | Accepted |
| [vServer paid writes](vserver-paid-writes.md) | Accepted |
| [IAM writes](iam-writes.md) | Accepted |
| [vServer network writes 2](vserver-network-writes-2.md) | Accepted |
| [CLI usability](cli-usability.md) | Accepted |
| [Encrypted volumes](encrypted-volumes.md) | Accepted |
| [VKS reads](vks.md) | Accepted for the first release |
| [Server service reads](server-services.md) | Accepted for snapshot policy reads |
| [Backup Center reads](backup-services.md) | Accepted for the first release |
| [vNetwork NAT and VPN reads](network-services.md) | Accepted |
| [Public NAT and VPN writes](network-writes.md) | Accepted; Public NAT is implemented; VPN writes remain evidence-gated |
| [Private endpoint lifecycle](network-endpoints.md) | Proposed; API, billing, DNS, and ownership gates remain open |
| [Database inventory](database-services.md) | Draft |
| [Pricing and options discovery](pricing-discovery.md) | Accepted |
| [Service-account authentication](service-account-auth.md) | Accepted and implemented for SDK login, configuration, cache, and CLI |

Service-account login, configuration, cache, and CLI support are implemented.
[Recorded evidence][sa-evidence] establishes the fixed-host exchange and the
tested security-group scope, not server discovery. Server scope, other
services and regions, token expiry behavior, and deletion revocation retain
their [evidence gates][sa-gates].
Approval grants no live-run or policy authority.

[sa-evidence]: service-account-auth-api.md#recorded-live-results
[sa-gates]: service-account-auth-api.md#open-evidence
[object-ops]: storage-object-operations.md
