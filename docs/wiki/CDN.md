# CDN

`cdn` is a separate package, `danny.vn/vngcloud/cdn`. It is not a client on
the root client from [Services](Services.md); it has its own `New(cfg)`.

vCDN has no public API. `cdn.ListIPRanges` instead reads GreenNode's public
FAQ page listing the CDN IP ranges an origin must allow, and parses the
current list out of it. Everything else about vCDN, such as cache rules, the
origin request header, and certificates, stays a console step: the vCDN
console accepts only root login, which this SDK does not automate.

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

A non-200 response from the docs host is a `*vngcloud.APIError`, exactly as
for any other SDK call, with operation `cdn.ListIPRanges`. A 401 or 403 from
the docs host does not match `vngcloud.ErrAuth`: the request never carried a
credential for that to mean.

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
