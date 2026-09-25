# CDN Design

Status: Approved (2026-09-26).

This design adds one read: the list of GreenNode CDN IP ranges that an origin
must allow. aboutme's deploy reads the list on every run and stops when it
changes. vCDN management stays out of scope, because vCDN has no API the SDK
can call.

It builds on [SDK and CLI](sdk-and-cli.md): package per service,
`Method(ctx, *Input) (*Output, error)`, operation tables, and the error
model.

## Source

vCDN has no public API; the API reference at `docs.api.greennode.ai` lists
none. The CDN IP ranges are published only on the public FAQ page
`https://docs.greennode.ai/faq/vcdn`, in the section whose heading reads
"[vCDN] I need support to provide GreenNode's CDN IP range". The old
`docs.vngcloud.vn` FAQ URL redirects there. The page states no update
cadence.

Facts about the page that shape the parser (checked 2026-09-26):

- It is a server-rendered HTML page of about 900 KB, served as
  `text/html; charset=utf-8`.
- The heading is an `<h3>` whose text holds HTML entities (`&quot;`,
  `&#x27;`). The next `<h3>` starts the following FAQ entry.
- The heading text appears four times: in the `<h3>`, in a table-of-contents
  link, and twice inside `<script>` payloads.
- The section lists CIDRs as plain text, separated by spaces and one `;`.
  It holds 21 CIDRs, 19 of them unique, all IPv4, including a `/32`.

## Goals

- Return the current CDN IP ranges as sorted, unique, validated CIDRs.
- Fail loudly, never with an empty or partial list, when the page no longer
  looks like the page this parser was written for.
- Let a caller tell "the page changed shape" apart from "the fetch failed".

## Non-goals

- vCDN management: cache rules, origin headers, certificates, and the rest of
  the console. The console at `https://vcdn.greennode.ai/` redirects on the
  server to `https://sso.greennode.ai/cas/login`, a root-account form with a
  Google reCAPTCHA and no IAM User option. Root login is already a
  [non-goal](sdk-and-cli.md#non-goals). aboutme keeps these as console steps.
  A GreenNode support request for a vCDN API, or IAM User access to the
  console, would reopen this.
- Watching the list or diffing it against a saved copy. aboutme owns its
  diff.
- IP ranges of other GreenNode services.

## Decisions

| Topic | Decision |
|-|-|
| Package | New public package `cdn` |
| Data source | The public FAQ page, parsed with the standard library |
| Output type | `Items []string` of canonical CIDRs, not `[]netip.Prefix` |
| Auth | None: the request carries no token and no cookie |
| Strictness | Any doubt about the page fails the call with `cdn.ErrPageFormat` |
| CLI config | The command loads its `Config` like every other command |

## SDK

### Package

```go
package cdn

func New(cfg vngcloud.Config) *Client

func (c *Client) ListIPRanges(ctx context.Context, in *ListIPRangesInput) (*ListIPRangesOutput, error)

type ListIPRangesInput struct{}

type ListIPRangesOutput struct {
	Items  []string // canonical CIDRs, sorted by address, then prefix length
	Source string   // the page URL fetched
}

var ErrPageFormat = errors.New("cdn: IP range page format not recognized")
```

A nil Input is valid. `ListIPRangesInput` has no fields today, so later
fields can be added without breaking callers.

`Items` holds strings, not `netip.Prefix`, for three reasons:

- The CLI encoder writes struct fields by reflection and treats only
  `time.Time` as a leaf, so a `netip.Prefix` would encode as `{}`. Strings
  need no CLI change.
- Other SDK models carry addresses as strings.
- aboutme compares the list as text. Canonical strings make that comparison
  exact.

Every item comes from `netip.Prefix.String()` on a validated prefix, so
`netip.ParsePrefix` never fails on it. A Go caller that needs prefixes parses
them.

### Endpoint

`endpoints.Set` gains `CDNDocs`, default `https://docs.greennode.ai/faq/vcdn`,
and `EndpointOverrides` gains a matching `CDNDocs` override. `CDNDocs` is the
full page URL, not a base URL. `internal/routes` gains `ProductCDNDocs`, as
[billing](billing.md#endpoints) did for `Billing`. The region is ignored, and
no project ID is sent. `Config` still requires a region, because the rest of
the SDK needs one.

The default must be the `greennode.ai` URL: the old `docs.vngcloud.vn` URL
redirects to another host, which the SDK refuses.

### Request

`ListIPRanges` sends one `GET` to `CDNDocs` through the shared transport:

- `SkipAuth` is set, so the transport never asks the credentials provider for
  a token and never sets `Authorization`. A `Config` built by `NewConfig`
  with a region and no credentials works.
- The request uses a copy of the `Config`'s `*http.Client` with `Jar` set to
  nil, so a caller-supplied client with a cookie jar sends no cookie. The
  copy keeps the SDK redirect rule: same host only, at most 10 hops. When the
  caller supplied its own client, the copy checks the same-host rule first,
  then calls the caller's `CheckRedirect`, if any.
- `Accept: text/html`, and the configured `User-Agent`. No other header.
- The transport's retry policy applies unchanged: a `GET` is idempotent, so
  network errors, 429, and 5xx are retried.
- The transport reads at most 4 MiB of the decoded body (`MaxBody` on
  `transport.Request`, read as limit plus one byte). A larger body fails the
  call. The limit also bounds a compressed response, because Go decodes gzip
  before the limit applies. The page is about 900 KB today.
- `--debug` logging records the request like any other: method, path, status,
  duration.

`transport.Request` gains `MaxBody`, and the transport gains a raw-body call
that returns the status, the `Content-Type`, and the body without decoding
JSON. `internal/core` wraps it for service packages. API calls keep today's
behavior.

### Parsing

Parsing uses `strings`, `html` (for `html.UnescapeString`), `mime`, and
`net/netip`. It needs no HTML parser, so the SDK stays standard-library
only.

1. Check the response. The final status must be 200 and the `Content-Type`
   media type must be `text/html`.
2. Remove HTML comments whole, then the contents of `<script>`, `<style>`,
   `<noscript>`, `<template>`, and `<svg>` elements, so payload copies of the
   page do not count. A tag ends at the first `>` outside quotes.
3. Find every `<h1>` to `<h6>` element. For each, take its text: replace
   each tag with a space, decode entities, collapse whitespace. A heading
   matches when its text contains `CDN IP range`, ignoring case. Exactly one
   heading must match. The table-of-contents link is an `<a>`, not a
   heading, so it never matches.
4. The section runs from the end of the matched heading to the next heading
   open tag of the same or a higher level (an `<h3>` section ends at the next
   `<h1>`, `<h2>`, or `<h3>`). No following heading is an error, so a truncated
   page cannot pass.
5. Turn the section into text as in step 3, then split it into tokens on
   every character that is not an ASCII letter, digit, `.`, `:`, or `/`.
   Trim trailing `.` and `:` from each token, so a CIDR that ends a sentence
   still counts.
6. Classify each token:
   - IPv4-shaped: four dot-separated digit groups, with or without `/n`.
   - IPv6-shaped: contains `/` and at least two `:`.
   - A token that holds four dot-separated digit groups anywhere but is not
     IPv4-shaped as a whole (for example `HN1.2.3.0/24`) fails the call, so
     a CIDR glued to other text never disappears silently.
   - Anything else is ignored, which skips prose such as "Here is a list".
7. Every shaped token must parse with `netip.ParsePrefix` and equal its own
   `Masked()` form. A bare address, a bad length, or set host bits fails the
   call. The parser never guesses what the page meant.
8. Remove duplicates, sort by address then prefix length, and format each
   with `Prefix.String()`. At least one CIDR must remain.

### Errors

| Case | Error |
|-|-|
| Network failure or cancelled context | As for every call |
| Final status other than 200 | `*vngcloud.APIError` |
| `Content-Type` not `text/html` | `ErrPageFormat` |
| Body over 4 MiB with status 200 | `ErrPageFormat` (any other status is an `*APIError`) |
| No matching heading, or more than one | `ErrPageFormat` |
| No heading after the section | `ErrPageFormat` |
| A shaped token that is not a canonical CIDR | `ErrPageFormat` |
| No CIDR in the section | `ErrPageFormat` |

The `*APIError` has operation `cdn.ListIPRanges`, the status, and the code
from the [status table](sdk-and-cli.md#errors). It wraps the status sentinel
(`ErrNotFound`, `ErrPermission`, `ErrRateLimited`) as other SDK errors do,
and `Retryable` is true for 429, 502, 503, and 504. A 401 or 403 from the
docs host does not match `ErrAuth`, because the request carried no
credential.

Every `ErrPageFormat` error wraps the sentinel and names the reason, for
example `cdn: IP range page format not recognized: heading "CDN IP range"
not found`. For a bad token it quotes the token, cut to 64 bytes with control
characters removed. It never includes the page body.

`ErrPageFormat` means "the page changed and the parser needs review". Every
other error means "the list could not be read this time". A caller that
retries should retry only the second kind.

## CLI

`svc_cdn.go` registers one read:

```go
cli.Read("list-ip-ranges", (*cdn.Client).ListIPRanges)
```

`vngcloud cdn list-ip-ranges` takes no operation flags. It is a read, so
read-only profiles allow it.

- JSON output is `{"Items": ["14.225.2.32/28", ...], "Source": "..."}`, with
  `Items` as CIDR strings.
- `--output text` prints one CIDR per line, because the text renderer prints
  one row per `Items` element.
- `--output table` prints one `Value` column.

The command builds its `Config` with `LoadConfig`, like every service
command, so it needs a region and credentials even though it sends neither.
aboutme's deploy already holds credentials for vMonitor. A credential-free
path would be the first special case in the operation table; it can come
later if a caller needs it.

An `ErrPageFormat` error prints as the JSON error with code `PageFormat` and
exits 1. [CLI](cli.md#errors-and-exit-codes) lists the other codes. A non-200
status prints as any `*APIError` does; a 404 exits 4.

## Security

- No token, `Authorization` header, or cookie reaches the docs host. Tests
  check this with a credentials provider that fails the test if called.
- TLS verification stays on, and redirects stay on one host, as for every
  SDK request.
- The 4 MiB body limit bounds memory for a hostile or broken response.
- Error messages quote at most one 64-byte token from a public page.
- The page is public, so the call reads no account data. It still sends the
  configured `User-Agent` to a third-party docs host; it sends nothing else
  that identifies the caller.

The page is an unsigned web page, not an API. A wrong list there would reach
aboutme's origin allowlist. aboutme's stop-on-change check is the control for
that: a person reviews every change before a deploy continues.

## Compatibility

The release adds a package, an endpoint field, and a transport option. No
existing method, field, or CLI command changes.

## Testing

- `testdata/cdn/faq-vcdn.html` holds the live page captured with a plain
  `GET`. The ranges are public documentation, not account data, so the
  fixture keeps them unchanged. The page may be trimmed to reduce size, but
  the trimmed file must keep the matched `<h3>` section, the following
  `<h3>`, the table-of-contents link, and one `<script>` that repeats the
  heading and the CIDRs.
- A fixture test checks the exact 19 CIDRs in order, with the duplicates and
  the `;` separator handled.
- Table tests on small inline pages cover: the heading missing, the heading
  matched twice, no heading after the section, a section with no CIDR, a bare
  address, an invalid length, host bits set, an IPv6 CIDR, a non-HTML
  `Content-Type`, a heading found only inside `<script>`, and entity-encoded
  heading text.
- `httptest` tests cover a 404 and a 500 (an `*APIError`, not
  `ErrPageFormat`), a body over 4 MiB, a same-host redirect that succeeds, and
  a cross-host redirect that fails.
- A test builds the `Config` with a credentials provider that fails the test
  if called, and a caller-supplied client whose cookie jar holds a cookie for
  the test host. The server fails the test if it sees `Authorization` or
  `Cookie`.
- CLI golden tests cover `json`, `table`, and `text` output, and the
  `PageFormat` error code and exit status.
- `make live` gains one read: `cdn.ListIPRanges` against the real page,
  checking that it returns at least one item and that every item parses. It
  does not pin the count, so a list change does not fail CI. The live CLI
  test gains `vngcloud cdn list-ip-ranges`.

## Docs

- A `cdn` SDK wiki page describes `ListIPRanges`, the data source, and
  `ErrPageFormat`.
- The CLI reference page is generated from the operation table.

## Release

`v0.7.0` ships the `cdn` package, the `CDNDocs` endpoint, the transport body
limit, and the `cdn list-ip-ranges` command in one release. It breaks no
caller. See [Releases](sdk-and-cli.md#releases) for the order after it.
