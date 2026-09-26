# vngcloud

[![CI](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml)
[![Semgrep](https://github.com/dannyota/vngcloud/actions/workflows/semgrep.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/semgrep.yml)
[![Go Reference](https://pkg.go.dev/badge/danny.vn/vngcloud.svg)](https://pkg.go.dev/danny.vn/vngcloud)
[![Release](https://img.shields.io/github/v/release/dannyota/vngcloud)](https://github.com/dannyota/vngcloud/releases)
[![Go version](https://img.shields.io/github/go-mod/go-version/dannyota/vngcloud)](go.mod)
[![License](https://img.shields.io/github/license/dannyota/vngcloud)](LICENSE)

A Go SDK and command-line tool for [GreenNode](https://greennode.ai), built to
become what the AWS SDK and AWS CLI are for AWS: one way for people and AI
agents to inspect, price, and change cloud resources.

GreenNode was called VNG Cloud until its rename. This project started before
the rename and keeps the `vngcloud` name.

## What it does

- **Cap spend:** create, pause, and delete budgets and alert thresholds; read
  cost and balances.
- **Price before you buy:** get a quote for a resource; a quote never orders.
- **Read your infrastructure:** servers, volumes, networks, load balancers,
  DNS, container registries, quotas, and the published CDN IP ranges.
- **Use it from a terminal:** `vngcloud <service> <operation>` for every
  service above, with JSON output, `--query`, stable exit codes, and
  read-only profiles for AI agents.

Expect breaking changes until `v1.0.0`.

## Quick start

```bash
go get danny.vn/vngcloud@latest                        # SDK
go install danny.vn/vngcloud/cmd/vngcloud@latest       # command

vngcloud configure
vngcloud billing list-budgets
```

```go
cfg, err := vngcloud.LoadConfig(ctx)
budgets, err := billing.New(cfg).ListBudgets(ctx, nil)
```

## Documentation

- [Getting Started](https://github.com/dannyota/vngcloud/wiki/Getting-Started):
  a full SDK example and the command line.
- [Authentication](https://github.com/dannyota/vngcloud/wiki/Authentication)
  and [Configuration](https://github.com/dannyota/vngcloud/wiki/Configuration):
  IAM User login, profiles, environment variables, and the token cache.
- [CLI reference](https://github.com/dannyota/vngcloud/wiki/CLI): every
  command and flag.
- [Security](https://github.com/dannyota/vngcloud/wiki/Security): what is safe
  by default.
- [Release notes](RELEASE_NOTES.md).

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
