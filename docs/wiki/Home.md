# vngcloud

Documentation for `danny.vn/vngcloud`, an unofficial Go SDK and command-line
tool for GreenNode. This is a personal project under the Apache 2.0 license,
not affiliated with GreenNode. GreenNode was called VNG Cloud until its
rename; the module keeps the `vngcloud` name.

## SDK

- [Getting Started](Getting-Started.md): install, create a client, list
  resources.
- [Authentication](Authentication.md): IAM User login, TOTP, and static tokens.
- [Configuration](Configuration.md): regions, projects, and endpoints.
- [Errors](Errors.md): `APIError`, `LoginError`, and debug logging.
- [Services](Services.md): every service client and its methods.
- [Billing and Pricing](Billing-and-Pricing.md): budgets, cost, balances, and
  price quotes.
- [CDN](CDN.md): the published GreenNode CDN IP ranges.

## CLI

- [CLI](CLI.md): commands, global flags, output, exit codes, and error
  classes.
- [CLI: Billing](CLI-Billing.md), [CLI: Pricing](CLI-Pricing.md),
  [CLI: Compute](CLI-Compute.md), [CLI: Network](CLI-Network.md), and
  [CLI: DNS](CLI-DNS.md): every operation, its flags, and an example.

## Source

These pages are published from
[`docs/wiki/`](https://github.com/dannyota/vngcloud/tree/master/docs/wiki) in
the repository. Edit them there; changes made in the wiki web UI are overwritten
on the next sync.
