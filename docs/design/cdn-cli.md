# vCDN CLI and Releases

Status: Accepted.

The CLI commands, tests, live checks, releases, and owner decisions for
[vCDN API](cdn-api.md). The SDK surface, errors, and write rules are in
that design.

## Commands

`svc_cdn.go` adds these to the table that holds `list-ip-ranges`. Flags
follow the Input fields; list fields (`Domains`, `Upstreams`,
`DefaultRuleActions`, `PageRules`, `CNames`, `Paths`) come through
`--cli-input-json`. The shared rename table gains `CDNID` as `--cdn-id`.

| Command | Kind | Needs `--yes` |
|-|-|-|
| `cdn list-web-accelerators`, `get-web-accelerator` | Read | No |
| `cdn list-certificates`, `get-certificate` | Read | No |
| `cdn list-api-keys` | Read | No |
| `cdn get-traffic-report`, `get-traffic`, `get-request-rate`, `get-cache-status`, `get-http-codes` | Read | No |
| `cdn create-web-accelerator`, `update-web-accelerator` | Write | No |
| `cdn enable-web-accelerator` | Write | No |
| `cdn disable-web-accelerator` | Write | Yes |
| `cdn delete-web-accelerator` | Write, destructive | Yes |
| `cdn purge-paths`, `purge-pattern` | Write | No |
| `cdn purge-all` | Write | Yes |
| `cdn import-certificate` | Write | Yes with `--certificate-id` |
| `cdn enable-certificate` | Write | No |
| `cdn disable-certificate` | Write | Yes |
| `cdn delete-certificate` | Write, destructive | Yes |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request. The analytics reads use `POST` and still run.
- A deleted CDN loses its generated `CDNDomain`, which customer DNS points
  at, and a deleted certificate needs its key again (ADR 0002 rule 6).
- Disable is not destructive under rule 6, since enable undoes it, but it
  takes the site off the CDN at once, so it needs `--yes`, as storage
  attach does. The same holds for `disable-certificate`, which breaks HTTPS
  on every CDN that uses the certificate.
- `purge-all` sends every request for the domain to the origin until the
  cache refills, so it needs `--yes`. Path and pattern purges do not.
- `import-certificate --certificate-id` replaces a certificate in place on
  every CDN that uses it, which a second command cannot undo without the
  old key, so it needs `--yes`. A new import does not.
- `enable-*` and `disable-*` print the resource and `Changed`. After
  `StatusUnconfirmed` the command exits 1 and is not retried, as for
  [vMonitor](monitor.md#after-errstatusunconfirmed).
- `get-traffic`, `get-request-rate`, `get-cache-status`, and
  `get-http-codes` take `--period`, or `--from` and `--to` in RFC 3339.
  `get-traffic-report` takes `--from` and `--to`.

### create-web-accelerator

The body is too nested for flags, so the usual call is
`--cli-input-json file://web-accelerator.json`. Flags exist for
`--domain-name`, `--certificate-id`, `--lb-type`, and
`--origin-host-header`. The wiki gives a JSON template: one HTTP origin,
`minimumTls` `TLS 1.2`, `alwaysHttps` `on`, and the cache defaults from the
reference. The command prints the `WebAccelerator`, whose `CDNDomain` is
the CNAME target.

`update-web-accelerator` takes the same file shape with `CDNID`; the usual
flow is `get-web-accelerator`, edit the JSON, and send it back, keeping the
`ID` of every action and rule to keep.

### import-certificate

As [vLB certificates](lb-certificates.md): `--certificate-file`,
`--private-key-file`, and `--ca-root-file`; no flag for the key, since
reflection makes none for a `vngcloud.Secret`; `--cli-input-json` that
sets `PrivateKey` is refused with exit 2. Files are read up to 64 KiB.
Stdout gets the matched certificate, which holds no key, or
`{"Certificate": null}`.

### Errors

The CLI codes list in [CLI](cli.md#errors-and-exit-codes) gains, for
`cdn`: `UnexpectedStatus` (a CDN or certificate status the SDK does not
toggle from, nothing sent) and `StatusUnconfirmed`, both exit 1, mapped
from `cdn.ErrUnexpectedStatus` and `cdn.ErrStatusUnconfirmed`.
`cdn.ErrNoAPIKey` prints `NoCredentials` and exits 3. A rejected key
prints `Unauthorized` and exits 3. A 400 prints `BadRequest` and exits 2.
A detail read of an unknown ID prints `NotFound` and exits 4. An envelope
failure prints its envelope code, usually `500`, and exits 1
([vCDN errors](cdn-api.md#errors)).

## Testing

Unit tests use `httptest`, with fixtures in `testdata/cdn/`:

- The request carries `Authorization: Bearer <key>`, no IAM token, and no
  `Origin` header; the credentials provider fails the test if called. A
  401 is not retried and invalidates no token.
- Key resolution: option, environment, and file in order; the explicit
  profile skips the environment; a bad key is refused naming the source; no
  key gives `ErrNoAPIKey` and no request; `ListIPRanges` still works
  without a key.
- The key never appears in stdout, stderr, `--debug`, an error, or a
  capture, including when the server echoes it in a 400 message.
- Error bodies from the [errors table](cdn-api.md#errors): a 401 with an
  empty body (fixed message), a 403 (fixed message), 400, 404, and 405
  problem+json (`detail`, then `title`), a 500, a 200 envelope with
  `success: false` and `code` 500 and each of a null, empty, and text
  `message`, a text message holding each at sign, `@`, U+FF20, and
  U+FE6B (withheld, no email in the output), an envelope message and code
  echoing the key (redacted), a detail read with empty `data`
  (`ErrNotFound`), and a non-JSON 200.
- Decode tests for every read, from live shapes sanitized per
  [live data](../../instructions/live-data.md), or from the reference
  examples until a live shape exists. The certificate fixture holds a fake
  `privateKey`; the test asserts it reaches no model field, capture, or
  error. The same for API key tokens and `userEmail`.
- `ListAPIKeys` from the envelope list: `Current` set for the matching key
  only, the RFC 3339 times with milliseconds and `+00:00`, and a bare
  object or bare list refused.
- Analytics: the Range rules; the UTC+7 conversion in the confirmed
  format; sorted points; a non-integer key fails; the string-encoded
  counts.
- Create bodies with defaults and with every field; update merge keeps
  IDs and `status`, sends nothing for an empty update; the purge shape
  checks; certificate PEM checks.
- Toggles: already at `T` or `P` (no request), unexpected status, toggle
  sent once after a 502, a 409 returned at once, an envelope failure
  returned at once, confirm reads on an injected clock reaching
  `ErrStatusUnconfirmed`.
- Statuses 200, 400, 401, 403, 404, 500, and a 200 envelope failure for
  every write; no create or purge retry after a 502 or an envelope
  failure; path rejection for `..`, `/`, `?`, and empty.
- CLI golden tests, `--yes` on each guarded command, and read-only refusal
  with no request sent.

Live tests follow [live data](../../instructions/live-data.md). They skip
unless `VNGCLOUD_VCDN_API_KEY` is set, and they log statuses and counts
only, never names, domains, emails, or IDs.

- C1: `make live` adds `ListCertificates` and `ListAPIKeys`, asserting
  exactly one key has `Current: true`, and one call with a wrong key that
  must fail with the fixed 401 message.
- C1b: `make live` adds `ListWebAccelerators`. When a CDN exists, it adds
  `GetWebAccelerator` and the five analytics reads over `24h` on the
  first CDN; on an empty account it logs that they were skipped. The live
  CLI test runs `cdn list-web-accelerators`.
- The C2 write test needs `VNGCLOUD_LIVE_CDN=1` and
  `VNGCLOUD_LIVE_CDN_DOMAIN`, a parent domain the owner controls, never
  logged. It deletes leftover `vngcloud-live-` CDNs, creates
  `vngcloud-live-<8 hex>.<parent>` with a documentation-range origin,
  updates browser cache, disables, enables, and deletes it, with
  `t.Cleanup` deleting what it made. No DNS record points at the CDN, so
  it serves no traffic.
- The C3 test purges one path and one pattern on the C2 test CDN. The C4
  test imports a self-signed certificate made in the test, disables,
  enables, and deletes it.

## Live checks

Done, read-only, on an account with no CDN: the base URL, the 401 body,
the CORS check, the live prefixes, the empty list and envelope failure
shapes, and the `apikey/list` fields. [vCDN API](cdn-api.md#source)
records the results.

Before any create, by the owner in the portal:

1. Which Accelerator Package and payment mode the account has, its price,
   and how many CDNs it allows. Whether creating a CDN with no traffic adds
   any charge. If a create costs money, no create happens until a price
   check design under ADR 0002 rule 8.
2. Whether the account must own the domain, and whether the package's CDN
   limit leaves a slot for the live test beside aboutme's CDN.

Before C1b code, one real CDN must exist: the owner creates it in the
portal after check 1, or runs one create by hand on `POST cdn/create` with
the reference body. Then, by hand, read-only, printing field names,
statuses, and counts only:

3. `GET cdn/list`: the CDN appears; the item's field names match
   `WebAcceleratorSummary`.
4. `GET cdn/detail/{cdnId}`: the field names match `WebAccelerator` and
   its nested models, and the status is in the status table.
5. `GET cdn/detail/{id}` with a well-formed unknown ID: still
   `success: false` with empty `data`, now that the account has a CDN.
6. `GET certificate/list`: whether the CDN's `default` certificate is
   listed, and its field names if so.
7. `POST analytic/traffic-report` with the CDN's `cdnDomain` and a range
   over the last day, once with `fromTime` and `toTime` as
   `dd/mm/yyyy hh:mm` and once as `dd/mm/yyyy`: which form the server
   accepts, and, from the returned epoch keys, which time zone it reads.
8. `POST analytic/traffic-consuming` with `period` `24h`, then `5m`: the
   series shape, and how the server refuses a bad `period`.
9. The same series call with `domainName` in place of `cdnDomain`.
10. `cdn-requestsps`, `cache-status`, and `cdn-http-codes` with `period`
    `24h`: the series shape and the string-encoded counts.

During C2 to C4, recorded in the API facts: the `type` value the `cdn`
prefix expects on create, the status after create, the transitional
statuses and how long toggles take, a repeat delete, a duplicate create, a
documentation-range origin, purge answers, and a certificate in use on
delete.

Cost: C1 and C1b are reads and free; C1b needs the CDN. The CDN is free
only if check 1 says so; traffic stays zero. C3 purges use two of the daily
purges. C4 certificates are free (50 per package).

## Releases

Each release ships the SDK and CLI together, with the `cdn` wiki pages.

| Release | Content |
|-|-|
| C1 | Shipped in v0.56.0. The `CDN` endpoint, `transport.Request.APIKey`, `WithCDNAPIKey`, `VNGCLOUD_VCDN_API_KEY`, the `vcdn_api_key` file key and `configure set vcdn_api_key -`, the envelope and problem+json error rules, `ListCertificates`, `GetCertificate`, and `ListAPIKeys`. Needs no CDN |
| C1b | `ListWebAccelerators`, `GetWebAccelerator`, and the five analytics reads. Ships after checks 3 to 10 pass on a real CDN |
| C2 | Web Accelerator create, update, delete, enable, and disable |
| C3 | Cache purge: paths, pattern, and all |
| C4 | Certificate import, enable, disable, and delete |
| Deferred | Video On Demand, Object Download (its S3 origin holds an access key and secret, so it needs its own secret rules), the other analytics calls, and API key writes. Each waits for a need from aboutme or the owner |

C1 changes no existing method or command; `cdn list-ip-ranges` is
unchanged. C1b to C4 add methods and commands only. C3 needs C1b's reads
but not C2: it works on a CDN made in the portal, so it can ship before C2
if aboutme needs purge sooner.

## Owner decisions

1 to 29 are approved; 17 is a gate the owner clears in the portal before any
create.

1. Approved: the API key resolves on its own, from `WithCDNAPIKey`,
   `VNGCLOUD_VCDN_API_KEY`, or `vcdn_api_key` in the credentials file, with
   the explicit-profile rule; never from a flag.
2. Approved: `LoadConfig` keeps requiring an IAM credential. The
   alternative, a profile with only a vCDN key, changes when
   `ErrNoCredentials` fires for every command.
3. Approved: `configure set vcdn_api_key -` only; the interactive
   prompts stay as they are.
4. Approved: `transport.Request.APIKey` sends the key as a bearer and
   skips the token flow. The alternative, a second credentials provider,
   would mix two credentials in one token cache.
5. Approved: 401 and 403 messages are fixed text. A 401 has no body; a
   403 may name the account user.
6. Approved: a 400 matches `ErrInvalidInput` and exits 2.
7. Approved: the names `WebAccelerator` and `CDNID`, so Video On Demand
   and Object Download can join later without a rename.
8. Approved: `UpdateWebAccelerator` reads, merges non-nil fields, and
   writes the whole CDN, since the server deletes rules left out.
9. Approved: no API key writes. A key that mints keys outlives the
   portal's one-year cap, and the portal is the only place keys should be
   made. `ListAPIKeys` ships without tokens.
10. Approved: `DefaultRuleActions` is required on create with no SDK
    default; the wiki template sets `TLS 1.2`.
11. Approved: `--yes` for delete, disable, `purge-all`, and certificate
    replace; enable, path purge, and pattern purge run without it.
12. Approved: toggles succeed on the target or its transitional status,
    with confirm reads at 0, 2, 4, and 8 seconds.
13. Approved: purge shape checks in the SDK, since the server answers a
    bad shape with a 200.
14. Approved: C1b ships five analytics reads; the rest wait for a need.
15. Approved: `ImportCertificate` finds the new certificate with one
    list and returns nil, not an error, when the match is not unique.
16. Approved: certificate and API key reads set `Sensitive`, and their
    models have no key or token field.
17. Approved: no create, by hand or by C2, until check 1 shows a create
    adds no charge.
18. Approved: the release order above, with purge free to move ahead of
    C2.
19. Approved: Web Accelerator operations use `cdn/*` paths, inferred
    from the live prefix and the reference bodies, and the Web Accelerator
    reads ship only after checks 3 to 5 confirm them on a real CDN.
20. Approved: the owner creates the one real CDN in the portal after
    check 1, since a hand-run create depends on an unconfirmed body.
21. Approved: a 2xx envelope with `success: false` is an `*APIError`
    with the envelope code, never retried, and a null or empty message
    becomes fixed text naming the operation.
22. Approved: a detail read whose envelope fails with empty `data`
    maps to `ErrNotFound`, an inference that check 5 rechecks.
23. Approved: an HTTP 404 keeps the standard `NotFound` mapping, even
    though it means an unknown route. The alternative, a separate
    `RouteNotFound` code, needs a change to `IsNotFound`, which matches
    any 404 status; the live tests catch a wrong route instead.
24. Approved: any error message that holds `@` is withheld whole and
    replaced by fixed text, rather than scrubbing the email, since a
    partial scrub can miss a form of it.
25. Approved: `transport.errorBody` gains `title` as its last message
    fallback, for problem+json bodies.
26. Approved: `APIKey` drops `token` and `userEmail`, adds
    `AllowOriginHeader`, parses its times as `time.Time`, and
    `ListAPIKeys` accepts only the envelope list.
27. Approved: the analytics reads ship only after check 7 confirms the
    `fromTime` and `toTime` format and time zone on a real domain.
28. Approved: C1 ships the reads that need no CDN, and C1b holds
    the Web Accelerator and analytics reads, so a release never waits on
    an owner-side step it does not need.
29. Approved: the at-sign rule in decision 24 also matches the
    fullwidth (U+FF20) and small (U+FE6B) at signs, a cheap superset of
    the ASCII byte. Other spellings, such as `user at example.com`, are
    out of scope, since the server's messages seen so far are ASCII. The
    alternative keeps the ASCII byte only and states that assumption.

## Open items

- The reference marks `fromTime`, `toTime`, and `period` all required,
  yet says `period` applies only without the pair. The SDK sends one form.
- Analytics units: `traffic-consuming` says its values follow
  `RequestSpsDto` (requests per second), while its name says traffic.
  Check 8 settles it before the wiki names a unit.
- Analytics on an unknown domain fails with envelope `code` 500, so it
  matches no sentinel and exits 1, though it is an input error.
- The certificate item shape stays the reference's until a certificate
  exists; C4's import gives the first one.
- The API key create body takes a `token`; the reference does not say
  whether the server or the caller makes it.
- The page rule docs describe `headerOverride` as a header sent to the
  browser. No documented action adds a request header toward the origin,
  so an origin-locking header may stay a console step, or impossible.
- Whether status changes and deletes are refused while a CDN is
  `DEPLOYING` or `DELETING`.
