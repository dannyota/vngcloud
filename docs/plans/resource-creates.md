# Missing resource creates

Add the six missing create workflows to the Go SDK and CLI with the catalog reads, confirmation, and cleanup each needs. Continue shipping individual `0.x` releases. Existing creates and billing reads stay in place; do not add account funding or payment-method workflows.

## Work order

|Slice|Established evidence|Next gate|
|-|-|-|
|[x] Global Load Balancer|Bare Layer 4 create and delete ship under the [GLB create design](../design/glb-create.md); TCP pool and listener create and delete ship under the [TCP children design](../design/glb-pool.md).|None.|
|[x] VKS cluster|Private cluster create and delete ship under the [VKS create design](../design/vks-create.md).|None.|
|[-] Site-to-site VPN|Out of scope for `v1.0.0` by owner decision (rarely used). The [accepted contract](../design/network-writes.md#evidence-gates-and-release-order) keeps inventory and the intended scope.|Reopen only by owner decision.|
|[x] Private endpoint|vStorage Standard endpoint quote, create, and delete in `hcm-3` ship under the [endpoint create design](../design/endpoint-create.md).|Evaluate other services and regions separately.|
|[-] CDN Web Accelerator|Out of scope for `v1.0.0` by owner decision (rarely used). The [create design](../design/cdn-writes.md#create) records the API refusal.|Reopen only by owner decision.|
|[x] vDB|Private single-node MySQL 8.0 create and delete ship under the [vDB create design](../design/vdb-create.md).|Evaluate other engines separately.|

## Gates per slice

1. Record the SDK/CLI contract and have it reviewed before implementation. Public schema proves what is documented, not account permission, actual charges, or refund terms.
2. Recheck incomplete or failing flows from the start in Playwright. Compare identity scope, region, prerequisite reads, selected products, and confirmation inputs. Stop before any unapproved submission; keep captures sanitized and out of Git.
3. Require an evidenced quote and explicit consent for a paid create. Send the order once; preserve uncertainty for read-back. Do not depend on refunds, automatic cleanup of unrelated resources, or account replenishment.
4. Implement and test the approved contract, update generated CLI references and wiki, and obtain scoped live evidence and exact-head CI before release. This plan authorizes no live write or charge.
