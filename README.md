# vngcloud

[![CI][ci-badge]][ci]
[![CodeQL][codeql-badge]][codeql]
[![Go Reference][go-reference-badge]][go-reference]
[![Go version][go-version-badge]](go.mod)
[![License][license-badge]](LICENSE)

A Go SDK and AWS-style command-line tool for
[GreenNode](https://greennode.ai), formerly VNG Cloud. Inspect resources, get
price quotes, and create, change, and delete resources from Go, a terminal,
or scripts.

This is a personal, unofficial project, not affiliated with or endorsed by
GreenNode or VNG. Since `v1.0.0` it follows semantic versioning: public SDK
names, types, and fields, CLI flags, query keys, and exit codes change only
in a new major version.

[Tags](https://github.com/dannyota/vngcloud/tags) |
[Release notes](RELEASE_NOTES.md)

## CLI quick start

Use the Go version required by [go.mod](go.mod) or later. Install the CLI,
configure an IAM-user profile, and read your budgets without creating or
changing cloud resources:

```bash
go install danny.vn/vngcloud/cmd/vngcloud@latest   # or @v1.0.0 to pin
vngcloud configure
vngcloud billing list-budgets --read-only
```

Add Go's binary directory to `PATH` if your shell cannot find `vngcloud`.
Configuration asks for a region, the root account email, IAM username,
password, and optional two-factor authentication secret. Passwords and
secrets are not echoed. The IAM user needs permission for the read.
See [credential setup][config] and [authentication methods][auth] for
service accounts, environment variables, static tokens, and custom
credential providers.

Commands follow `vngcloud <service> <operation> [flags]`:

```bash
vngcloud compute list-servers --read-only --output table
vngcloud compute list-servers --read-only --query 'Items[].Name' --output text
vngcloud compute list-servers --help
```

Create a named profile with `vngcloud configure --profile production`, then
select it with `--profile production`. Use `--region` to select another
region and `--project-id` when more than one project matches the region.
The [CLI reference][cli] covers commands, flags, output, queries, and exit
codes.

## Go SDK quick start

Add the module to your Go project:

```bash
go get danny.vn/vngcloud@latest
```

The SDK uses the same profiles and environment variables as the CLI. After
configuring credentials above, this program reads your budgets:

```go
package main

import (
	"context"
	"fmt"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.LoadConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}

	budgets, err := billing.New(cfg).ListBudgets(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("Budgets: %d\n", len(budgets.Items))
}
```

Each service has its own package and `New(cfg)` client. Clients built from
the same configuration share authentication and project discovery. The SDK
packages use only the Go standard library. See
[Getting Started][getting-started] for client setup and the
[Go reference][go-reference] for package details.

## What you can do

- Inspect resources and quotas across services. Use the canonical
  [service coverage][services] for supported operations and prerequisites.
- Create and delete resources, including credit-paid servers, volumes, NAT
  gateways, private VKS clusters, Global Load Balancers with TCP pools and
  listeners, vDB MySQL instances, and private endpoints. Creates with a price
  quote refuse a quote above `--max-price`; creates without a quote API
  (Global Load Balancer, VKS, vDB) need explicit `--yes` consent.
- Read budgets, costs, and balances, or get price quotes before an order.
  See [Billing and Pricing][billing].
- Manage vStorage buckets and keys or transfer objects through the separate
  native S3 client. See [Storage][storage] and
  [Storage Objects][storage-objects].
- Automate supported management operations from scripts or Go. Check each
  service guide and the [known limitations][limitations] before writing.

## Safe automation

- JSON is the default output. Use `--query` for JMESPath filtering and
  `--output table` or `--output text` for other formats.
- Errors go to stderr as JSON with [stable exit codes][exit-codes].
  Commands never prompt without a terminal.
- `--read-only` refuses every CLI write before a request. A profile's
  `read_only = true` or `VNGCLOUD_READ_ONLY=1` also enables the guard.
- Destructive commands require `--yes`. Supported paid creates and resizes
  compare a quote against `--max-price` before ordering. That check is not
  a provider price lock or a final billing guarantee. Quotes place no orders.

To prepare a separate profile for scripts or an AI agent:

```bash
vngcloud configure --profile agent
vngcloud configure set read_only true --profile agent
vngcloud compute list-servers --profile agent --output json
```

The read-only guard applies to the CLI. Use IAM policies to restrict the
credentials themselves. See [Security][security] for credential storage,
token caching, and write guards.

## Documentation and support

- [Getting Started][getting-started]: installation and client setup.
- [Configuration][config] and [Authentication][auth]: profiles, identities,
  regions, projects, and credentials.
- [CLI reference][cli]: commands, flags, queries, and exit codes.
- [Service coverage][services] and [known limitations][limitations].
- [Storage Workload Access][workload]: application identities, bucket grants,
  private key handoff, rotation, and network prerequisites for vServer and VKS.
- [Contributing](CONTRIBUTING.md): development setup and required checks.

If you want, share what you use vngcloud for on
[GitHub Issues](https://github.com/dannyota/vngcloud/issues), without
configuration or account details. Report security problems privately through
[SECURITY.md](SECURITY.md).

Licensed under [Apache 2.0](LICENSE).

[ci]: https://github.com/dannyota/vngcloud/actions/workflows/ci.yml
[ci-badge]:
  https://github.com/dannyota/vngcloud/actions/workflows/ci.yml/badge.svg
[codeql]: https://github.com/dannyota/vngcloud/actions/workflows/codeql.yml
[codeql-badge]:
  https://github.com/dannyota/vngcloud/actions/workflows/codeql.yml/badge.svg
[go-reference]: https://pkg.go.dev/danny.vn/vngcloud
[go-reference-badge]: https://pkg.go.dev/badge/danny.vn/vngcloud.svg
[go-version-badge]:
  https://img.shields.io/github/go-mod/go-version/dannyota/vngcloud
[license-badge]: https://img.shields.io/github/license/dannyota/vngcloud
[getting-started]: docs/wiki/Getting-Started.md
[cli]: docs/wiki/CLI.md
[exit-codes]: docs/wiki/CLI.md#exit-codes
[services]: docs/wiki/Services.md#coverage
[config]: docs/wiki/Configuration.md
[auth]: docs/wiki/Authentication.md
[security]: docs/wiki/Security.md
[limitations]: docs/wiki/Limitations.md
[billing]: docs/wiki/Billing-and-Pricing.md
[storage]: docs/wiki/Storage.md
[storage-objects]: docs/wiki/Storage-Objects.md
[workload]: docs/wiki/Storage-Workload-Access.md
