# Configuration

## Region

`Config.Region` selects the region, for example `hcm-3` or `han-1`. A client
serves one region.

## Project

`Config.ProjectID` is optional. When a method needs a project and none is set,
the SDK lists the projects visible to the IAM User in the region and uses the
only match. If more than one project matches, set `ProjectID`.

List projects yourself:

```go
projects, err := client.ListProjects(ctx, nil)
```

Or for another region:

```go
projects, err := client.ListProjects(ctx, &vngcloud.ListProjectsOptions{
	Region: "han-1",
})
```

## Endpoints

The SDK targets the GreenNode domains (`*.console.greennode.ai`,
`signin.greennode.ai`) by default. The old `*.vngcloud.vn` hosts redirect to
them and drop the Authorization header on the way, so the SDK refuses
cross-host redirects and returns an error instead.

Point any product at another host with `WithEndpointOverrides`, passed to
`NewConfig` alongside the region and auth options:

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(iamUser),
	vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{
		Dashboard: "https://custom-dashboard.example.com",
	}),
)
if err != nil {
	log.Fatal(err)
}
client, err := vngcloud.NewClient(ctx, cfg)
```

`Dashboard` sets the OAuth redirect URI used during IAM login. Setting it also
derives the `Token` endpoint, unless you set `Token` too.

## Retries

The SDK retries network errors and HTTP 429, 502, 503, and 504 with jittered
exponential backoff, and honors `Retry-After`. Change the count and base
interval with `WithRetry(count, interval)`. Cancel the context to stop
retrying. `vngcloud.IsRetryable(err)` reports whether a returned error was
retryable.
