# CDN

`cdn` is a separate package, `danny.vn/vngcloud/cdn`, with its own
`New(cfg)`. It has two parts:

- `cdn.ListIPRanges` reads GreenNode's public FAQ page listing the CDN IP
  ranges an origin must allow, and parses the current list out of it. It
  needs no credential.
- The [vCDN API](#vcdn-api) calls: certificate and API key reads, the
  [Web Accelerator](#web-accelerators) reads and writes, and
  [traffic analytics](#analytics). They take a vCDN API key.

A person creates the API key in the vCDN Portal, which accepts only root
login, so this SDK cannot make one. The API cannot create a CDN either: see
[Create](#create).

## Setup

```go
package main

import (
	"context"
	"log"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/cdn"
)

func main() {
	ctx := context.Background()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{
			RootEmail: "<root-email>",
			Username:  "<iam-username>",
			Password:  "<password>",
		}),
	)
	if err != nil {
		log.Fatal(err)
	}

	client := cdn.New(cfg)
	out, err := client.ListIPRanges(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	for _, cidr := range out.Items {
		log.Println(cidr)
	}
}
```

`ListIPRangesInput` has no fields; a nil Input is valid. `Config` still needs
a region, because the rest of the SDK needs one, but `ListIPRanges` neither
sends nor requires a credential: the request is an unauthenticated `GET`
with no `Authorization` header and no cookie, even if the configured HTTP
client has a cookie jar.

## Output

```go
type ListIPRangesOutput struct {
	Items  []string // canonical CIDRs, sorted by address then prefix length
	Source string   // the page URL fetched
}
```

`Items` holds strings, not `netip.Prefix`: other SDK models carry addresses
as strings, and the CLI's output encoder does not know how to render a
`netip.Prefix`. Every item comes from `netip.Prefix.String()` on a validated
prefix, so `netip.ParsePrefix` never fails on it in your own code. Items are
de-duplicated: the source page lists a few ranges more than once.

## Errors

A non-200 response from the docs host is a `*vngcloud.APIError` with
operation `cdn.ListIPRanges`. It wraps the status sentinel as other SDK
errors do: 403 matches `vngcloud.ErrPermission`, 404 `vngcloud.ErrNotFound`,
and 429 `vngcloud.ErrRateLimited`. `vngcloud.IsRetryable` is true for 429,
502, 503, and 504. A 401 does not match `vngcloud.ErrAuth`: the request never
carried a credential for that to mean. A body over 4 MiB is `ErrPageFormat`
only when the status is 200; otherwise the status decides the error.

```go
var ErrPageFormat = errors.New("cdn: IP range page format not recognized")
```

`cdn.ErrPageFormat` means the FAQ page no longer looks like the page this
parser was written for: the heading it looks for is missing or appears more
than once, the matched section has no valid CIDR, the response is not
`text/html`, or the body is larger than 4 MiB. Every `ErrPageFormat` error
names the reason, and quotes at most one 64-byte token from the page.

`ErrPageFormat` means the parser needs a look, and a caller should not treat
it as safe to retry. Every other `ListIPRanges` error means only that the
read failed this time, the same as any other SDK call: retry it the same
way.

## Endpoint

`ListIPRanges` reads `https://docs.greennode.ai/faq/vcdn` by default.
Override it with `CDNDocs` on `vngcloud.EndpointOverrides`, passed through
`vngcloud.WithEndpointOverrides`, for example to point at a mirror or a test
server. The configured region is ignored: the FAQ page is not region-specific.

## vCDN API

The vCDN API is at `https://vcdn-api.vngcloud.vn/vcdn-api/`. It takes
`Authorization: Bearer <API key>` and does not accept the IAM token. It
ignores the region and takes no project ID. Override the endpoint with `CDN`
on `vngcloud.EndpointOverrides`.

Set the key with `vngcloud.WithCDNAPIKey`, `VNGCLOUD_VCDN_API_KEY`, or
`vcdn_api_key` in the credentials file; see
[Configuration](Configuration.md#vcdn-api-key). A call with no key returns
`cdn.ErrNoAPIKey`, which wraps `vngcloud.ErrNoCredentials`, before any
request.

The basic example runs read-only vCDN list, detail, and analytics calls only
when `VNGCLOUD_VCDN_API_KEY` is set at runtime.

```go
cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"))
if err != nil {
	log.Fatal(err)
}
client := cdn.New(cfg)

keys, err := client.ListAPIKeys(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, k := range keys.Items {
	if k.Current {
		log.Println("the key in use expires", k.ExpiresAt)
	}
}
```

| Method | Input | Output |
|-|-|-|
| `ListCertificates` | none | `Items []Certificate` |
| `GetCertificate` | `CertificateID` | `Certificate` |
| `ListAPIKeys` | none | `Items []APIKey` |
| `ListWebAccelerators` | none | `Items []WebAcceleratorSummary` |
| `GetWebAccelerator` | `CDNID` | `WebAccelerator` |
| `PurgePaths` | `CDNDomain`, `Paths` | empty |

- `Certificate` holds the ID, common name, issuer, dates as the server's
  strings, `Status` (1 active, 0 inactive), and `CDNUsing`, the number of
  CDNs that use it. `GetCertificate` also fills `Certificate` and `CARoot`
  with the PEM text. The model has no private key field.
- `APIKey` holds `ID`, `ExpiresAt`, `CreateTime`, and `UpdateTime` as
  `time.Time`, `AllowOriginHeader`, and `Current`. `Current` is true for the
  key this client sends, found by comparing tokens in memory. The model has
  no token or email field.
- The lists return every item in one call; an account with none gives empty
  `Items`. An unknown certificate ID matches `vngcloud.ErrNotFound`.
- All three reads are sensitive: the server returns every certificate's
  private key and every key's token in these answers, so the response
  capture hook never sees them and no error quotes the body.

Any holder of one API key can list every key's token and every
certificate's private key through the API, so a key is as strong as the
account's whole vCDN access. Pick the shortest expiry that works.
`allowOriginHeader` limits browsers only, not a stolen key used from a
script.

### vCDN errors

Every failure is a `*vngcloud.APIError` with the operation.

| Response | Code | Matches |
|-|-|-|
| 401 | `Unauthorized` | `ErrAuth`; never retried |
| 403 | `Forbidden` | `ErrPermission` |
| 400 | `BadRequest` | `ErrInvalidInput` |
| 404 | `NotFound` | `ErrNotFound` |
| 405 | `ClientError` | none |
| 5xx | `ServerError` | none; retried as for every service |
| 200 with `success: false` | the envelope `code`, usually `500` | `ErrInvalidInput`, `ErrAuth`, `ErrPermission`, or `ErrNotFound` for envelope code 400, 401, 403, or 404; never retried |
| 200, not the expected JSON | `EmptyResponse` | none |

- The 401 message is `vCDN API key rejected: check the key and its expiry`,
  and the 403 message is `vCDN API key not allowed to call this API`, whatever
  the server sends.
- Most server failures arrive as a 200 with `success: false` and a null
  message. The message is then `vCDN <operation> failed; the server gave no
  reason`.
- A detail read of an unknown ID arrives the same way, with empty `data`. The
  SDK maps it to `NotFound`.
- A server message that holds `@`, the fullwidth at sign (U+FF20), or the small
  at sign (U+FE6B) is replaced whole, so no account
  email reaches an error. Every message is cut to 256 bytes with control
  characters removed.
- The API key is added to the request's redaction list, so a server that
  echoes it in a message shows `[redacted]` instead. This holds for a failed
  HTTP status and for a 200 with `success: false`, in both the message and
  the code.

## Web Accelerators

A Web Accelerator is one CDN for a customer domain. `CDNDomain` is the name
GreenNode generates, such as `<random>web.vcdn.cloud`; point the customer's
DNS at it with a CNAME.

```go
list, err := client.ListWebAccelerators(ctx, nil)
if err != nil {
	log.Fatal(err)
}
for _, item := range list.Items {
	log.Println(item.DomainName, item.StatusName)
}
got, err := client.GetWebAccelerator(ctx, &cdn.GetWebAcceleratorInput{
	CDNID: list.Items[0].CDNID,
})
```

- `WebAcceleratorSummary` holds `CDNID`, `DomainName`, `CDNDomain`, `CNames`,
  `Status`, and `StatusName`. The list fills nothing else.
- `WebAccelerator` adds `Type` (`webacc`), `CertificateID` (`default` for the
  shared certificate), `LBType`, `OriginHostHeader`, `FailOverErrorCodes`,
  `UseSSL`, `UseSmallFile`, `EnableGzip`, `Upstreams`, `DefaultRuleActions`,
  `PageRules`, and `AdvancedRule`. `PageRules` and `AdvancedRule` stay the
  server's JSON (`json.RawMessage`); no call writes them.
- `Upstream` holds `ID`, `Priority`, `IPAddress`, and `Status`.
- `RuleAction` holds `ID`, `Name`, `Value`, and `Order`. `Value` is the
  server's string. For `hsts` it is JSON object text, such as
  `{"hsts":"off","preload":"off","includeSubDomains":"off","maxAge":"0m"}`,
  and for `minify` JSON array text, such as `["js","css","html"]`; the SDK
  does not parse either.
- IDs are strings. The decoder accepts a JSON string or integer.
- The models have no field for the account fields the server sends.
- An unknown or malformed `CDNID` matches `vngcloud.ErrNotFound`.

### Status

| Constant | Value | `StatusName` |
|-|-|-|
| `StatusDisabled` | 0 | `DISABLED` |
| `StatusActive` | 1 | `ACTIVE` |
| `StatusDeploying` | 3 | `DEPLOYING` |
| `StatusDeleting` | 4 | `DELETING` |
| `StatusDisabling` | 5 | `DISABLING` |

Any other value is `UNKNOWN(<n>)`, and every write refuses it with
`cdn.ErrUnexpectedStatus`. A CDN goes from 3 to 1 in about 3 minutes after a
create, 1 to 5 to 0 in about 4 minutes after a disable, 0 to 3 to 1 in about
5 minutes after an enable, and 1 to 3 to 1 in about 5 minutes after an update.
A delete of an `ACTIVE` CDN leaves it `DELETING` for about 5 minutes before it
disappears.

## Analytics

```go
out, err := client.GetTraffic(ctx, &cdn.GetTrafficInput{
	CDNDomains: []string{got.WebAccelerator.CDNDomain},
	Period:     "24h",
})
```

| Method | Output |
|-|-|
| `GetTraffic` | `Points []CacheSample` |
| `GetRequestRate` | `Points []CacheSample` |
| `GetCacheStatus` | `Counts map[string]float64` |
| `GetHTTPCodes` | `Counts map[string]float64` |
| `GetTrafficReport` | `Items []DomainTraffic` |

- `CDNDomains` holds generated names (`WebAccelerator.CDNDomain`), not
  customer domain names, and needs at least one entry. A domain that is not a
  CDN of the account fails with a message that withholds the account user.
- The four series calls take `Period` or the pair `From` and `To`, never
  both. `Period` is one of `30m`, `1h`, `3h`, `6h`, `12h`, `24h`, `3d`, `7d`,
  `14d`, `30d`, `90d`, `180d`, or `360d`. `From` and `To` are `YYYY-MM-DD`
  dates read in UTC+7, from 00:00 on `From` to the end of `To`; the SDK sends
  them as `dd/mm/yyyy`. A time of day, another format, or `To` before `From`
  is `vngcloud.ErrInvalidInput`, with no request.
- `GetTrafficReport` takes `From` and `To` only, and buckets by day at UTC
  midnight. Its `DomainTraffic` holds `DomainName`, `CDNDomain`,
  `TrafficType`, `GroupType`, and `Points []Sample` of `Time` and `Value`.
- `CacheSample` holds `Time` (UTC), `Cached`, and `Uncached`, sorted by time.
  The points are not evenly spaced: with no traffic only the two edges of the
  window appear.
- With no data, `GetCacheStatus` and `GetHTTPCodes` give an empty `Counts`.
- Every value seen so far was zero, so this page names no unit. All five are
  reads that use `POST`, so the SDK retries them like any read.

## Writes

`UpdateWebAccelerator`, `DeleteWebAccelerator`, `EnableWebAccelerator`, and
`DisableWebAccelerator` change a CDN. Each reads the CDN first and checks its
status, so a call the server would refuse sends nothing:

| Status | Update | Delete | Enable | Disable |
|-|-|-|-|-|
| 1 `ACTIVE` | sends | sends | `Changed: false` | sends |
| 0 `DISABLED` | `ErrInvalidInput` | sends | sends | `Changed: false` |
| 3 `DEPLOYING`, 4 `DELETING`, 5 `DISABLING` | `ErrBusy` | `ErrBusy` | `ErrBusy` | `ErrBusy` |
| Any other | `ErrUnexpectedStatus` | `ErrUnexpectedStatus` | `ErrUnexpectedStatus` | `ErrUnexpectedStatus` |

- `cdn.ErrBusy` means nothing changed: run the same call again after the CDN
  settles. The server's own refusal during a change, `Current cdn status is
  not allow to update or delete`, also matches it.
- Update, delete, enable, and disable are sent once and never retried, even
  after a failed connection. After a server error or a network failure the
  write may have landed; read the CDN before running it again.
- Enable and disable return the CDN and `Changed`. A call on a CDN already at
  its target sends nothing and returns `Changed: false`.
- Update, enable, and disable wait for the CDN to settle: they read it every 10
  seconds for up to 6 minutes. Update with `NoWait` makes one follow-up read
  and skips the settle wait. Enable and disable still confirm with reads at 0,
  2, 4, and 8 seconds, then skip the settle wait. When the bound passes, the
  call returns the last good read and an error that
  matches `cdn.ErrNotSettled`; the server accepted the write, so do not repeat
  it. Another status during the wait ends it with `cdn.ErrUnexpectedStatus` and
  the read. A deleted CDN ends it with `vngcloud.ErrNotFound`. An enable or
  disable that no read confirms returns `cdn.ErrStatusUnconfirmed`: read the CDN
  before doing anything else.
- The server can answer an update, delete, enable, or disable of a CDN that no
  longer exists with a 401 and an empty body, like a rejected key. The SDK
  reads the CDN again after such a 401 and returns `vngcloud.ErrNotFound` when
  it is gone.
- Delete takes an `ACTIVE` or `DISABLED` CDN and does not wait. The CDN loses
  its generated `CDNDomain`, which the customer's DNS points at. An `ACTIVE`
  CDN stays `DELETING` for about 5 minutes, and reads of it work until it
  disappears; then they match `vngcloud.ErrNotFound`.
- Package limits arrive as a failure whose message names the limit, such as
  `Current user package is not allow to use feature developmentMode, please
  upgrade your package`. They match no sentinel.
- A refusal with code 202 matches `vngcloud.ErrInvalidInput`, except for the
  purge cooldown described below. A message that starts `Not found cdn`
  matches `vngcloud.ErrNotFound`. A failure with a null code has the code
  `EnvelopeError`.

### Purge paths

`PurgePaths` removes cached objects by path from the CDN named by its generated
`CDNDomain`. It needs at least one non-empty path. A path cannot contain `*`.
Other path syntax is sent to the server without extra SDK rules. The server
refuses a bare `/` as an invalid content URI; name files such as `/index.html`.

```go
_, err := client.PurgePaths(ctx, &cdn.PurgePathsInput{
	CDNDomain: got.WebAccelerator.CDNDomain,
	Paths:     []string{"/assets/app.js", "/index.html"},
})
```

The API allows one purge on a CDN every 30 seconds. A refusal whose message
starts `Last CDN flush cache time` matches `cdn.ErrPurgeCooldown` only. The SDK
does not wait and retry after that refusal. Other code 202 refusals match
`vngcloud.ErrInvalidInput`.

A purge is a non-idempotent `POST`. The transport retries only after a 429 or a
failed dial, where the server did not act. It does not retry after a 5xx, a
failed envelope, or an ambiguous network failure. Check the CDN before running
the purge again after an ambiguous failure. Each purge counts against the
account package's daily purge limit.

### Update

The update call takes the whole CDN and deletes every rule action it leaves
out. `UpdateWebAccelerator` therefore reads the CDN, merges your changes by
action name, and sends the whole object back, with every field the SDK does
not model unchanged. You never handle an action ID.

```go
out, err := client.UpdateWebAccelerator(ctx, &cdn.UpdateWebAcceleratorInput{
	CDNID: id,
	SetRuleActions: []cdn.RuleActionInput{
		{Name: "browserCache", Value: "1d"},
	},
	RemoveRuleActions: []string{"imgOptimize"},
	CNames:            []string{}, // clears the alternative names
})
```

- At least one change is required; an Input with only `CDNID` and `NoWait` is
  `vngcloud.ErrInvalidInput`. A merge that changes nothing sends nothing and
  returns the read.
- `SetRuleActions` changes the value of the action with that name, or adds the
  action. `RemoveRuleActions` drops an action by name; a name the CDN does not
  have is ignored. A name may appear once across both lists.
- Scalar fields (`LBType`, `CertificateID`, `OriginHostHeader`) are pointers;
  nil keeps the value. `CNames` and `FailOverErrorCodes` keep the value when
  nil and replace it when set, so an empty non-nil list clears it.
- **No call removes an origin.** `Upstreams` sends only the origins you give,
  and the server keeps the others. An entry with an `ID` edits that origin in
  place, replacing all its fields; an entry without one adds an origin. To
  change an origin, edit it in place. A package that allows one origin refuses
  an add.
- The server gives `alwaysHttps` a new ID on every update. Other action IDs are
  kept.
- The CDN must be `ACTIVE`. The update answer lacks some actions and repeats an
  origin, so the Output always comes from a read of the CDN.
- A package may refuse an action: the test account's package refuses
  `developmentMode`.

## CLI

Create the CDN in the vCDN Portal, then find its ID and generated CNAME target:

```sh
vngcloud cdn list-web-accelerators
vngcloud cdn get-web-accelerator --cdn-id <cdn-id>
```

Analytics uses the generated domain, not the customer domain. It remains a
read under a read-only profile, although the API uses POST. Set `CDNDomains`
through `--cli-input-json`.

```sh
vngcloud cdn get-traffic --cli-input-json '{"CDNDomains":["<cdn-domain>"]}' \
  --period 24h
vngcloud cdn get-traffic-report --cli-input-json '{"CDNDomains":["<cdn-domain>"]}' \
  --from 2026-10-08 --to 2026-10-09
```

Use a JSON file for nested updates. `SetRuleActions`, `RemoveRuleActions`, and
`Upstreams` use their Go field names. Update `--no-wait` makes one follow-up
read and skips the settle wait. Toggle `--no-wait` still confirms at 0, 2, 4,
and 8 seconds, then skips the settle wait.

```sh
vngcloud cdn update-web-accelerator --cdn-id <cdn-id> \
  --cli-input-json file://changes.json
vngcloud cdn enable-web-accelerator --cdn-id <cdn-id>
vngcloud cdn disable-web-accelerator --cdn-id <cdn-id> --yes
vngcloud cdn delete-web-accelerator --cdn-id <cdn-id> --yes
```

Update and enable do not need `--yes`. Disable and delete do. A command that
returns `NotSettled` prints the last CDN read to stdout and exits 1. Read the
CDN before repeating a write. `ResourceBusy`, `UnexpectedStatus`, and
`StatusUnconfirmed` also exit 1.

### Create

The API cannot create a Web Accelerator. `POST cdn/create` checks its body and
then answers `Create CDN failed.` for every valid body, so the SDK has no
`CreateWebAccelerator`. Create each CDN in the vCDN Portal, then manage it
with the calls above.
