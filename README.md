# vngcloud

Read-only Go SDK for VNG Cloud IAM User workflows.

This SDK currently supports IAM User authentication, project discovery, and
read-only APIs for Compute, Volume, Network, top-level Load Balancer resources,
Global Load Balancer metadata, DNS, and Container Registry.

Service account authentication and write APIs are intentionally out of scope for now.

## Install

```bash
go get danny.vn/vngcloud
```

Requires Go 1.24+.

## Quick Start

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
)

func main() {
	ctx := context.Background()

	client, err := vngcloud.NewClient(ctx, vngcloud.Config{
		Region: "hcm-3",
		IAMUser: &vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	servers, err := client.Compute.ListServers(ctx, &vngcloud.ListServersOptions{
		Page: 1,
		Size: vngcloud.DefaultPageSize,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("servers: %d", len(servers.Items))
}
```

`ProjectID` is optional. When a service method needs a project, the SDK lists IAM
User projects for the configured region and auto-selects the project when exactly
one match exists. You can also call `client.ListProjects(ctx, nil)` explicitly.

## SDK Shape

The SDK exposes one flat public package. Service clients are grouped by VNG Cloud
product:

```go
client.Compute
client.Volume
client.Network
client.LoadBalancer
client.GlobalLoadBalancer
client.DNS
client.ContainerRegistry
client.Portal
```

Public resource models live in the root package, for example
`vngcloud.Server`, `vngcloud.Volume`, and `vngcloud.VPC`.

Server URL versions such as `v1` and `v2` are internal route metadata, not SDK
package versions.

## Example

```bash
cp examples/basic/config.example.json examples/basic/config.local.json
$EDITOR examples/basic/config.local.json
go run ./examples/basic -config examples/basic/config.local.json
```

The basic example reads IAM User credentials, runs read-only API calls, writes
local output under `examples/basic/output/`, and validates the output shape before
exit. The output directory is ignored by git and may contain sensitive data.

## Docs

- [Guides](GUIDES.md)
- [Features](FEATURES.md)
