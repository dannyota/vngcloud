# vStorage object transfer protocol and evidence

Status: Accepted. Implementation is pending. This specification supports
[vStorage object transfers](storage-objects.md). Public S3 documentation
specifies signing and REST shapes; it does not establish that GreenNode
implements every S3 behavior. No new live results are claimed here.

## Evidence and sources

The project has live HCM04 evidence for console-created S3 keys, path-style
Signature Version 4 requests, and object PUT/GET with bucket-policy scoping in
[Data plane](storage-api.md#data-plane). The in-repository test helper
`livetest/s3signer_test.go` implements a standard-library signer and checks an
AWS published GET signature vector. It is a test helper, not a reviewed public
streaming transport. Production code must not import `livetest`.

[Storage settings](storage-settings.md) records HCM04 compatibility checks:
create-only PUT with `If-None-Match: *` returns 200 for a new key and 412 for an
existing key; matching quoted `If-Match` fails; unquoted matching ETag succeeds.
It also records multipart and listing observations. Those facts do not justify
adding multipart, listing, or conditional replacement here. The
`livetest/live_write_bucket_encryption_test.go` source exercises the create-only
condition. Test source alone is not proof of a new live run.

GreenNode's [HCM04 S3 integration guide][gn-integration] and [HAN02 S3
integration guide][gn-han] describe project-selected access and secret keys and
language-specific SDK setup through the console. The [HCM04 usage guide][gn-use]
gives file PUT examples and warns that incomplete multipart uploads consume
space. These are published compatibility claims; they do not document exact
signer region casing, conditional-write races, content-length behavior, maximum
accepted single PUT size, or payload-hash validation. The [rclone integration
guide][gn-rclone] describes another client setup, not an authorization to
execute a subprocess here.

AWS's [SigV4 single-payload specification][aws-sign] supplies the canonical
request algorithm and known signature examples. Its [authorization
overview][aws-auth] distinguishes hashed payloads from unsigned and chunk-signed
payloads. [PutObject][aws-put] specifies whole object writes, conditional
creation, and response headers. [GetObject][aws-get] specifies a body and object
metadata. Apply only the narrow subset below. AWS's [upload guide][aws-upload]
states a 5 GB single-PUT limit. The accepted 5,000,000,000-byte upload cap is
deliberately conservative about units. AWS's limits and guarantees are not
GreenNode evidence.

[gn-integration]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/3rd-party-softwares/s3-sdk/integrate-s3-sdk-with-vstorage.md
[gn-han]: https://docs.greennode.ai/vstorage/object-storage/object-storage-han02/3rd-party-softwares/s3-sdk/integrate-s3-sdk-with-vstorage.md
[gn-use]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/3rd-party-softwares/s3-sdk/using-s3-sdk.md
[gn-rclone]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/3rd-party-softwares/rclone/integrate-rclone-with-vstorage.md
[aws-sign]: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
[aws-auth]: https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-auth-using-authorization-header.html
[aws-put]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html
[aws-get]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html
[aws-upload]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/upload-objects.html

## Endpoint and authentication boundary

Use only the configured region's trusted host: `HCM04` maps to
`https://hcm04.vstorage.vngcloud.vn`, and `HAN02` maps to
`https://han02.vstorage.vngcloud.vn`. These hosts appear in the recorded
`testdata/storage/list_regions.json` fixture. Keep TLS certificate and host
verification enabled. The signing scope is `<UTC date>/<exact
region>/s3/aws4_request`. HCM04 has recorded live object evidence. HAN02's exact
signing-region casing is inferred from its recorded region metadata and
published S3 compatibility, not live-verified. Do not label HAN02 object
behavior as live-verified. Do not send management Bearer tokens, `region`,
`region_id`, project IDs, cookies, or service-account client credentials to this
host.

`ObjectClient` owns its HTTP client and transport. No shared management
transport, auth refresh, token cache, or capture hook sees object traffic. Use
system trust roots, no insecure TLS switch, and no custom public RoundTripper.
Ignore HTTP proxy environment variables in this slice; proxy support needs a
separate explicit design. Do not consult netrc or install a cookie jar. Disable
transparent decompression so bytes and lengths stay those of the stored object.
Use a bounded response-header budget of 32 KiB, a 30-second dial timeout, a
10-second TLS handshake timeout, and a 30-second response-header timeout within
the overall operation deadline.

Reject every redirect, including same-host and HTTP-to-HTTPS redirects. Never
copy Authorization to a redirected request, re-sign another host, change region
from an error header, or fetch a server-supplied URL. A 3xx is an APIError.
Internal test construction may supply loopback HTTPS roots and transport seams,
but public configuration cannot enable them.

## Addressing and key bytes

Use path-style `/<bucket>/<key>`. The bucket uses the existing storage bucket
validator. This avoids creating a new pattern for an already supported path
component. Reject a nil input, empty bucket/key, invalid UTF-8 key, or key
exceeding 1,024 UTF-8 bytes before dispatch. The key cap is a deliberate client
bound. Do not apply the management path-ID pattern to object keys.

Treat the key as data, never as a URL or local filesystem path. Spaces, Unicode,
`%`, `+`, `?`, `#`, repeated slashes, leading/trailing slashes, and `.`/`..`
segments retain their meaning. Percent-encode UTF-8 bytes except RFC 3986
unreserved bytes and key slashes, with uppercase hex. A literal `%2F` is encoded
as `%252F`. Spaces become `%20`, never `+`. Do not URL-decode input, clean
paths, resolve references, collapse slashes, or normalize Unicode. Construct the
escaped URL path and canonical URI from the same bytes and assert the actual
HTTP RequestURI in tests.

Control bytes in a valid UTF-8 key are percent-encoded on the wire, never
inserted into headers. CLI text metadata escapes them; error messages never
include keys. A server refusing an unusual key returns a bounded APIError, not a
fallback request to a normalized key. No query string, version ID,
response-header override, or fragment is accepted in this slice.

## PUT request and response

Send one `PUT /<bucket>/<key>` with:

- Exact `Content-Length`, including zero for an empty object.
- `Content-Type: application/octet-stream`.
- `x-amz-date` from the UTC signing clock and
  `x-amz-content-sha256` containing the lowercase hex SHA256 of the payload.
- `If-None-Match: *` when `Replace` is false, omitted when true.
- The `AWS4-HMAC-SHA256` Authorization header.

Sign host, content type, both `x-amz-*` headers, and the conditional header when
present. Use the computed payload hash in the canonical request. The transport
owns Content-Length framing; tests assert zero-length bodies and prohibit
chunked framing. The signed-header list is sorted and lowercase; canonical
header values follow the AWS whitespace rules. Derive the signing key from date,
exact region, `s3`, and `aws4_request`. Never expose signing keys,
Authorization, canonical requests, or strings to sign through debug.

Hash from offset zero with fixed-size buffers. Verify declared length using at
most one extra byte, rewind, then transmit at most the declared length. No
entire-object allocation, unsigned payload, `aws-chunked`, trailer, checksum
negotiation, or multipart machinery exists. Do not set GetBody or an idempotency
header. Ensure the transport cannot transparently replay PUT, including a
zero-byte PUT on a reused connection. One explicit dispatch must mean at most
one request reaching the server.

Accept HTTP 200 only. A successful PUT body must be empty, consistent with the
required fixture check. Read at most 1 KiB plus one byte to establish that;
nonempty or oversize success data is a protocol error with an unconfirmed write
outcome. Extract optional ETag, bounded to 1 KiB, without assuming its shape.
Apply the [success-metadata filter](#success-metadata) before exposing ETag.
Missing or filtered ETag does not invalidate an otherwise complete 200. Do not
return raw headers or a response body. A 412 on create-only PUT matches
`storage.ErrObjectExists`; no replacement attempt follows.

No read-before-write or success-confirmation GET runs automatically. PUT
requires `s3:PutObject` only within the intended bucket policy. The server may
still deny a key based on its actual policy. The SDK does not widen permissions,
alter ACLs/encryption, or change a bucket setting.

## GET request and response

Send one `GET /<bucket>/<key>` without a body. Sign the host, `x-amz-date`, and
`x-amz-content-sha256` using the SHA256 of an empty payload. GET never becomes
HEAD or a range/resume request. GET requires `s3:GetObject` on the selected key
and no listing grant.

Accept HTTP 200 only. Reject 206, redirects, malformed length, or a declared
length over the selected MaxBytes before returning a body. Missing
Content-Length becomes -1 and uses a bounded stream reader, including for
chunked responses. Capture only Content-Length, optional ETag up to 1 KiB, and
optional Content-Type up to 256 bytes. Apply the [success-metadata
filter](#success-metadata) before exposing these strings. Reject control bytes
in retained display metadata. Ignore Content-Disposition entirely; it cannot
choose a local filename. Never decompress, execute, or sniff content. A
zero-byte body is valid.

Expose a closing body wrapper that counts bytes and reports a short body when a
length is known, a size-limit violation, network interruption, deadline, or
cancellation. Consume through EOF; do not report success after only reading
headers or the expected number of bytes. Enforce the size cap even if the
underlying transport changes. A failed SDK read can have yielded bytes already;
SDK callers discard their partial destination. The CLI's temporary file contract
prevents publishing those bytes. Close releases the network body and operation
timer.

An ETag retained by the metadata filter is opaque. Do not strip its quotes,
equate ETag with MD5, or infer encryption from it. This slice requests no
checksum mode and promises no independent server checksum verification. TLS
protects the channel; local SHA256 supports comparison with a trusted local
source. Signature payload hashing does not prove a downloaded object matches
that source.

## Success metadata

Treat every server-derived success field as untrusted. Before constructing an
SDK output, compare each bounded string field against the shared
credential-redaction policy using both configured AccessKeyID and
SecretAccessKey. This includes ETag and ContentType. The shared policy covers
literal and JSON-escaped credential values and its sensitive-key markers. If
filtering would change a field, discard that entire optional field and expose
the empty string. Never return a partly redacted ETag as though it were the
server's value. Validate numeric fields without retaining or echoing their
original header text.

The SDK returns only accepted metadata. SDK field access, formatting (including
`%+v` and `%#v`), and JSON must not retain or reveal the rejected value,
original headers, request credentials, or body internals. The CLI uses only
these filtered SDK fields in every output format and query. CLI metadata
assembled from local inputs follows the same credential filter; filtering does
not change the request key or local destination. Debug and errors cannot emit
the discarded text. The existing security policy's limits on crafted nested
encodings still apply; no guarantee covers arbitrary transformed encodings
chosen by a hostile peer.

Omitting credential-bearing optional metadata is not a transfer failure. An
otherwise valid PUT 200 still returns its normal output, with empty ETag, and no
APIError or `ErrObjectWriteUnconfirmed`. GET returns its body and accepted
metadata normally, and completion still requires clean EOF. Existing protocol
failures keep their APIError and unconfirmed-write rules; optional metadata
filtering adds neither a retry nor a new error sentinel.

## Errors, redaction, and retries

Read at most 64 KiB plus one probe byte for an error body. Parse only a bounded
S3 XML Error/Code; disable external resolution and reject malformed XML or extra
documents. Unknown elements are ignored without retaining values. Never expose
Message, Resource, HostId, RequestId, raw body, Location, Authorization, object
bytes, credentials, or raw transport errors. Use local messages based on status
and allowlisted S3 error codes. A missing, unknown, invalid, or oversize code
becomes a local `S3Error` code. HTTP status still determines shared
permission/not-found/rate-limit sentinels.

Recognized codes are `AccessDenied`, `InvalidAccessKeyId`,
`SignatureDoesNotMatch`, `RequestTimeTooSkewed`, `NoSuchBucket`, `NoSuchKey`,
`PreconditionFailed`, `SlowDown`, `InternalError`, and `ServiceUnavailable`. Do
not use arbitrary server text as a code. HTTP 412 on create-only PUT uses the
local `ObjectExists` code even if the XML is empty. A 409 remains an ordinary
conflict error; AWS's 409 description is not a verified GreenNode response. No
message-based sentinel matching occurs.

No automatic retries run for PUT or GET in this slice. PUT can create versions
even when the bytes are unchanged. GET retry after partial output would need
restart/resume rules. Go transport behavior must not add hidden PUT replays;
GET's connection-level replay before a response is acceptable because GET
changes no remote state and no bytes have reached the caller. The CLI never
retries either operation.

A definitive pre-dispatch validation, hash, or dial failure has no remote write.
Every failure after PUT may have reached the server is conservatively
unconfirmed unless a definitive 4xx refusal establishes no accepted write. Treat
5xx, malformed 200, response loss, and post-dispatch cancellation as
unconfirmed. Preserve the APIError's status but wrap
`storage.ErrObjectWriteUnconfirmed` and say to download and compare before
retrying. For these outcomes `Retryable` is false. A definitive failed dial or
429 may set `Retryable` true as caller guidance; no request is retried.

Debug output is restricted to operation, elapsed time, status, byte count, and a
local safe error code. It excludes the URL, bucket, key, source and destination
contents, credential values, headers, and response bodies. Credential/config
structs redact both access ID and secret under formatting and JSON through
`vngcloud.Secret`. Errors from caller-supplied readers, writers, and network
failures must not bypass that policy.

## Required tests

Deterministic tests use synthetic credentials and loopback servers only:

- Reproduce AWS's published GET and PUT signatures exactly. Check path-style
  canonical requests, UTC day boundaries, empty-body hash, and signed
  conditions. Add actual RequestURI tests for Unicode, `%2F`, `+`, spaces,
  `?`, `#`, controls, dot segments, and repeated/leading/trailing slashes.
- Assert a native signer request matches the test helper's simple supported
  cases without treating agreement between two local signers as a vector.
- Exercise zero, ordinary, boundary, and oversize transfers with generated
  readers. Verify constant-memory behavior, two-pass hashing, length/seek
  failures, context cancellation, a mid-body disconnect, and deadline
  coverage after GetObject returns.
- Test 200, 206, 3xx, 400, 403, 404, 409, 412, 429, and 5xx outcomes. Use
  empty/malformed/oversize XML, malicious codes, reflected credentials,
  hostile headers, and unexpected nonempty PUT success bodies. Verify
  secrets and object contents never enter output or debug logs.
- Count PUT requests after disconnects, 429, 5xx, zero-length input, and
  stale keep-alive connections. No transport resend may reach the server.
  Test that GET failures publish no file and do not trigger CLI retries.
- Reject proxy environment use, bad certificates, and every redirect before
  credential forwarding. Assert no IAM login, management endpoint call,
  token-cache access, metadata credential lookup, or capture hook runs.
- Exercise credential mode/owner checks, symlinks and swaps, FIFOs, duplicate
  INI fields, oversize files, and unsupported platforms without credential
  disclosure or a request.
- Exercise CLI read-only sources, missing consent, explicit global-region
  misuse, unsupported flags, and metadata query validation before requests.
  Verify replacement is opt-in and `--yes` alone stays create-only.
- Inject disk-full, sync/close/link/rename/unlink failures, destination races,
  existing/dangling symlinks, and cancellation. Preserve old destinations,
  clean temporary files, distinguish published-but-cleanup-failed output,
  and never truncate a raced path. Test Linux/macOS and fail-closed Windows.
- Reflect each credential literally and JSON-escaped in PUT ETag and GET
  ETag/ContentType, including embedded values and short credentials. Assert
  the whole optional field becomes empty while otherwise valid PUT/GET
  succeeds. Check direct SDK fields, `%+v`, `%#v`, JSON, every CLI output
  format, queries, errors, and debug. Preserve safe quoted ETags unchanged,
  expose no body internals, and send no retry after filtering.
- Check constructor sentinel identities and nil clients on failure, nil
  Outputs on method failure, and GET response-body cleanup. Check Body.Read
  APIError operation/status/code and `errors.Is` for unexpected EOF and
  context failures; keep partial output unpublished.
- Check shared exit precedence: 401 and credential-file failures exit 3,
  404 exits 4, 403 and 412 exit 1, invalid input exits 2, and wrapped context
  cancellation exits 1. Unconfirmed-write messages and sentinels survive.
- Verify generated help/reference, exit codes, metadata-only stdout, safe
  table rendering, and a stdout failure after a successful transfer.

Reuse raw, sanitized protocol fixtures only with their provenance stated. Label
synthetic adversarial replies as synthetic; do not manufacture live captures.
Existing JSON management fixtures do not prove S3 XML parsing. Run normal
repository checks and the focused transfer/security tests under an independently
reviewed implementation plan. No tests in this document have been run as part of
writing the design.

## Release evidence

Reuse the recorded HCM04 data-plane results, storage-settings observations, and
known signature vector. These establish the selected access-key flow, path-style
hashed PUT/GET, and existing-key conditional refusal. A new client
implementation does not invalidate that evidence or require repeating every
compatibility probe before code can start. Public GreenNode S3 guidance and the
recorded HAN02 region metadata support ordinary GET and explicit whole-object
PUT there. Label that basis as published compatibility, not a live result.

One unresolved behavior changes a safety guarantee: whether HAN02 enforces
`If-None-Match: *` rather than ignoring it. Before enabling create-only PUT
there, require a targeted manager-run probe: new key succeeds, repeated key
returns 412, and GET proves the first bytes remain. A concurrent two-writer
probe adds confidence in race handling. Until established, HAN02 supports GET
and explicit replacement only; default PUT refuses before dispatch. No automatic
fallback to unconditional PUT is allowed. HCM04 create-only PUT, and ordinary
transfers in either region, do not wait on that probe.

Create-only means conditional creation against the server's current object, not
permanent reservation of a key name. Deletion and current delete markers can
affect whether a key exists. Version-history and delete-marker semantics are
outside this slice; do not claim stronger behavior from the 412 evidence. No
fresh live versioning experiment is required to offer simple transfers.

Implementation verification uses the repository's required live checks and
sanitized fixture procedure. Prefer existing captures that establish the needed
response shape. When new observations are needed, the manager runs a small
authorized binary round trip with the public client: empty and nonempty files,
create-only refusal where enabled, explicit replacement, and GET byte
comparison. Reuse an existing approved test project. Do not provision a paid
project merely to satisfy a new test matrix.

Treat unusual-key examples as a standards-based client encoding contract. Test
exact request bytes locally. Record any observed server rejection as an APIError
rather than silently normalizing the key. A targeted live probe is needed only
if evidence contradicts that encoding or a release claims specific unusual-key
compatibility. The same rule applies to optional metadata: absent ETag and
absent Content-Length already have explicit client behavior and do not block
ordinary transfers.

The 5 GB PUT cap remains a client compatibility bound, not a verified GreenNode
maximum. A large boundary upload needs separate resource and cost approval; it
is not a mandatory precondition for shipping bounded streaming of ordinary
files. Unit tests exercise the byte limits with generated readers. Publish the
untested server-size limit honestly. Download bounds are caller safety controls,
not claims about server-side maximum sizes.

Every new live run needs authority naming the account, region, resources,
maximum bytes, possible storage/traffic cost, and cleanup. Serialize it under
the repository's storage live-run rules. Use synthetic non-sensitive bytes, keep
captures private, and never expose a key secret. Clean created objects/versions
plus temporary keys and buckets through existing approved test tooling. This
public slice does not gain deletion to support its tests. Report leftovers
instead of broad cleanup. Fault injection covers ambiguous writes locally; no
live forced network fault is required.
