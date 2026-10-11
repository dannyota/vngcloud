# Service-account authentication

Accepted: the owner approved the eight decisions below. Add SDK-owned
service-account login and CLI profile support so teams can use separate
machine identities with server-enforced policies. Implementation waits for
an independent plan review and the remaining
[checks](service-account-auth-api.md#open-evidence).
This document authorizes no live calls or policy grants.

## Scope and evidence

The SDK currently selects a custom credentials provider, a static token,
or IAM-user login. IAM service-account management already exists, including
secret-file CLI commands. The [evidence](service-account-auth-api.md) records
the relevant repository files, first-party sources, disagreements, and facts
that remain unverified. Recorded live results come from the manager; the
designer made no authenticated requests.

Authentication establishes an identity; attached IAM policies establish its
rights. A client ID is not a server ID. Neither a profile's `project_id` nor
CLI `read_only` limits what stolen credentials can do. Server-scoped access
requires a customer policy that the server enforces for each selected read.
Server scope and enumeration remain blocked without test servers. The
[security-group results](service-account-auth-api.md#recorded-live-results)
establish a narrower capability; do not advertise server discovery from it.

Exclude impersonation, trusted roots, root login, activation, automatic
policy creation, S3/Swift authentication, and changes to paid-write gates.
Keep the SDK standard-library only. Do not import the official SDK or its
OAuth library; use their code only as evidence.

## SDK surface and selection

Add these public names in package `vngcloud`:

```go
type ServiceAccountAuth struct {
    ClientID     string
    ClientSecret Secret
}

func WithServiceAccount(auth *ServiceAccountAuth) LoadOption
```

`LoadOption` already aliases the option accepted by `NewConfig`. Keep
`Secret` in its existing public package. Define the public auth struct and
option wrapper there; pass an immutable, privately held credential snapshot
to `internal/core`, avoiding an import cycle. Environment and file loading
construct the same internal representation. Do not move or redefine the
existing `Secret` type. Copy credentials when building the option; later
mutation of the caller's struct does not alter a configured identity.

Selection is independent of option order:

1. Non-nil `WithCredentialsProvider`.
2. Non-empty `WithStaticToken`.
3. Non-nil `WithServiceAccount`.
4. Non-nil `WithIAMUser`.

Multiple methods remain legal. Validate only the selected method; never
fall back after a validation or login failure. `WithServiceAccount(nil)`
is unset. A selected non-nil value requires both fields. Reject whitespace,
control characters, and values over 4096 bytes in either field; also reject
`:` in `ClientID` because Basic auth uses it as the separator. These are SDK
input-safety limits, not claims about GreenNode credential formats. Do not
require a UUID or trim a secret. Name fields and sources in errors, never
values.

`NewConfig` remains offline and reads no environment or files. Login stays
lazy; `cfg.Authenticate(ctx)` runs it eagerly and returns no token. Services
built from one config share one token source. Separate configs share tokens
only through the optional disk cache. No new public token getter, login
command, auth endpoint field, or public test hook is needed.

## Token request and endpoint trust

Use a separate internal service-account exchange, not `iamuser.Login` and
not the bearer-authenticated product transport. Use the exact
[verified request contract](service-account-auth-api.md#token-exchange).
That section owns the fixed URL, encoding, fields, and source disagreements.
Do not negotiate auth styles, retry another encoding,
or expose a caller-supplied scope. Scope is not a resource policy.

If the fixed endpoint redirects or fails, block the release and review
first-party evidence for a replacement. Never follow `Location` with
credentials, invent a replacement hostname, or silently try another host.

Use a private `http.Client` and a new standard transport with TLS
verification enabled, not a mutable global default transport. Refuse all
redirects, including same-origin 301, 302, 303, 307, and 308. Use no cookie
jar and no application response capture.
Do not take the token URL from `EndpointOverrides.Token`, `IAM`, `Dashboard`,
profiles, environment variables, a response, or discovery. Those overrides
retain their existing product/IAM-user meanings.

`WithHTTPClient` and `WithTransport` continue to affect product requests;
they do not receive service-account client secrets. The exchange honors
`WithTimeout` when positive, otherwise uses 120 seconds, always bounded by
the caller's context. It ignores `WithRetry`: one exchange attempt per
refresh, including on 429, 5xx, network failure, and redirect. Tests inject
an internal transport, never an exported arbitrary token URL. This narrower
hook behavior is an explicit owner decision.

Accept HTTP 200 containing exactly one JSON object. Bound decoded response
bytes to 64 KiB plus one overflow byte; reject oversized, malformed, null,
duplicate known fields, wrong field types, or trailing JSON. Require a
non-empty `access_token` without whitespace/control characters and an
integer `expires_in` greater than 30 seconds. Check duration overflow and
that the token is still more than 30 seconds from expiry after the request.
Set expiry from request start plus `expires_in`, using an injected clock in
tests. Use the returned lifespan even when it differs from the
[observed default](service-account-auth-api.md#token-exchange); never derive
expiry from JWT claims. Permit absent `token_type`; if present require
case-insensitive
`bearer`. Ignore unknown fields and discard all refresh-token fields. Renew
by another client-credentials exchange. Return no partial token on failure.

## Profiles, environment, and CLI

Preserve [configuration precedence](../wiki/Configuration.md#precedence):
options, environment, then the resolved profile. Select a whole credential
set without mixing sources. A non-nil auth option reserves the option
source even when its fields are empty; apply method precedence there, then
validate the winner. `LoadConfig(WithServiceAccount(&ServiceAccountAuth{}))`
fails rather than taking environment or profile credentials. A nil
auth/provider value or empty static token is unset. Only when no option
selects credentials does the first environment/file source with a non-empty
value win; empty values there are unset. Within that source, static token
wins over service-account keys,
which win over IAM-user fields. A selected partial set requires its missing
fields from that same source and never falls back.

| Source | Client ID | Client secret |
|---|---|---|
| Environment | `VNGCLOUD_CLIENT_ID` | `VNGCLOUD_CLIENT_SECRET` |
| Credentials section | `client_id` | `client_secret` |
| Config section | Refused if non-empty | Refused if non-empty |

Both file keys belong in the existing mode 0600 credentials file, under
`[default]` or the bare profile name. Keep regular-file checks, safe profile
names, missing-file behavior, and the Windows mode-check exception. Reject
either non-empty key in the resolved profile's config section with
`ErrInvalidConfig` before storing profile settings. This reserves two formerly
unknown keys so misplaced credentials cannot reach `Config.ProfileSetting`.
Name the credential key and required file, never its value. This change
needs owner approval. No static-token file key is added.

Explicit `WithProfile` or CLI `--profile` skips all environment credentials,
including both new variables, and the environment project ID. Region can
still come from the environment. `VNGCLOUD_PROFILE` remains non-explicit.
Keep the vCDN API key independent; service-account auth does not replace it.

Extend `configure set/get/list` with both keys. `client_secret` accepts stdin
only, using the existing 4096-byte bound and newline handling. Mask it in
every output format and on `get`. `client_id` may print as existing public
client IDs do, but remains account data excluded from fixtures and logs.
Keep interactive IAM-user prompts unchanged and document explicit commands:

```sh
vngcloud --profile discovery configure set region hcm-3
vngcloud --profile discovery configure set project_id <project-id>
vngcloud --profile discovery configure set client_id <client-id>
vngcloud --profile discovery configure set client_secret - < /private/secret
vngcloud --profile discovery configure set read_only 1
```

Use a fresh profile to avoid leftover IAM credentials. Set `read_only` last;
the existing latch prevents later configure writes, including rotation.
An operator must edit that profile's protected credentials file through a
trusted editor or provisioner to rotate its secret. Do not add an auth flag
or an override for read-only. No secret goes in argv or `--cli-input-json`.

Reuse `iam create-service-account --name ... --secret-file ...` and
`iam reset-service-account-secret --service-account-id ... --yes
--secret-file ...`. The current create returns `ServiceAccount.ClientID`
from its confirm read and `ClientSecret Secret` from the create response.
A failed confirm read preserves ID and secret but may have no client ID;
recover with `get-service-account`, not another create. Preserve
`ErrNoSecret`, `ErrCreateUnconfirmed`, mode 0600 exclusive creation,
symlink refusal, and existing cleanup behavior. No create-and-configure
command or automatic secret import is needed. See [IAM secret
files](iam-writes.md#secret-file) and [implementation evidence][local].

## Cache and credential rotation

Reuse `internal/tokencache` locking, atomic replacement, and expiry logic.
Add a distinct service-account key path without changing IAM key hashes.
Under the configured cache directory, use `service-account-v1/<hash>.json`
and its persistent lock file. Hash length-prefixed auth kind, profile,
client ID, fixed token URL, and client-secret bytes with SHA-256. The secret
is included only in the hash computation, never in stored JSON, metadata,
logs, or error text. This binds a new secret to a fresh cache entry. The
filename is still sensitive local data, not a public identifier.

Use mode 0700 for the cache and namespace directories and mode 0600 for
token and lock files. Retain atomic temp-file replacement and nonfatal cache
write failures after a successful exchange. For the new namespace, require
regular files, refuse symlink token/lock entries and group/other access,
validate the opened inode, and bound reads to 64 KiB. Missing or malformed
token content is a miss; unsafe filesystem entries fail closed under the
[cache error contract](#errors-and-secret-handling). Implement checks without
following a FIFO or widening Windows' current permission promise. Trust the
cache parent and its ancestors; mode bits cannot defend against the same
OS user.

Keep the existing 30-second expiry margin and 401 rejection age rule for
both memory and disk. Invalidate only the exact rejected token when it is
at least 30 seconds old or has a future obtain time. A younger rejected
token is reused for the transport's single retry, which can end in
`ErrAuth`. This age rule throttles rapid reauthentication even though a
service account has no TOTP. Keep pending rejection state across canceled
lock waits. Concurrent refreshes for the same key reuse the winner.
Context cancellation stops exchanges and disk-lock waits. It does not
promise immediate cancellation while waiting for the shared transport's
`refreshMu` in `EnsureToken`, `invalidateAndRefresh`, or `invalidateOnce`
([transport](../../internal/transport/transport.go)). Those locks precede
the token source; a waiter can remain blocked until the active exchange
finishes. Preserve that behavior here. Cancellable shared refresh waits
require a separate design covering every auth kind and `Once` invalidation.

The product transport retains its one 401 recovery attempt; `Once` requests
invalidate but never resend. Never refresh on 403. No auth path grants a
write retry that [ADR 0002](../adr/0002-write-api-conventions.md) or
[ADR 0003](../adr/0003-toggle-writes.md) forbids.

The [reset results](service-account-auth-api.md#recorded-live-results) mean
rotation replaces exchange credentials, not all active access. Replace the
protected file, rebuild configs, and restart consumers. Old processes can
reuse issued tokens from memory or the old disk entry, then fail when they
need to exchange their stale secret. A new config's secret-bound key must
force a fresh exchange. Do not evict old entries automatically or delete
another profile's cache. Local eviction cannot revoke a copied token.
CLI rotation documentation must state this access window and make no
immediate-revocation promise. Do not treat deletion or activation as a proven
emergency revocation mechanism. SDK disk caching remains opt-in; CLI caching
remains enabled as [configured][local]. Static tokens and custom providers
remain outside this disk cache.

## Project discovery and feature gates

Keep region required and explicit project selection recommended. Token
issuance is global; do not include project or region in the token request
or derive either from client ID, JWT claims, or IAM caller identity.
`RequireProjectID` uses an explicit ID or the current `ListProjects` flow:
zero matches means `ErrProjectNotFound`, one selects it, multiple mean
`ErrProjectAmbiguous`. A discovery 403 remains `ErrPermission`. Do not add
permissions or silently switch identities to make discovery work.

Use the existing console product endpoints and discovery flow supported by
[recorded results](service-account-auth-api.md#recorded-live-results).
Treat the observed caller as a service-account principal; `UserID` identifies
that principal, not the client credential or the numeric root account.
Methods using `RequireProject` can still list projects with an explicit ID.
Publish only the service-account capability matrix actually verified. A
failed endpoint needs a separate reviewed contract, not a hidden route
rewrite. Existing pagination and cancellation rules remain unchanged.

Set `UsesIAMUserLogin()` from the selected auth kind, never from absence of
provider/static options. It must be false for service-account auth. NAT
create/delete remain restricted to SDK IAM-user login in `han-1`. Preserve
quote auth and region behavior: quotes do not run that guard and continue
to accept static tokens, custom providers, and `hcm-3`, as tested in
[NAT quote tests](../../network/nat_write_test.go). Preserve all
[IAM guards](iam-writes.md#guards), including refusal of service-account
targets for service-account callers and policies attached to them. A
verified caller identity does not approve relaxing those guards.
Other operations remain permission-dependent, not newly live-verified.

## Errors and secret handling

Add `ServiceAccountLoginError` with `Status int`, `Reason string`, and
`Err error`, plus `Error` and `Unwrap`. `errors.As` identifies this type;
`errors.Is` matches `ErrAuth`, or the caller's context error when canceled
or expired. Do not reuse the IAM-specific `LoginError` or captcha fields.
Use only fixed reasons: `token request failed`, `token endpoint redirected
request`, `token endpoint rejected request`, or `invalid token response`.
No raw transport/decode error, header, response body, URL, or server message
is retained. A timeout not caused by the caller's context remains an auth
failure. `LoadConfig` still returns an already-ended context directly.

- A selected partial set in `LoadConfig` matches `ErrNoCredentials` and
  `ErrInvalidConfig`; CLI `NoCredentials`, exit 3. A partial set in
  `NewConfig` or invalid field shape matches `ErrInvalidConfig`; CLI
  `InvalidConfig`, exit 2.
- Credentials-file refusal keeps `ErrCredentialsFile`; CLI `NoCredentials`,
  exit 3. Exchange failures return `ServiceAccountLoginError` and `ErrAuth`;
  CLI `ServiceAccountLoginFailed`, exit 3. Caller context termination wraps
  only that context error; the same CLI code exits 1.
- Product 401 after recovery keeps `ErrAuth`, existing API code, exit 3.
  Product 403 keeps `ErrPermission`, existing API code, exit 1.
- Add public `ErrTokenCache`, with fixed text `vngcloud: token cache
  unavailable`, for service-account cache directory, read, and lock failures.
  Return that sentinel alone: no path, hash, credential, or filesystem cause
  in the message or unwrap chain. CLI `TokenCacheFailed`, exit 1. At the cache
  boundary, an ended caller context takes precedence: return `ctx.Err()`
  directly; CLI `RequestFailed`, exit 1.
  These errors match neither `ErrAuth` nor `ErrInvalidConfig`. Defer CLI cache
  preflight until auth selection to apply this rule to service accounts;
  other auth kinds retain their current preflight/error behavior. Missing
  files, malformed cache content, and post-login write failures retain the
  cache behavior specified above.

Handle the [token statuses](service-account-auth-api.md#token-exchange) and
[product denial shape](service-account-auth-api.md#recorded-live-results)
without changing fixed auth errors or product `ErrPermission` mapping.
Never echo arbitrary `code` or `error_description` fields in login errors.

Use `Secret` on the public input and a redacting private credential holder
internally. Never retain a plain secret as a printable struct field. Test
whole-config, option, auth struct, and error formatting as well as individual
values. Preserve `Secret`'s documented limits for `%p`, gob, reflection, and
explicit string conversion; do not claim Go memory isolation or zeroization.
Debug output contains only exchange started/finished and success booleans.
Token exchanges never enter response capture. Redact credential-key variants
in generic CLI/error paths; fixed auth errors require no body redaction.

## Security obligations and deterministic checks

These are design constraints for independent adversarial review, not a
review verdict. Existing path locations are in [local evidence][local].

- High: logging or wrapping a peer's echo could disclose a credential.
  Follow `internal/core/auth.go:115`'s fixed-error boundary. Inject secret,
  token, Basic-header, URL, and nested-JSON markers into every failure;
  inspect errors, unwrap chains, formatting, stdout, stderr, and captures.
- High: hostile response lengths or expiry overflow could exhaust memory
  or retain a token indefinitely. Bound the proposed exchange and cache
  decode; test duplicate keys, wrong types, null, trailing JSON, empty
  tokens, unknown token types, huge expiry, and both 30-second boundaries.
- High: an override or redirect could steal the secret. The proposed
  private exchange ignores public HTTP hooks and endpoint overrides.
  Test every redirect status, TLS failures, no cookies, no second send,
  context cancellation, timeout, and zero requests after validation errors.
- High: profile mixing or cache substitution could select another identity.
  Extend `internal/core/load.go:271` and `internal/tokencache/cache.go:123`
  with source-selection and new-namespace tests. Cover partial/mixed sets,
  explicit profiles, changed secret/client/profile/URL keys, unsafe files,
  symlinks, FIFOs, lock cancellation, corrupt cache, and concurrent processes.
- High: misclassified auth or replays could enable an unverified purchase.
  Fix selection at `internal/core/client.go:154`; retain the refusal at
  `network/nat_write_guards.go:33`. Test NAT create/delete reject service
  accounts before requests, quotes keep read auth/regions, and
  higher-precedence methods retain their own provenance. Confirm
  `Once` sends only once on 401, 429, 5xx, and network failures.

Use injected clocks and transports, temporary files, and synthetic secrets.
Decode manager-sanitized token fixtures through the new auth source. Label
synthetic fixtures as synthetic until live sanitation establishes provenance.
Test file modes, cache error identity and path/hash suppression, cache write
failure, canceled disk-lock waits, no disk use for provider/static auth,
CLI stdin-only input, masked get/list/debug, and read-only configure refusal.
Retain IAM-user behavior tests. Implementation checks include `make check`,
generated CLI docs, wiki preview, and independent adversarial review.

## Owner decisions and release order

The wire choice is settled by evidence; standing probe authority needs no
new decision. These proposed product contracts still need owner approval:

1. Recommend the named SDK surface and precedence between static token and
   IAM user, selected-method-only validation, and immutable credentials.
2. Recommend private token HTTP with no public hooks, overrides, redirects,
   or fallback. Adopt the stated timeout, one-send, decoding, expiry, fixed
   errors, and shared-waiter limitation.
3. Recommend the two environment variables and credentials keys, rejecting
   non-empty config-file copies. Use stdin-only secret input and masked
   output; add no interactive or provisioning flow.
4. Recommend the secret-bound cache namespace, stricter file checks, and
   existing 30-second rejection rule. Preserve other auth kinds' behavior.
5. Recommend the login/cache errors and CLI codes, with cache preflight
   deferred until auth selection.
6. Recommend unchanged discovery and write guards. Advertise only the
   observed capability; keep server scope and enumeration gated.
7. Recommend explicit CLI/wiki rotation guidance from the cache section:
   replace credentials and restart consumers, explain surviving token access,
   and promise neither automatic cache purge nor immediate revocation.
8. Recommend shipping SDK and CLI together after review and checks, including
   the implemented auth/cache repeats. Add no dependency, public auth test
   hook, cache-clear command, or paid probe.

Review the implementation plan, then build SDK auth, configuration, cache,
CLI support, tests, and docs together. Verify the remaining implementation
checks before release. A narrow release can omit server-scoped guidance;
do not present that omission as completion of the discovery-team use case.

Update [Authentication](../wiki/Authentication.md),
[Configuration](../wiki/Configuration.md), [Errors](../wiki/Errors.md),
[CLI](../wiki/CLI.md), generated [CLI-IAM](../wiki/CLI-IAM.md) where examples
change, and [Services](../wiki/Services.md) with verified coverage. Add a
feature entry to [release notes](../../RELEASE_NOTES.md), without assigning a
version here. The manager must also reconcile the auth sections of
[SDK design](sdk-and-cli.md), [CLI design](cli.md), the design index, and the
stale auth evidence in [IAM API](iam-writes-api.md); these paths are outside
this brief. Follow [wiki publication](../adr/0001-wiki-from-docs.md).

[local]: service-account-auth-api.md#repository-evidence
