# Release Notes

## v0.18.0 - Container Registry Command

### Highlights

- New `vngcloud containerregistry list-repositories`. Rows print the API's
  own keys; the test account has no repository, so the output is marked
  unverified. `list-users` waits until its model is typed from a live
  capture.
- Map-backed output now also hides values under keys that look like an
  access key or a Docker config, and under keys named exactly `auth` or
  `auths`.
- Every SDK service now has CLI read commands.

### Behavior changes

None.

## v0.17.0 - Global Load Balancer Commands

### Highlights

- New `vngcloud globalloadbalancer` commands for global load balancers,
  pools, listeners, pool members, usage histories, packages, and regions.
  Reads only, with global scope: no project ID. The test account has no
  global load balancer, so its reads are marked unverified in the wiki.
- The live checks now fail when a read that always has rows, such as
  package or region lists, returns none.

### Behavior changes

None.

## v0.16.0 - Load Balancer Commands

### Highlights

- New `vngcloud loadbalancer` commands for load balancers, listeners,
  pools, health monitors, pool members, policies, tags, packages, and
  certificates. Reads only. The test account has no load balancer or
  certificate, so their reads are marked unverified in the wiki; the live
  tests check them the day one exists.
- Certificates print metadata only; the model has no key or PEM field.

### Behavior changes

None.

## v0.15.0 - vMonitor Webhook Channels

### Highlights

- `monitor.CreateChannel`, `UpdateChannel`, and `DeleteChannel` manage
  webhook notification channels. Other channel types need a one-time code
  and are not supported yet.
- `UpdateChannel` reads the channel and sends a full body, because the API
  replaces the channel; unset fields keep their values, and a non-nil empty
  `Headers` clears the headers. Deleting a channel also removes it from
  every check that named it.
- Server error messages never repeat a channel's address or header values:
  the SDK redacts them, including their JSON-escaped forms.
- New `vngcloud monitor create-channel`, `update-channel`, and
  `delete-channel` commands. A webhook address and any headers go only
  through `--cli-input-json file://...`, so a secret never lands in shell
  history; a literal or inline value is refused. Output is redacted as for
  the channel reads. Delete needs `--yes`, and a read-only profile refuses
  all three.

### Behavior changes

A not-found error that wraps an `*APIError` now prints the CLI code
`NotFound` (exit 4) with the server's status and message.

## v0.14.0 - Volume Commands

### Highlights

- New `vngcloud volume` commands for block volumes, volume types, and
  snapshots. Reads only. `get-volume`, `get-underlying-volume`, and
  `list-snapshots` are marked unverified: the test account has no volume,
  so their output shape comes from GreenNode's official SDK.
- `get-default-volume-type` is left out: the API returns 404 in every
  region tested.

### Breaking changes

- `volume.VolumeTypeZone` drops `UUID`, `PoolName`, `VolumeTypeZones`,
  `Extra`, `Success`, `ErrorCode`, and `ErrorMsg`. A live
  `ListVolumeTypeZones` item never sets them; `ID`, `Name`, `Description`,
  and `Zone` are unchanged.

## v0.13.0 - Project and Portal Commands

### Highlights

- New `vngcloud project list-projects` and `vngcloud portal` commands:
  `get-user-info`, `list-zones`, `list-quota-used`, `get-quota`, and
  `get-tag-quota`. Reads only, so read-only profiles allow them.
- Output built from raw maps hides the value of any key that looks secret
  (password, passphrase, secret, token, credential, private key, API key,
  or authorization, in any spelling) as `<redacted>`, in every format and
  before `--query`. Typed fields are not touched.
- Generated examples for single-resource reads now show `--query <Field>`.

### Behavior changes

None for SDK callers.

## v0.12.0 - vDNS Records

### Highlights

- `dns.CreateRecord`, `UpdateRecord`, and `DeleteRecord` manage records in
  vDNS private hosted zones. Every record create or update locks its zone
  for about 11 seconds, so each write waits for the zone to be ready
  first and, unless `NoWait` is set, for the record to settle after.
- `UpdateRecord` sends only the fields that are set; the API applies a
  partial record body. An empty update is refused before any request.
- The apex is an empty `SubDomain`. The server stores names in lower case,
  `Type` must be upper case, and an MX host must be lower case with no
  trailing dot.
- A create that fails with a 5xx or a network error now says to list
  before creating again, for both records and hosted zones, since the
  resource may exist.
- New `vngcloud dns create-record`, `update-record`, and `delete-record`
  commands. Values come through `--cli-input-json`; delete needs `--yes`,
  and a read-only profile refuses all three.

### Behavior changes

None: this release adds methods and commands, and an error message hint.

## v0.11.0 - vMonitor Notification Channels

### Highlights

- `monitor.ListChannelTypes`, `ListChannels`, and `GetChannel` read
  vMonitor notification channels. `GetChannel` finds a channel by listing,
  because the API has no get-by-ID call.
- `Check` gains `Notifications`: the channel IDs alerted when a check goes
  into alarm, comes back up, or turns undetermined.
- New `vngcloud monitor list-channel-types`, `list-channels`, and
  `get-channel` commands. Channel addresses and header values can hold
  secrets, so the CLI always redacts them: it shows only Email, SMS, and
  Telegram addresses, keeps only scheme and host of any other `http(s)`
  address, and prints every header value as `<redacted>`. No flag reveals
  them; the SDK returns them in full.
- A not-found result that is not an `*APIError`, such as `GetChannel`
  finding no match, now prints the CLI code `NotFound` and exits 4.

### Behavior changes

`portal.GetQuota` and the volume type lookups report a missing item as
`NotFound` instead of `RequestFailed`; both already exited 4.

## v0.10.0 - vDNS Private Hosted Zones

### Highlights

- `dns.CreateHostedZone`, `UpdateHostedZone`, and `DeleteHostedZone`
  manage vDNS private hosted zones. vDNS has no public zones: a zone
  resolves only inside the VPCs linked to it, and each VPC needs Private
  DNS turned on in the console first. See
  [DNS](https://github.com/dannyota/vngcloud/wiki/DNS).
- Writes wait for the zone to be ready before sending, and for the result
  after, polling every 2 seconds for up to 60 seconds. `NoWait` skips the
  wait after. `ErrZoneBusy`, `ErrFailed`, and `ErrNotSettled` report the
  outcomes; the last two return the Output too, so the new ID is never
  lost.
- `UpdateHostedZone` reads the zone and sends a full body, because the API
  replaces the zone. Only an explicit empty `VPCIDs` list detaches every
  VPC.
- New `vngcloud dns create-hosted-zone`, `update-hosted-zone`, and
  `delete-hosted-zone` commands, with `--no-wait`. Delete needs `--yes`,
  and a read-only profile refuses all three. New CLI error codes
  `ZoneBusy`, `WriteFailed`, and `NotSettled`, all exit 1.
- `ListHostedZonesInput` gains `Page` and `Size`.

### Fixes

- `network.GetVPC` and `network.GetSubnet` returned empty fields: their
  responses are not wrapped in `data`. They now decode.

### Behavior changes

The two network fixes change those outputs from empty to real values. No
other method, field, or command changes.

## v0.9.0 - vMonitor Create and Delete

### Highlights

- `monitor.CreateCheck` creates an HTTP synthetic check. It always verifies
  the target's TLS certificate and sends no notifications yet, so a created
  check alerts nobody. Zero-value fields take the console's defaults. It is
  never retried after a failure that may have reached the server; after
  such an error, list checks and look for the name before trying again.
- `monitor.DeleteCheck` deletes a check, and `monitor.ListLocations` lists
  the probe locations whose IDs `CreateCheck` takes.
- New `vngcloud monitor create-check`, `delete-check`, and
  `list-locations` commands. `delete-check` needs `--yes`, and a read-only
  profile refuses both writes. Locations, headers, query parameters, and
  assertions go through `--cli-input-json`.

### Behavior changes

None: this release adds methods and commands only.

## v0.8.0 - vMonitor Pause and Resume

### Highlights

- New `monitor` package for vMonitor synthetic checks: `ListChecks`,
  `GetCheck`, `PauseCheck`, and `ResumeCheck`. The API has one toggle
  request for both, so `PauseCheck` and `ResumeCheck` read the status
  first, send the toggle at most once, and confirm by reading. They report
  `Changed`, so a deploy resumes only a check it paused itself. See
  [Monitor](https://github.com/dannyota/vngcloud/wiki/Monitor) and ADR 0003.
- New `vngcloud monitor list-checks`, `get-check`, `pause-check`, and
  `resume-check` commands. Pause and resume are writes, so a read-only
  profile refuses them.
- New CLI error codes `UnexpectedStatus` and `StatusUnconfirmed`, both
  exit 1.
- `EndpointOverrides` gains `Monitor`.

### Behavior changes

None for existing callers. A new internal transport option sends a toggle
request once, with no retry, no resend after a 401, and no redirect.

## v0.7.0 - vCDN IP Ranges

### Highlights

- New `cdn` package: `cdn.New(cfg).ListIPRanges(ctx, nil)` returns the
  current GreenNode CDN IP ranges an origin must allow, as sorted,
  de-duplicated, canonical CIDR strings. vCDN has no API, so the SDK reads
  GreenNode's public FAQ page instead; the request carries no credential
  and no cookie. Any doubt about the page's shape fails the call with
  `cdn.ErrPageFormat` rather than an empty or partial list. See
  [CDN](https://github.com/dannyota/vngcloud/wiki/CDN).
- New `vngcloud cdn list-ip-ranges` command, a read allowed under a
  read-only profile.
- `EndpointOverrides` gains `CDNDocs`, for pointing the read at a mirror or
  a test server.

### Behavior changes

None: this release adds a package, an endpoint field, and an internal
transport option. No existing method, field, or command changes.

Notes for `v0.1.0` to `v0.6.0` are in
[docs/release-notes/v0.1-v0.6.md](docs/release-notes/v0.1-v0.6.md).
