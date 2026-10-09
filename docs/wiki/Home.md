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
- [Compute](Compute.md): vServer instances, images, and SSH keys, including
  importing and creating them.
- [Container Registry](Container-Registry.md): repositories and users,
  including writes and secret handling.
- [IAM](IAM.md): service account writes, and their self-change and
  privileged-change guards.
- [Monitor](Monitor.md): vMonitor synthetic checks, and pausing and resuming
  them.
- [Monitor Alerts](Monitor-Alerts.md): notification channels, log projects,
  and alarms.
- [Monitor Log Alarms](Monitor-Log-Alarms.md): creating, updating, and
  deleting log alarms.
- [Storage](Storage.md): vStorage regions, projects, and buckets (reads).
- [Network](Network.md): security groups and their rules, VPCs, subnets,
  and Private DNS, including writes and waits.
- [Network Route Tables](Network-RouteTables.md): route tables and routes.
- [Network ACLs](Network-ACLs.md): ACLs, rules, and subnet associations.
- [Network DHCP Options](Network-DHCPOptions.md): DHCP options sets and the
  set a VPC uses.
- [Network Virtual IPs](Network-VirtualIPs.md): private virtual IP writes.
- [Tagging](Tagging.md): reading and writing resource tags.
- [Security](Security.md): what is safe by default, and cannot be turned off.
- [Limitations](Limitations.md): GreenNode server behaviors the SDK works
  around but cannot fix.

## CLI

- [CLI](CLI.md): commands, global flags, output, exit codes, and error
  classes.
- [CLI: Billing](CLI-Billing.md), [CLI: Pricing](CLI-Pricing.md),
  [CLI: Compute](CLI-Compute.md), [CLI: IAM](CLI-IAM.md),
  [CLI: Network](CLI-Network.md), [CLI: DNS](CLI-DNS.md),
  [CLI: CDN](CLI-CDN.md), [CLI: Monitor](CLI-Monitor.md), and
  [CLI: Storage](CLI-Storage.md): every operation, its flags, and an
  example.

## Source

These pages are published from
[`docs/wiki/`](https://github.com/dannyota/vngcloud/tree/master/docs/wiki) in
the repository. Edit them there; changes made in the wiki web UI are overwritten
on the next sync.
