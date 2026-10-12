# vStorage object operations checks

Status: Accepted for independent releases; implementation pending.

These checks verify the [design](storage-object-operations.md) and
[protocol](storage-object-operations-api.md). They are mechanism requirements,
not an implementation plan or authority for live operations.

## Deterministic protocol checks

Use synthetic credentials, raw synthetic XML fixtures, injected clocks, and
loopback HTTP/TLS servers. Label fixture provenance. Existing management JSON
and tests do not establish fresh S3 compatibility.

- Keep the existing published AWS GET/PUT signature vectors unchanged. Add
  the published [SigV4 bucket-list query vector][signing] with its exact
  canonical hash and signature. Independently assert actual RequestURI for
  prefix, delimiter, and continuation tokens containing spaces, Unicode,
  `%2F`, literal `+`, `/`, `=`, `&`, `?`, and `#`. Include UTC day rollover.
  An independent local signer comparison supplements, not replaces, vectors.
- Verify list addresses the bucket and HEAD/DELETE address the exact object.
  Preserve dot segments, repeated slashes, Unicode bytes, and literal percent
  sequences. Reject nil inputs, invalid bucket/key shapes, delimiter variants,
  invalid UTF-8, invalid page bounds, and oversize tokens before a request.
- Count exactly one application request per list, HEAD, or delete; a page with
  IsTruncated true does not fetch another page. The next invocation sends the
  exact opaque token under the same filters. Read replay before a response is
  distinct from automatic pagination or application retries.
- Exercise 200, 204, 206, 3xx, 400, 401, 403, 404, 409, 412, 429, and 5xx as
  appropriate to each operation. HEAD handles empty error bodies without GET.
  Verify operation names, safe codes, sentinel identities, context precedence,
  nil Outputs, and response closure on every path.

## Listing and HEAD defenses

- Decode empty, ordinary, prefix-only, delimiter, and truncated pages. Omitted
  KeyCount succeeds. Empty and short truncated pages require a usable next
  token. Reject missing, same, unsafe, or oversize next tokens, final pages
  carrying a next token, invalid echoes, and excessive combined entries.
- Decode URL-encoded keys once, leave `+` literal, and never decode tokens as
  URLs. Reject missing EncodingType, invalid escapes/UTF-8, empty keys,
  negative/overflow Size, missing required fields, duplicate singleton fields,
  and keys outside the requested prefix. Return no partial page after failure.
- Test XML at and above the byte and depth bounds, duplicate roots, wrong
  namespaces, nested scalar fields, external entities, directives, trailing
  content, unknown subtrees, malformed 200 bodies, and cancellation while
  reading. Ignoring unknown fields never bypasses whole-body/depth limits.
- HEAD uses no body download, length-based drain, or checksum request. Test
  absent, zero, huge valid, negative, and malformed Content-Length. Raw HTTP/1
  loopback responses with identical duplicate lengths succeed after net/http
  normalization; conflicting lengths and framing errors fail. Separately inject
  http.Response headers with observable duplicates and require rejection.
  A valid remote size above download limits remains inspectable through HEAD.
- Test every optional field's size limit, duplicate headers, invalid dates,
  user-metadata name/value filtering, deterministic metadata-budget omissions,
  absent encryption metadata, and diagnostic version IDs. No arbitrary header
  or response object can escape through formatting or JSON.

## Mutation invariants

- DELETE needs no HEAD, GET, or listing grant. A synthetic 204 for a missing
  key succeeds without a claim it existed. A returned 404 stays not found.
  Verify no versionId, If-Match, lock bypass, MFA, or retention headers appear.
  Synthetic enabled/suspended versioning responses only test header handling;
  they are not provider evidence of permanent deletion.
- Count mutation requests on failed dials, stale connections, disconnects,
  cancellation, 429, 5xx, unexpected 2xx, and malformed responses. No transport
  replay, application retry, verification DELETE, or rollback follows. Test
  zero-body DELETE as carefully as zero-byte PUT. Delete ambiguity uses its
  own sentinel and recovery text, never upload recovery advice.
- Copy with Replace false, invalid targets, identical targets, read-only
  policy, or missing CLI consent sends zero requests. Replace true is usable
  in both configured regions without a condition-capability lookup.
- Assert one bodyless destination PUT, zero Content-Length, the empty payload
  hash, and signed x-amz-copy-source. Verify source header and destination
  RequestURI preserve Unicode, spaces, percent sequences, controls, `+`, `?`,
  `#`, dot segments, and repeated slashes. No source text can inject a header,
  host, query parameter, version selector, or signature scope.
- Assert the copy sends no conditions, metadata directives, content headers,
  ACL, tags, encryption, retention, or legal-hold controls. Neither source nor
  destination is read first. No GET, HEAD, console call, multipart call,
  subprocess, spool file, or DELETE can occur on success or failure.
- Decode complete CopyObjectResult XML and optional version/encryption
  headers. Test missing ETag, empty body, duplicate fields, nested scalars,
  malformed/truncated XML, wrong namespace, multiple roots, and exact/over-limit
  64 KiB results. A 200 Error is never successful; no code accepts status alone.
  Apply the same depth/entity/trailing-data defenses as listing.
- Test filtered or over-limit result ETag after valid shape: the accepted copy
  remains successful with ETag omitted. Invalid optional timestamps and unsafe
  version/encryption headers are omitted without inventing replacement values.
  Output contains no fabricated content length, local SHA256, or source ETag.
- Count copy requests under a stale connection, failed dial, disconnect, 429,
  501 NotImplemented, other 5xx, embedded 200 Error, cancellation, and deadline
  during result reads. ErrObjectCopyUnconfirmed preserves the safe APIError
  with operation storage.CopyObject; definitive 4xx retains status mapping.
  A 412 without a sent destination condition never becomes ErrObjectExists.
- Model source and destination changes during copy without a preflight HEAD.
  Assert the request retains expressly authorized unconditional replacement
  and never deletes the source. Tests prove client dispatch and reporting, not
  undocumented provider source-snapshot or metadata-preservation behavior.

Create-only copy checks apply only after its separate condition contract is
approved. A mock honoring If-None-Match cannot establish GreenNode support.

Move checks require a concrete approved source and destination safety mechanism.
Model a source rewrite after copy, an equal-ETag metadata rewrite, null version
identity, and destination overwrite/delete before source removal. Prove deletion
targets only the copied immutable generation or refuses removal. Test retained
destination versions only if that guarantee is selected. Inject every failure
between phases and assert exact copy/delete states, safe causes, nil failure
Output, exit 1, and no automatic cleanup. An unsafe mock that always honors
If-Match cannot stand in for the provider evidence this mechanism needs.

## Credential and CLI checks

Reuse transfer endpoint, redirect, proxy, certificate, credential-file, and
read-only tests. Assert no IAM login, management request, token-cache access,
environment credential chain, or subprocess occurs. Refusals happen before
credential loading or any request. Preserve Linux/macOS support and unsupported
platform refusal without weakening the safe credential reader.

Reflect both synthetic credentials literally and JSON-escaped in every new
output location: key/prefix, token, scalar headers, dates, numeric text,
metadata names and values, and XML errors. Include short credentials and
percent/XML-encoded reflections. Optional fields disappear wholly; unsafe
required identities cause a fixed failure. Inspect direct SDK access, `%+v`,
`%#v`, JSON, every CLI format and query, errors, and debug. Content remains
opaque and never printed. Keep the existing nested-encoding limitation explicit.

For List and Copy, use an offset RFC 3339 source that lacks the synthetic
credential but normalizes to it in UTC; use an HTTP-date source for HEAD.
Exercise credentials matching each supported normalized time representation,
including fractional seconds and text/JSON marshaling. Assert nil through
direct SDK access and credential absence in `%v`, `%+v`, `%#v`, JSON, every CLI
format, and direct field queries. JSON null omits the optional time value.
Safe timestamps retain their UTC values. Unsafe Copy timestamps do not revoke
confirmed success or cause retries. Also test a metadata suffix whose lowercase
form matches a credential absent from its source spelling; omit the whole pair.

Test all flag/profile/environment read-only sources, missing consent,
`--yes` without `--replace`, explicit global-region/project misuse,
`--cli-input-json` rejection, output/query prevalidation, one-page envelopes,
control-character escaping, all exit classes, and stdout failure after a
confirmed mutation. Help must state server-side copy, replacement consent,
same-project scope, metadata/encryption limits, delete versioning effects, and
move unavailability. Future generated help and wiki must agree with the slice
actually enabled.

## Provider evidence and release

Reuse the [recorded facts and scoped gates][evidence]. Authoring this design
does not authorize API calls. The manager owns any necessary live checks under
[live data](../../instructions/live-data.md) and
[live verification](../../instructions/testing.md#live-verification).

For implementation verification, first reuse an existing sanitized raw response
whose provenance and shape match. If unavailable, request only the authorized
observations needed by that slice: a small multi-page list with prefix/delimiter
and empty result, HEAD of an existing and missing synthetic key, one delete,
or a tiny native copy with byte comparison and retained metadata observations.
Ordinary read verification needs no destructive copy or move probe.
Mutation verification names
the region, resources, finite bytes, costs, and cleanup authority. Do not buy
resources or run every combination of regions/settings for a matrix.

Capture raw shapes privately, sanitize before fixtures, and label any region
without a live observation as published compatibility. Record actual failures
instead of widening conditions or silently normalizing keys. Reuse the existing
approved project where authorized; serialize storage live calls. Clean only
resources created by that run through authorized tooling, including versions
and markers where applicable. Report leftovers rather than broad cleanup.

Independent design and implementation-plan review precede code. Completed
mutation and credential code needs independent adversarial review of races,
resends, ambiguous outcomes, endpoint trust, output leaks, and limits. An
author's self-review does not count. Run the repository's required checks when
implementation begins and before the manager commits, with exact-commit release
evidence. Release security products remain gitleaks secrets plus Semgrep
Code/SAST and Supply Chain/SCA; add no separate Secrets product gate.

[signing]: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
[evidence]: storage-object-operations-api.md#evidence-gates
