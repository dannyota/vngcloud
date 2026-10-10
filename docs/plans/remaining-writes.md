# Remaining writes

Continue the accepted designs. Release one feature per tag. Do not run paid or uncleanable writes without the owner's approval.

## Authorities

- [vStorage](../design/storage.md) owns the storage releases S2 to S6.
- [Verification](../../instructions/verification.md) and [live data](../../instructions/live-data.md) govern checks and releases.

## Shipped

On 2026-10-09: DHCP options sets (v0.38.0), private virtual IPs (v0.39.0), resource tags (v0.40.0), vStorage S1 reads (v0.41.0), the paid vServer writes P2 to P5 (v0.42.0 to v0.45.0), the paid load balancer writes L2 to L6 (v0.46.0 to v0.50.0), and the vMonitor log alarm writes (v0.51.0, checked on a Pro log project bought and deleted the same day for 637 VND net). Every paid write was checked live on the test account with 2,000,000 VND of credit; deletes refunded the unused value and the day cost about 574 VND net. Both paid designs' checks docs hold the results. v0.51.1 (same day) made every vStorage call send the `region` header, so the S1 reads find the project. v0.52.0 (same day) shipped S2: bucket create and delete with the delete wait, checked live on the test project. v0.52.1 decodes the IAM accounts API errors wrapper into the error code. v0.53.0 shipped S3: S3 keys on the vStorage console API with `create-s3-key --secret-file`, checked live including the ten-key limit and an S3 listing. v0.54.0 shipped S4: keys bound to service accounts through the console attach calls and an explicit principal write, checked live.

## Work order

| Branch | State | Remaining work | Release gate |
|-|-|-|-|
| vCDN | C1 pushed (v0.56.0 on green CI); C1b to C4 next | A `cdn` package on the documented vCDN API (`https://vcdn-api.vngcloud.vn/vcdn-api`, `Authorization: Bearer <API key>` from `VNGCLOUD_VCDN_API_KEY`): web accelerator domains, origins, certificates, purge, statistics | One real Web Accelerator on the account for the C1 reads; the owner's cost check before C2 |

## Open on the account

- vStorage project purchase works for the IAM user once the project name is filled: the console form shows Billing method (Pay monthly only; no pay-as-you-go on this account), project type (Gold or Instant Archive), and a 30 GB minimum. The `users/details?generated=true` code 114 error is unrelated and harmless. The create flow is `POST billing-api/v2/price` then `POST internal/v2/orders` (`resourceType: object_storage`, `action: create`, `paymentType: manual`, `resourceInfo: {projectName, purchaseTypeId: 4, projectType: 1, quota, archivePeriod: 0, billingTimeType: block}`), which redirects to the payment console checkout; untick Auto-renew there. The console lists projects with `GET internal/v1/projects?reload=false&load_all=true`; the server fills results only when the request carries the console's `region: <region id>` header, which v0.51.1 added.
- The VPC quota is 2 and one slot is held by `stuck-acl-vpc-*`, which the server cannot delete (needs a support ticket). Live tests that need a VPC borrow an existing one through `VNGCLOUD_LIVE_NETWORK_VPC_ID` when the quota is full.
- vStorage shipped through v0.55.0; the test project was deleted on 2026-10-09 and the unused value was refunded at once (balance 1,968,789 to 1,998,684 VND). A later vStorage live run needs a new project (console purchase, 30,000 VND, refunded on delete).
- vCDN C1 needs one real Web Accelerator to confirm the `cdn/*` body shape (the documented `webacc/*` routes answer 404): the owner creates one in the vCDN Portal, or clears the cost check and C2 creates one.
- The budget `vngcloud-live-cap` (1,000,000 VND a month, alert at 50%) stays on the account for later paid runs.

## Verification state

Local `make check` passed on master on 2026-10-09 with Go 1.27.2. No release is approved solely by local tests.
