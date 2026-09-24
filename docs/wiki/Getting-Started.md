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

`NewClient` logs in right away, so bad credentials fail at construction. Each
client targets one region. Create one client per region you need.

## Next steps

- Pick an auth method in [Authentication](Authentication.md).
- Set the project or endpoints in [Configuration](Configuration.md).
- Find the method you need in [Services](Services.md).
