# vStorage object operations protocol and evidence

Status: ListObjects, HeadObject, DeleteObject, and CopyObject implemented.

This specification supports the [object operations design][design]. It adds
only the mechanics described here to the [transfer protocol][transfer]. Its
[checks][checks] distinguish local invariants from provider compatibility.

## Evidence and sources

**Recorded live facts:** [storage data-plane evidence][recorded] establishes
HCM04 S3 key access, path-style signing, and object listing and deletion under
bucket policies. [Storage settings][settings] records ListObjectsV2 with prefix
and delimiter, omitted KeyCount for an empty result, conditional PUT creation,
and DELETE ignoring a wrong If-Match. It also records CopyObject returning 501
for an encrypted source and copying an unencrypted source without encrypting
the result. These are existing observations, not new live results.

The live encryption helper uses HEAD and parses CopyObjectResult. Its source
shows test intent, not fresh server behavior. Fixtures and unit tests likewise
prove client behavior only. No private captures were inspected.

**Published GreenNode compatibility:** the [HCM04 API guide][gn-api-hcm]
and [HAN02 API guide][gn-api-han] distinguish S3 key authentication from console
service-account authentication. Both describe bucket listing, HEAD Object,
DELETE Object, and PUT with x-amz-copy-source. The [regional SDK
examples][gn-use-hcm] and [HAN02 examples][gn-use-han] implement move as copy
followed by delete. None of those descriptions establishes atomic rename,
source-generation guards, or destination copy conditions.

The [HCM04 portal guide][gn-portal-hcm] and [HAN02 portal guide][gn-portal-han]
describe copy/move between buckets within one project and exclude cross-project
operations. This bounds the accepted S3 surface; it does not establish a native
rename operation or transactional semantics. Broader AWS copy scope is not
evidence of broader GreenNode support.

The [HCM04 AWS CLI guide][gn-cli] also shows head-object, but its examples use
the HAN02 endpoint. The HAN02 API guide labels the region HAN02 while example
signature scopes use `hcm02`. Do not copy example Authorization values or
change the accepted endpoint/signing map from these inconsistent examples.
The existing exact HAN02 scope remains a compatibility assumption, not live
verification. Both guides' delete/copy examples include retention-related
headers that this design deliberately does not send.

**S3 wire references:** [ListObjectsV2][aws-list], [HeadObject][aws-head],
[DeleteObject][aws-delete], [CopyObject][aws-copy], and [SigV4][aws-sign]
define AWS request and response shapes. AWS CopyObject documents destination
If-Match and If-None-Match separately from x-amz-copy-source conditions. These
headers exist in AWS's contract; their enforcement on GreenNode is unresolved.
AWS [conditional deletes][aws-conditional-delete] do not override the contrary
HCM04 observation. Do not infer GreenNode behavior from a presumed Ceph version.

**Client guarantees:** finite decoding, explicit replacement, no fallback,
credential filtering, at-most-one mutation dispatch, and no source deletion
by CopyObject are locally testable. No source above proves cross-page snapshot
consistency, every metadata field, maximum server object size, or safe rename.

## Shared request rules

Reuse the fixed HTTPS endpoints, exact signing region, isolated transport,
timeouts, hashing, and path encoding in the [transfer protocol][transfer].
Do not import the live signer or share the management transport. Preserve
PutObject and GetObject's signatures and all no-query behavior.

List targets `/<bucket>` without manufacturing an empty object key. HEAD and
DELETE target the existing `/<bucket>/<key>` path. Every request signs host,
x-amz-date, and x-amz-content-sha256. Bodyless requests use the empty payload
hash. No generic query/header input is public.

The action names below are S3 permission requirements. Recorded GreenNode tests
use broader grants and do not prove every minimal-action combination. Listing
targets the bucket; other methods target objects. Source and destination grants
can differ. No method changes permissions to compensate for a 403. Permission
sufficiency is separate from regional compatibility.

The native signer uses the exact encoded request query in the signature.
Listing constructs private query name/value pairs and percent-encodes both with
RFC 3986 unreserved bytes only. It sorts by encoded name then value and joins
pairs with `&` and `=`. Encode `/`, `+`, `%`,
spaces, `?`, `#`, and `&` in values; spaces are `%20`, never `+`. Assign exactly
the resulting bytes to URL.RawQuery and the canonical query. Never sign one
encoding while sending another. Empty values use `name=` when included.

No double decoding, path cleaning, query injection, credential-bearing debug,
redirect, proxy fallback, or server-selected endpoint is allowed. Query tokens
are data, not URLs or credentials to use elsewhere. Bound input before encoding;
the 8 KiB token bound permits at most 24 KiB of encoded token data.

## ListObjectsV2

Send one GET with `list-type=2`, `encoding-type=url`, and effective `max-keys`.
Include prefix, delimiter, and continuation-token only when nonempty. Do not
send start-after, fetch-owner, version selection, or optional object attributes.
This requires `s3:ListBucket` on the bucket resource under the S3 policy model.
No per-object GET or HEAD accompanies a page.

Accept HTTP 200 only. Read at most 8 MiB plus one probe byte before accepting
the page. This client bound allows 1,000 encoded keys and bounded metadata;
oversize replies fail with a safe APIError and nil Output, never a partial page.
Stop counting entries beyond requested MaxKeys even before the body cap.

Use streaming XML token decoding with a maximum nesting depth of 16. Permit an
XML declaration, whitespace, and comments. Reject directives, DTDs, processing
instructions other than the initial XML declaration, invalid UTF-8, malformed
XML, multiple roots, trailing non-whitespace data, and nested scalar fields.
Never resolve entities or external resources. Require ListBucketResult as the
root, with absent namespace or the S3 namespace
`http://s3.amazonaws.com/doc/2006-03-01/`. Ignore unknown bounded subtrees;
namespace-qualified lookalikes cannot populate known fields.

Reject duplicate known singleton elements at their expected level. Require
one IsTruncated boolean, one EncodingType equal to `url`, and each Contents
entry's Key and nonnegative int64 Size. Accept `true`/`false` only. Require
exactly one Prefix per CommonPrefixes entry. Unknown elements and Owner are
not retained. Missing Contents is a valid empty Items slice.

Percent-decode Key and common Prefix exactly once after XML decoding, without
form-style plus-to-space conversion. Validate UTF-8 and the 1,024-byte decoded
key bound. Key is nonempty; common Prefix is nonempty. Allow control characters
in decoded keys, as existing object addressing does; render them safely. Reject
bad escapes or missing/unknown EncodingType rather than guessing whether `%`
was literal. Validate any echoed Prefix/Delimiter using the same encoding;
if present they must match the request. Name, if present, must match the bucket.
Unknown server encoding support needs a targeted compatibility check only if
the provider refuses this published S3 mechanism; never silently drop it.

Only XML-decode tokens. Apply the 8 KiB UTF-8/control and credential checks.
IsTruncated true requires a nonempty next token unequal to input; false forbids
a nonempty next token. Any echoed token must match input exactly. Never log
tokens or include them in errors.

Optional MaxKeys and KeyCount must be single nonnegative bounded integers.
Missing KeyCount is allowed, as in HCM04's empty result. Present MaxKeys must
match the request. Neither controls pagination or allocation; public counts
describe decoded arrays, never a total or KeyCount semantics beyond the page.

The accepted page has at most MaxKeys combined Contents/CommonPrefixes entries.
Each decoded key/prefix must match the requested prefix. CommonPrefixes require
delimiter `/` and must end in `/`; do not treat them as downloadable objects.
Do not sort, deduplicate, or infer pages from the last key. Optional entry ETag
is at most 1 KiB, StorageClass 128 bytes, LastModified 128 bytes before RFC 3339
parsing. Apply the source and normalized-value filters below; invalid or unsafe
optional metadata becomes empty or nil.

## HEAD metadata

Send one bodyless HEAD without range, conditions, checksum mode, or encryption
headers. S3 requires `s3:GetObject`, not ListBucket, for successful lookup.
Missing objects may return 403 or 404 by permission; preserve status mapping.
Require no XML error body and never issue GET for error details.

Accept HTTP 200 only. Content-Length describes object size, without download
limits or body draining. Use the strict decimal int64 rule; missing is -1.
Accept identical duplicates already normalized by net/http. Reject observable
duplicates, negative/malformed values, and transport framing errors, including
conflicting wire lengths. Keep the existing transport. Close responses at once,
including on errors, and check context before returning.

Keep the transport's 32 KiB response-header budget. Allow only these optional
headers: ETag (1 KiB), Content-Type (256 bytes), Last-Modified (128 bytes,
HTTP-date parsed to UTC), x-amz-version-id (1 KiB), and
x-amz-server-side-encryption (128 bytes). Invalid or duplicate optional scalar
headers are omitted. No checksum, owner, request ID, raw headers, or encryption
key material enters the output.

Accept at most 64 distinct x-amz-meta-* headers with one value each. Lowercase
suffixes of 1 through 128 ASCII letters, digits, hyphens, or underscores.
Values are at most 2 KiB, UTF-8, without controls; retained suffix/value bytes
total at most 8 KiB. Sort names before applying that budget for deterministic
omissions. Omit invalid, duplicate, sensitive, or over-budget pairs wholly.
Never decode MIME, percent, JSON, or base64 into another representation.

Apply [success filtering][filter] to optional values and metadata pairs, before
and after lowercasing names. Empty encryption metadata means unavailable.
Last-Modified and ETag describe observations, not a lock or a unique generation.

## Single-object delete

Send one bodyless DELETE. Sign the empty payload hash. Send no versionId,
If-Match, MFA, governance bypass, legal-hold, or retention header. Require
`s3:DeleteObject` on the selected key. Do not require read/list permissions by
adding a preflight request. Retention and permissions can still cause refusal.

Accept HTTP 204 only. HTTP defines this as bodyless; do not wait for a declared
object length or drain arbitrary content. Invalid framing surfaced by the
transport is an unconfirmed outcome. A non-204 success status is also
unconfirmed; do not accept an arbitrary 2xx envelope from management code.
The filtered optional x-amz-delete-marker must be exactly `true` or `false`;
otherwise omit it. Filter x-amz-version-id as for HEAD. Missing or filtered
optional headers do not turn an accepted delete into failure.

S3's current-name DELETE is permanent for an unversioned object, inserts a
marker when versioning is enabled, and may remove a null version before adding
a marker when suspended. GreenNode's [versioning observations][versioning]
establish retained versions, not every S3 marker race. These methods never
select a version or promise that bytes were erased. Missing-name 204 remains
success; HTTP 404 remains ErrNotFound even without an XML body.

Treat known pre-dispatch failure as no mutation. Once dispatch may have reached
the server, transport loss, cancellation, unexpected success, redirects, 5xx,
or malformed protocol return ErrObjectDeleteUnconfirmed with Retryable false.
Definitive 4xx refusals return their normal safe APIError. Do not retry even a
429. Never issue a follow-up DELETE to repair an ambiguous outcome.

## Native CopyObject

Require Replace true before building or sending any request in either region.
Otherwise return ErrInvalidInput with a fixed native-create-only-unavailable
message. There is no destination HEAD and no conditional-create emulation.

Send one `PUT /<bucket>/<key>` to the destination with zero Content-Length,
no request body, and the empty payload SHA256. Set x-amz-copy-source to
`/<source-bucket>/<source-key>`, percent-encoded with the existing path rules:
preserve separators and key slashes, encode spaces as `%20`, encode literal `%`
once, and never clean dot segments. A key's `?` is encoded data, never a query.
Sign x-amz-copy-source alongside host, x-amz-date, and x-amz-content-sha256.
The destination query and canonical query are empty.

Send no If-Match, If-None-Match, x-amz-copy-source-if-* conditions, versionId,
metadata/tagging directive, ACL, content metadata, encryption, retention,
legal-hold, or governance-bypass header. Do not copy the provider example's
legal-hold header into this contract. No GET, HEAD, multipart call, console
call, local spool, or DELETE accompanies CopyObject.

The S3 permission baseline is s3:GetObject on the source and s3:PutObject on
the destination. Default provider metadata/tag behavior or bucket policies may
need additional rights; no grant is widened or metadata silently stripped to
avoid refusal. Both buckets belong to one project in the configured region.
No listing grant is introduced for preflight and no second credential is used.

Accept HTTP 200 only after reading at most 64 KiB plus a probe byte to clean
EOF under the operation deadline. Response Content-Length, when present, is
the XML result length, never the copied object's length. Short, oversize, lost,
or malformed result bodies are unconfirmed copies. Close every response body.
Disable hidden transport replay for this bodyless PUT as for ordinary PUT.

Apply the listing XML defenses with this smaller body cap and depth 16. Require
one CopyObjectResult root in the S3 namespace or no namespace, with one
nonempty scalar ETag. Require the whole document and reject duplicate recognized
fields, nested scalars, multiple roots, and trailing data. Ignore unknown
bounded fields. An Error root under HTTP 200 is a failed result, not success:
extract only the existing bounded allowlisted code and discard all messages.
An empty result or absent ETag is unconfirmed, not a successful empty output.

ETag is opaque, with a 1 KiB display limit. Once the required result shape is
valid, omit an unsafe or over-limit ETag wholly without revoking the accepted
copy. Optional LastModified is bounded to 128 bytes, parsed as RFC 3339 to UTC,
and subject to both filters below; invalid values become nil. Optional headers:
x-amz-version-id and x-amz-copy-source-version-id (1 KiB each), and
x-amz-server-side-encryption (128 bytes). Duplicate or unsafe optional headers
are omitted. No returned version value authorizes source deletion.

The request leaves metadata copying to the service. AWS's default COPY behavior
is the reference for user metadata and ordinary content headers, not a verified
GreenNode preservation matrix. The client supplies no metadata replacement and
does not claim to preserve ACLs, tags, retention, redirects, storage class, or
encryption. Recorded HCM04 encryption differences remain explicit in the
public contract. Do not synthesize a ContentType, SHA256, or ContentLength.

No preflight size read is needed. The service enforces its single-request size
limit; AWS's 5 GB limit is not a measured GreenNode limit or a hard client
bound. A source can change between any HEAD and the copy. There is no splitting,
automatic fallback, or content-size allocation. Server storage consumption and
elapsed work may outlive a client timeout, which reports an unconfirmed copy.

Use operation `storage.CopyObject` on all APIErrors. Known pre-dispatch failures
have no mutation. Definitive HTTP 4xx refusals retain their normal sentinels;
412 is an ordinary precondition failure, not ErrObjectExists, because no
destination condition was sent. Once dispatched, 3xx, 5xx, unexpected 2xx,
embedded Error under 200, result failure, or cancellation wrap
ErrObjectCopyUnconfirmed with Retryable false. Add NotImplemented to the safe
S3 code allowlist; 501 still follows the conservative 5xx ambiguity rule.
No response error triggers another copy, source deletion, or rollback.

## Metadata, errors, and output safety

Retain the transfer error-body cap of 64 KiB plus a probe and its allowlisted
codes, status-based sentinels, safe context causes, and fixed messages. Extend
the private operation allowlist for ListObjects, HeadObject, DeleteObject, and
CopyObject. Add local `InvalidResponse` for invalid bounded metadata/XML pages;
use `RequestFailed` for transport failures. No raw decode/transport error is
retained if it can quote remote text. Read errors have nil Output.

Optional fields follow the [existing whole-field filter][filter]. Required
page identities and usable next tokens must never be partially redacted,
silently omitted, or substituted. If credential filtering would change one,
fail the whole page with fixed InvalidResponse, no values and no partial output.
Scan after percent/XML decoding so encoded reflections cannot bypass filtering.
Controls in keys use the key exception above, never a general header exception.

Filter bounded source text before numeric/time parsing. Unsafe required numbers
fail; omit unsafe optional fields, even for short-credential coincidences.
Client-derived counts, lengths, and hashes keep existing transfer semantics.

For List, HEAD, and Copy timestamps, also filter the UTC value before retaining
it. Check these finite representations: Format(time.RFC3339Nano), MarshalText,
MarshalJSON, String (`%v`, `%+v`), and GoString (`%#v`). Omit the time as nil if
any representation fails marshaling or would change under the whole-field
filter. CLI formats and direct field queries use that accepted JSON value.
Arbitrary caller layouts, transformations, or constructed queries are outside
this guarantee; keep existing crafted nested-encoding limits.

Supported formatting, JSON, and CLI projections expose only accepted metadata.
Never retain credentials, content, raw headers, URLs, or private state.
Errors/debug also exclude bucket/key names and tokens. CLI input echoes follow
transfer redaction. Debug keeps operation, duration, status, count, local code.

## Evidence gates

### Independently shippable operations

List and HEAD rely on published S3 compatibility in both regions, with stronger
recorded listing evidence in HCM04. Detailed optional fields and HAN02 V2
pagination are standards-based assumptions, not fresh live facts. Local strict
decoding handles disagreement as an error. No unrelated mutation experiment is
a prerequisite to implementing reads under an approved design and plan.

Standalone unconditional DELETE uses recorded HCM04 behavior and published
HAN02 compatibility. Conditional-delete evidence is irrelevant to this narrowly
consented operation. Native copy with Replace true uses both regions' published
CopyObject contract and HCM04's recorded plain-copy success. It needs no proof
of copy preconditions or conditional deletion. HAN02 is published compatibility,
not live-verified copy. Its encryption behavior is unverified; HCM04's known
restriction is disclosed. No cross-region compatibility matrix is required.
The manager owns repository fixture verification under the [checks][checks].

### Native create-only copy

The inspected GreenNode API and SDK guides show x-amz-copy-source but no copy
destination If-Match or If-None-Match guarantee. Absence from those guides means
unknown, not proven unsupported. PUT's If-None-Match evidence does not transfer
to CopyObject. HEAD-then-copy cannot implement create-only safely.

Before offering native create-only copy, establish enforcement for the chosen
region: absent destination succeeds, occupied destination returns 412 without
changing its bytes, and competing writers cannot both replace the same current
object. These checks gate only changing Replace false from refusal to native
conditional creation. They do not block explicit replacement. No source-copy
precondition is promised or needed for the ordinary current-source copy above.

### Move source and destination safety

The smallest immediate blocker is safe removal of the exact copied source.
HCM04's ignored DELETE If-Match is contrary evidence, not merely an untested
header. Do not repeat that experiment unless a provider change or alternative
mechanism gives reason. HAN02 needs evidence of its own removal guard; success
of an unconditional DELETE proves nothing about it.

A usable current-name guard must atomically reject a changed generation,
including equal content with changed metadata. The copy must identify that
same generation. No public GreenNode document inspected establishes such a
guard. An ETag-only check or a second HEAD is insufficient.

The smallest concrete alternative to investigate is exact-version movement:

1. Obtain a documented immutable, non-null source version ID. Neither an ETag,
   an absent ID, nor the literal `null` value qualifies.
2. Copy exactly that version using a proved copy-source selector, or obtain
   trustworthy copied-source identity from the complete copy result.
3. Delete that exact version, proving a newer source version is preserved and
   documenting whether an older version becomes visible at the source name.

GreenNode documents DELETE versionId, but not the connected copy/identity
contract. AWS specifies a versionId suffix inside x-amz-copy-source and source
version response metadata; do not enable those merely from AWS documentation.
Seek provider clarification first. If a targeted live check is later authorized,
use two tiny source versions, copy the chosen older version, replace the current
source, then delete only the chosen version and confirm the newer one remains.
Record missing/null identity handling and which source version becomes current.
No such run is authorized by this design.

The owner must choose version-scoped semantics and their extra permissions, or
retain ordinary name-move scope until an atomic source guard/native rename is
established. Destination replacement/deletion races remain separate: an exact
source guard does not freeze the copy. If recoverability despite those races
is required, establish retained destination-version behavior or a server
transaction. State that guarantee before approving a move contract. A successful
copy alone never authorizes an unguarded source DELETE. These open decisions
block move only, not list, HEAD, standalone delete, or native replacement copy.

[design]: storage-object-operations.md
[checks]: storage-object-operations-checks.md
[transfer]: storage-objects-api.md
[filter]: storage-objects-api.md#success-metadata
[recorded]: storage-api.md#data-plane
[settings]: storage-settings.md#encryption
[versioning]: storage-settings.md#versioning
[gn-api-hcm]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/api-developers.md
[gn-api-han]: https://docs.greennode.ai/vstorage/object-storage/object-storage-han02/api-developers.md
[gn-use-hcm]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/3rd-party-softwares/s3-sdk/using-s3-sdk.md
[gn-use-han]: https://docs.greennode.ai/vstorage/object-storage/object-storage-han02/3rd-party-softwares/s3-sdk/using-s3-sdk.md
[gn-cli]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/3rd-party-softwares/aws-cli/using-aws-cli.md
[gn-portal-hcm]: https://docs.greennode.ai/vstorage/object-storage/object-storage-hcm04/cac-tinh-nang-cua-object-storage/lam-viec-voi-object-va-directory/working-with-objects-and-directories-via-vstorage-portal.md
[gn-portal-han]: https://docs.greennode.ai/vstorage/object-storage/object-storage-han02/features-of-object-storage/working-with-objects-and-directories/working-with-objects-and-directories-via-vstorage-portal.md
[aws-list]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html
[aws-head]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_HeadObject.html
[aws-delete]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteObject.html
[aws-copy]: https://docs.aws.amazon.com/AmazonS3/latest/API/API_CopyObject.html
[aws-sign]: https://docs.aws.amazon.com/AmazonS3/latest/developerguide/sig-v4-header-based-auth.html
[aws-conditional-delete]: https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-deletes.html
