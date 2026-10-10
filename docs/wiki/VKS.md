# VKS

`danny.vn/vngcloud/vks` reads Kubernetes cluster inventory, supported
versions, and account quota in `hcm-3` and `han-1`.

```go
client := vks.New(cfg)
clusters, err := client.ListClusters(ctx, nil)
versions, err := client.ListClusterVersions(ctx, nil)
quota, err := client.GetQuota(ctx, nil)
```

Each call sends one GET with the Config's IAM bearer token to the verified
regional console endpoint. The service uses no project ID or project
header. `Config`'s project ID does not select a VKS workspace. Unsupported
regions return `vngcloud.ErrInvalidConfig` before login, even with a VKS
endpoint override. A zero Config returns the same error without a panic.

## Pagination

`ListClusters` accepts `Page` and `Size`. Pages start at 0. Nil input and
zero fields select page 0 and size 10. To request the next page:

```go
next, err := client.ListClusters(ctx, &vks.ListClustersInput{
    Page: 1,
    Size: 10,
})
```

Negative values and values above 2147483647 return
`vngcloud.ErrInvalidInput` before a request. A positive size is sent as
`pageSize`. The service has no verified page-size cap. The output has
`Items`, `Page`, `PageSize`, `TotalItem`, and `TotalPage`. Total pages are
computed from the returned total and size. An empty total yields zero
pages. Calls do not collect later pages automatically. Filters are not
exposed.

## Models and errors

`Cluster` carries ID, name, description, status, release channel, version,
availability-zone strategy, timestamps, node count, and private-cluster
enablement. Dates and enum values stay strings. Cluster item decoding is
schema-backed and has not been verified against a populated live list.

`ListClusterVersions` returns `Items []ClusterVersion` with `Version`,
`Enable`, `Stage`, and optional `DeprecatedAt`. Absent or null deprecation
dates remain empty strings. `GetQuota` returns `Quota` with `MaxClusters`,
`NumClusters`, `MaxNodeGroupsPerCluster`, and `MaxNodesPerNodeGroup`.
Present false enablement and zero quota values are valid.

The three reads require HTTP 200 and the documented envelopes. Missing
fields, null required fields, wrong types, and malformed pagination fail
with a fixed malformed-response error. HTTP errors retain status,
retryability, and the shared authentication, permission, not-found, and
rate-limit classes. Upstream messages, codes, and arbitrary error causes
are withheld. Cancellation and deadline errors retain their sentinels.
See [Errors](Errors.md).

## Output and scope

Inventory includes account data such as names and descriptions. Unknown
fields are dropped. SDK response capture is disabled for VKS, including
error responses. The basic example writes decoded inventory under its
ignored SDK output directory and creates no VKS raw-capture file.

Cluster detail, node groups, nodes, and events are held for later releases.
Kubeconfig, credentials, cluster writes, upgrades, scaling, workspace
activation, and fleet operations are excluded. No method follows returned
Kubernetes endpoints or reads local Kubernetes configuration.
