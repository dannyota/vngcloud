# vStorage object transfers

Status: Implemented. The SDK and CLI provide the accepted transfer surface.
HAN02 create-only PUT remains gated on conditional-write evidence.

Upload and download arbitrary binary object files through the SDK and CLI. Each
file becomes one object under a caller-chosen key. Logs are one use case; the
API does not require a log format or rotated files. Upload never appends to an
existing object. [Wire rules and evidence](storage-objects-api.md) distinguish
observed GreenNode behavior from S3 specifications.

## Approach

Use a small native S3 data-plane client in `storage`, separate from the console
management client. Keep both the SDK and signer standard-library only. The CLI
calls that public client.

Two alternatives are unsuitable for this slice. Wrapping rclone adds an external
executable and bypasses a public SDK transfer contract. Importing an AWS SDK
supplies many unneeded S3 features and breaks the SDK dependency rule. Existing
rclone guidance remains useful for sync and large transfers.

The first slice has `PutObject` and `GetObject`, plus `storage put-object` and
`storage get-object`. Known object keys need neither listing nor HEAD. GET
supplies download metadata; conditional PUT protects creation without a racy
HEAD check. Listing, pagination, and HEAD remain separate features.

Exclude append, tail, directory recursion, sync, copy, move, delete, ACLs,
presigned URLs, version selection, multipart uploads, streaming signatures, new
bucket/key management, and anonymous or public-URL fallback. Object content
stays opaque bytes. No compression, newline conversion, MIME sniffing,
extraction, or log parsing runs during transfer.

## Credentials and region

Require `--s3-credentials-file <path>` on both commands. Read only the
`[default]` access-key pair in the AWS shared credentials format written by
[create-s3-key](storage-cli.md#create-s3-key). The flag contains a path, never a
credential. No key flags, secret JSON fields, environment credential chain,
`credential_process`, instance metadata, or automatic key creation exist. Do not
fall back to IAM credentials or anonymous requests.

The credential file reader accepts UTF-8, LF or CRLF, blank lines, and full line
`#` or `;` comments. Split each assignment at its first `=` only. It requires
exactly one `[default]` section with one non-empty `aws_access_key_id` and one
non-empty `aws_secret_access_key`. It rejects duplicates, other sections or
keys, interpolation, quotes, inline comments, control bytes in values, and files
over 16 KiB. Trim surrounding ASCII whitespace, not bytes within values. Errors
name the file or field, never a value or source line. A missing, unreadable,
unsafe, or malformed explicit file wraps `vngcloud.ErrCredentialsFile`. An
absent in-memory credential pair wraps `vngcloud.ErrNoCredentials`; missing
required CLI flags remain usage errors.

Open without following the final symlink and verify the opened handle is a
regular file. On Unix require owner ownership and mode 0600. Reject FIFOs,
devices, directories, and symlink swaps without blocking. Parent directories
must be trusted by the caller. Platforms without an equivalent safe open and
privacy check reject credential-file transfers before a network call; there is
no insecure fallback. Linux and macOS are the first CLI targets. The SDK remains
portable because callers supply credentials in memory.

Require `--s3-region HCM04` or `--s3-region HAN02`. The endpoint map uses
`https://hcm04.vstorage.vngcloud.vn` and `https://han02.vstorage.vngcloud.vn`,
respectively, matching recorded `Region.S3Host`. No case folding, region
guessing, management lookup, endpoint override, region redirect, or fallback to
`us-east-1` occurs. Both regions support GET and explicit replacement under the
published S3 contract. HCM04 also supports default create-only PUT under
existing live evidence. HAN02 create-only PUT remains gated on its
conditional-write behavior; reject that mode before reading the body or sending
a request until the gate is answered. This is a capability limit, not a reason
to block ordinary native transfers. See [release
evidence](storage-objects-api.md#release-evidence).

Global `--profile` still selects local configuration and read-only policy.
Object commands do not load IAM secrets or token caches, perform login, or
require a management region. Reject an explicit global `--region` or
`--project-id` on these commands because neither selects the S3 resource. Ignore
their environment/profile defaults. The S3 key scopes the project; `--bucket`
and `--key` select the object.

## Public SDK

`storage.Client` and `storage.New(vngcloud.Config)` remain unchanged. The
separate object client exposes:

```go
type ObjectCredentials struct {
    AccessKeyID     vngcloud.Secret
    SecretAccessKey vngcloud.Secret
}

type ObjectConfig struct {
    Region      string
    Credentials ObjectCredentials
    Timeout     time.Duration
}

func NewObjectClient(ObjectConfig) (*ObjectClient, error)
func (*ObjectClient) PutObject(context.Context, *PutObjectInput) (
    *PutObjectOutput, error)
func (*ObjectClient) GetObject(context.Context, *GetObjectInput) (
    *GetObjectOutput, error)
```

Construction validates credentials, region, and timeout without network access.
On failure it returns a nil client and a safe error. An absent or incomplete
credential pair wraps `ErrNoCredentials`; invalid credential shape, unsupported
region, and invalid timeout wrap `ErrInvalidConfig`. These are `vngcloud`
sentinels, with no APIError or raw credential value. Credential values must be
non-empty and contain no control bytes. Clients are safe for concurrent calls;
inputs and bodies are not shared between calls. No public transport,
arbitrary-header, signer, endpoint, capture-hook, or cookie-jar option belongs
to this slice.

`PutObjectInput` has `Bucket string`, `Key string`, `Body io.ReadSeeker`,
`ContentLength int64`, and `Replace bool`. Bucket, key, and body are required.
Length includes zero for an empty object. `Replace` defaults to false. The
caller keeps the body immutable and seekable from offset zero until the call
returns. The SDK neither closes the body nor restores its position. It streams a
bounded hashing pass, rewinds, then streams one PUT. A size mismatch or seek
failure before dispatch sends no request. Concurrent body changes violate the
input contract; the CLI provides a stable snapshot.

`PutObjectOutput` has `ContentLength int64`, `SHA256 string`, and `ETag string`.
SHA256 is the locally computed lowercase hex digest. ETag is opaque and
optional. Preserve its quotes only after the [success-metadata
filter](storage-objects-api.md#success-metadata) accepts it; omit
credential-bearing optional metadata. Filtering optional metadata does not turn
a successful PUT into an unconfirmed write. A successful PUT does not claim a
verified download or interpret ETag as a checksum.

`GetObjectInput` has required `Bucket string` and `Key string`, plus `MaxBytes
int64`. Zero selects the default download limit; a negative value is invalid. A
positive value selects that finite byte limit. `GetObjectOutput` has `Body
io.ReadCloser`, `ContentLength int64`, `ETag string`, and `ContentType string`.
ContentLength is -1 when the server does not supply a length. ETag and
ContentType pass the same success-metadata filter before the SDK exposes them.
Body is excluded from JSON and formatted output. The caller must close Body,
including after a read error, and must read to clean EOF to establish success.
Returning headers alone establishes no completed transfer. The wrapper enforces
the declared length and limit; short reads and cancellation return errors from
Body. The operation's deadline remains active until EOF or Close.

Reject a malformed Content-Length or a declared length over the selected limit
before exposing Body. If no length is supplied, read at most the limit plus one
probe byte; crossing the limit returns a body error. Do not buffer the body in
memory or treat an unknown length as zero.

Both methods return `*vngcloud.APIError` for HTTP, protocol, and transport
failures, with operations `storage.PutObject` and `storage.GetObject`. Local
object-input failures wrap `vngcloud.ErrInvalidInput`; credential failures use
the credential sentinels above. Add `storage.ErrObjectExists` for create-only
PUT's 412 and `storage.ErrObjectWriteUnconfirmed` for an ambiguous write,
wrapping the underlying APIError. Other status sentinels follow shared APIError
rules. A method error always returns a nil Output, including an unconfirmed PUT.
A failed initial GET closes any response body before returning. A successful GET
returns the Output once; later Body errors do not revoke that pointer or turn
its metadata into evidence of a completed download.

GET Body.Read returns ordinary `io.EOF` only after a complete allowed body.
Short content returns `*vngcloud.APIError` with code `IncompleteObject`;
exceeding MaxBytes uses `ObjectTooLarge`; other read failures use
`RequestFailed`. These errors name operation `storage.GetObject`, retain
StatusCode 200, set Retryable false, and use fixed safe messages. A short body
wraps `io.ErrUnexpectedEOF`. Cancellation or deadline failure wraps only the
matching context sentinel, so both `errors.As` to APIError and `errors.Is` to
the context sentinel work. Other raw body-reader errors are not retained or
echoed. Bytes already read remain partial caller output. Close releases the body
and deadline; a read after Close returns a safe APIError with code
`ObjectBodyClosed`. A Close failure uses the same safe `RequestFailed` APIError
rules. Close alone establishes no successful download and does not retry. These
transfer errors exit 1 in the CLI.

## Limits and cancellation

`storage.MaxPutObjectSize` is 5,000,000,000 bytes. This conservative decimal 5
GB cap stays within the published S3 single-PUT boundary and avoids multipart
state. It is not a verified GreenNode server limit. Fixed buffers keep memory
use independent of object size. Larger uploads require a later multipart design;
splitting application content is not an implicit feature.

GET has no multipart dependency. `storage.DefaultMaxDownloadBytes` is
5,000,000,000 bytes, matching the first slice's upload bound. SDK `MaxBytes` and
CLI `--max-download-bytes` can raise or lower that positive finite int64 limit,
up to 9,223,372,036,854,775,806 bytes so a probe byte cannot overflow the
counter. Reject a larger declared object before consuming its body. There is no
unlimited value. Users can read larger existing objects with an explicit
disk/transfer bound; the client does not claim the server will serve every
requested size. These limits support general object files and do not reserve the
commands for logs.

Timeout defaults to five minutes. SDK `Timeout` zero selects that default;
negative values and values over one hour fail validation. CLI
`--transfer-timeout` accepts one second through one hour, default five minutes.
The earlier caller deadline wins. The CLI deadline spans snapshot, hashing,
network transfer, and local commit. Cancellation closes network bodies and
removes uncommitted temporary files. Checks between file reads stop normal local
work; cancellation cannot interrupt an operating-system filesystem call that has
already blocked. SDK callers supply prompt Read/Seek implementations; an
arbitrary blocking reader cannot be forcibly cancelled safely.

## CLI transfers

Upload and download a binary file with explicit S3 credentials and region:

```sh
vngcloud storage put-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz \
  --body ./bundle.tar.gz

vngcloud storage get-object --s3-region HCM04 \
  --s3-credentials-file ./storage.credentials \
  --bucket app-files --key archives/bundle.tar.gz \
  --destination ./downloaded.tar.gz
```

Log files use the same commands with their own file paths and object keys.

Upload accepts one regular file through required `--body`. Follow ordinary
input-file symlinks, open nonblocking where needed, then check the opened
handle's type. Empty files are valid. Reject `-`, pipes, devices, and `file://`
interpretation. Stdin and stdout byte streaming are deferred; finite files
support general binary transfers without partial pipeline output or an unbounded
input producer. Download requires `--destination` naming a file, never `-`. No
object bytes reach stdout or stderr.

Validate flags, read-only policy, replacement consent, region, credentials, and
local paths before any object request. Reject `--cli-input-json` on these two
commands; file bodies and destinations use their named flags. Support normal
`--output` and `--query` for metadata only. Resolve output format and query
syntax before dispatch, so invalid output settings cannot cause a write. Help
and generated docs describe this command-specific contract rather than
reflecting a reader into flags.

### Upload and replacement

PUT is a write. Flag, environment, or profile read-only policy refuses it with
exit 2 before loading S3 credentials, opening the body, or requesting the
network. Create-only PUT needs no `--yes`: it sends signed `If-None-Match: *`.
An existing key fails without replacing its contents. No preflight GET or HEAD
is required, so a write-only key can upload.

`--replace --yes` sends an unconditional whole-object PUT. `--replace` without
`--yes` exits 2 before any request. `--yes` alone does not enable replacement.
The command cannot preserve metadata, arbitrate concurrent writers, or promise
rollback. Bucket versioning may create another billable version; no version is
selected, restored, or removed by this feature. Do not expose `If-Match`, whose
quoted form has incompatible live behavior.

Copy the source into an exclusive mode-0600 file inside a new mode-0700 system
temporary directory. Copy at most the limit plus one probe byte and refuse
excess before PUT. The uploaded content is the completed snapshot; source
changes during copying may produce a mixed snapshot. Tell users to finish
writing the source before upload. Hash and send only the snapshot, and remove it
on success or failure. Disk-full and snapshot failures send no PUT. No resumable
state or predictable temporary name remains. A process kill or crash can leave
private temporary files; document their naming for manual local cleanup without
a broad automatic sweep.

### Download and local replacement

GET is a remote read and works in read-only mode. Local filesystem writes still
need an explicit destination. The parent directory must already exist and be
trusted. Reject a symlink, directory, or non-regular existing destination. By
default refuse any occupied path, including dangling symlinks. `--replace --yes`
permits replacing a regular local file; it has no effect on the remote object.
Missing consent exits 2 before GET.

Write to an exclusive mode-0600 temporary file in the destination directory.
Require clean EOF, the expected byte count, successful sync, and close before
publishing. Without replacement, atomically publish without clobbering any entry
created during the download. Use an atomic no-replace filesystem primitive, or a
same-directory hard-link publish followed by unlink of the temporary name. If
the filesystem cannot support this operation, fail with the destination
unchanged. Never fall back to a check-then-rename or truncate of the
destination.

With replacement, rename atomically over the destination entry on supported Unix
filesystems. Recheck destination type before rename and never follow the entry.
A hostile process can race the entry check; trusted destination directories are
required. Never remove the old file before rename. A failed rename keeps the old
destination. Windows CLI transfers remain unsupported until equivalent
credential and publication guarantees are designed and tested. Do not emulate
atomic replacement with delete followed by rename.

On pre-publication failure, close and remove only the temporary file; leave any
existing destination intact. After publication, failure to remove an extra
temporary link or sync the directory reports that the download was published and
names the local cleanup needed. It never deletes the finished file or retries
GET. Atomic visibility does not promise survival through a power loss on every
filesystem.

### Output and failure

After success, stdout metadata contains `Bucket`, `Key`, `ContentLength`,
`SHA256`, and `ETag`; a download adds `Destination`. The CLI computes the
download SHA256 and actual ContentLength while copying. The digest describes
bytes received, not an independent remote checksum. Table/text rendering escapes
control characters in keys; JSON uses normal JSON escaping. No progress output
is needed.

Use the [shared CLI exit codes](cli.md#errors-and-exit-codes), including
sentinel precedence. Exit 0 means the transfer completed. Exit 2 covers usage,
read-only, missing consent, non-credential configuration errors, and
`ErrInvalidInput`. Exit 3 covers `ErrNoCredentials`, `ErrCredentialsFile`, and
`ErrAuth`, including HTTP 401. Exit 4 covers not found, including HTTP 404 for a
bucket or key. HTTP 403 remains a permission failure with exit 1; an S3
error-code string alone does not change the shared status mapping.

Other API/network errors, transfer or local I/O errors, `ErrObjectExists`, and
`ErrObjectWriteUnconfirmed` exit 1. Cancellation or a deadline anywhere in the
error chain exits 1 before credential and not-found matching. Preserve the
unconfirmed-write sentinel and its recovery message; no retry follows.
Credential failures take precedence over their shared `ErrInvalidConfig` parent.
Runtime query and metadata-stdout failures after a successful transfer exit 1
and say the transfer completed; users must not infer a failed upload from the
absence of metadata.

A timeout, connection loss, 5xx, malformed success, or cancellation after PUT
dispatch can leave an object stored. The error explicitly says the write may
have happened. No automatic PUT retry or cleanup DELETE occurs. The caller can
GET that exact key to a fresh path and compare byte length and SHA256 before
deciding whether to upload again. A 412 after a manual rerun does not prove the
previous bytes match. An unconditional rerun can replace a newer writer or
create another version.

## Owner decisions

1. Approved: the separate `storage.ObjectClient`, native signer, and the two
   public SDK methods and CLI commands as specified.
2. Approved: HCM04 and HAN02, with HAN02 create-only PUT gated on evidence,
   explicit S3 region and credential-file flags, and no management login
   or arbitrary endpoint override.
3. Approved: create-only PUT by default and unconditional replacement only
   through `Replace` or CLI `--replace --yes`.
4. Approved: the 5 GB single-PUT bound, configurable finite GET bound,
   five-minute default deadline, file-only CLI I/O, and private snapshot
   disk use up to one object per upload.
5. Approved: Linux/macOS CLI support first, fail-closed unsupported platform
   behavior, and atomic local publication with explicit replacement consent.

## Release scope

Ship the SDK, commands, generated CLI reference, and wiki together only when the
[evidence and tests](storage-objects-api.md#required-tests) pass. Document setup
using the existing per-bucket key, binary-file examples, permissions, limits,
read-only behavior, local/remote replacement, ambiguous writes, and the checksum
limits in `docs/wiki/Storage-Objects.md` and generated
`docs/wiki/CLI-Storage.md`. The storage package comment, root coverage docs, and
release notes describe the added surface without claiming a full S3
implementation.

Owner contract approval grants no live-run, storage-cost, or cleanup authority.
When live evidence is needed, the manager obtains approval for a run against
named resources. Implementation requires a separate plan and independent plan
review; contract approval does not waive those gates.
