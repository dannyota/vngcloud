# vCDN API Design

Status: Accepted (2026-10-10).

This design adds GreenNode vCDN management to the `cdn` package: Web
Accelerator CDNs, certificates, cache purge, API key reads, and traffic
analytics, on the vCDN API. It holds the source facts, credentials,
transport, errors, reads, and analytics. The write rules are in
[vCDN writes](cdn-writes.md). The CLI commands, tests, live checks,
releases, and owner decisions are in [vCDN CLI](cdn-cli.md). The CDN IP
range read stays in [CDN IP ranges](cdn.md).

It builds on [SDK and CLI](sdk-and-cli.md) and [CLI](cli.md). Writes follow
[ADR 0002](../adr/0002-write-api-conventions.md), and the status toggles
follow [ADR 0003](../adr/0003-toggle-writes.md).

## Source

The vCDN API reference, v1.1.0, at `https://api-docs.vngcloud.vn/vcdn/`,
and the GreenNode vCDN pages on API developers, HTTP origin, CNAME, purge,
page rules, and pricing (`docs.greennode.ai/vcdn/...`), read on 2026-10-09.
Probes with the owner's API key correct the reference where the two
disagree: read-only on an empty account on 2026-10-09, and on 2026-10-10 on
a real CDN made in the portal and deleted through the API.

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
- No call lists, buys, or prices a package. No call quotes a price.

Facts from the probes:

- The reference's Web Accelerator prefix `webacc` does not exist: every
  `webacc/*` route answers 404 problem+json, `"detail": "No static
  resource webacc/list."`. The live prefixes are `cdn`, `vod`, and
  `obj-download`.
- Success is HTTP 200 with the envelope
  `{"success", "code", "message", "data"}`. Most failures are also HTTP
  200, with `success: false`.
- A write route reached with the wrong method still reaches its handler:
  `GET cdn/create`, `cdn/update`, `cdn/flush-cache`, and `vod/create`
  answer `success: false, code: 500, message: null, data: ""` and create
  nothing. Other routes answer a wrong method with 405.
- The key's `allowOriginHeader` limits the `Origin` request header only.
  A request with an `Origin` outside GreenNode and VNG Cloud hosts gets
  `403 Invalid CORS request` as plain text.
- Responses carry no rate-limit headers.
- Statuses seen: 3 deploying, 1 active, 5 disabling, 0 disabled. Create
  goes 3 to 1 in about 3 minutes; disable 1 to 5 to 0 in about 4; enable
  0 to 3 to 1 in about 5; update 1 to 3 to 1 in 4 to 5. Delete removes
  the CDN at once. The reference's 2 `DELETED`, 4 `DELETING`, and
  6 `SUSPENDING` never appeared.
- A status change, update, or delete during a transition is refused with
  `Current cdn status is not allow to update or delete` (envelope code 500
  for the toggle, 400 for delete) and changes nothing.
- Package limits are counted on the request body and refused with a
  message that names the limit, such as `...not allow to create more than
  0 cname, please upgrade your package`. The test account's package
  allows 0 alternative names and 1 origin.
- `certificate/list` does not list the `default` certificate a CDN uses.

### Paths

Every Web Accelerator path is confirmed live: `cdn/list`,
`cdn/detail/{cdnId}`, `cdn/create`, `cdn/update`, `cdn/delete/{cdnId}`,
`cdn/status/change/{cdnId}`, and `cdn/flush-cache`. `cdn/detail?cdnId=`
and an unknown or malformed ID both answer `success: false, code: 500,
message: null, data: ""`.

`POST cdn/create` checks its body, then fails. On 2026-10-10 a bad
`lbType` got code 400 `wrong lbType` and a bad origin IP got code 400, so
the body parses. Every valid body got HTTP 200 with
`{"success": false, "code": 500, "message": "Create CDN failed.",
"data": ""}` and created nothing: the reference body, the portal's full
12-action set, and the reference example with `order: 0` and
`childrenRule: []`. The failure is past validation, such as an
entitlement or an undocumented field like `userUuid` or `customerId`. The
portal creates a CDN on package Basic through a server-rendered form, not
this API. Create is [deferred](cdn-writes.md#create).

### Pricing

The pricing page names no prices. A Web Accelerator needs an Accelerator
Package (Basic, Standard, Pro, Enterprise), which caps the number of CDNs
(1, 3, 5, 20), alternative names, page rules (1, 3, 5, 100), origin IPs,
and purges per day (5, 20, 50, 1000). Traffic is prepaid in 3, 6, or 12
month domestic or international packages, or postpaid and billed monthly
by use. Packages are bought in the portal only. On the test account a CDN
with no traffic is free to create and delete.

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
  mask it, as for `password` ([configure](cli.md#configure)).
- The key does not count as an IAM credential. `LoadConfig` still returns
  `ErrNoCredentials` when no IAM credential set resolves.
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

`endpoints.Set` and `EndpointOverrides` have `CDN`, default
`https://vcdn-api.vngcloud.vn/vcdn-api/`. The region is ignored and no
project ID is sent. The SDK refuses cross-host redirects.

`transport.Request.APIKey`, when set, is sent as `Authorization: Bearer`.
The transport then never asks the credentials provider for a token, never
retries a 401, and adds the key to `Redact`. `SkipAuth` with `APIKey` fails
before any request. Every vCDN call sets `APIKey`, and every URL comes from
the `CDN` endpoint, so the key reaches no other host. The SDK sends no
`Origin` header. `--debug` logs the path only.

## Errors

Every vCDN error is an `*APIError` with the operation.
`transport.decodeError` builds it for a non-2xx status; the `cdn` package
builds it for a 2xx envelope and applies the message rules below.

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
`instance`. `transport.errorBody` reads `detail`, then `title` as the last
fallback before the status text.

The envelope rule: a 2xx with `success: false` is an `*APIError` with the
HTTP status, `Code` set to the envelope `code` as text, and `Message` set
to `message`, both redacted with the request's secrets. It is never
retried, since the HTTP status is 2xx. The first matching row applies:

| Envelope | Code | Matches |
|-|-|-|
| Detail read, `data` `""` or null | `NotFound` | `ErrNotFound`; CLI exit 4 |
| Message holds `is not allow to update or delete`, any code | envelope code | `cdn.ErrBusy` only |
| Message starts `Not found cdn`, any code | `NotFound` | `ErrNotFound`; CLI exit 4 |
| Message starts `Last CDN flush cache time`, code 202 | `202` | `cdn.ErrPurgeCooldown` only |
| Code 202 | `202` | `ErrInvalidInput`; CLI exit 2 |
| Code 400, 401, 403, or 404 | that code | that status's sentinel |
| Code null or absent | `EnvelopeError` | none |
| Any other code, such as 500 | that code | none; CLI exit 1 |

- A null or empty message becomes `vCDN <operation> failed; the server
  gave no reason`.
- Package limit messages pass through as text; they match no sentinel.
- The detail rule covers `GetWebAccelerator` and `GetCertificate`, whose
  unknown, malformed, and wrongly routed IDs all give that answer. The
  write requests do not use it, since a refused toggle also has
  `data: ""`.
- Analytics on a domain the account does not own answers `User <email>
  is not the owner of all the request CDN.` Any message that holds an
  at sign (`@`, fullwidth U+FF20, or small U+FE6B) becomes `vCDN
  <operation> refused: the server message named an account user and was
  withheld; check that every domain is a CDN of this account`, so no
  account email reaches an error. Other spellings of an address are out
  of scope; see [decision 29](cdn-cli.md#owner-decisions).

Every message is cut to 256 bytes with control characters removed.

## SDK

Operation names are `cdn.<Method>`. "(r)" marks `vngcloud:"required"`, and
"L[T]" is `core.List[T]`. Paths are under `v1/`. The write operations are
in [vCDN writes](cdn-writes.md#sdk).

### Reads

| Operation | Method and path | Input | Output |
|-|-|-|-|
| `ListWebAccelerators` | `GET cdn/list` | none | L[WebAcceleratorSummary] |
| `GetWebAccelerator` | `GET cdn/detail/{cdnId}` | `CDNID` (r) | `{WebAccelerator}` |
| `ListCertificates` | `GET certificate/list` | none | L[Certificate] |
| `GetCertificate` | `GET certificate/detail/{id}` | `CertificateID` (r) | `{Certificate}` |
| `ListAPIKeys` | `GET apikey/list` | none | L[APIKey] |
| `GetTrafficReport` | `POST analytic/traffic-report` | `CDNDomains` (r), `From` (r), `To` (r) | `{Items []DomainTraffic}` |
| `GetTraffic` | `POST analytic/traffic-consuming` | [Range](#analytics) | `{Points []CacheSample}` |
| `GetRequestRate` | `POST analytic/cdn-requestsps` | [Range](#analytics) | `{Points []CacheSample}` |
| `GetCacheStatus` | `POST analytic/cache-status` | [Range](#analytics) | `{Counts map[string]float64}` |
| `GetHTTPCodes` | `POST analytic/cdn-http-codes` | [Range](#analytics) | `{Counts map[string]float64}` |

### Models

Models keep their API JSON tags. They drop `customerId`, `userUuid`,
`userEmail`, `createdUser`, `updatedUser`, `deletedUser`, and
`drmSecretKey`, which are account data or secrets no caller needs.

- `WebAcceleratorSummary`: `CDNID`, `DomainName`, `CDNDomain`,
  `CNames []string` (`cName`), `Status int`, and `StatusName`. The list
  item has 33 fields, but only these, `limitBw`, and five flags are
  filled; the rest, the certificate (`sslCertificateId`) among them, are
  null.
- `WebAccelerator`: `CDNID`, `Type` (`webacc`), `DomainName`,
  `CDNDomain`, `CNames`, `Status`, `StatusName`, `CertificateID` (`sslId`,
  `default` for the shared certificate), `LBType` (`rr`),
  `OriginHostHeader`, `FailOverErrorCodes []string` (`failOverErrorCode`),
  `UseSSL`, `UseSmallFile`, and `EnableGzip` as `bool`,
  `Upstreams []Upstream`, `DefaultRuleActions []RuleAction`
  (`defaultRuleAction`), `PageRules` (`childrenRule`), and `AdvancedRule`
  (`childrenAdvanceRule`). `PageRules` and `AdvancedRule` are
  `json.RawMessage`: no page rule has been seen filled, and the update
  sends them back unchanged.
- `Upstream`: `ID` (`cdnUpstreamId`), `Priority int`, `IPAddress`
  (`ipaddress`), and `Status int`. The detail has no `upstreamType`,
  `originValue`, or per-origin `useSsl`.
- `RuleAction`: `ID`, `Name` (`actionName`), `Value string`, and
  `Order int` (always 0 so far). `Value` stays the server's string. For
  `hsts` it is JSON object text, such as
  `{"hsts":"off","preload":"off","includeSubDomains":"off","maxAge":"0m"}`,
  and for `minify` JSON array text, such as `["js","css","html"]`; the SDK
  does not parse either.
- `ID` fields are `string`. The decoder accepts a JSON string or integer,
  and the update sends each ID back in the JSON form it read.
- `Certificate` and `APIKey` are as shipped in C1: `Certificate` has no
  private key field and keeps the server's date strings; `APIKey` has
  `ID int`, `ExpiresAt`, `CreateTime`, `UpdateTime` as `time.Time`,
  `AllowOriginHeader`, and `Current bool`, and no token or email.

Status constants:

| Constant | Value | `StatusName` |
|-|-|-|
| `StatusDisabled` | 0 | `DISABLED` |
| `StatusActive` | 1 | `ACTIVE` |
| `StatusDeploying` | 3 | `DEPLOYING` |
| `StatusDisabling` | 5 | `DISABLING` |

Any other value is `UNKNOWN(<n>)`, and every write refuses it with
`cdn.ErrUnexpectedStatus`, sending nothing.

### Reads in detail

- `ListWebAccelerators` and `ListCertificates` return every item in one
  call; the API has no paging. A `data` of `[]` or null gives empty
  `Items` and no error.
- `ListAPIKeys` accepts only the envelope list. It decodes each `token`
  into a private field, sets `Current` when it equals the key in use
  (compared in constant time), and drops the token before it returns.
- `ListCertificates`, `GetCertificate`, and `ListAPIKeys` set `Sensitive`:
  the server returns every certificate's private key and every API key's
  token in these reads.
- `CDNID` and `CertificateID` pass `core.CheckPathID` before any request.

### Analytics

The Range Input of the four series calls has `CDNDomains []string` (r),
`Period`, `From`, and `To`. Exactly one of `Period` or the pair `From` and
`To` is set; anything else is `ErrInvalidInput` before any request.

- `CDNDomains` holds generated `cdnDomain` names; it goes out as
  `domains`. A `domainName` is refused by the server, and an empty list
  gives `Something went wrong`, so the SDK requires at least one entry,
  none empty.
- `Period` must be one of `30m`, `1h`, `3h`, `6h`, `12h`, `24h`, `3d`,
  `7d`, `14d`, `30d`, `90d`, `180d`, or `360d`, checked before any
  request. The server refuses others with `not support period value`.
- `From` and `To` are calendar dates as `YYYY-MM-DD` strings, so the CLI
  can set them as flags. The server reads only `dd/mm/yyyy`; it refuses a
  time of day and ISO 8601. The SDK parses each date and sends it as
  `dd/mm/yyyy`. The series calls read the range in UTC+7
  (Asia/Ho_Chi_Minh, which has no daylight saving, so the SDK needs no
  time zone database): from 00:00 on `From` to 23:59:59.999 on `To`.
  `To` before `From` is `ErrInvalidInput`, since the server answers it
  with an empty list.
- With `Period`, the SDK sends `period` and no dates; with dates, it sends
  `fromTime` and `toTime` and no `period`. The server lets `period` win
  when both are sent.
- All analytics calls are reads that use `POST` (ADR 0002 rule 1), so they
  set `Idempotent` and keep the transport's retries.
- `GetTraffic` and `GetRequestRate` decode `data` as an object from
  epoch-millisecond keys to `{"cached", "uncached"}` floats. `CacheSample`
  has `Time time.Time` (UTC, from `time.UnixMilli`), `Cached`, and
  `Uncached`. `Points` is sorted by `Time`. Points are not evenly spaced:
  with no traffic only the two window edges appear. A key that is not an
  integer fails the call.
- `GetCacheStatus` and `GetHTTPCodes` receive `data` as a JSON string. The
  string `"[]"` (no data) gives empty `Counts`. A string that holds an
  object decodes into `Counts`. Anything else fails the call.
- `GetTrafficReport` takes no `Period`; the server refuses one. Its `data`
  is a list. `DomainTraffic` has `DomainName`, `CDNDomain`, `TrafficType`
  (`Domestic`), `GroupType` (`Standard`), and `Points []Sample`, each with
  `Time` and `Value float64`. The server buckets this report at UTC
  midnight, not UTC+7.
- No call has returned a non-zero value, so the wiki names no unit for any
  value.

The other analytics calls (origin request rate, unique visitors, speed,
average speed, monthly traffic, origin HTTP codes, country traffic) are
deferred; they reuse these shapes.

## Security

- The API key reaches only the `CDN` endpoint, in the `Authorization`
  header. It never reaches argv, stdout, stderr, `--debug`, an error, a
  capture, or a fixture. Tests assert each.
- Any holder of one key can list every key's token and every certificate's
  private key through the API. A key is therefore as strong as the
  account's whole vCDN access; the wiki says so and advises the shortest
  expiry that works. `allowOriginHeader` limits browsers only.
- 401 and 403 messages are fixed text, and any message that holds an at
  sign is withheld, so no account user or email reaches a log.
- Models drop `userUuid`, `customerId`, `userEmail`, and `drmSecretKey`.
  The update body carries `userUuid` back to the server as read; it never
  reaches an Output, an error, or `--debug`.
- Every write gets an adversarial review before its release; the review
  list is in [vCDN writes](cdn-writes.md#security).
