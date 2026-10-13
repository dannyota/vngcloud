# Getting Started

## Prerequisites

Use Go 1.27.2 or later. Choose credentials that have permission for the read
you want to make, and select a region. Compute also needs a project: set one
explicitly when more than one project matches the region.

- For IAM-user login, configure a profile with `vngcloud configure` after
  installing the CLI below. You need the root account email, IAM username,
  password, and a two-factor secret if the account requires it.
- For service-account login or environment-based setup, follow
  [Configuration](Configuration.md#loadconfig) and choose the method in
  [Authentication](Authentication.md).
- For application S3 access from vServer or VKS, follow
  [Storage Workload Access](Storage-Workload-Access.md). Native S3 object
  credentials are separate from the management clients used below.

## Install

```bash
go get danny.vn/vngcloud@latest
```

Install the CLI if you will use commands or configure profiles interactively:

```bash
go install danny.vn/vngcloud/cmd/vngcloud@latest
```

Add Go's binary directory to `PATH` if your shell cannot find `vngcloud`.

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

	log.Printf("servers on page 1: %d", len(servers.Items))
}
```

The example counts only the returned page; `ListServers` does not fetch
later pages automatically. See [Compute](Compute.md#reading) for more server
reads. To build a `Config` from Go values only, use `vngcloud.NewConfig`; see
[Configuration](Configuration.md#loadconfig).

Neither `LoadConfig` nor `NewConfig` logs in. Call `cfg.Authenticate(ctx)` to
log in now, so bad credentials fail before the first service call runs; skip
it and the SDK logs in lazily on that first call instead. Each Config
targets one region. Build one Config per region you need, and pass it to a
service package's `New`. Every service client built from the same Config
shares one login and one project lookup.

## Command line

Commands follow `vngcloud <service> <operation> [flags]`. With an IAM-user
profile, configure credentials and make a first read:

```bash
vngcloud configure
vngcloud billing list-budgets --read-only
```

Configuration prompts for credentials without echoing secrets. Select a
configured profile with `--profile <name>`. Service-account login does not
establish access to every product; see the limits in
[Authentication](Authentication.md#service-account).

- Output is JSON by default; `--output table` and `--output text` also work,
  and `--query` takes a JMESPath expression.
- Errors go to stderr as one JSON line with a stable exit code, so scripts
  and AI agents can act on them.
- A profile with `read_only = true`, `VNGCLOUD_READ_ONLY=1`, or `--read-only`
  refuses every CLI write command before a request. This guard does not
  restrict the credentials' IAM permissions or direct SDK calls. See
  [Security](Security.md) for write guards and credential handling.

[CLI](CLI.md) lists every command and flag.

## Repository checks

For development setup and required checks, see
[CONTRIBUTING.md][contributing].

## Next steps

- Pick an auth method in [Authentication](Authentication.md).
- Set the project or endpoints in [Configuration](Configuration.md).
- Find a service guide and method examples in [Services](Services.md).
- Read about budgets and quotes in
  [Billing and Pricing](Billing-and-Pricing.md).

[contributing]: https://github.com/dannyota/vngcloud/blob/master/CONTRIBUTING.md
