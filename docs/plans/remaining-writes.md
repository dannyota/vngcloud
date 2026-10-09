# Remaining writes

Continue the accepted designs. Release one feature per tag. Do not run paid or uncleanable writes without the owner's approval.

## Authorities

- The `monitor-log-alarm-writes` branch holds the accepted log alarm design in `docs/design/monitor-log-alarms.md`; the `free-writes` branch holds that work merged on top of master.
- [vStorage](../design/storage.md) owns the storage releases S2 to S6.
- [Verification](../../instructions/verification.md) and [live data](../../instructions/live-data.md) govern checks and releases.

## Shipped

On 2026-10-09: DHCP options sets (v0.38.0), private virtual IPs (v0.39.0), resource tags (v0.40.0), vStorage S1 reads (v0.41.0), the paid vServer writes P2 to P5 (v0.42.0 to v0.45.0), and the paid load balancer writes L2 to L6 (v0.46.0 to v0.50.0). Every paid write was checked live on the test account with 2,000,000 VND of credit; deletes refunded the unused value and the day cost about 574 VND net. Both paid designs' checks docs hold the results.

## Work order

| Branch | State | Remaining work | Release gate |
|-|-|-|-|
| `free-writes` | Holds the log alarm merge, current with master as of v0.41.0 | Update from master; live check; release | An ACTIVE log project. The Basic class order quota (3 in a rolling month) was still exhausted on 2026-10-09 (409); retry after 2026-10-26 |
| vStorage S2 to S6 | Not started | Buckets, S3 keys, key attach, bucket policy, bucket settings | A pay-as-you-go vStorage project, about 1,400 VND a month, bought in the console |

## Open on the account

- The VPC quota is 2 and one slot is held by `stuck-acl-vpc-*`, which the server cannot delete (needs a support ticket). Live tests that need a VPC borrow an existing one through `VNGCLOUD_LIVE_NETWORK_VPC_ID` when the quota is full.
- The budget `vngcloud-live-cap` (1,000,000 VND a month, alert at 50%) stays on the account for later paid runs.

## Verification state

Local `make check` passed on master and `free-writes` on 2026-10-09 with Go 1.27.2. No release is approved solely by local tests.
