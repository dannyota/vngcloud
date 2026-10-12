# vStorage object operations

Status: ListObjects, HeadObject, DeleteObject, and CopyObject implemented.

`storage.ObjectClient` supports bounded listing, metadata lookup, single
object deletion, and direct server-side copying.
Automatic move and rename remain gated on safe source removal. The
[protocol and evidence][api] and [mechanism checks][checks] govern the accepted
behavior.

## Scope and approach

Preserve the implemented [transfer contract](storage-objects.md), including
its separate S3 client, credentials, endpoints, redaction, and no-retry rules.
Console management remains on `storage.Client`. Add no SDK dependency, external
executable, management login, or implicit policy change.

Three practical copy approaches have different consequences:

1. Native S3 CopyObject keeps content on the service and uses its metadata-copy
   behavior. The owner selected direct copy with explicit unconditional
   destination replacement first; default refusal avoids promising an unproved
   create-only condition. Encryption limits remain visible compatibility facts.
2. Client download/upload changes traffic, storage, and metadata behavior.
   It is not selected and is not a mode or fallback for this operation.
3. Wrapping rclone or importing an AWS SDK adds deployment or dependency costs
   without establishing GreenNode's conditions. Neither resolves safe deletion
   after copy. Keep them outside the native SDK design.

Copy sends no object content through the caller and creates no local spool.

Exclude bulk deletion, wildcard expansion, recursive copy, sync, multipart,
presigned links, ACL edits, metadata edits, versions administration, retention
bypass, and cross-region or cross-project transfers. A slash-ending key is one
object, not a directory operation. No new bucket or key provisioning belongs
here.

## Shared boundaries

Use `ObjectConfig` unchanged. Its explicit credentials and exact `HCM04` or
`HAN02` region select one trusted endpoint. Preserve the private HTTP transport,
TLS verification, redirect refusal, proxy exclusion, and credential filtering
in [the transfer protocol][boundary]. No server field selects another host.

All object identifiers use the existing bucket and UTF-8 key rules. Source and
destination keys are literal strings, never URLs. Copy takes one credential
pair and one region. Source and destination must belong to the same project,
consistent with the provider's published scope. No console lookup is added to
prove project membership; the caller selects that scope and the service owns
authorization. Identical bucket and key pairs are invalid for copy and move,
even with replacement consent.

Each method keeps the SDK signature `Method(ctx, *Input) (*Output, error)`.
Inputs and outputs use Go field names. A nil Input is invalid. Invalid shapes
wrap `vngcloud.ErrInvalidInput` before any request. SDK calls never prompt or
enforce CLI read-only policy. A method error returns nil Output.

`ObjectConfig.Timeout` covers the entire operation, including result decoding.
Its five-minute default, one-hour maximum, and earlier caller deadline remain.
There are no application retries, waiters, or automatic confirmation reads.
Mutation transports must not replay requests. Read connection-level replay
before a response remains permissible under the transfer rules.

## ListObjects

`ListObjectsInput` has required `Bucket string`, optional `Prefix string`,
`Delimiter string`, `ContinuationToken string`, and `MaxKeys int`.

- Empty prefix selects all keys. A nonempty prefix is valid UTF-8 of at most
  1,024 bytes, using exact byte matching without path normalization.
- Delimiter is empty or `/` in this narrow surface. Empty returns keys without
  grouping; `/` groups descendants into common prefixes. This is a client
  scope restriction, not a claim that the server accepts no other delimiter.
- MaxKeys zero selects 1,000; positive values must be 1 through 1,000.
- ContinuationToken is opaque UTF-8, at most 8 KiB, without control bytes.
  Empty starts a listing. Never parse, trim, or URL-decode a token supplied by
  the caller. Use a returned token with the same bucket, prefix, and delimiter.

`ListObjectsOutput` has `Items []ObjectSummary`, `CommonPrefixes []string`,
`MaxKeys int`, `IsTruncated bool`, and `NextContinuationToken string`.
MaxKeys is the effective requested bound, not a total. Empty slices are non-nil.
`ObjectSummary` has `Key string`, `Size int64`, `ETag string`,
`LastModified *time.Time`, and `StorageClass string`. Size is required and
nonnegative; optional metadata may be omitted by the safety filter.

Return one complete ListObjectsV2 page only. The combined number of Items and
CommonPrefixes cannot exceed MaxKeys. No automatic pagination, integer page
number, total count, `--all`, or iterator is introduced. Only IsTruncated and
its validated next token govern continuation; a short page can still continue.
Reject an unusable or unchanged next token on a truncated response. A final
page has an empty next token. Preserve server order without claiming a snapshot
across pages. Concurrent changes may cause omissions or repeats across calls.

## HeadObject

`HeadObjectInput` has required `Bucket string` and `Key string`.
`HeadObjectOutput` has `ContentLength int64`, `ETag string`,
`ContentType string`, `LastModified *time.Time`, `Metadata map[string]string`,
`VersionID string`, and `ServerSideEncryption string`.

This sends HEAD only and never downloads content as a fallback. A missing
length is -1; zero means an empty object. The other fields are bounded,
filtered observations, not integrity or encryption attestations. Metadata is
a non-nil map of accepted user metadata with lowercase keys and the
`x-amz-meta-` prefix removed. Unsupported, unsafe, or oversize optional fields
are omitted; the output is not a lossless metadata export.

VersionID is diagnostic only. It cannot be supplied to any method in this
extension. A current delete marker or missing object follows the returned HTTP
error. Never convert a 403 to absence or use a failed HEAD to authorize writing.

## DeleteObject

`DeleteObjectInput` has required `Bucket string` and `Key string`.
`DeleteObjectOutput` has `DeleteMarker *bool` and `VersionID string`, taken
only from accepted response headers. Nil DeleteMarker means not reported;
empty VersionID means absent or filtered. Neither field proves prior existence.

Delete the current name once, unconditionally. Do not HEAD first, send an
`If-Match` promise, select a version, or verify absence afterward. A successful
204 means the server accepted deletion, including deletion of an already
missing name. A returned 404 stays `ErrNotFound`; do not suppress a missing
bucket or invent a distinction without response evidence.

This intentionally deletes whatever current object the server targets at
dispatch. It does not protect concurrent writers. A user who reviewed metadata
earlier has not locked that object. Deletion is independently useful under
explicit destructive consent; it is not suitable as the final step of an
automatic safe move.

The [S3 versioning model][delete-wire] can retain old versions, create a delete
marker, or remove a null version in a suspended bucket. Do not promise permanent
erasure, reclaimed quota, or an empty bucket. No version enumeration, permanent
version deletion, lock bypass, or bucket-setting lookup occurs. Repeating a
delete can create another marker or delete a newly written object.

Add `storage.ErrObjectDeleteUnconfirmed` for a delete that may have happened.
It wraps a safe APIError and any safe context sentinel. Its fixed recovery
message says to inspect the current key and version history before deciding
whether to delete again. Absence now does not prove the earlier request's exact
effect. Do not reuse the upload sentinel's download-and-compare advice.

## CopyObject

`CopyObjectInput` has required `SourceBucket string`, `SourceKey string`,
`Bucket string`, and `Key string`, plus `Replace bool`. Bucket and Key name the
destination. There is one implementation: a native S3 CopyObject request.

Require Replace true for the initial release in both regions.
Replace false, including the zero value, wraps ErrInvalidInput before any
request and says native create-only copy is unavailable. It never becomes
unconditional implicitly. CLI copy requires `--replace --yes`. This consent
permits replacing any current destination, including one created or changed
concurrently; it also permits creation at an empty name. Neither a destination
HEAD nor `--yes` alone authorizes replacement.

Default refusal is independent of ordinary replacement support. GreenNode's
published CopyObject and recorded HCM04 plain-copy evidence support this
expressly consented operation. Do not wait for destination-precondition proof
to implement it. PutObject's regional create-only capability does not establish
CopyObject's and does not restrict this unconditional request.

`CopyObjectOutput` has `ETag string`, `LastModified *time.Time`,
`VersionID string`, `SourceVersionID string`, and `ServerSideEncryption string`.
All are bounded, filtered server metadata. Version identifiers are diagnostic,
not accepted inputs or evidence sufficient for automated cleanup. No local
checksum or content length is invented for a server-side transfer.

Send one request and read its complete bounded result before success. Do not
HEAD either target, download content, select a source version, or delete the
source. Copy uses the current source selected by the service during execution.
It is not a copy of an earlier HEAD observation. A concurrent source change can
change which object is copied; success does not lock either name afterward.
No version history is copied. Destination versioning may create another version
or replace a null version; output version IDs are observations only. A missing
source or current delete marker follows the service's error response.

Use the native default metadata behavior without replacement headers or
metadata directives. The conservative S3 reference expects user metadata and
ordinary content headers to be copied. GreenNode's guides do not enumerate
every preserved field. Do not promise a lossless metadata clone, retained ACL,
tags, retention, redirect, storage class, or identical encryption. The client
sends no overrides for those settings and does not fetch and replay headers.

In recorded HCM04 behavior, encrypted sources return 501 NotImplemented and an
unencrypted-source copy can remain unencrypted despite destination encryption
being enabled. State this in help and SDK comments. No encryption inheritance
guarantee belongs to this copy contract; workloads requiring it need separate
provider evidence. HAN02 encryption behavior remains unverified. A refusal
never triggers a content transfer or changes bucket encryption.

This is single-request copy without multipart. AWS documents a 5 GB boundary;
GreenNode's exact limit is unverified. Let the service enforce its accepted
size and report refusal without splitting or fallback. No client content-size
cap can be enforced by a racy source HEAD. Memory is bounded by the response
limits, independent of object size; the operation deadline covers result reads.
Possible storage and version costs remain subject to the user's service terms.

Add `storage.ErrObjectCopyUnconfirmed` for a copy that may have happened. It
wraps a safe APIError and any safe context sentinel, with fixed advice to
inspect the destination before deciding to repeat the copy. A missing or
malformed result, embedded error under 200, timeout, or response loss cannot
trigger retry or source deletion. An unconditional rerun can replace another
writer or create another version. No rollback DELETE runs.

## Move and rename: gated composite

Recommend `MoveObject` and `storage move-object` as the one eventual operation;
rename is a move within the same bucket. Do not introduce an alias or a second
SDK method. Do not publish a command that performs a copy but calls it a move.

The candidate `MoveObjectInput` uses CopyObjectInput's object identifiers and
Replace field. Version selection needs a separate explicit decision below.
`MoveObjectOutput` contains `Copy CopyObjectOutput` and
`Delete DeleteObjectOutput`. Successful return would mean both phases were
confirmed, not atomic visibility or permanent removal of all source versions.
This candidate surface remains unshippable until the [source identity
gate][move-gate] has a concrete reviewed protocol. No stub API is required.

A safe composite must bind copied content and metadata to a source generation,
then atomically refuse deletion if that generation changed. Comparing HEAD
before and after copy leaves a race before DELETE. ETag alone need not identify
metadata or a generation: equal content can be rewritten under the same ETag.
HCM04 ignores DELETE If-Match outright. HAN02 has no evidence establishing the
needed condition. User assertions of exclusive access are not a server guard.

The narrowest candidate is copy and deletion of the same immutable, non-null
source version while preserving any newer version. Provider documentation
establishes versionId deletion but not the complete identity path. The owner
must choose whether version-scoped movement, which can expose an older source
version, satisfies the request. If so, explicit SourceVersionID input and its
permissions need an approved contract after [targeted evidence][move-gate].
An absent or literal `null` version and an ETag are not immutable identity.
Otherwise keep ordinary name move gated on an atomic source-generation guard
or native rename. No version API is added to the independent releases.

Destination replacement consent does not freeze the destination. Another writer
can overwrite or delete the copy before source removal. A second HEAD cannot
prevent that race. A source-version guard protects newer source versions; it
does not promise the destination remains available. If move must preserve a
recoverable copy against concurrent destination changes, it also needs retained
destination-version evidence or a server transaction. This guarantee and the
version-scoped semantics are part of the remaining owner decision. No force
flag or automatic cleanup may bypass unresolved source safety.

For a future enabled composite, add `ErrObjectMoveIncomplete` and a safe
`*MoveObjectError` exposing `CopyState string` and `DeleteState string`, with
only `not-attempted`, `refused`, `confirmed`, or `unconfirmed`. Unwrap only safe
causes; return nil Output on error. Any error after copy dispatch carries these
states. A source-change refusal preserves the copy and the source. Cancellation
after copy reports confirmed/not-attempted. Delete refusal reports
confirmed/refused; delete response loss reports confirmed/unconfirmed. Copy
ambiguity reports unconfirmed/not-attempted and never triggers deletion.

CLI incomplete moves exit 1 with `ObjectMoveIncomplete` and both phase states
in the fixed stderr message, ahead of underlying not-found or credential
classification. Print no success Output. Never retry the whole composite or
automatically delete the destination. Recovery inspects both names and version
history. Rerunning a move is a new operation that could target new data.

## CLI contract

All commands require `--s3-region` and `--s3-credentials-file`, using the
existing safe credential-file reader and Linux/macOS boundary. Retain
`--profile`, read-only resolution without IAM secrets or token caches, and the
rejection of explicitly supplied global `--region` or `--project-id`. Ignore
their environment/profile defaults. Use `--transfer-timeout` for consistency,
with the existing 1s through 1h range and five-minute default.

- `storage list-objects`: required `--bucket`; optional `--prefix`,
  `--delimiter`, `--continuation-token`, `--max-keys` (default 1,000).
- `storage head-object`: required `--bucket` and `--key`.
- `storage delete-object`: required `--bucket`, `--key`, and `--yes`.
- `storage copy-object`: required `--source-bucket`, `--source-key`,
  `--bucket`, `--key`, `--replace`, and `--yes` for the initial release.
- A future `storage move-object` uses the copy identifiers and requires `--yes`.
  Replacement consent remains separate from source-deletion consent.
  It is not registered until the move contract is resolved and approved.

Reject `--cli-input-json` on these custom object commands, as transfers do.
No object-body path, local destination, arbitrary header, region override, or
version flag is accepted. Reject negative or explicit zero CLI page bounds;
only the SDK uses zero to select defaults. `--replace` is valid only for copy
and future move, and requires `--yes`. `--yes` alone never enables replacement.

List and HEAD work in read-only mode. Delete, copy, and move refuse under any
read-only source before loading S3 credentials or sending any request.
Validate all flags, capability, consent, output format, query syntax, and
profile policy before dispatch. Never prompt.

Support normal metadata `--output` and `--query`. List prints the page envelope;
HEAD and delete print their outputs plus input Bucket and Key. Copy prints its
output plus SourceBucket, SourceKey, Bucket, and Key. Filter local echoed
inputs against credentials. Never print content. Keep control-character
escaping from the shared renderer, including keys and common prefixes.

Use the existing [exit precedence](storage-objects.md#output-and-failure): 0 for
success; 2 for usage/config/input/read-only/consent refusal; 3 for credentials
or HTTP 401; 4 for HTTP 404; 1 for HTTP 403 and other failures. Unconfirmed
delete/copy and incomplete move always exit 1, preserving the outcome message
ahead of wrapped causes. The new ambiguity codes are `ObjectDeleteUnconfirmed`
and `ObjectCopyUnconfirmed`, ahead of the underlying APIError code. A
post-success query or stdout failure exits 1 and says
the operation completed. It never causes a resend.

## Independent releases and remaining gates

1. Reads: ListObjects and HeadObject with SDK, CLI, help, and wiki. Published
   compatibility supports both regions; HCM04 has additional recorded listing
   evidence. No copy/delete safety probe blocks these reads.
2. Deletion: one unconditional current-key delete with explicit CLI consent,
   versioning caveats, and separate ambiguity reporting. It does not wait for
   conditional-delete support.
3. Native copy: one server-side request with explicit replacement consent and
   documented metadata/encryption limits. Both regions use published
   compatibility; HCM04 adds recorded plain-copy evidence. No create-only or
   safe-move condition evidence blocks this release.
4. Create-only copy and move: only as their [specific gates][gates] permit.
   They do not block the three independent releases above.

The owner approved the independently reviewed ListObjects and HeadObject reads,
unconditional DeleteObject with `--yes`, and direct same-project/same-region
CopyObject with `--replace --yes` and its stated encryption limits. All four
operations are implemented. Create-only copy and move/rename remain deferred
behind their specific gates. This approval does not authorize live calls.
Implementation-plan review precedes implementation; mutation and credential
code require adversarial review. This document is not a plan.
[Checks][checks] state verification and release gates.

[api]: storage-object-operations-api.md
[checks]: storage-object-operations-checks.md
[boundary]: storage-objects-api.md#endpoint-and-authentication-boundary
[delete-wire]: storage-object-operations-api.md#single-object-delete
[gates]: storage-object-operations-api.md#evidence-gates
[move-gate]: storage-object-operations-api.md#move-source-and-destination-safety
