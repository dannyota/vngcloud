# vngcloud

[![CI](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml)
[![Semgrep](https://github.com/dannyota/vngcloud/actions/workflows/semgrep.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/semgrep.yml)
[![Go Reference](https://pkg.go.dev/badge/danny.vn/vngcloud.svg)](https://pkg.go.dev/danny.vn/vngcloud)
[![Release](https://img.shields.io/github/v/release/dannyota/vngcloud)](https://github.com/dannyota/vngcloud/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/dannyota/vngcloud)](go.mod)
[![License](https://img.shields.io/github/license/dannyota/vngcloud)](LICENSE)

A Go SDK for [GreenNode](https://greennode.ai), built to become what the AWS
SDK and AWS CLI are for AWS: one way for people and AI agents to inspect,
price, and change cloud resources from code or a terminal.

GreenNode was called VNG Cloud until its rename. This project started before
the rename and keeps the `vngcloud` name for its module and, later, its
command. It talks to the GreenNode hosts (`*.console.greennode.ai`,
`signin.greennode.ai`) directly.

## What it does

- **Cap spend before you create anything.** Create, pause, and delete budgets
  and their alert thresholds. Read current-period cost, the cost explorer,
  and account balances.
- **Price a resource before you buy it.** Get a quote from the same pricing
  API the GreenNode console uses. A quote places no order.
- **Read your infrastructure.** Servers, volumes, networks, security groups,
  load balancers, global load balancers, DNS zones, container registries,
  and account quotas.

| Area | Package | Reads | Writes |
|-|-|-|-|
| Budgets, cost, balances | `billing` | Yes | Budgets and thresholds |
| Price quotes | `pricing` | Yes | None; a quote never orders |
| Compute | `compute` | Yes | Planned |
| Volumes | `volume` | Yes | Planned |
| Networking | `network` | Yes | Planned |
| Load balancers | `loadbalancer`, `globalloadbalancer` | Yes | Planned |
| DNS | `dns` | Yes | Planned |
| Container registry | `containerregistry` | Yes | Planned |
| Portal, quotas | `portal` | Yes | Planned |
| Project listing | `project` | Yes | N/A |
| Command-line tool | `vngcloud` | Billing, pricing, compute, network, DNS | Budgets and thresholds |

Expect breaking changes until `v1.0.0`.

## Install

```bash
go get danny.vn/vngcloud@latest
```

It needs Go 1.27 or later. The SDK packages use only the standard library.

To install the command-line tool:

```bash
go install danny.vn/vngcloud/cmd/vngcloud@latest
```

## Example

Sign in as an IAM User, set a monthly budget, price a snapshot, and list
servers. `LoadConfig` reads the region and credentials from
`VNGCLOUD_REGION`, `VNGCLOUD_ROOT_EMAIL`, `VNGCLOUD_USERNAME`,
`VNGCLOUD_PASSWORD`, and `VNGCLOUD_TOTP_SECRET`, or from an
`~/.vngcloud/credentials` profile; see
[Configuration](https://github.com/dannyota/vngcloud/wiki/Configuration#loadconfig):

```go
package main

import (
	"context"
	"fmt"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/pricing"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.LoadConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}

	// A monthly budget of 2,000,000 VND on actual spend.
	budget, err := billing.New(cfg).CreateBudget(ctx, &billing.CreateBudgetInput{
		Name:        "monthly",
		PeriodType:  billing.PeriodMonthly,
		Type:        billing.TypeActual,
		LimitAmount: 2_000_000,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("budget:", budget.Budget.UUID)

	quote, err := pricing.New(cfg).GetQuote(ctx, &pricing.GetQuoteInput{
		ResourceType: pricing.ResourceSnapshot,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("snapshot: %.0f VND\n", quote.OptimumPrice)

	servers, err := compute.New(cfg).ListServers(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("servers:", len(servers.Items))
}
```

Every service client built from the same `Config` shares a single login, so
the program signs in once no matter how many packages it calls.

## Command line

`vngcloud` works like the AWS CLI: `vngcloud <service> <operation> [flags]`.

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
- Deletes need `--yes`, and nothing ever prompts without a terminal.
- A profile with `read_only = true`, `VNGCLOUD_READ_ONLY=1`, or `--read-only`
  refuses every write command. Give an AI agent a read-only profile.

The [CLI reference](https://github.com/dannyota/vngcloud/wiki/CLI) lists
every command and flag.

## Authentication

The SDK signs in as a GreenNode IAM User with a password and, when the user
has 2FA, a TOTP secret that it turns into codes itself. It can also use an
access token you already have (`vngcloud.WithStaticToken`). Root-account
login is not supported, because the root sign-in page requires a reCAPTCHA.

## Safe by default

- TLS verification is always on, and the SDK refuses redirects to another
  host.
- A create is never retried after a failure that may have reached the
  server, so a network error cannot create a resource twice.
- Write APIs check every ID that goes into a URL path before any request.
- Errors and logs never include passwords, TOTP secrets, tokens, or cookies.

## Documentation

The [wiki](https://github.com/dannyota/vngcloud/wiki) covers authentication,
configuration, and every service and method. Changes per release are in
[RELEASE_NOTES.md](RELEASE_NOTES.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as
described in [SECURITY.md](SECURITY.md).

## About

I built it because I wanted what the AWS CLI gives me on AWS: one tool that I
and AI coding agents can use to inspect and change cloud resources from a
terminal or a script.

This is a personal project. I don't work for GreenNode (formerly VNG Cloud)
or VNG, and the project is not affiliated with or endorsed by them. It is
free to use, change, and redistribute under the
[Apache 2.0 license](LICENSE).
