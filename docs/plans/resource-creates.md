# Missing resource creates

Add the six missing create workflows to the Go SDK and CLI with the catalog reads, confirmation, and cleanup each needs. Continue shipping individual `0.x` releases. Existing creates and billing reads stay in place; do not add account funding or payment-method workflows.

## Work order

|Slice|Established evidence|Next gate|
|-|-|-|
|[ ] Global Load Balancer|Bare Layer 4 create and delete ship under the [GLB create design](../design/glb-create.md). The [official API reference](https://docs.api.greennode.ai/service-docs/glb-api.html) declares listener and pool writes.|Review the [TCP listener and pool design](../design/glb-pool.md), then implement and run its live probe.|
|[x] VKS cluster|Private cluster create and delete ship under the [VKS create design](../design/vks-create.md).|None.|
|[ ] Site-to-site VPN|The [accepted contract](../design/network-writes.md#evidence-gates-and-release-order) has inventory and intended SDK/CLI scope. The [console guide](https://docs.greennode.ai/vnetwork/vpn-virtual-private-network-site-to-site/create-vpn-site-to-site) describes checkout, not proof of an API-only credit purchase.|Verify the guided prerequisites, quote inputs, and whether existing credit covers the approved flow without checkout. Keep [body and payment evidence](../design/network-writes-api.md#probes-before-implementation) open.|
|[ ] Private endpoint|The [console guide](https://docs.greennode.ai/vnetwork/endpoint/create-endpoint) describes VPC and private DNS choices and a price summary. The SDK exposes inventory.|Recheck service, VPC/subnet, package, private DNS, and price at the console. Establish the [missing API and billing contract](../design/network-endpoints-api.md) before SDK writes.|
|[ ] CDN Web Accelerator|The [create design](../design/cdn-writes.md#create) records an API refusal and a separate console form. GreenNode publishes a [vCDN API guide](https://docs.greennode.ai/vcdn/api-developers).|Rewalk the console flow and compare prerequisites and submission format before classifying the refusal as an API fault. Do not implement a console form or guess missing identity fields.|
|[x] vDB|Private single-node MySQL 8.0 create and delete ship under the [vDB create design](../design/vdb-create.md).|Evaluate other engines separately.|

## Gates per slice

1. Record the SDK/CLI contract and have it reviewed before implementation. Public schema proves what is documented, not account permission, actual charges, or refund terms.
2. Recheck incomplete or failing flows from the start in Playwright. Compare identity scope, region, prerequisite reads, selected products, and confirmation inputs. Stop before any unapproved submission; keep captures sanitized and out of Git.
3. Require an evidenced quote and explicit consent for a paid create. Send the order once; preserve uncertainty for read-back. Do not depend on refunds, automatic cleanup of unrelated resources, or account replenishment.
4. Implement and test the approved contract, update generated CLI references and wiki, and obtain scoped live evidence and exact-head CI before release. This plan authorizes no live write or charge.
