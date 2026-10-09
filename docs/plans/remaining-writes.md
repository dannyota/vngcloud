# Remaining writes

Continue the accepted designs in existing feature worktrees. Release one feature per tag. Do not run paid or uncleanable writes without the owner's approval.

## Authorities

- The `monitor-log-alarm-writes` branch holds the accepted log alarm design in `docs/design/monitor-log-alarms.md`.
- [vStorage](../design/storage.md) owns the storage releases S1 to S6.
- [Paid vServer writes](../design/vserver-paid-writes.md) owns the remaining volume and server releases.
- [Load balancer writes](../design/lb-writes.md) owns the remaining load balancer releases.
- [Verification](../../instructions/verification.md) and [live data](../../instructions/live-data.md) govern checks and releases.

## Shipped

DHCP options sets (v0.38.0), private virtual IPs (v0.39.0), and resource tags (v0.40.0) shipped on 2026-10-09 after their live runs passed and the bill for 2026-09-25 to 2026-10-09 showed no cost. The tag contract is one writer per resource at a time, stated in the wiki.

## Work order

| Branch | State | Remaining work | Release gate |
|-|-|-|-|
| `storage-reads` | S1 SDK committed; CLI in progress | Review, release notes, merge, verify CI, tag | None: reads only |
| `free-writes` | Holds the log alarm merge on top of v0.40.0 | Update from master when the gate opens; live check; release | An ACTIVE log project. The Basic class order quota (3 in a rolling month) was still exhausted on 2026-10-09 (409); retry after 2026-10-26 |
| `paid-vserver` | Merged with master on 2026-10-09; adversarial review running | Fix review findings; live runs L1 to L3 in design order; tag P2 to P5 | Credit of at least 1,000,000 VND and an approved cap per run |
| `paid-lb` | Merged with master on 2026-10-09; adversarial review running | Fix review findings; live checks L2 to L6 in design order; tag each | Credit, a billing budget with an alert at the cap, and next-day bills matching quotes |
| vStorage S2 to S6 | Not started | Buckets, S3 keys, key attach, bucket policy, bucket settings | A pay-as-you-go vStorage project, about 1,400 VND a month |

Quote-only releases for paid vServer and load balancer work have already shipped. Preserve the remaining branch commits when integrating each later release.

## Verification state

Local `make check` passed on every branch above on 2026-10-09 with Go 1.27.2. The paid branches have no live evidence yet. No release is approved solely by local tests.
