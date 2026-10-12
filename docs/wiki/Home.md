# vngcloud

Documentation for `danny.vn/vngcloud`, an unofficial Go SDK and command-line
tool for GreenNode. This is a personal project under the Apache 2.0 license,
not affiliated with GreenNode. GreenNode was called VNG Cloud until its
rename; the module keeps the `vngcloud` name.

## SDK

- [Getting Started](Getting-Started.md): install, create a client, list
  resources.
- [Authentication](Authentication.md): IAM User and service-account login,
  TOTP, static tokens, and custom credential providers.
- [Configuration](Configuration.md): regions, projects, and endpoints.
- [Errors](Errors.md): `APIError`, `LoginError`, and debug logging.
- [Services](Services.md): every service client and its methods.
- [Backup](Backup.md): Backup Center backends and policies in `hcm-3`.
- [Billing and Pricing](Billing-and-Pricing.md): budgets, cost, balances, and
  price quotes.
- [CDN](CDN.md): published GreenNode CDN IP ranges, vCDN certificate and API
  key reads, Web Accelerator reads and writes, and traffic analytics.
- [Compute](Compute.md): vServer instances, images, and SSH keys, including
  importing and creating them.
- [Container Registry](Container-Registry.md): repositories and users,
  including writes and secret handling.
- [DNS](DNS.md): hosted zones and records, including writes and waits.
- [IAM](IAM.md): service account writes, and their self-change and
  privileged-change guards.
- [Load Balancer](LoadBalancer.md): load balancers, pools, listeners, and L7
  policies, including the price guard, writes, and waits.
- [Monitor](Monitor.md): vMonitor synthetic checks, and pausing and resuming
  them.
- [Monitor Alerts](Monitor-Alerts.md): notification channels, log projects,
  and alarms.
- [Monitor Log Alarms](Monitor-Log-Alarms.md): creating, updating, and
  deleting log alarms.
- [Storage](Storage.md): vStorage regions, projects, buckets, S3 keys, and
  bucket settings, including bucket and key writes.
- [Storage Objects](Storage-Objects.md): native SDK and CLI binary object
  uploads and downloads, one-page listing, and HEAD metadata.
- [Storage: Projects](Storage-Projects.md): package price discovery, purchase,
  deletion, and auto-renew state and updates.
- [Storage: Bucket Policy](Storage-Bucket-Policy.md): the per-bucket key
  policy, its template, and the rules.
- [Storage: Bucket Settings](Storage-Bucket-Settings.md): bucket versioning
  and encryption, plus CORS rules.
- [Network](Network.md): security groups and their rules, VPCs, subnets,
  and Private DNS, including writes and waits.
- [Public NAT](Network-NAT.md): inventory, zone and package catalogs, quotes,
  and guarded create and delete operations; paid writes require IAM-user
  login in `han-1`.
- [Site-to-site VPN](Network-VPN.md): VPN inventory with sites and tunnels.
- [Network Route Tables](Network-RouteTables.md): route tables and routes.
- [Network ACLs](Network-ACLs.md): ACLs, rules, and subnet associations.
- [Network DHCP Options](Network-DHCPOptions.md): DHCP options sets and the
  set a VPC uses.
- [Network Virtual IPs](Network-VirtualIPs.md): private virtual IP writes.
- [Tagging](Tagging.md): reading and writing resource tags.
- [VKS](VKS.md): Kubernetes cluster inventory, versions, and quota.
- [Security](Security.md): what is safe by default, and cannot be turned off.
- [Limitations](Limitations.md): GreenNode server behaviors the SDK works
  around but cannot fix.

## CLI

- [CLI](CLI.md): commands, global flags, output, exit codes, and error
  classes.
- [IDs for Create Server](IDs-for-Create-Server.md): the command or call that
  finds each ID a server quote or create takes.
- [CLI: Billing](CLI-Billing.md), [CLI: Pricing](CLI-Pricing.md),
  [CLI: Compute](CLI-Compute.md), [CLI: IAM](CLI-IAM.md),
  [CLI: Network](CLI-Network.md), [CLI: DNS](CLI-DNS.md),
  [CLI: CDN](CLI-CDN.md), [CLI: Monitor](CLI-Monitor.md),
  [CLI: Storage](CLI-Storage.md), [CLI: VKS](CLI-VKS.md), and
  [CLI: Backup](CLI-Backup.md): every operation, its flags, and an example.

## Source

These pages are published from
[`docs/wiki/`](https://github.com/dannyota/vngcloud/tree/master/docs/wiki) in
the repository. Edit them there; changes made in the wiki web UI are overwritten
on the next sync.
