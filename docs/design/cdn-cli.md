# vCDN CLI and Releases

Status: Accepted (2026-10-10).

The CLI commands, tests, live checks, releases, and owner decisions for
[vCDN API](cdn-api.md) and [vCDN writes](cdn-writes.md). The SDK surface,
errors, and write rules are in those designs.

## Commands

`svc_cdn.go` adds these to the table that holds `list-ip-ranges`. Flags
follow the Input fields; list fields (`CDNDomains`, `Upstreams`,
`DefaultRuleActions`, `SetRuleActions`, `RemoveRuleActions`, `CNames`,
`FailOverErrorCodes`, `Paths`) come through `--cli-input-json`. The
shared rename table maps `CDNID` to `--cdn-id`.

| Command | Kind | Needs `--yes` |
|-|-|-|
| `cdn list-web-accelerators`, `get-web-accelerator` | Read | No |
| `cdn list-certificates`, `get-certificate` | Read | No |
| `cdn list-api-keys` | Read | No |
| `cdn get-traffic-report`, `get-traffic`, `get-request-rate`, `get-cache-status`, `get-http-codes` | Read | No |
| `cdn update-web-accelerator` | Write | No |
| `cdn enable-web-accelerator` | Write | No |
| `cdn disable-web-accelerator` | Write | Yes |
| `cdn delete-web-accelerator` | Write, destructive | Yes |
| `cdn purge-paths` | Write | No |
| `cdn import-certificate` | Write | Yes with `--certificate-id` |
| `cdn enable-certificate` | Write | No |
| `cdn disable-certificate` | Write | Yes |
| `cdn delete-certificate` | Write, destructive | Yes |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request. The analytics reads use `POST` and still run.
- A deleted CDN loses its generated `CDNDomain`, which customer DNS points
  at, and a deleted certificate needs its key again (ADR 0002 rule 6).
- Disable is not destructive under rule 6, since enable undoes it, but it
  takes the site off the CDN, so it needs `--yes`, as storage attach does.
  The same holds for `disable-certificate`, which breaks HTTPS on every
  CDN that uses the certificate.
- `import-certificate --certificate-id` replaces a certificate in place on
  every CDN that uses it, which a second command cannot undo without the
  old key, so it needs `--yes`. A new import does not.
- Update, enable, and disable wait for the CDN to settle, up to 6
  minutes; `--no-wait` comes from `NoWait` by the usual flag reflection.
- `enable-*` and `disable-*` print the resource and `Changed`. After
  `StatusUnconfirmed` the command exits 1 and is not retried, as for
  [vMonitor](monitor.md#after-errstatusunconfirmed).
- `get-traffic`, `get-request-rate`, `get-cache-status`, and
  `get-http-codes` take `--period`, or `--from` and `--to` as
  `YYYY-MM-DD` dates in UTC+7. `get-traffic-report` takes `--from` and
  `--to` only.

### Getting a CDN

No command creates a CDN while [create](cdn-writes.md#create) is
deferred. The wiki recipe says: the owner creates the CDN in the vCDN
Portal, then runs `cdn list-web-accelerators` to find its `CDNID` and its
`CDNDomain`, the CNAME target. For a live test the owner names it
`vngcloud-live-<8 hex>.<parent>`, under a parent domain the owner
controls, with one origin and no alternative names.

### update-web-accelerator

The body is too nested for flags, so the usual call is `--cdn-id` with
`--cli-input-json file://changes.json` holding only the changes, such as
`{"SetRuleActions": [{"Name": "browserCache", "Value": "1d"}]}`. Flags
exist for `--certificate-id`, `--lb-type`, `--origin-host-header`, and
`--no-wait`. Action IDs never appear in the file. The command prints the
`WebAccelerator`. The wiki says that no call removes an origin.

### import-certificate

As [vLB certificates](lb-certificates.md): `--certificate-file`,
`--private-key-file`, and `--ca-root-file`; no flag for the key, since
reflection makes none for a `vngcloud.Secret`; `--cli-input-json` that
sets `PrivateKey` is refused with exit 2. Files are read up to 64 KiB.
Stdout gets the matched certificate, which holds no key, or
`{"Certificate": null}`.

### Errors

The CLI codes for `cdn` are in [vCDN writes](cdn-writes.md#errors) and
[vCDN errors](cdn-api.md#errors). [CLI](cli.md#errors-and-exit-codes)
gains `PurgeCooldown` (exit 1); `cdn.ErrBusy` joins `ResourceBusy`, and
`cdn.ErrNotSettled` joins `NotSettled`. `cdn.ErrNoAPIKey` prints
`NoCredentials` and exits 3. A rejected key prints `Unauthorized` and
exits 3.

## Testing

Unit tests use `httptest`, with fixtures in `testdata/cdn/`:

- The request carries `Authorization: Bearer <key>`, no IAM token, and no
  `Origin` header. A 401 is not retried and invalidates no token.
- The key never appears in stdout, stderr, `--debug`, an error, or a
  capture, including when the server echoes it in a message.
- Every row of the envelope table in [vCDN errors](cdn-api.md#errors),
  each against a write and a read: the busy message with code 500 and
  400, `Not found cdn` with a null code, the cooldown message, another
  code 202, a null code, and the at-sign rule.
- Decode tests from sanitized live fixtures: the list item with its null
  fields, the detail with 12 actions (the `hsts` and `minify` values kept
  as text), an upstream, string and integer IDs, the update response,
  and each analytics shape, including only two edge points, the `"[]"`
  counts string, and the report list.
- Analytics inputs: exactly one of `Period` or the dates; each allowed
  period and one refused; a bad date, a time of day, and `To` before
  `From` refused; `YYYY-MM-DD` sent as `dd/mm/yyyy`; empty `CDNDomains`
  refused; sorted points from `time.UnixMilli`; a non-integer key fails.
- Update merge: one action changed keeps its `id`; a new action has no
  `id`; a removed action is absent; unmodeled fields and `status` sent as
  read; upstreams nil, edit by ID, add, and unknown ID; empty Input and a
  merge with no change send nothing; a disabled CDN is refused.
- Status guard: every cell of its table sends nothing where it refuses,
  status 4 included; `StatusName` derived for every constant.
- A 401 on update, delete, and each toggle: one detail read follows;
  `NotFound` gives `ErrNotFound`; a 401 or a success on the read gives
  the 401 error; the write is not resent.
- Settle wait on an injected clock: settled, pending, another status,
  `NotFound`, a read error then settled, the bound, a cancelled context,
  and `NoWait`; errors carry the Output.
- Toggles: the confirm reads at 0, 2, 4, and 8 seconds; a 502 on the
  toggle not resent; `ErrStatusUnconfirmed`.
- Delete: sent once, empty Output, no wait, on status 0 and 1; status
  guard; a 502 not resent.
- Purge: path checks, the cooldown sentinel, code 202 as invalid input.
- Statuses 200, 400, 401, 403, 404, 500, and a 200 envelope failure for
  every write; path rejection for `..`, `/`, `?`, and empty.
- CLI golden tests, `--yes` on each guarded command, `--no-wait`, and
  read-only refusal with no request sent.

Live tests follow [live data](../../instructions/live-data.md). They skip
unless `VNGCLOUD_VCDN_API_KEY` is set, and they log statuses and counts
only, never names, domains, emails, or IDs.

- C1: `make live` runs `ListCertificates`, `ListAPIKeys` (exactly one key
  `Current`), and one call with a wrong key that fails with the 401 text.
- C1b: `make live` adds `ListWebAccelerators`. When a CDN exists, it adds
  `GetWebAccelerator` and the five analytics reads on its `cdnDomain`,
  over `24h` and over yesterday to today; on an empty account it logs
  that they were skipped. The live CLI test runs
  `cdn list-web-accelerators`.

### Live write test

`TestLiveWriteCDN` needs `VNGCLOUD_LIVE_WRITE=1` and
`VNGCLOUD_LIVE_CDN=1`. It takes its CDN from the list: the first whose
`domainName` starts `vngcloud-live-` or `vngcloud-probe-`. With none, it
skips and logs that the owner creates one in the portal
([getting a CDN](#getting-a-cdn)). It leaves every other CDN alone and
ends by deleting the one it took, so each passing run needs a new portal
CDN. A failed step leaves the CDN in place and logs that a leftover CDN
remains, so the next run takes it again and the owner need not make
another. It runs about 20 minutes, including up to 10 minutes for the
delete, under `-timeout 60m`. The CDN serves no traffic, since no DNS
record points at it. Each step asserts statuses and names only:

1. Take the CDN. Read until it is 0 or 1, and enable it when 0. Assert
   type `webacc`, a `CDNDomain`, and `minimumTls`, `nosniff`, and
   `alwaysHttps` among the actions.
2. Reads: the list holds it; the detail matches; the five analytics
   reads on its `cdnDomain` decode.
3. Disable with `NoWait`: status 5; `EnableWebAccelerator` returns
   `ErrBusy`. Poll the detail until 0. Disable again: `Changed: false`.
4. Enable: status 1. Enable again: `Changed: false`.
5. Update `browserCache` to `1d`, or to `1M` when it is `1d`: status 1,
   the value changed, every other action name still there, and every
   `id` kept except `alwaysHttps`. The test account's package refuses
   `developmentMode`.
6. Delete the active CDN: status 4 `DELETING`, and a second delete
   returns `ErrBusy`. Poll the detail every 10 seconds, up to 10 minutes,
   until `NotFound`. A third delete returns `NotFound` from its read.

The C4 test imports a self-signed certificate made in the test, disables,
enables, and deletes it.

## Live checks

Done on 2026-10-10 on CDNs made in the portal and deleted through the
API: the paths, the list and detail shapes, the statuses and their
timings, the toggle, update, delete, and purge answers, a delete of an
active CDN, a toggle and a delete on a deleted CDN, the package limits,
the analytics formats and shapes, `period` alone, and an unknown ID after
a CDN exists. The live write test passed every C2 step.
[vCDN API](cdn-api.md#source) records the results.

The API create probe ran on 2026-10-10 and failed on every body
([paths](cdn-api.md#paths)). C1b fixtures come from one hand capture of
raw list, detail, and analytics bodies on a CDN the owner makes in the
portal, then the CDN is deleted through the API. The capture prints field
names, statuses, and counts only.

Still open: whether a purge on a CDN with traffic removes cached paths.

## Releases

Each release ships the SDK and CLI together, with the `cdn` wiki pages.

| Release | Content |
|-|-|
| C1 | Shipped in v0.56.0. The `CDN` endpoint, `transport.Request.APIKey`, `WithCDNAPIKey`, `VNGCLOUD_VCDN_API_KEY`, the `vcdn_api_key` file key and `configure set vcdn_api_key -`, the envelope and problem+json error rules, `ListCertificates`, `GetCertificate`, and `ListAPIKeys`. Needs no CDN |
| C1b | `ListWebAccelerators`, `GetWebAccelerator`, the models and status constants, the five analytics reads, and the null envelope code. Fixtures come from a CDN made in the portal |
| C2 | `UpdateWebAccelerator`, `DeleteWebAccelerator`, `EnableWebAccelerator`, and `DisableWebAccelerator`; the status guard, settle wait, `ErrBusy`, and `ErrNotSettled`; the busy and `Not found cdn` envelope rows; the live write test on a CDN from the list, without the purge step |
| C3 | `PurgePaths`, `ErrPurgeCooldown`, and the code 202 rows; the purge step joins the live write test |
| C4 | Certificate import, enable, disable, and delete |
| Deferred | `CreateWebAccelerator` and `create-web-accelerator`: portal-only until the API accepts a create; the retest sends a body carrying `userUuid` and `customerId` from the detail of a portal-made CDN ([create](cdn-writes.md#create)) |
| Deferred | `PurgePattern` and `PurgeAll`, Video On Demand, Object Download (its S3 origin holds an access key and secret, so it needs its own secret rules), page rule writes, the other analytics calls, and API key writes. Each waits for a need from aboutme or the owner |

C1b and C2 ship together as v0.60.0, after the CLI half and the review.
C3 and C4 follow as separate releases. C1b to C4 add methods and commands
only. Package limits are per account: the test account's package allows
one origin, no alternative names, and five purges a day, so no test adds
an origin or a name. The C3 live run purges once.

## Owner decisions

1 to 48 are approved; 17 is a gate the owner cleared on 2026-10-10.

1. Approved: the API key resolves on its own, from `WithCDNAPIKey`,
   `VNGCLOUD_VCDN_API_KEY`, or `vcdn_api_key` in the credentials file, with
   the explicit-profile rule; never from a flag.
2. Approved: `LoadConfig` keeps requiring an IAM credential.
3. Approved: `configure set vcdn_api_key -` only; the interactive
   prompts stay as they are.
4. Approved: `transport.Request.APIKey` sends the key as a bearer and
   skips the token flow.
5. Approved: 401 and 403 messages are fixed text.
6. Approved: a 400 matches `ErrInvalidInput` and exits 2.
7. Approved: the names `WebAccelerator` and `CDNID`, so Video On Demand
   and Object Download can join later without a rename.
8. Approved: `UpdateWebAccelerator` reads, merges, and writes the whole
   CDN, since the server deletes rules left out. Decision 39 sets how it
   merges.
9. Approved: no API key writes. A key that mints keys outlives the
   portal's one-year cap. `ListAPIKeys` ships without tokens.
10. Approved, replaced by decision 41 when approved:
    `DefaultRuleActions` is required on create with no SDK default.
11. Approved: `--yes` for delete, disable, `purge-all`, and certificate
    replace; enable and path purge run without it.
12. Approved, replaced by decision 36 when approved: toggles succeed on
    the target or its transitional status.
13. Approved: purge shape checks in the SDK, since the server answers a
    bad shape with a 200.
14. Approved: C1b ships five analytics reads; the rest wait for a need.
15. Approved: `ImportCertificate` finds the new certificate with one
    list and returns nil, not an error, when the match is not unique.
16. Approved: certificate and API key reads set `Sensitive`, and their
    models have no key or token field.
17. Approved and cleared: a create with no traffic adds no charge.
18. Approved: the release order above.
19. Approved and confirmed live: Web Accelerator operations use `cdn/*`.
20. Approved and done: the owner created the first CDN in the portal.
21. Approved: a 2xx envelope with `success: false` is an `*APIError`
    with the envelope code, never retried, and a null or empty message
    becomes fixed text naming the operation.
22. Approved and confirmed live: a detail read whose envelope fails with
    empty `data` maps to `ErrNotFound`.
23. Approved: an HTTP 404 keeps the standard `NotFound` mapping.
24. Approved: any error message that holds `@` is withheld whole.
25. Approved: `transport.errorBody` gains `title` as its last message
    fallback, for problem+json bodies.
26. Approved: `APIKey` drops `token` and `userEmail`, adds
    `AllowOriginHeader`, and parses its times as `time.Time`.
27. Approved and confirmed live: the analytics format is `dd/mm/yyyy`,
    read in UTC+7.
28. Approved: C1 ships the reads that need no CDN; C1b holds the Web
    Accelerator and analytics reads.
29. Approved: the at-sign rule also matches U+FF20 and U+FE6B.
30. Approved: status constants 0 `DISABLED`, 1 `ACTIVE`,
    3 `DEPLOYING`, 4 `DELETING`, 5 `DISABLING`; any other value is
    `UNKNOWN(<n>)` and every write refuses it.
31. Approved: page rules stay `json.RawMessage` and round-trip on
    update; rule action values stay strings, `hsts` and `minify` as JSON
    text; IDs are strings decoded from a JSON string or integer.
32. Approved: analytics `From` and `To` are `YYYY-MM-DD` dates read in
    UTC+7 and sent as `dd/mm/yyyy`; no time of day, since the server
    refuses one.
33. Approved: the SDK checks `Period` against the 13 allowed values
    and sends either `period` or the dates, never both.
34. Approved: the analytics Input field is `CDNDomains`, required and
    non-empty, since the server refuses `domainName` values and an empty
    list.
35. Approved: cache status and HTTP code counts are
    `map[string]float64`, and the `"[]"` string is empty counts.
36. Approved: every write refuses status 3, 4, or 5 with `ErrBusy`,
    sending nothing, since the server refuses writes during a transition
    and 3 does not tell an enable from an update. A toggle on its target
    returns `Changed: false`.
37. Approved: update refuses a disabled CDN until a check shows an
    update deploys it without enabling it.
38. Approved: update, enable, and disable wait, polling the detail
    every 10 seconds for up to 6 minutes; `NoWait` skips it; the bound
    returns `ErrNotSettled` with the Output. The deferred create waits
    the same way when it ships.
39. Approved: update merges rule actions by `actionName` on a fresh
    raw read, through `SetRuleActions` and `RemoveRuleActions`, so callers
    never handle action IDs; an empty Input is `ErrInvalidInput`, and a
    merge with no change sends nothing.
40. Approved: update sends only the given upstreams and the read's
    list when none are given; removing an origin is documented as
    impossible.
41. Approved: an empty `DefaultRuleActions` on create sends
    `minimumTls` `TLS 1.2` and `nosniff` `on`; a non-empty list is sent
    as given. The alternative, sending `[]`, leaves the TLS floor to the
    server.
42. Approved: update, delete, and the toggles set `Once`, since a
    resend after the first landed meets a busy or not-found refusal that
    hides the success.
43. Approved: delete runs on status 0 or 1 without disabling first,
    and does not wait, though an active CDN stays 4 `DELETING` for about
    5 minutes.
44. Approved: the busy message wraps `ErrBusy`, `Not found cdn` maps
    to `NotFound`, code 202 matches `ErrInvalidInput`, the purge cooldown
    wraps `ErrPurgeCooldown`, and a null code is `EnvelopeError`.
45. Approved: C3 ships `PurgePaths` only; `PurgePattern` and
    `PurgeAll` wait for a check on a CDN with traffic, since the server
    accepts any purge `type`.
46. Approved: `CreateWebAccelerator` is deferred, since the API
    refuses every create; the owner makes each test CDN in the portal.
    C1b fixtures come from a hand capture on such a CDN. One live write
    test takes the first `vngcloud-live-` or `vngcloud-probe-` CDN from
    the list, skips when none exists, covers the C1b reads and every C2
    write, and ends by deleting it. A failed step leaves the CDN for
    the next run.
47. Approved: after a 401 on update, delete, or a toggle, the SDK
    reads the CDN once and returns `ErrNotFound` when it is gone,
    otherwise the 401 error, since a toggle on a gone CDN answers the
    same 401 as a rejected key.
48. Approved: each resource-returning Web Accelerator write Output holds
    the CDN in a field named `WebAccelerator`, as `GetCertificateOutput`
    holds `Certificate`; the toggles add `Changed`. Delete returns an
    empty Output.

## Open items

- Why `POST cdn/create` fails past validation; the
  [retest](cdn-writes.md#create) adds `userUuid` and `customerId`.
- Analytics units: every value seen is zero.
- What an update of a disabled CDN does to its status.
- The certificate item shape stays the reference's until a certificate
  exists; C4's import gives the first one.
- The page rule docs describe `headerOverride` as a header sent to the
  browser. No documented action adds a request header toward the origin,
  so an origin-locking header may stay a console step, or impossible.
