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
	"danny.vn/vngcloud/compute"
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

	// Authenticate logs in now, so bad credentials fail here instead of on
	// the first service call. It is optional: a call below would log in
	// lazily on its own if this were skipped.
	if err := cfg.Authenticate(ctx); err != nil {
		log.Fatal(err)
	}

	servers, err := compute.New(cfg).ListServers(ctx, &compute.ListServersInput{
		Page: 1,
		Size: vngcloud.DefaultPageSize,
	})
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("servers: %d", len(servers.Items))
}
```

`NewConfig` does not log in. Call `cfg.Authenticate(ctx)` to log in now, so
bad credentials fail before the first service call runs; skip it and the SDK
logs in lazily on that first call instead. Each Config targets one region.
Build one Config per region you need, and pass it to a service package's
`New`. Every service client built from the same Config shares one login and
one project lookup.

## Billing example

`billing` and `pricing` are separate packages built from the same Config,
exactly like `compute`. See [Billing and Pricing](Billing-and-Pricing.md) for
their full surface. A minimal read:

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
