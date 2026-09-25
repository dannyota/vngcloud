# Getting Started

## Install

```bash
go get danny.vn/vngcloud
```

The module needs Go 1.27.1 or later.

## Create a client

`vngcloud.LoadConfig` is the default way to build a `Config`: it resolves
region, project, and credentials from options, `VNGCLOUD_*` environment
variables, and the AWS-style profile files under `~/.vngcloud/`, in that
order. See [Configuration](Configuration.md#loadconfig) for the precedence
rules, the file formats, and every environment variable.

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

	cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"))
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

That example needs a `~/.vngcloud/credentials` file (see
[Configuration](Configuration.md#loadconfig)) or `VNGCLOUD_ROOT_EMAIL`,
`VNGCLOUD_USERNAME`, and `VNGCLOUD_PASSWORD` set. To build a `Config` from Go
values only, with no environment or file lookups, use `vngcloud.NewConfig`
instead:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
		RootEmail: "<root-email>",
		Username:  "<iam-username>",
		Password:  "<password>",
	}),
)
```

Neither `LoadConfig` nor `NewConfig` logs in. Call `cfg.Authenticate(ctx)` to
log in now, so bad credentials fail before the first service call runs; skip
it and the SDK logs in lazily on that first call instead. Each Config
targets one region. Build one Config per region you need, and pass it to a
service package's `New`. Every service client built from the same Config
shares one login and one project lookup.

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

## Command line

Install the `vngcloud` command:

```bash
go install danny.vn/vngcloud/cmd/vngcloud@latest
```

It works like the AWS CLI: `vngcloud <service> <operation> [flags]`.

```bash
vngcloud configure                      # prompts; secrets are not echoed
vngcloud billing list-budgets
vngcloud compute list-servers --query 'Items[].Name' --output text
vngcloud billing delete-budget --budget-uuid <uuid> --yes
```

- Output is JSON by default; `--output table` and `--output text` also work,
  and `--query` takes a JMESPath expression.
- Errors go to stderr as one JSON line with a stable exit code, so scripts
  and AI agents can act on them.
- Deletes need `--yes`, and nothing prompts without a terminal.
- A profile with `read_only = true`, `VNGCLOUD_READ_ONLY=1`, or `--read-only`
  refuses every write command. Give an AI agent a read-only profile.

[CLI](CLI.md) lists every command and flag.

## Next steps

- Pick an auth method in [Authentication](Authentication.md).
- Set the project or endpoints in [Configuration](Configuration.md).
- Find the method you need in [Services](Services.md).
- Cap spend and price a resource in [Billing and Pricing](Billing-and-Pricing.md).
