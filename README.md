# vngcloud

[![CI](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml/badge.svg)](https://github.com/dannyota/vngcloud/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/danny.vn/vngcloud.svg)](https://pkg.go.dev/danny.vn/vngcloud)

An unofficial Go SDK and command-line tool for VNG Cloud (now GreenNode), in
the spirit of the AWS SDK and AWS CLI.

I built it because I wanted what the AWS CLI gives me on AWS: one tool that I
and AI coding agents can use to inspect and change cloud resources from a
terminal or a script.

This is a personal project. I don't work for VNG Cloud or GreenNode, and the
project is not affiliated with or endorsed by them. It is free to use, change,
and redistribute under the [Apache 2.0 license](LICENSE).

## Status

| Part | State |
|-|-|
| Go SDK, read APIs | Compute, Volume, Network, Load Balancer, Global Load Balancer, DNS, Container Registry, Portal |
| Go SDK, write APIs | Planned |
| `vngcloud` CLI | Planned, shaped like the AWS CLI: `vngcloud <service> <operation> [flags]` |

Expect breaking changes until `v1.0.0`.

## Quick start

```bash
go get danny.vn/vngcloud
```

```go
client, err := vngcloud.NewClient(ctx, vngcloud.Config{
	Region:  "hcm-3",
	IAMUser: &vngcloud.IAMUserAuth{RootEmail: "<root-email>", Username: "<iam-username>", Password: "<password>"},
})
servers, err := client.Compute.ListServers(ctx, nil)
```

Read the [wiki](https://github.com/dannyota/vngcloud/wiki) for authentication,
configuration, and every service method.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Report security problems privately as
described in [SECURITY.md](SECURITY.md).
