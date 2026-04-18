# Basic Example

This example demonstrates IAM User auth, project discovery, read-only API calls,
and local output capture.

The example code is intentionally kept in a few focused files:

- `main.go`: config loading, client setup, and run order.
- `helpers.go`: shared printing, pagination, and detail helpers.
- `resources.go`: SDK calls grouped by product.
- `output.go`: local raw/sdk JSON capture.
- `check.go`: raw/sdk output validation before exit.

```bash
cp examples/basic/config.example.json examples/basic/config.local.json
$EDITOR examples/basic/config.local.json
go run ./examples/basic -config examples/basic/config.local.json
```

By default, the example runs every ignored `examples/basic/config.*.json` file
except `config.example.json`, in sorted order. Keep one config file per account
or test target:

```bash
examples/basic/config.local.json
examples/basic/config.another.json
```

Use `-config` to run only one config file.

Add one or more regions to `regions`. The example does not accept project IDs in
config; it relies on SDK project discovery.

For 2FA, set either `totpSecret` to the base32 shared secret or `totpCode` to the
current 6-digit code. Leave both empty when the IAM user does not enforce OTP.
`totpCode` is only useful for one immediate smoke run.

Root-user auth is intentionally unsupported. The observed browser flow requires
Google reCAPTCHA validation before the SSO server accepts the password step, so
it is not suitable for a non-interactive SDK auth path.

The example writes focused local output under `examples/basic/output/`.
That folder is gitignored. Raw HTTP response captures and SDK-decoded model
outputs are split by service and resource:

```text
examples/basic/output/raw/server/instance.json
examples/basic/output/sdk/server/instance.json
examples/basic/output/raw/network/vpc.json
examples/basic/output/sdk/network/vpc.json
```

The raw file is a local capture envelope with the HTTP response body under `body`.
The SDK file is the decoded public model output. Sanitize output files before
sharing them or turning them into test fixtures.
The example validates the raw/sdk output shape before it exits.
