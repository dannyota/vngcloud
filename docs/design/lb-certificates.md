# vLB Certificates Design

Status: Accepted (2026-09-27). The owner approved every recommendation
under [Owner decisions](#owner-decisions).

This design adds vLB certificate import and delete to `loadbalancer`, beside
the existing `ListCertificates` and `GetCertificate`, with CLI commands. It
is pick 3 of the [free writes survey](free-writes-survey.md). An import
sends a private key, so the design is mostly about keeping that key out of
every output.

It follows [ADR 0002](../adr/0002-write-api-conventions.md) for every
write, the `vngcloud.Secret` contract of [vStorage](storage.md#secrets),
and the delete guard shape of [vServer free writes](vserver-writes.md).

## Source

Public sources only; no write was sent.

- The vLB API reference on `docs.api.greennode.ai`
  (`service-docs/vlb-api.html`), tag `Certificates`.
- VNG Cloud's Go SDK (`vngcloud/vngcloud-go-sdk`,
  `services/loadbalancer/v2/certificate_request.go`), which sends the key,
  chain, and passphrase only for type `TLS/SSL`.
- VNG Cloud's Terraform provider (`resource/vloadbalancing/
  resource_certificate.go`), which imports with no wait and deletes with
  one call. It does not mark `private_key` sensitive, so its plan output
  and state hold the key; this SDK must not copy that.
- The product pages "Certificate" and "Upload a certificate" on
  `docs.greennode.ai` (Application Load Balancer).
- A read-only `list-certificates` on the test account, through this
  repository's CLI: it succeeds on the IAM vLB gateway.

The reference documents the public gateway
(`.../vserver/vlb-gateway/`). The SDK's reads use the IAM gateway (`VLB`
endpoint, `.../vserver/iam-vlb-gateway/`) with the same `v2/{projectId}`
paths; that the writes exist there is a [live check](#live-checks).

### Calls

| Call | Method and path under `v2/{projectId}` | Success |
|-|-|-|
| List | `GET cas` | 200 |
| Get | `GET cas/{caId}` | 200 |
| Import | `POST cas` | 201 |
| Delete | `DELETE cas/{caId}` | 204 |

### Bodies and responses (inferred)

- Import body: `name` and `type` (required), `certificate`,
  `certificateChain`, `privateKey`, and `passphrase`. `type` is `TLS/SSL`
  or `CA`. The reference gives the name pattern `^[a-zA-Z0-9.-]{5,50}$`.
- Import response: `data` holding the same fields as a get: `uuid`,
  `name`, `certificateType`, `expiredAt`, `importedAt`, `notAfter`,
  `notBefore`, `keyAlgorithm`, `serial`, `subject`,
  `subjectAlternativeNames`, `domainName`, `inUse`, `issuer`, and
  `signatureAlgorithm`. No response in the reference holds a key or a
  passphrase.
- Get returns the certificate unwrapped, as `GetCertificate` already
  decodes it. The model lacks `subjectAlternativeNames`.
- Error statuses are not documented. The Go SDK names no certificate
  error message.

### Server rules (from the product docs)

- A `TLS/SSL` certificate takes a PEM certificate, a PEM private key, an
  optional chain in leaf-first order, and a passphrase when the key is
  encrypted. A `CA` certificate takes the PEM certificate only; listeners
  use it for client certificate authentication.
- GreenNode says it stores certificates encrypted and that no one can read
  them back. Nothing in the reference returns a key.
- Listeners name certificates in `certificateAuthorities`,
  `defaultCertificateAuthority`, and `clientCertificateAuthentication`,
  and the certificate read has `inUse`.

### Cost

No price list, calculator item, or quote resource type names a vLB
certificate, and an import needs no load balancer. The design treats
import as free, so ADR 0002 rule 8 (quote) does not apply. The test
account has no credit, so a paid import would fail rather than bill. The
next day's bill must show no vLB line; see [Live checks](#live-checks).

## Non-goals

- Listener writes, or attaching a certificate to a listener: both need a
  paid load balancer.
- Certificate update or renewal: the API has none. Import a new one and
  point listeners at it.
- The console's `expiration` field, which the reference does not list.
- Parsing or checking a certificate's dates, key match, or chain order in
  the SDK (ADR 0002 rule 5). The server checks them.
- Generating keys or certificates.

## Decisions

- The private key and passphrase are `vngcloud.Secret` in the Input. The
  CLI reads them only from files.
- No read returns a key. `Certificate` has no key field, and typed models
  drop unknown fields.
- A delete of a certificate a listener uses is refused before any
  request.
- One release.

## SDK

All methods live in `loadbalancer`.

```go
func (c *Client) ImportCertificate(ctx context.Context, in *ImportCertificateInput) (*ImportCertificateOutput, error)
func (c *Client) DeleteCertificate(ctx context.Context, in *DeleteCertificateInput) (*DeleteCertificateOutput, error)

const (
	CertificateTypeTLS = "TLS/SSL"
	CertificateTypeCA  = "CA"
)

var ErrCertificateInUse = errors.New("loadbalancer: certificate in use")
```

"(r)" marks `vngcloud:"required"`.

| Operation | Input | Output |
|-|-|-|
| `ImportCertificate` | `Name` (r), `Type` (r), `Certificate` (r), `CertificateChain`, `PrivateKey vngcloud.Secret`, `Passphrase vngcloud.Secret` | `{Certificate}` |
| `DeleteCertificate` | `CertificateID` (r) | `{}` |

`Certificate` gains `SubjectAlternativeNames []string`
(`subjectAlternativeNames`). The change adds a field and breaks no caller.

### Import

Shape checks, all `ErrInvalidInput` before any request. No error quotes an
input value; each names the field.

| Field | Check |
|-|-|
| `Certificate` | Holds a PEM `CERTIFICATE` block. Holds no `PRIVATE KEY` text |
| `CertificateChain` | Empty, or only PEM `CERTIFICATE` blocks. Holds no `PRIVATE KEY` text |
| `PrivateKey` | Required when `Type` is `TLS/SSL`. When given, holds one PEM block whose type ends in `PRIVATE KEY` |
| `Passphrase` | Empty unless `PrivateKey` is given |
| `Type` | Sent as given. When it is not `TLS/SSL`, `PrivateKey`, `Passphrase`, and `CertificateChain` must be empty |

- The `PRIVATE KEY` check on the public fields stops a caller who passes
  the key file as the certificate: those fields are not secrets, so their
  text could reach an error unredacted.
- The last row keeps a key off the wire when the server has no use for
  it. It names `TLS/SSL` only; other `Type` values still go to the server,
  per ADR 0002 rule 5.
- The request body is a private wire struct with plain `string` fields,
  filled with `Reveal()`. A `vngcloud.Secret` placed in the body would
  encode as `[redacted]`, and the server would get that text as the key. A
  test asserts the body holds the fixture key.
- Empty optional fields are left out of the body.
- The request sets `Sensitive`, so the capture hook never sees the
  response, and `Redact` (below) with the key and passphrase.
- The Output is the mapped `data` of the 201. A 201 with no `uuid` is an
  `*APIError` whose message says a certificate may exist and names
  `list-certificates --name <name>`.
- No wait: the reference and Terraform show a final response.

### Error redaction

A 400 from the server may quote the input it rejected. `transport.Request`
gains `Redact []string`. When a request fails, the transport replaces, in
the `*APIError` message and code, every occurrence of each non-empty
value, its JSON-escaped form, and each of its lines that is 8 characters or
longer after trimming. The line rule catches a server that quotes one line
of a key. `--debug` already logs no body.

If the [live checks](#live-checks) show the server echoes key text in a
form this misses, the SDK instead withholds the server message for this
call and keeps only the status and code. That change amends this section.

### Delete

`DeleteCertificate` reads the certificate with `GetCertificate` first and
returns `ErrCertificateInUse`, sending nothing, when `InUse` is true. The
server's refusal is the final guard: an API error whose message contains
`in use` or `is used` (case-insensitive) also wraps `ErrCertificateInUse`,
whatever its status. That message is assumed; no source shows it, and the
test account has no load balancer to provoke it.

Delete is synchronous: the Output is `{}` after the 204. A retried delete
that finds the certificate gone returns `NotFound`; how a missing
certificate reads (404, 400, or 500) is a live check. If it is not 404,
the SDK confirms by `ListCertificates` before mapping it to `NotFound`, as
[vServer network writes](vserver-network-writes.md#acl-delete) does for
ACLs.

### Identifiers and retries

- `GetCertificate` and `DeleteCertificate` check the ID with
  `core.CheckPathID` before any request. `GetCertificate` sends any value
  today. Upstream IDs look like `secret-<uuid>`, which the pattern allows.
- Import is `POST`: retried only after a 429 or a failed dial (ADR 0002
  rule 2). After a 5xx or a network error the certificate may exist; the
  error names `list-certificates --name <name>` and says to match the name
  exactly. Whether names are unique is a live check. A certificate found
  that way is complete, since the key went in the request; nothing is lost.
- Delete keeps the transport's retries. The CLI never retries a write.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, PEM shape, key with type `CA`, key text in a public field | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| A secret given other than by file, missing `--yes`, unreadable file | No request | `InvalidUsage`, 2 |
| Unknown certificate | `NotFound` | `NotFound`, 4 |
| Delete of a certificate in use, or the server's in-use refusal | `ErrCertificateInUse` | `ResourceInUse`, 1 |
| Bad name, expired or mismatched certificate, wrong passphrase, quota | The server's `*APIError`, redacted | 1 |
| 5xx or network error on import | The error; the message names the list | 1 |

`ResourceInUse` keeps the meaning [vServer network
writes](vserver-network-writes.md#errors) gave it; no new CLI code.

## CLI

| Command | Kind | `--yes` |
|-|-|-|
| `loadbalancer import-certificate` | Write | No |
| `loadbalancer delete-certificate` | Write, destructive | Yes |

```sh
vngcloud loadbalancer import-certificate --name example-com \
  --type TLS/SSL --certificate-file cert.pem \
  --certificate-chain-file chain.pem --private-key-file key.pem
```

- Every field comes from a file flag: `--certificate-file`,
  `--certificate-chain-file`, `--private-key-file`, and
  `--passphrase-file`. PEM text is multi-line and awkward in argv, and the
  key must never be in argv, where `ps` and shell history keep it.
- Flag reflection makes no flag for a `vngcloud.Secret` field. Without
  this rule it would make `--private-key`, because `Secret` is a string
  kind. The rule applies to every Input, so a later Secret field is safe
  by default.
- `--cli-input-json` may set `Name`, `Type`, `Certificate`, and
  `CertificateChain`. It is refused with exit 2, inline or `file://`,
  when it sets `PrivateKey` or `Passphrase`, as `monitor create-channel`
  refuses a literal webhook address: one input path for a secret is
  easier to review.
- File reads: at most 64 KiB each, following symlinks like any file
  argument. The key and certificate are sent as read. The passphrase drops
  one trailing newline (`\n` or `\r\n`), as `configure set <key> -` does.
  An empty file is refused. Errors name the flag and path, never content.
- The CLI does not check the key file's mode. It is the owner's file, and
  a refusal would push people to copy the key somewhere else.
- A [read-only](cli.md#read-only) profile refuses both writes with exit 2
  before any request.
- Delete is destructive: a deleted certificate needs its key again to
  re-import, and the key may no longer exist (ADR 0002 rule 6).
- Stdout gets the imported `Certificate`, which holds no key.

## Security

- The adversarial review checks: the key and passphrase never reach
  stdout, stderr, `--debug`, an error, a response capture, a fixture, or
  argv; the wire body holds the revealed key and the Input prints as
  `[redacted]`; no flag exists for a Secret field; `--cli-input-json`
  refuses the secret fields; `Redact` covers the message and code on every
  failing status, including after a retry; the `CA` rule keeps the key off
  the wire; path ID checks on get and delete; the in-use pre-read sends
  nothing; `--yes` on delete; read-only refusal of both writes; no import
  resend after a 5xx.
- The wiki says the key is sent to GreenNode, which then holds it, and
  shows `--private-key-file` with a key file only the owner can read.
- Certificate names, subjects, domains, serials, and IDs are account
  data. Fixtures use `<id>`, `<name>`, `<hostname>`, `<value>`, and
  `<secret>`.

## Testing

Unit tests use `httptest`.

- A sanitized raw fixture and decode test for the import response in
  `testdata/loadbalancer/`, with `subjectAlternativeNames`.
- Request bodies: `TLS/SSL` with key only; with chain and passphrase;
  `CA` with certificate only. The body holds the fixture key text, never
  `[redacted]`, and no empty optional key.
- Shape refusals with no request: no PEM block; key text in `Certificate`
  and in `CertificateChain`; a `CERTIFICATE` block as the key; `TLS/SSL`
  without a key; `CA` with a key, chain, or passphrase; passphrase without
  a key. No error holds the fixture key or certificate text.
- Redaction: a 400 whose message quotes the whole key, one line of it, its
  JSON-escaped form, and the passphrase; the error, `--debug` output,
  stdout, and stderr hold none of them.
- Secret: `fmt` verbs, `slog`, and `json.Marshal` of the Input give
  `[redacted]` for both fields.
- Delete: `InUse` true sends no `DELETE`; the server's in-use message at
  400 and 409 wraps `ErrCertificateInUse`; the not-found mapping the live
  checks choose.
- Statuses 201, 204, 400, 404, 409, and 5xx; no import resend after a
  502; a 201 without `uuid` fails.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on get and delete.
- CLI golden tests for both commands; the file flags; no `--private-key`
  or `--passphrase` flag exists; `--cli-input-json` inline and `file://`
  with `PrivateKey` refused; a missing, empty, or oversized file refused
  with no request; `--yes` on delete; read-only refusal.

## Live checks

The manager runs these on the test account in `hcm-3` before the code
merges, following [live data](../../instructions/live-data.md), with the
owner's approval naming the account, region, and resources. Runs log only
statuses, field names and types, booleans, and timings.

The test builds a throwaway self-signed certificate and key in the test
process with the standard library. The key lives only in memory and is
never written to disk or logged. Names are `vngcloud-live-<8 hex>`, which
fits the documented name pattern.

Read-only, first:

1. `list-certificates` and, when any exist, `GET` on one: the raw field
   names, and whether any key name contains `key`, `pass`, or `secret`
   (log field names only, never values).

Writes:

2. Import an RSA 2048 `TLS/SSL` certificate on the IAM gateway: status,
   response field names, `inUse`, `certificateType` value.
3. `GET` the new certificate: log a boolean for any field holding
   `PRIVATE KEY` text, and the field names.
4. Import the same name again: status and message (name uniqueness).
5. Import an ECDSA P-256 `TLS/SSL` certificate, and an encrypted key with
   its passphrase: accepted or refused. Import a `CA` certificate.
6. Import with a malformed key (a valid PEM header around random bytes)
   and with a key that does not match the certificate: status, and a
   boolean for whether the message holds any line of the sent key. The
   result decides [Error redaction](#error-redaction).
7. Delete each: status. Repeat delete: status. `GET` after delete: status.
8. The next day's bill shows no vLB line, and `get-balances` is
   unchanged.

Not checkable without a paid load balancer: the server's refusal of a
delete in use. The `inUse` pre-read covers it.

The live write test deletes leftover certificates whose names start with
`vngcloud-live-` and are not in use, registers `t.Cleanup` for each ID as
soon as it is known, and asserts none remain. If an import fails, it
lists by exact name and deletes a match. It never touches a certificate
without the prefix.

## Releases

One release: `ImportCertificate`, `DeleteCertificate`,
`ErrCertificateInUse`, the type constants, `SubjectAlternativeNames`, the
path ID check on `GetCertificate`, `transport.Request.Redact`, the CLI rule
that skips Secret fields, the file flags, and both commands. It is
numbered when it ships. No caller breaks; `GetCertificate` starts
rejecting a malformed ID. `Services.md` and `CLI-LoadBalancer.md` in the
wiki gain the writes, the file flags, and the retry advice. It ships after
the live checks and an adversarial review.

## Owner decisions

1. Secret type. Options: `PrivateKey` and `Passphrase` as
   `vngcloud.Secret`; plain strings. Recommend `Secret`: printing or
   encoding the Input then leaks nothing.
2. CLI secret input. Options: file flags only, refused in
   `--cli-input-json`; file flags plus `--cli-input-json file://`; stdin.
   Recommend file flags only: one path to review, and no JSON file holding
   a key is left behind.
3. Certificate and chain input. Options: file flags, also settable through
   `--cli-input-json`; plain string flags. Recommend file flags: PEM text
   in argv is error-prone.
4. `CA` with a key. Options: refuse a key, passphrase, or chain unless the
   type is `TLS/SSL`; send what the caller gives. Recommend refuse: a key
   the server does not use should not leave the machine.
5. Server error text. Options: redact the key, passphrase, and their
   lines (`Redact`); withhold the server message on every import error;
   trust the server. Recommend redact, with step 6 of the live checks
   deciding whether to withhold instead.
6. Delete guard. Options: `inUse` pre-read plus the server refusal; also
   scan every load balancer's listeners; server refusal only. Recommend
   the pre-read: the flag is the server's own answer, and a scan costs a
   request per listener.
7. Secret flag rule scope. Options: the CLI skips every `vngcloud.Secret`
   field in every Input; only these two fields. Recommend every field, so
   a later Secret input cannot become a flag by accident.

## Open questions

- Whether the IAM vLB gateway accepts the writes.
- Whether any read or error ever returns key text.
- Whether names are unique, and how a missing certificate reads.
- Which key types the server accepts (RSA, ECDSA, encrypted PEM).
- The server's message for a delete in use.
