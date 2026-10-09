# vCDN CLI and Releases

Status: Proposed.

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

## Testing

Unit tests use `httptest`, with fixtures in `testdata/cdn/`:

- The request carries `Authorization: Bearer <key>` and no IAM token; the
  credentials provider fails the test if called. A 401 is not retried and
  invalidates no token.
- Key resolution: option, environment, and file in order; the explicit
  profile skips the environment; a bad key is refused naming the source; no
  key gives `ErrNoAPIKey` and no request; `ListIPRanges` still works
  without a key.
- The key never appears in stdout, stderr, `--debug`, an error, or a
  capture, including when the server echoes it in a 400 message.
- Error bodies from the reference table: 400, 401 (message replaced, no
  IP in the output), 403 (message replaced), 500, a 200 with
  `success: false` and code 202, and a non-JSON 200.
- Decode tests for every read, from the reference examples sanitized per
  [live data](../../instructions/live-data.md), then from live shapes.
  The certificate fixture holds a fake `privateKey`; the test asserts it
  reaches no model field, capture, or error. The same for API key tokens.
- `ListAPIKeys` with an array, a single object, the envelope, and bare;
  `Current` set for the matching key only.
- Analytics: the Range rules; the UTC+7 conversion; sorted points; a
  non-integer key fails; the string-encoded counts.
- Create bodies with defaults and with every field; update merge keeps
  IDs and `status`, sends nothing for an empty update; the purge shape
  checks; certificate PEM checks.
- Toggles: already at `T` or `P` (no request), unexpected status, toggle
  sent once after a 502, a 409 returned at once, confirm reads on an
  injected clock reaching `ErrStatusUnconfirmed`.
- Statuses 200, 400, 401, 403, 404, 500 for every write; no create or
  purge retry after a 502; path rejection for `..`, `/`, `?`, and empty.
- CLI golden tests, `--yes` on each guarded command, and read-only refusal
  with no request sent.

Live tests follow [live data](../../instructions/live-data.md). They skip
unless `VNGCLOUD_VCDN_API_KEY` is set, and they log statuses and counts
only, never names, domains, or IDs.

- `make live` adds `ListWebAccelerators`, `ListCertificates`, and
  `ListAPIKeys`, asserting exactly one key has `Current: true`. When a CDN
  exists, it adds `GetWebAccelerator` and the five analytics reads over
  `24h` on the first CDN; on an empty account it logs that they were
  skipped. The live CLI test runs `cdn list-web-accelerators`.
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

Before C1 code, each by hand, read-only, on the owner's account:

1. The owner creates an API key in the vCDN Portal, with the shortest
   expiry that covers the work and no domain restriction, and puts it in
   `.env` as `VNGCLOUD_VCDN_API_KEY`. Nothing prints it.
2. Whether `vcdn-api.vngcloud.vn` answers directly or redirects to a
   GreenNode host, so the default endpoint is the final host.
3. A wrong key: the 401 body shape, and whether it holds the caller's IP.
4. On the empty account: what `webacc/list`, `certificate/list`, and an
   analytics call with an unknown domain answer (`[]`, null, or an error).
5. `apikey/list`: a list or one object, envelope or bare, and which fields
   it returns. The probe prints field names only.
6. What the key's domain restriction limits: the `Origin` header, the
   caller's IP, or the CDN domains it may manage.

Before C2 code, by the owner in the portal:

7. Which Accelerator Package and payment mode the account has, its price,
   and how many CDNs it allows. Whether creating a CDN with no traffic adds
   any charge. If a create costs money, C2 waits for a price check design
   under ADR 0002 rule 8.
8. Whether the account must own the domain, and whether the package's CDN
   limit leaves a slot for the live test beside aboutme's CDN.

During C2 to C4, recorded in the API facts: the status after create, the
transitional statuses and how long toggles take, a repeat delete, a
duplicate create, a documentation-range origin, the analytics time zone
and domain form, purge answers, and a certificate in use on delete.

Cost: C1 is reads, free. C2 is free only if check 7 says so; traffic stays
zero. C3 purges use two of the daily purges. C4 certificates are free (50
per package).

## Releases

Each release ships the SDK and CLI together, with the `cdn` wiki pages.

| Release | Content |
|-|-|
| C1 | The `CDN` endpoint, `transport.Request.APIKey`, `WithCDNAPIKey`, `VNGCLOUD_VCDN_API_KEY`, the `vcdn_api_key` file and `configure` key, envelope errors, and the reads: Web Accelerators, certificates, API keys, and five analytics calls |
| C2 | Web Accelerator create, update, delete, enable, and disable |
| C3 | Cache purge: paths, pattern, and all |
| C4 | Certificate import, enable, disable, and delete |
| Deferred | Video On Demand, Object Download (its S3 origin holds an access key and secret, so it needs its own secret rules), the other analytics calls, and API key writes. Each waits for a need from aboutme or the owner |

C1 changes no existing method or command; `cdn list-ip-ranges` is
unchanged. C2 to C4 add commands only. C3 does not need C2: it works on a
CDN made in the portal, so it can ship first if aboutme needs purge
sooner.

## Owner decisions

All 18 are approved as recommended; 17 is a gate the owner clears in the
portal before C2.

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
5. Approved: replace the 401 and 403 messages with fixed text, since
   they hold the caller's IP and the account user.
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
14. Approved: C1 ships five analytics reads; the rest wait for a need.
15. Approved: `ImportCertificate` finds the new certificate with one
    list and returns nil, not an error, when the match is not unique.
16. Approved: certificate and API key reads set `Sensitive`, and their
    models have no key or token field.
17. Approved: C2 ships only after check 7 shows a create adds no
    charge.
18. Approved: the release order above, with purge free to move ahead of
    C2.

## Open items

- The reference marks `fromTime`, `toTime`, and `period` all required,
  yet says `period` applies only without the pair. The SDK sends one form.
- Analytics units: `traffic-consuming` says its values follow
  `RequestSpsDto` (requests per second), while its name says traffic. The
  live check settles it before the wiki names a unit.
- The API key create body takes a `token`; the reference does not say
  whether the server or the caller makes it.
- The page rule docs describe `headerOverride` as a header sent to the
  browser. No documented action adds a request header toward the origin,
  so an origin-locking header may stay a console step, or impossible.
- Whether status changes and deletes are refused while a CDN is
  `DEPLOYING` or `DELETING`.
