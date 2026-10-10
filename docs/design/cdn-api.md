# vCDN API Design

Status: Accepted.

This design adds GreenNode vCDN management to the `cdn` package: Web
Accelerator CDNs, certificates, cache purge, API key reads, and traffic
analytics, on the vCDN API. The CLI commands, tests, live checks, releases,
and owner decisions are in [vCDN CLI](cdn-cli.md). The CDN IP range read
stays in [CDN IP ranges](cdn.md).

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md), and the status toggles
follow [ADR 0003](../adr/0003-toggle-writes.md).

## Source

The vCDN API reference, v1.1.0, at `https://api-docs.vngcloud.vn/vcdn/`,
and the GreenNode vCDN pages on API developers, HTTP origin, CNAME, purge,
page rules, and pricing (`docs.greennode.ai/vcdn/...`), read on 2026-10-09.
Read-only probes with the owner's API key on an account with no CDN, on
2026-10-09, correct the reference where the two disagree.

Facts from the reference:

- Base URL `https://vcdn-api.vngcloud.vn/vcdn-api`; paths start `v1/`.
- Every call needs `Authorization: Bearer <API key>`. The IAM token is not
  accepted, and the reference names no other scheme.
- A person creates the key in the vCDN Portal, which takes root login only
  ([CDN non-goals](cdn.md#non-goals)). The key expires after 1 minute to 1
  year, as chosen. The key is a JWT.
- vCDN has three CDN types: Web Accelerator, Video On Demand, and Object
  Download. Each has create, update, delete, detail, list, and status
  change calls under its own prefix.
- A CDN has a `cdnId`, the customer `domainName`, alternative `cName`
  names, and a generated `cdnDomain` such as `<random>webacc.vcdn.cloud`,
  which the customer's DNS points at.
- Status codes: 0 `DISABLED`, 1 `ENABLED`, 2 `DELETED`, 3 `DEPLOYING`,
  4 `DELETING`, 5 `DISABLING`, 6 `SUSPENDING`.
- Delete and status change answer "will effect after 5 minutes".
- No call lists, buys, or prices a package. No call quotes a price.

Facts from the probes:

- The probes reached the API at the base URL above.
- The reference's Web Accelerator prefix `webacc` does not exist: every
  `webacc/*` route answers 404 problem+json, `"detail": "No static
  resource webacc/list."`. The live prefixes are `cdn`, `vod`, and
  `obj-download`. `GET cdn/list`, `vod/list`, and `obj-download/list`
  answer `200 {"success": true, "code": 200, "message": "Get CDN data
  successful.", "data": []}`.
- A write route reached with the wrong method still reaches its handler:
  `GET cdn/create`, `cdn/update`, `cdn/flush-cache`, and `vod/create`
  answer `200 {"success": false, "code": 500, "message": null, "data":
  ""}` and create nothing. Other routes answer a wrong method with 405.
- `certificate/list` answers the envelope with `data: []`.
- `apikey/list` answers the envelope with a list in `data`.
- The key's `allowOriginHeader` limits the `Origin` request header only.
  A request with an `Origin` outside GreenNode and VNG Cloud hosts gets
  `403 Invalid CORS request` as plain text.
- Responses carry no rate-limit headers.

### Paths

The Web Accelerator operations use the `cdn` prefix: `cdn/list`,
`cdn/detail/{cdnId}`, `cdn/create`, `cdn/update`, `cdn/delete/{cdnId}`,
`cdn/status/change/{cdnId}`, and `cdn/flush-cache`. Only `cdn/list` and
the existence of the write routes are confirmed. The rest are inferred
from the live prefix and the reference's `webacc` paths, with the
reference's bodies. The first read of a real CDN
([live checks](cdn-cli.md#live-checks)) confirms the list item and detail
shapes; the Web Accelerator reads ship only after it does.

### Pricing

The pricing page names no prices. A Web Accelerator needs an Accelerator
Package (Basic, Standard, Pro, Enterprise), which caps the number of CDNs
(1, 3, 5, 20), alternative names, page rules (1, 3, 5, 100), origin IPs,
and purges per day (5, 20, 50, 1000). Traffic is prepaid in 3, 6, or 12
month domestic or international packages, or postpaid and billed monthly
by use. Packages are bought in the portal only.

Unconfirmed: whether an account with no package can create a Web
Accelerator, what Basic costs, and whether a CDN with zero traffic costs
anything. The [owner check](cdn-cli.md#live-checks) settles this before
any create, by hand or by code.

## Credentials

The API key is a separate credential from IAM User login. It resolves on
its own, with the [precedence](sdk-and-cli.md#precedence) the SDK already
uses:

1. `vngcloud.WithCDNAPIKey(key string)`, a `LoadOption`.
2. `VNGCLOUD_VCDN_API_KEY`, skipped when the profile is explicit, as for
   every credential, so a `.env` key never reaches `--profile prod` calls.
3. `vcdn_api_key` in the profile's section of the credentials file, which
   keeps its 0600 check.

- No flag carries the key. `vngcloud configure set vcdn_api_key -` reads it
  from stdin; a literal value is refused, and `configure get` and `list`
  mask it, as for `password` ([configure](cli.md#configure)). The
  interactive `configure` prompts do not change.
- The key does not count as an IAM credential. `LoadConfig` still returns
  `ErrNoCredentials` when no IAM credential set resolves, so the CLI keeps
  its current behavior for every command.
- `LoadConfig` refuses, with `ErrInvalidConfig`, a key that holds
  whitespace or a control character or is longer than 4 KiB. The error
  names the source and never the value.
- The shared session keeps the key in a redacting type internal to
  `internal/core`: `fmt`, JSON, text marshaling, and `slog` all show
  `[redacted]`. `Config` has no getter for it; `cdn.New` reads it through
  `internal/core`.
- A vCDN call with no key returns `cdn.ErrNoAPIKey`, which wraps
  `vngcloud.ErrNoCredentials`, before any request. The CLI exits 3.
  `ListIPRanges` needs no key.

## Endpoint and transport

`endpoints.Set` and `EndpointOverrides` gain `CDN`, default
`https://vcdn-api.vngcloud.vn/vcdn-api/`, and `internal/routes` gains
`ProductCDN`. The region is ignored and no project ID is sent. The SDK
refuses cross-host redirects, as for every service.

`transport.Request` gains `APIKey string`. When it is set:

- The transport sends `Authorization: Bearer <APIKey>`, never asks the
  credentials provider for a token, and never invalidates a token after a
  401. A 401 is not retried.
- The transport adds `APIKey` to `Redact`, so no error echoes it.
- `SkipAuth` and `APIKey` together are a programming error that fails the
  call before any request.

Every vCDN call sets `APIKey`. The `cdn` client builds every URL from the
`CDN` endpoint, so the key reaches no other host. The SDK sends no
`Origin` header, so the server's CORS check and the key's
`allowOriginHeader` never apply to it. `--debug` logs the path only, as
for every request.

## Errors

Success is the envelope
`{"success": true, "code": 200, "message": "...", "data": ...}`. Every
vCDN error is an `*APIError` with the operation. `transport.decodeError`
builds it for a non-2xx status; the `cdn` package builds it for a 2xx
envelope and applies the message rules below.

| Response | Error |
|-|-|
| 401, empty body | Code `Unauthorized`, fixed message; matches `ErrAuth`; not retried; CLI exit 3 |
| 403, any body | Code `Forbidden`, fixed message; matches `ErrPermission` |
| 400 problem+json (malformed JSON) | Code `BadRequest`; matches `ErrInvalidInput`; CLI exit 2 |
| 404 problem+json (unknown route) | Code `NotFound`; matches `ErrNotFound`; CLI exit 4 |
| 405 problem+json (wrong method) | Code `ClientError` |
| 5xx | Code `ServerError`; retried as for every service |
| 2xx, `success: false` | The envelope rule |
| 2xx, not the expected JSON | Code `EmptyResponse`; for a write, the message says it may have happened |

The fixed messages:

- 401: `vCDN API key rejected: check the key and its expiry`. The server
  sends an empty body for a missing header, a wrong key, and a key
  without `Bearer` alike.
- 403: `vCDN API key not allowed to call this API`. The reference's 403
  message names the account user.

Problem+json bodies hold `type`, `title`, `status`, `detail`, and
`instance`. `transport.errorBody` already reads `detail`; it gains `title`
as the last fallback before the status text. That fallback changes no
other service's message unless a body holds `title` and none of
`message`, `error`, and `detail`.

The envelope rule: a 2xx with `success: false` is an `*APIError` with the
HTTP status, `Code` set to the envelope `code` as text (such as `500`),
and `Message` set to `message`. Both are redacted with the request's
secrets, the API key among them, before they reach the error, as the
transport does for a non-2xx body.

- Most failures arrive this way, with `code` 500 and `message` null, `""`,
  or text. A null or empty message becomes `vCDN <operation> failed; the
  server gave no reason`.
- An envelope `code` 400, 401, 403, or 404 also matches that status's
  sentinel. Code 500 matches none, and the call is not retried, since the
  HTTP status is 2xx.
- A detail read (`GetWebAccelerator`, `GetCertificate`) whose envelope is
  `success: false` with `data` `""` or null maps to Code `NotFound`,
  matching `ErrNotFound`; the CLI exits 4. This is inferred: an unknown ID
  gave that answer on both detail routes, and no other cause of it is
  known.
- Analytics on a domain the account does not own answers `User <email>
  is not the owner of all the request CDN.` Any message that holds an
  at sign (`@`, fullwidth U+FF20, or small U+FE6B) becomes `vCDN
  <operation> refused: the server message named an account user and was
  withheld; check that every domain is a CDN of this account`, so no
  account email reaches an error. Other spellings of an
  address are out of scope; see [decision 29](cdn-cli.md#owner-decisions).

Every message is cut to 256 bytes with control characters removed.

## SDK

Operation names are `cdn.<Method>`. "(r)" marks `vngcloud:"required"`, and
"L[T]" is `core.List[T]`. Paths are under `v1/`. "Inferred" marks a path
from [Paths](#paths) that a live check confirms before it ships.

### Reads

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListWebAccelerators` | `GET cdn/list` | none | L[WebAcceleratorSummary] |
| `GetWebAccelerator` | `GET cdn/detail/{cdnId}` (inferred) | `CDNID` (r) | `{WebAccelerator}` |
| `ListCertificates` | `GET certificate/list` | none | L[Certificate] |
| `GetCertificate` | `GET certificate/detail/{id}` | `CertificateID` (r) | `{Certificate}` |
| `ListAPIKeys` | `GET apikey/list` | none | L[APIKey] |
| `GetTrafficReport` | `POST analytic/traffic-report` | `Domains` (r), `From` (r), `To` (r) | `{Items []DomainTraffic}` |
| `GetTraffic` | `POST analytic/traffic-consuming` | [Range](#analytics) | `{Points []CacheSample}` |
| `GetRequestRate` | `POST analytic/cdn-requestsps` | [Range](#analytics) | `{Points []CacheSample}` |
| `GetCacheStatus` | `POST analytic/cache-status` | [Range](#analytics) | `{Counts map[string]int64}` |
| `GetHTTPCodes` | `POST analytic/cdn-http-codes` | [Range](#analytics) | `{Counts map[string]int64}` |

### Web Accelerator writes

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `CreateWebAccelerator` | `POST cdn/create` (inferred) | `DomainName` (r), `Upstreams` (r), `DefaultRuleActions` (r), `LBType`, `FailOverErrorCodes`, `CertificateID`, `OriginHostHeader`, `PageRules`, `CNames` | `{WebAccelerator}` |
| `UpdateWebAccelerator` | `GET cdn/detail/{cdnId}`, then `PUT cdn/update` (inferred) | `CDNID` (r), pointers to every create field except `DomainName` | `{WebAccelerator}` |
| `DeleteWebAccelerator` | `DELETE cdn/delete/{cdnId}` (inferred) | `CDNID` (r) | `{}` |
| `EnableWebAccelerator` | [Status toggle](#status-toggles) on `PUT cdn/status/change/{cdnId}` (inferred) | `CDNID` (r) | `{WebAccelerator; Changed bool}` |
| `DisableWebAccelerator` | as above | `CDNID` (r) | `{WebAccelerator; Changed bool}` |

### Purge and certificate writes

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `PurgePaths` | `POST cdn/flush-cache`, type `URI` | `CDNDomain` (r), `Paths` (r) | `{}` |
| `PurgePattern` | `POST cdn/flush-cache`, type `BEGIN`, `END`, or `CONTAIN` | `CDNDomain` (r), `Match` (r), `Pattern` (r) | `{}` |
| `PurgeAll` | `POST cdn/flush-cache`, type `ALL` | `CDNDomain` (r) | `{}` |
| `ImportCertificate` | `POST certificate/api/upload` | `Certificate` (r), `PrivateKey vngcloud.Secret` (r), `CARoot`, `CertificateID` | `{Certificate *Certificate}` |
| `EnableCertificate` | [Status toggle](#status-toggles) on `POST certificate/status/change/{id}` | `CertificateID` (r) | `{Certificate; Changed bool}` |
| `DisableCertificate` | as above | `CertificateID` (r) | `{Certificate; Changed bool}` |
| `DeleteCertificate` | `POST certificate/delete/{id}` | `CertificateID` (r) | `{}` |

### Models

Models keep their API JSON tags. They drop `customerId`, `userUuid`,
`createdUser`, `updatedUser`, `deletedUser`, and `userEmail`, which are
account data no caller needs.

- `WebAcceleratorSummary`: `CDNID`, `DomainName`, `CDNDomain`, `CNames`,
  `Status int`, and `StatusName`, the name from the status table, or
  `UNKNOWN(<n>)`.
- `WebAccelerator`: the summary fields plus `Type`, `CertificateID`
  (`sslId`), `LBType`, `OriginHostHeader`, `FailOverErrorCodes []string`,
  `Upstreams []Upstream`, `DefaultRuleActions []RuleAction`, and
  `PageRules []PageRule` (`childrenRule`).
- `Upstream`: `ID` (`cdnUpstreamId`), `Priority int`, `IPAddress`
  (`ipaddress`), `UpstreamType`, `OriginValue *string`, `UseSSL bool`,
  `Status int`.
- `RuleAction`: `ID`, `ActionName`, `Value string`, `Order int`. `Value`
  stays the server's string, which is JSON text for some actions (`hsts`,
  `redirect`, `originOverride`); the SDK does not parse it.
- `PageRule`: `ID`, `Order`, `URLPattern []string`, `Actions`,
  `Criteria []Criterion` (`criterias`), `Status`, `PageRules`
  (`childrenRule`), and `CriteriaMustSatisfy *string`.
  `Criterion`: `ID`, `CriteriaName`, `Value string`, `Order`.
- `Certificate`: `ID` (`cdnSslcertificateId`), `CommonName`, `CName`,
  `Issuer`, `ValidFrom`, `ExpiresOn`, `Status int`, `CDNUsing int`,
  `CreatedTime`, and, from `GetCertificate` only, `Certificate` and
  `CARoot`. It has no private key field, so a decode never holds the key
  the server returns. Dates stay the server's strings, such as
  `28 Apr 2025 06:53:20 GMT`.
- `APIKey`: `ID int` (`apiKeyId`), `ExpiresAt` (`expiredDate`),
  `CreateTime`, `UpdateTime`, `AllowOriginHeader string`, and
  `Current bool`. The three times are `time.Time`, parsed as RFC 3339:
  the server sends ISO 8601 with milliseconds and `+00:00`. It has no
  token or email field.

### Reads in detail

- `ListWebAccelerators` and `ListCertificates` return every item in one
  call; the API has no paging. A `data` of `[]` or null gives empty
  `Items` and no error, so an empty account reads as empty.
- `ListAPIKeys` decodes the envelope with a list in `data`; anything else
  is an `EmptyResponse` error. Nothing in the response marks the key in
  use, so the SDK decodes each `token` into a private field, sets
  `Current` when it equals the key the client sends (compared in constant
  time), and drops the token before it returns. A caller can then find
  the expiry of the key in use.
- `ListCertificates`, `GetCertificate`, and `ListAPIKeys` set `Sensitive`:
  the server returns every certificate's private key and every API key's
  token in these reads. The capture hook never sees them, and a decode
  error never quotes the body.
- `CDNID` and `CertificateID` pass `core.CheckPathID` before any request,
  reads included. Both are UUIDs or short hex in the reference.

### Analytics

The Range Input of the four series calls has `Domains []string` (r),
`Period`, `From`, and `To`. Exactly one of `Period` or the pair `From` and
`To` must be set; anything else is `ErrInvalidInput` before any request.

- `Period` is sent as given. The reference lists `30m`, `1h`, `3h`, `6h`,
  `12h`, `24h`, `3d`, `7d`, `14d`, `30d`, `90d`, `180d`, `360d`. The
  server checks domain ownership before it checks `period`, so no probe
  has seen it validate one.
- `From` and `To` are RFC 3339 strings, so the CLI can set them as flags.
  The SDK converts each to `fromTime` and `toTime` in UTC+7 with
  `time.FixedZone`. The reference names `dd/mm/yyyy hh:mm`. With an empty
  `domains`, the server accepted `dd/mm/yyyy` and refused
  `dd/mm/yyyy hh:mm`, so the format, and the time zone, stay unconfirmed
  until a [live check](cdn-cli.md#live-checks) with a real domain. The
  analytics reads ship only after it.
- `Domains` holds the generated `cdnDomain` names, as the reference
  examples do; the same live check tries `domainName`.
- All analytics calls are reads that use `POST` (ADR 0002 rule 1), so they
  set `Idempotent` and keep the transport's retries.
- Series responses map epoch-millisecond keys (UTC) to values. The SDK
  returns `Points` sorted by `Time time.Time` in UTC. `CacheSample` has
  `Time`, `Cached`, and `Uncached` as `float64`. A key that is not an
  integer fails the call.
- `GetCacheStatus` and `GetHTTPCodes` receive `data` as a JSON string that
  holds an object, such as `"{\"hit\":73,\"miss\":2074}"`. The SDK decodes
  the string, then the object, into `Counts`.
- `GetTrafficReport` takes no `Period`. `DomainTraffic` has `DomainName`
  and `Points []Sample` with `Time` and `Value` in bytes.

The other analytics calls (origin request rate, unique visitors, speed,
average speed, monthly traffic, origin HTTP codes, country traffic) are
deferred; they reuse these shapes.

## Write rules

### Create

- The SDK fills defaults when a field is empty: `LBType` `rr`,
  `CertificateID` `default`, and `FailOverErrorCodes` `500`, `502`, `503`,
  `504`, all from the reference. It sends `type: webacc` and `cName: []`.
- `DefaultRuleActions` is required and has no SDK default. The allowed
  actions depend on the package (Basic has no image optimization or
  Brotli), so a fixed default could fail on one package and weaken another.
  The wiki gives a template with `minimumTls` `TLS 1.2`.
- `Upstreams` needs at least one entry with a non-empty `IPAddress`;
  `UpstreamType` defaults to `httpOrigin`. Other value rules, such as the
  domain name pattern and the error code list, stay on the server
  (ADR 0002 rule 5).
- Create is a `POST`, retried only after a 429 or a failed dial. After a
  5xx, an envelope with `success: false` and `code` 500, or a network
  error, the CDN may exist; the error names `list-web-accelerators` and the
  `DomainName` to look for.
- The Output is the server's `data`, which holds `CDNID` and the generated
  `CDNDomain`.

### Update

The update call takes the whole CDN, and the server deletes every rule
action and page rule whose `id` the body leaves out. So
`UpdateWebAccelerator` reads, merges, and writes:

1. `GET cdn/detail/{cdnId}`.
2. Apply each non-nil Input field over the read. A list field replaces the
   whole list; the caller keeps an entry by sending its `ID`.
3. `PUT cdn/update` with the merged object, `cdnId`, `cdnDomain`, and
   the read's `status` unchanged. A `PUT` keeps the transport's retries.

Two updates that race lose one; the API has no conditional request. An
update with no non-nil field sends nothing and returns the read.

### Delete

`DeleteWebAccelerator` and `DeleteCertificate` send one request and do not
wait. A deleted CDN may show `DELETING` in reads for about five minutes.
A repeat delete returns whatever the server answers; a live check records
it.

### Status toggles

Both status calls flip the state and name no target, so they follow
ADR 0003. One implementation serves the four operations, with target
status `T` (1 or 0) and its transitional status `P` (3 `DEPLOYING` for
enable, 5 `DISABLING` for disable):

1. Check the ID; read with the detail call.
2. Status `T` or `P`: return with `Changed: false`, sending nothing.
3. Status other than 0 or 1: return `cdn.ErrUnexpectedStatus` naming it,
   sending nothing.
4. Send the toggle once, with `Once` set.
5. A 4xx, an envelope with `success: false`, or a failed dial: return
   that error.
6. Otherwise read at once and after 2, 4, and 8 seconds, stopping at the
   first read that shows `T` or `P`, and return it with `Changed: true`.
7. No such read: return `cdn.ErrStatusUnconfirmed` wrapping the toggle
   error and the last read error.

Certificates have only 0 and 1, so they have no `P`. The confirm bound is
about 14 seconds plus request time; a live check measures the real one.

### Purge

The three purge operations share one body, `{cdnDomain, type, patterns}`.
The SDK checks shape before any request, because the server answers a bad
shape with a 200 and `success: false`:

- `PurgePaths`: at least one path, none empty, none holding `*`.
- `PurgePattern`: `Match` is `BEGIN`, `END`, or `CONTAIN`; one non-empty
  `Pattern`, which may hold `*`.
- `PurgeAll`: sends `patterns: []`.

A purge is a `POST`, retried only after a 429 or a failed dial. Each purge
counts against the package's daily purge limit.

### Certificates

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

### API key writes

The API can create, edit, and delete API keys with an API key. This design
does not ship them; see [decision 9](cdn-cli.md#owner-decisions). If they
ship later: create sets `Once` and `Sensitive`, returns the token as a
`vngcloud.Secret` written only to `--secret-file`, and delete, which takes
the token in its body, looks the token up by ID inside the SDK.

## Security

- The API key reaches only the `CDN` endpoint, in the `Authorization`
  header. It never reaches argv, stdout, stderr, `--debug`, an error, a
  capture, or a fixture. Tests assert each.
- Any holder of one key can list every key's token and every certificate's
  private key through the API. A key is therefore as strong as the
  account's whole vCDN access; the wiki says so and advises the shortest
  expiry that works. `allowOriginHeader` limits browsers only, not a
  stolen key used from a script.
- 401 and 403 messages are fixed text, and any message that holds an at
  sign is withheld, so no account user or email reaches a log.
- `ListAPIKeys` drops each token and the account email before it returns.
- Certificate reads and imports are `Sensitive`; the model has no key
  field.
- Every write gets an adversarial review before its release.
