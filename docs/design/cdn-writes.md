# vCDN Writes

Status: Accepted (2026-10-10).

The write rules for [vCDN API](cdn-api.md): Web Accelerator update,
delete, enable, and disable, cache purge, certificate writes, and the
deferred create.
Facts, errors, models, and reads are in that design; CLI commands, tests,
and releases are in [vCDN CLI](cdn-cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md), and the status toggles
follow [ADR 0003](../adr/0003-toggle-writes.md).

## SDK

"(r)" marks `vngcloud:"required"`. Paths are under `v1/`.

### Web Accelerator writes

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `CreateWebAccelerator` (deferred) | `POST cdn/create` | `DomainName` (r), `Upstreams` (r), `DefaultRuleActions`, `LBType`, `FailOverErrorCodes`, `CertificateID`, `OriginHostHeader`, `CNames`, `NoWait` | `{WebAccelerator}` |
| `UpdateWebAccelerator` | `GET cdn/detail/{cdnId}`, then `PUT cdn/update` | `CDNID` (r), `SetRuleActions`, `RemoveRuleActions`, `Upstreams`, `LBType`, `FailOverErrorCodes`, `CertificateID`, `OriginHostHeader`, `CNames`, `NoWait` | `{WebAccelerator}` |
| `DeleteWebAccelerator` | `GET cdn/detail/{cdnId}`, then `DELETE cdn/delete/{cdnId}` | `CDNID` (r) | `{}` |
| `EnableWebAccelerator` | `GET cdn/detail/{cdnId}`, then `PUT cdn/status/change/{cdnId}` | `CDNID` (r), `NoWait` | `{WebAccelerator; Changed bool}` |
| `DisableWebAccelerator` | as above | `CDNID` (r), `NoWait` | `{WebAccelerator; Changed bool}` |

Input types:

- `UpstreamInput`: `ID`, `Priority int`, `IPAddress` (r), `UpstreamType`
  (default `httpOrigin`), `OriginValue *string`, and `UseSSL bool`, sent
  as `cdnUpstreamId`, `priority`, `ipaddress`, `upstreamType`,
  `originValue`, and `useSsl`. `ID` is empty on create.
- `RuleActionInput`: `Name` (r) and `Value`, sent as `actionName` and
  `value`. `Value` is the server's string; for `hsts` and `minify` it is
  JSON text, as in [the model](cdn-api.md#models).
- On update, a nil list keeps what the CDN has, and a non-nil empty list,
  such as `"CNames": []` in `--cli-input-json`, clears it. Scalar fields
  are pointers; nil keeps the value.

### Purge and certificate writes

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `PurgePaths` | `POST cdn/flush-cache`, type `URI` | `CDNDomain` (r), `Paths` (r) | `{}` |
| `PurgePattern` (deferred) | `POST cdn/flush-cache`, type `BEGIN`, `END`, or `CONTAIN` | `CDNDomain` (r), `Match` (r), `Pattern` (r) | `{}` |
| `PurgeAll` (deferred) | `POST cdn/flush-cache`, type `ALL` | `CDNDomain` (r) | `{}` |
| `ImportCertificate` | `POST certificate/api/upload` | `Certificate` (r), `PrivateKey vngcloud.Secret` (r), `CARoot`, `CertificateID` | `{Certificate *Certificate}` |
| `EnableCertificate` | [Status toggle](#status-toggles) on `POST certificate/status/change/{id}` | `CertificateID` (r) | `{Certificate; Changed bool}` |
| `DisableCertificate` | as above | `CertificateID` (r) | `{Certificate; Changed bool}` |
| `DeleteCertificate` | `POST certificate/delete/{id}` | `CertificateID` (r) | `{}` |

## Shared rules

### Status guard

Every Web Accelerator write except create reads the CDN first and acts on
its status, sending nothing when it refuses:

| Status | Update | Delete | Enable | Disable |
|-|-|-|-|-|
| 1 `ACTIVE` | Sends | Sends | `Changed: false` | Sends |
| 0 `DISABLED` | `ErrInvalidInput` | Sends | Sends | `Changed: false` |
| 3 `DEPLOYING`, 5 `DISABLING` | `ErrBusy` | `ErrBusy` | `ErrBusy` | `ErrBusy` |
| Any other | `ErrUnexpectedStatus` | `ErrUnexpectedStatus` | `ErrUnexpectedStatus` | `ErrUnexpectedStatus` |

- Status 3 follows a create, an enable, or an update alike, so it does
  not show that an enable is under way. The server refuses every write
  during a transition, so the guard saves a request and gives a clear
  error.
- An update of a disabled CDN has not been tried, and it may enable the
  CDN on deploy. The refusal message says to enable the CDN first.
- The server's own busy message, from a write that raced past the guard,
  also wraps `ErrBusy` ([errors](cdn-api.md#errors)).
- `ErrBusy` means nothing changed; the caller runs the same call again
  after the CDN settles.

### Settle wait

Update, enable, and disable wait for the CDN to settle unless `NoWait`
is set. The wait reads `GET cdn/detail/{cdnId}` every 10
seconds, on an injected clock, for up to 6 minutes from the write's
response:

| Write | Pending | Settled |
|-|-|-|
| Update | 3 | 1 |
| Enable | 3 | 1 |
| Disable | 5 | 0 |

- Settled: the Output holds that read.
- Another status ends the wait with the Output and an error wrapping
  `ErrUnexpectedStatus` that names it.
- A detail `NotFound` ends the wait with that error: the CDN was deleted.
- Another read error is retried at the next tick within the bound.
- The bound, or a cancelled context, returns the Output from the last
  good read and an error wrapping `ErrNotSettled`. The message says the
  write was accepted and must not be repeated. The CLI prints the Output
  on stdout and the error on stderr.

The measured transitions take 3 to 5 minutes, so the bound leaves one
minute of margin. With `NoWait`, each write returns after one detail read.

### Sending

- Purge is a `POST`, retried only after a 429 or a failed dial (ADR 0002
  rule 2).
- Update, delete, and the toggles set `Once`. A resend after the first
  attempt landed meets the busy refusal or `Not found cdn` and hides the
  success.
- Every `CDNID` passes `core.CheckPathID` before any request.

## Create

`CreateWebAccelerator` is deferred and portal-only until the API accepts
a create: every valid body fails with `Create CDN failed.`
([paths](cdn-api.md#paths)). The owner creates each CDN in the vCDN
Portal. A capture of the portal create gives nothing to copy, since the
portal posts a form, not JSON.

The retest, a free and cleanable write on the test account: send the
body below with `userUuid` and `customerId` added, copied from the detail
of a portal-made CDN, and delete any CDN it makes. When a retest
succeeds, create ships in its own release on this contract. The body is
the reference's:

```json
{"type": "webacc", "domainName": "...",
 "upstreams": [{"priority": 1, "ipaddress": "...",
   "upstreamType": "httpOrigin", "originValue": null, "useSsl": false}],
 "lbType": "rr", "failOverErrorCode": ["500", "502", "503", "504"],
 "defaultRuleAction": [{"actionName": "minimumTls", "value": "TLS 1.2"},
   {"actionName": "nosniff", "value": "on"}],
 "cName": [], "sslId": "default", "originHostHeader": ""}
```

- The SDK fills an empty `LBType` with `rr`, `CertificateID` with
  `default`, and `FailOverErrorCodes` with the four codes: the values the
  server fills itself, sent so the body does not depend on that.
- An empty `DefaultRuleActions` sends `minimumTls` `TLS 1.2` and `nosniff`
  `on`, two actions the portal sets on every CDN. A non-empty list is sent
  exactly as given. The server adds a default `alwaysHttps` when the body
  has none.
- `Upstreams` needs at least one entry, none with an `ID` and none with
  an empty `IPAddress`. `Priority` is sent as given; the wiki template
  uses 1.
- The domain name pattern, the error code list, the action names, and the
  package limits stay on the server (ADR 0002 rule 5). A package limit
  refusal names the limit, and the SDK passes it through.
- The Output comes from a detail read. The SDK takes `cdnId` from the
  response `data`. When `data` has none, it lists once and takes the one
  CDN whose `domainName` matches; no unique match is an `EmptyResponse`
  error saying the CDN may exist.
- After a 5xx, an envelope failure with no sentinel, or a network error,
  the CDN may exist; the error names `list-web-accelerators` and the
  `DomainName` to look for.
- A `POST`, retried only after a 429 or a failed dial. It waits as an
  update does, from 3 to 1.

## Update

The update call takes the whole CDN. The server deletes every rule action
the body leaves out, creates every action without an `id`, and gives
`alwaysHttps` a new `id` on every update. `UpdateWebAccelerator` therefore
reads, merges by action name, and writes:

1. An Input with no field set is `ErrInvalidInput`, with no request.
2. `GET cdn/detail/{cdnId}` and apply the [status guard](#status-guard).
3. Start from the read's `data` as a raw JSON object, so every field the
   SDK does not model, such as `userUuid`, `status`, `childrenRule`, and
   `childrenAdvanceRule`, goes back unchanged.
4. Rule actions: for each `SetRuleActions` entry, change the `value` of
   the read action with the same `actionName`, keeping its `id` and
   `order`, or append a new action without `id` when none has that name.
   Then drop each action named in `RemoveRuleActions`; a name that is not
   there is ignored. A name twice in `SetRuleActions`, or in both lists,
   is `ErrInvalidInput`.
5. Upstreams: with `Upstreams` nil, the read's list goes back as read.
   Otherwise `upstreams` holds only the given entries: one with an `ID`
   edits that origin in place, replacing all its fields, and one without
   adds an origin. An `ID` that the read does not hold is
   `ErrInvalidInput`.
6. Scalar and list fields that are set replace theirs.
7. When the merged object equals the read, send nothing and return the
   read.
8. `PUT cdn/update` once, then the [settle wait](#settle-wait). The
   response `data` lacks some actions, so the Output always comes from a
   detail read.

- No call removes an origin: leaving one out of `upstreams` keeps it.
  The wiki says so, and says to edit an origin in place to change it. On
  a package that allows one origin, an add is refused.
- The merge works on the fresh read, so a caller never needs an action
  `id`, and the changing `alwaysHttps` `id` never goes stale.
- Two updates that race lose one; the API has no conditional request.
  The second usually meets the busy refusal.
- After a 5xx or a network error the update may have landed; the error
  says to read the CDN before running it again.

## Delete

`DeleteWebAccelerator` reads, applies the [status guard](#status-guard),
and sends `DELETE cdn/delete/{cdnId}` once. The CDN is gone from the next
read, so the call does not wait. An unknown ID fails the read with
`NotFound`, and a delete that loses a race to another gets `Not found
cdn`, also `NotFound`. A delete of an active CDN has not been tried; the
live test does it.

## Status toggles

`PUT cdn/status/change/{cdnId}` takes no body and flips the status, so
the toggles follow ADR 0003. For target `T` and pending status `P` from
the [settle wait](#settle-wait) table:

1. Read, and apply the [status guard](#status-guard).
2. Send the toggle once. Success is `success: true` with `data: ""`.
3. A 4xx, an envelope failure, or a failed dial: return that error; the
   server did not act. The busy message wraps `ErrBusy`.
4. Otherwise confirm by reading at once and after 2, 4, and 8 seconds,
   stopping at the first read that shows `P` or `T`. No such read:
   return `ErrStatusUnconfirmed` wrapping the toggle error and the last
   read error.
5. With `NoWait`, return that read with `Changed: true`. Otherwise run
   the settle wait to `T` and return with `Changed: true`, or the wait's
   error with the Output.

Certificates have only 0 and 1, so they have no `P` and no settle wait;
steps 1 to 4 apply with the certificate detail read.

## Purge

The purge body is `{cdnDomain, type, patterns}`. The server checks
neither `type` nor the CDN's state before it answers success: it accepted
`type: "BOGUS"`. Only `URI` is known to purge, so C3 ships `PurgePaths`
alone; `PurgePattern` and `PurgeAll` wait for a check on a CDN with
traffic.

- `PurgePaths` sends type `URI` and needs at least one path, none empty,
  none holding `*`. The server refuses `*` in a `URI` pattern with code
  202, though its message lists `*` as allowed.
- A purge within 30 seconds of the last one on the same CDN is refused
  with code 202 and `Last CDN flush cache time is ...`, which wraps
  `ErrPurgeCooldown`. The SDK does not wait and retry; the caller does.
- Other code 202 answers are input errors and match `ErrInvalidInput`.
- An unknown `cdnDomain` is code 500 and matches no sentinel.
- Each purge counts against the package's daily purge limit.

## Certificates

- `ImportCertificate` checks before any request that `Certificate` parses
  with `crypto/x509` and `PrivateKey` is one PEM block whose type ends in
  `PRIVATE KEY`, as [vLB certificates](lb-certificates.md) does.
- With `CertificateID` set, the call replaces that certificate in place,
  for a renewal; every CDN that uses it changes at once.
- The request sets `Sensitive`, `Redact` with the key, and
  `WithholdMessage`, since a 400 may quote the rejected input.
- The server answers no ID. After a 2xx the SDK lists certificates once
  and sets `Certificate` when exactly one matches the uploaded
  certificate's common name and expiry; otherwise `Certificate` is nil and
  there is no error.
- A `POST`, retried only after a 429 or a failed dial.
- `DeleteCertificate` sends one request and does not wait.

## API key writes

The API can create, edit, and delete API keys with an API key. This design
does not ship them; see [decision 9](cdn-cli.md#owner-decisions). If they
ship later: create sets `Once` and `Sensitive`, returns the token as a
`vngcloud.Secret` written only to `--secret-file`, and delete, which takes
the token in its body, looks the token up by ID inside the SDK.

## Errors

| Case | Error | CLI code and exit |
|-|-|-|
| Missing field, bad ID, empty update, update of a disabled CDN, bad merge | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| Status 3 or 5 before a write, or the server's busy message | `cdn.ErrBusy`, nothing changed | `ResourceBusy`, 1 |
| Status outside the table | `cdn.ErrUnexpectedStatus`, nothing sent | `UnexpectedStatus`, 1 |
| Toggle sent, no confirm read shows it | `cdn.ErrStatusUnconfirmed` | `StatusUnconfirmed`, 1 |
| Write accepted, not settled within 6 minutes | `cdn.ErrNotSettled`, with Output | `NotSettled`, 1 |
| Purge within 30 seconds of the last | `cdn.ErrPurgeCooldown` | `PurgeCooldown`, 1 |
| Unknown CDN, repeat delete | `ErrNotFound` | `NotFound`, 4 |
| Package limit, other server refusal | The envelope error | 1 |

The CLI checks `ErrNotSettled` before its cancelled-context rule, so a
Ctrl-C during a wait still reports that the write was accepted.

## Security

Every write gets an adversarial review before its release. The review
checks:

- No purge retry after a 5xx or an envelope failure; update,
  delete, and toggles send once (`Once`), even after a failed dial.
- The status guard sends nothing on 0 for update, 3, 5, or an unknown
  status, and on the target status for a toggle.
- The update body keeps every unmodeled field of the read, including
  `userUuid` and the page rules, and every action `id` it does not drop.
  `userUuid` never reaches an Output, an error, or `--debug`.
- The settle wait sends nothing, and its errors carry the Output.
- The delete and disable commands need `--yes`
  ([commands](cdn-cli.md#commands)).
- The API key never reaches a request body, an error, or a capture.
