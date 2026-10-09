# Remaining writes

Continue the accepted designs. Release one feature per tag. Do not run paid or uncleanable writes without the owner's approval.

## Authorities

- [vStorage](../design/storage.md) owns the storage releases S2 to S6.
- [Verification](../../instructions/verification.md) and [live data](../../instructions/live-data.md) govern checks and releases.

## Shipped

On 2026-10-09: DHCP options sets (v0.38.0), private virtual IPs (v0.39.0), resource tags (v0.40.0), vStorage S1 reads (v0.41.0), the paid vServer writes P2 to P5 (v0.42.0 to v0.45.0), the paid load balancer writes L2 to L6 (v0.46.0 to v0.50.0), and the vMonitor log alarm writes (v0.51.0, checked on a Pro log project bought and deleted the same day for 637 VND net). Every paid write was checked live on the test account with 2,000,000 VND of credit; deletes refunded the unused value and the day cost about 574 VND net. Both paid designs' checks docs hold the results.

## Work order

| Branch | State | Remaining work | Release gate |
|-|-|-|-|
| vStorage S2 to S6 | Not started | Buckets, S3 keys, key attach, bucket policy, bucket settings | A pay-as-you-go vStorage project, about 1,400 VND a month, bought in the console |

## Open on the account

- vStorage S2 to S6 need a vStorage project. The account has no vStorage storage user record and the vStorage backend cannot create one: `/internal/v1/users/details?generated=true` answers code 114 "Error occurred in adding storage user <root email>" for the IAM user in both regions, so the console's create-project form stays empty and disabled. No IAM policy fixes it (the user already holds `vstorage:*` and `billing:*`). The owner signs in as root and opens vStorage, Project, Create a project once; if the form fills, buys the smallest Pay monthly project in HCM04; if the same error shows, opens a ticket quoting that endpoint and code.
- The VPC quota is 2 and one slot is held by `stuck-acl-vpc-*`, which the server cannot delete (needs a support ticket). Live tests that need a VPC borrow an existing one through `VNGCLOUD_LIVE_NETWORK_VPC_ID` when the quota is full.
- The budget `vngcloud-live-cap` (1,000,000 VND a month, alert at 50%) stays on the account for later paid runs.

## Verification state

Local `make check` passed on master on 2026-10-09 with Go 1.27.2. No release is approved solely by local tests.
