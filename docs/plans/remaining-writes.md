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
| vStorage S2 to S6 | Project ready | Buckets, S3 keys, key attach, bucket policy, bucket settings | None: project `vngcloud-live-s2` (id `baa489b3c534843d810ec8d3c63bde92`, HCM04, Gold, 30 GB, Pay monthly, 30,000 VND, no auto-renew, bought 2026-10-09, ends 2026-11-08) |

## Open on the account

- vStorage project purchase works for the IAM user once the project name is filled: the console form shows Billing method (Pay monthly only; no pay-as-you-go on this account), project type (Gold or Instant Archive), and a 30 GB minimum. The `users/details?generated=true` code 114 error is unrelated and harmless. The create flow is `POST billing-api/v2/price` then `POST internal/v2/orders` (`resourceType: object_storage`, `action: create`, `paymentType: manual`, `resourceInfo: {projectName, purchaseTypeId: 4, projectType: 1, quota, archivePeriod: 0, billingTimeType: block}`), which redirects to the payment console checkout; untick Auto-renew there. The console lists projects with `GET internal/v1/projects?reload=false&load_all=true`; the SDK `ListProjects` returned no items for the new project because the server fills the list only when the request carries the console's `region: <region id>` header (`region_id` alone, `user_id`, and `portal-user-id` do not); every vStorage call must send `region` and this fix ships before S2.
- The VPC quota is 2 and one slot is held by `stuck-acl-vpc-*`, which the server cannot delete (needs a support ticket). Live tests that need a VPC borrow an existing one through `VNGCLOUD_LIVE_NETWORK_VPC_ID` when the quota is full.
- The budget `vngcloud-live-cap` (1,000,000 VND a month, alert at 50%) stays on the account for later paid runs.

## Verification state

Local `make check` passed on master on 2026-10-09 with Go 1.27.2. No release is approved solely by local tests.
