# Getting Started

## Install

```bash
go get danny.vn/vngcloud
```

The module needs Go 1.27.1 or later.

## Create a client

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client, err := vngcloud.NewClient(ctx, cfg)
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

`NewConfig` logs the IAM User in right away, so bad credentials fail before
`NewClient` runs. Each Config targets one region. Build one Config per region
you need, and pass it to `NewClient` or to a service package's `New`.

## Billing example

`billing` and `pricing` are separate packages built from the same Config;
they do not hang off the client `NewClient` returns. See
[Billing and Pricing](Billing-and-Pricing.md) for their full surface. A
minimal read:

```go
import "danny.vn/vngcloud/billing"

billingClient := billing.New(cfg)
budgets, err := billingClient.ListBudgets(ctx, nil)
if err != nil {
	log.Fatal(err)
}
log.Printf("budgets: %d", len(budgets.Items))
```

## Next steps

- Pick an auth method in [Authentication](Authentication.md).
- Set the project or endpoints in [Configuration](Configuration.md).
- Find the method you need in [Services](Services.md).
- Cap spend and price a resource in [Billing and Pricing](Billing-and-Pricing.md).
