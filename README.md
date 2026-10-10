# vngcloud

[![CI](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml)
[![Semgrep](https://github.com/dannyota/vngcloud/actions/workflows/semgrep.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/semgrep.yml)
[![Go Reference](https://pkg.go.dev/badge/danny.vn/vngcloud.svg)](https://pkg.go.dev/danny.vn/vngcloud)
[![Release](https://img.shields.io/github/v/release/dannyota/vngcloud)](https://github.com/dannyota/vngcloud/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/dannyota/vngcloud)](go.mod)
[![License](https://img.shields.io/github/license/dannyota/vngcloud)](LICENSE)

A Go SDK and AWS-style command-line tool for
[GreenNode](https://greennode.ai), formerly VNG Cloud. Inspect resources, get
price quotes, and manage infrastructure from Go, a terminal, or scripts.

This is a personal, unofficial project, not affiliated with or endorsed by
GreenNode or VNG. Expect breaking changes until `v1.0.0`.

## CLI quick start

Install with the Go version required by [go.mod](go.mod) or later:

```bash
go install danny.vn/vngcloud/cmd/vngcloud@latest
```

Add Go's binary directory to your `PATH` if your shell cannot find
`vngcloud`. Then configure your region and IAM User credentials:

```bash
vngcloud configure
vngcloud billing list-budgets
```

Configuration prompts for the root account email, IAM username, password,
and optional two-factor authentication secret. Passwords and secrets are
not echoed. The IAM User needs permission for the operations you call.

Commands follow `vngcloud <service> <operation> [flags]`:

```bash
# Show servers as a table.
vngcloud compute list-servers --output table

# Print only server names.
vngcloud compute list-servers --query 'Items[].Name' --output text

# Use a named profile in another region.
vngcloud compute list-servers --profile production --region han-1

# Discover operations and their flags.
vngcloud --help
vngcloud compute list-servers --help
```

Create a named profile with `vngcloud configure --profile production`.
Set `--project-id` when more than one project matches the region. See the
[CLI reference][cli] for all commands, flags, and exit codes.

## Go SDK quick start

Add the module to your Go project:

```bash
go get danny.vn/vngcloud@latest
```

The SDK uses the same profiles and environment variables as the CLI. After
`vngcloud configure`, this program lists your budgets:

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
packages use only the Go standard library.

For credentials supplied through environment variables, static tokens, or a
custom credentials provider, see [Authentication][auth] and
[Configuration][config]. The [basic example](examples/basic/README.md)
shows multiple service clients in one program.

## Service coverage

The SDK and CLI cover the services below. Coverage varies by service;
the linked guides explain supported operations and lead to detailed references.

| Service | Supported resources and operations |
| --- | --- |
| [Billing and pricing][billing] | Budgets, alerts, costs, balances, quotes |
| [Compute][compute] | Servers, images, flavors, SSH keys, placement groups |
| [Volumes][volume] | Create, attach, resize; read snapshots and disk types |
| [Network][network] | VPCs, subnets, IPs, security groups, routes, ACLs |
| [Load balancers][lb] | Create, resize, pools, listeners, policies, TLS certs |
| [DNS][dns] | Create, update, and delete private zones and records |
| [Container registry][registry] | Repositories and repository users |
| [Storage][storage] | Projects, buckets, S3 keys, policies, bucket settings |
| [CDN][cdn] | Web Accelerator updates, enable/disable, analytics, IP ranges |
| [Monitoring][monitor] | Uptime checks, channels, log projects, alarms |
| [IAM][iam] | Users, service accounts, policies, groups, access guards |
| [Tagging][tagging] | Read, add, and remove resource tags |
| [Kubernetes][vks] | Read clusters, versions, and quota |
| [Backup][backup] | Read backends and policies |

Project discovery, account quotas, and global load balancer reads are also
available. See the [service reference][services] for the full SDK surface.
Use an S3 client to upload and download vStorage objects.

## Scripts and AI agents

- **Structured output:** JSON by default, with table and text formats.
  Filter results with JMESPath expressions through `--query`.
- **Predictable failures:** errors go to stderr as JSON with stable exit
  codes. Commands never prompt without a terminal.
- **Read-only access:** `--read-only` refuses every CLI write before any
  request. Set `read_only = true` in a profile to keep the guard enabled.
- **Explicit writes:** destructive commands require `--yes`. Paid creates
  and resizes use `--max-price` to cap the quoted price. Quote commands
  place no orders.

```bash
vngcloud configure --profile agent
vngcloud configure set read_only true --profile agent
vngcloud compute list-servers --profile agent --output json
```

The read-only guard applies to the CLI. Use IAM policies to restrict the
credentials themselves.
See [Security][security] for credential storage, token caching, and write
guards.

## Documentation

- [Getting started][getting-started]: installation and client setup.
- [CLI reference][cli]: commands, flags, queries, and exit codes.
- [Service reference][services]: SDK methods and service-specific behavior.
- [Configuration][config]: profiles, environment variables, regions, projects.
- [Authentication][auth]: IAM User login, two-factor codes, and tokens.
- [Known limitations][limitations]: API quirks and recovery guidance.
- [Release notes](RELEASE_NOTES.md): changes by version.
- [Go reference](https://pkg.go.dev/danny.vn/vngcloud): package documentation.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for setup and checks. Report security
problems privately through [SECURITY.md](SECURITY.md). Live tests and their
helpers live in [`livetest/`](livetest/); see the compile-only commands in
[CONTRIBUTING.md](CONTRIBUTING.md#live-api-data).

Licensed under [Apache 2.0](LICENSE).

[getting-started]: https://github.com/dannyota/vngcloud/wiki/Getting-Started
[cli]: https://github.com/dannyota/vngcloud/wiki/CLI
[services]: https://github.com/dannyota/vngcloud/wiki/Services
[config]: https://github.com/dannyota/vngcloud/wiki/Configuration
[auth]: https://github.com/dannyota/vngcloud/wiki/Authentication
[security]: https://github.com/dannyota/vngcloud/wiki/Security
[limitations]: https://github.com/dannyota/vngcloud/wiki/Limitations
[billing]: https://github.com/dannyota/vngcloud/wiki/Billing-and-Pricing
[compute]: https://github.com/dannyota/vngcloud/wiki/Compute
[volume]: https://github.com/dannyota/vngcloud/wiki/Volume
[network]: https://github.com/dannyota/vngcloud/wiki/Network
[lb]: https://github.com/dannyota/vngcloud/wiki/LoadBalancer
[dns]: https://github.com/dannyota/vngcloud/wiki/DNS
[registry]: https://github.com/dannyota/vngcloud/wiki/Container-Registry
[storage]: https://github.com/dannyota/vngcloud/wiki/Storage
[cdn]: https://github.com/dannyota/vngcloud/wiki/CDN
[monitor]: https://github.com/dannyota/vngcloud/wiki/Monitor
[iam]: https://github.com/dannyota/vngcloud/wiki/IAM
[tagging]: https://github.com/dannyota/vngcloud/wiki/Tagging
[vks]: https://github.com/dannyota/vngcloud/wiki/VKS
[backup]: https://github.com/dannyota/vngcloud/wiki/Backup
