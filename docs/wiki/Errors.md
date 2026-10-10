# Errors

## APIError

Every failed call to the API returns `*vngcloud.APIError`:

```go
type APIError struct {
	Operation  string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	Err        error
}
```

`Code` is the API's own error code when the response carries one. When it
does not, including a null code or one that is just the HTTP status repeated,
`Code` falls back to a status-derived value, so it is never empty for a
4xx or 5xx response:

| Status | Code |
|-|-|
| 400 | `BadRequest` |
| 401 | `Unauthorized` |
| 403 | `Forbidden` |
| 404 | `NotFound` |
| 409 | `Conflict` |
| 429 | `Throttled` |
| 5xx | `ServerError` |
| Other 4xx | `ClientError` |

The SDK reads three error body shapes into `Code` and `Message`: an object
`{"code","message"}`, an array `[{"code","message"}]`, and the IAM accounts API
wrapper `{"errors":[{"code","message"}]}`. In the wrapper, the first object
entry gives `Code` and the messages of all object entries are joined with
`; `. An empty list or a list with only `null` or non-object entries leaves the
status text as `Message` and the status-derived `Code`. No other body field
reaches the error. Echoed access tokens and vCDN API keys are replaced with
`[redacted]` in `Code` and `Message`, including errors carried in HTTP 2xx
envelopes. Redirect refusal messages also redact credentials in host names.

`Err` wraps a sentinel matching the failure, so `errors.Is` works without
inspecting `Code` or `StatusCode` directly:

| Sentinel | Meaning |
|-|-|
| `vngcloud.ErrAuth` | 401, or a login failure (see below) |
| `vngcloud.ErrPermission` | 403 |
| `vngcloud.ErrNotFound` | 404 |
| `vngcloud.ErrRateLimited` | 429 |

Helper functions cover the common checks:

```go
vngcloud.IsNotFound(err)
vngcloud.IsPermissionDenied(err)
vngcloud.IsRateLimited(err)
vngcloud.IsRetryable(err)   // true if the SDK would retry this call itself
vngcloud.ErrorCode(err)     // APIError.Code, or "" for a non-APIError
```

A service can map its own error shape onto `NotFound`; see the billing
wiki page for an example. That mapping always wins over the status-derived
fallback above.

Network failure messages use fixed descriptions, such as `canceled`,
`timed out`, or the network operation that failed. Unknown causes use
`network request failed`. Certificate failures name the class: unknown
authority, hostname mismatch, or invalid certificate. Other TLS certificate
verification failures use `TLS certificate verification failed`. Certificate
subjects and details are withheld. Errors never include request URLs,
redirect paths, or query values. Cross-host redirect errors name only the
source and target hosts. DNS lookup failures use `no such host`, prefixed
by the network operation when available. DNS names, servers, and raw details
are withheld. Cancellation and deadlines still match their context
sentinels with `errors.Is`. Network error chains expose fixed text and safe
sentinels, never the original URL error or arbitrary transport cause.
Timeouts retain a `net.Error` whose `Timeout()` returns true.

## LoginError

Every IAM User login failure returns `*vngcloud.LoginError`:

```go
type LoginError struct {
	Status           int
	CaptchaSuspected bool
	Reason           string
	Err              error
}
```

`Reason` is fixed text naming the step that failed (loading the sign-in
page, submitting it, exchanging the authorization code, and so on).
`Status` is the HTTP status observed at that step, when there is one.
`CaptchaSuspected` is true when the sign-in form was redisplayed after a
submit, which the console does both for wrong credentials and for a
required captcha.

`Err` is `vngcloud.ErrAuth`, so `errors.Is(err, vngcloud.ErrAuth)` is true
for a login failure, or the context's own error when the attempt was
canceled or timed out, so `errors.Is(err, context.Canceled)` is true
instead. Neither `LoginError` nor anything it wraps ever holds a password,
TOTP secret or code, token, cookie, authorization code, root email, or
username: its message is built only from `Status`, `CaptchaSuspected`, and
`Reason`, never from the failing HTTP call's own response.

```go
err := cfg.Authenticate(ctx)
var loginErr *vngcloud.LoginError
if errors.As(err, &loginErr) {
	log.Printf("login failed: %s", loginErr.Reason)
	if loginErr.CaptchaSuspected {
		log.Print("a captcha may be required; sign in with a static token instead")
	}
}
```

## Logging

`vngcloud.WithLogger(logger *slog.Logger)` is the only way to get logs from
the SDK; without it, nothing is logged. With it, at Debug level:

- Every HTTP attempt logs one `request` record with `method`, `path` (the
  URL path, with no query string), `status` (omitted when no response was
  received), and `duration`.
- An IAM User login logs `login started` before the attempt and
  `login finished` with `ok` (`true` or `false`) after. Login's own HTTP
  requests are not logged as `request` records.

Nothing else is logged: no body, header, cookie, token, root email,
authorization code, or credential.

```go
cfg, err := vngcloud.NewConfig(
	vngcloud.WithRegion("hcm-3"),
	vngcloud.WithIAMUser(iamUser),
	vngcloud.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))),
)
```
