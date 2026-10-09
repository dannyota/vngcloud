# CDN

`cdn` is a separate package, `danny.vn/vngcloud/cdn`, with its own
`New(cfg)`. It has two parts:

- `cdn.ListIPRanges` reads GreenNode's public FAQ page listing the CDN IP
  ranges an origin must allow, and parses the current list out of it. It
  needs no credential.
- The [vCDN API](#vcdn-api) reads: `ListCertificates`, `GetCertificate`, and
  `ListAPIKeys`. They take a vCDN API key.

A person creates the API key in the vCDN Portal, which accepts only root
login, so this SDK cannot make one. The SDK has no write calls for vCDN yet.

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
- A server message that holds `@` is replaced whole, so no account email
  reaches an error. Every message is cut to 256 bytes with control characters
  removed.
- The API key is added to the request's redaction list, so a server that
  echoes it in a message shows `[redacted]` instead.
