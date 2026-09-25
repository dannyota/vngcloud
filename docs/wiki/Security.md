# Security

The SDK and the `vngcloud` command are safe by default. None of these can be
turned off.

## Network

- TLS verification is always on.
- The SDK refuses a redirect to another host, so a moved endpoint cannot
  receive your token.
- Public pages, such as the CDN IP range FAQ, are fetched with no token and
  no cookies.

## Credentials

- Errors and logs never include passwords, TOTP secrets, tokens, cookies, or
  the root email. `--debug` logs only each request's method, path, status,
  and timing.
- A credentials file that group or others can read is refused.
- Credentials files are written with mode 0600, and the token cache with mode
  0600 files in a 0700 directory.
- An explicit profile (`--profile` or `WithProfile`) never takes credentials
  or a project ID from environment variables, so a stray `.env` cannot send
  calls to the wrong account.
- `vngcloud configure` never takes a password or TOTP secret from the command
  line, and never echoes it.

## Writes

- A create is never retried after a failure that may have reached the
  server, so a network error cannot create a resource twice.
- Write APIs check every ID that goes into a URL path before any request.
- Destructive commands need `--yes`, and nothing prompts without a terminal.
- A read-only profile (`read_only = true`, `VNGCLOUD_READ_ONLY=1`, or
  `--read-only`) makes every write command fail before any request. Give an
  AI agent a read-only profile.

## Supply chain

- The SDK packages use only the Go standard library. The command adds cobra,
  go-jmespath, and x/term, and CI checks that no SDK package imports them.
- CI runs govulncheck, gitleaks, and Semgrep. Tools and Actions are pinned
  and kept on their latest releases.
- Every commit and release tag is signed.

Report a security problem privately, as described in
[SECURITY.md](https://github.com/dannyota/vngcloud/blob/master/SECURITY.md).
