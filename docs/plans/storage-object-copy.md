# Native vStorage CopyObject implementation plan

> **For agentic workers:** Use `superpowers:subagent-driven-development` for the authorized worker execution. Follow repository routing, ownership, review, and Git rules rather than generic skill defaults.

**Goal:** Release one native same-project, same-region server-side copy with explicit destination replacement consent, SDK, CLI, help, and wiki.

**Architecture:** Extend `storage.ObjectClient` without changing its credential or endpoint boundary. Send one bodyless destination PUT and accept success only after a complete bounded XML result. Keep the CLI on the public SDK and reuse the released metadata-operation foundation.

**Tech stack:** Go pinned in `.tool-versions`, standard-library SDK, existing Cobra/query/rendering CLI dependencies; no dependency additions.

**Spec:** [Accepted design](../design/storage-object-operations.md#copyobject), [wire contract](../design/storage-object-operations-api.md#native-copyobject), [checks](../design/storage-object-operations-checks.md), [transfer boundary](../design/storage-objects-api.md), [ADRs](../adr/README.md), and [workflow](../../instructions/workflow.md).

## Release boundary and constraints

- SDK work may overlap release work after manager acceptance of the shared read foundation and deletion SDK/error changes; serialize SDK writers on shared error paths. CLI work requires manager-verified read/deletion CLI helpers and copy SDK interfaces. Before complete-slice checks and independent code review, integrate only copy feature paths onto the tagged deletion release. Copy landing still waits for the deletion feature tag and successful master validation. Consume verified helpers, never an active writer's unfinished files; do not duplicate their implementation or plans.
- The owner approved ordinary native CopyObject in HCM04 and HAN02. Independent plan review precedes code; existing execution and standing landing authority remain. Create-only evidence and safe-move evidence do not gate this release.
- `Replace` must be true before dispatch. The CLI requires `--replace --yes`; either alone is insufficient. Reject identical source/destination bucket/key pairs and invalid or nil inputs before any request.
- One credential pair and exact configured region serve both buckets in one project. Caller scope and service authorization enforce project membership; no console lookup or second credential is added.
- Keys are literal UTF-8 strings under the existing 1,024-byte rule. Preserve slashes, repeated slashes, dot segments, controls, Unicode, and literal percent sequences without normalization.
- No HEAD, GET, upload fallback, local spool, subprocess, source version selection, multipart, source deletion, rollback, confirmation read, retry, or request replay. No create-only or move surface, alias, stub, or capability gate.
- Keep fixed HTTPS endpoints, TLS verification, redirect refusal, proxy exclusion, 32 KiB response-header budget, isolated transport, and credential filtering. `ObjectConfig.Timeout` covers result decoding, with earlier caller deadlines winning.
- Published compatibility supports both regions; recorded plain-copy and encryption facts apply to HCM04. Synthetic fixtures prove local behavior only. No fresh live call, paid resource, encryption matrix, or private capture is required or authorized by this plan.
- The manager owns worktrees, verification, signed Git, CI evidence, and tags. At most three workers run across slices, each with separate credential-free worktrees and disjoint paths. Implementers use Sol medium, troubleshooters Sol xhigh, and independent reviewers Sol xhigh under project routing.

## Review focus

1. A bodyless PUT can be replayed after a stale connection; Task 1 counts dispatches across transport failures and forbids every secondary operation.
2. HTTP 200 can carry an Error or a truncated result; Task 1 requires bounded clean EOF and copy-specific ambiguity.
3. A safe source timestamp can normalize into a credential; Task 1 filters all specified UTC representations before retaining it.
4. Unconditional replacement can affect concurrent source/destination writers; Tasks 1 and 2 preserve explicit consent without invented snapshot or cleanup guarantees.
5. A confirmed copy can have query/stdout failure; Task 2 reports completion with exit 1 and never resends.

## Ownership and interfaces

|Task|Owned paths|Purpose|
|-|-|-|
|1 SDK|New `storage/objects_copy.go`, `storage/objects_copy_test.go`, `storage/objects_copy_security_test.go`; modify `storage/object_errors.go`, `storage/object_errors_test.go`, `storage/example_objects_test.go`, `testdata/storage/objects/README.md`; new `testdata/storage/objects/copy_result.xml`, `testdata/storage/objects/copy_error.xml`|Public native copy, bounded decoding, copy ambiguity, synthetic raw fixtures and compile-only SDK example|
|2 CLI|New `internal/cli/svc_storage_objects_copy.go`, `internal/cli/svc_storage_objects_copy_test.go`; modify `internal/cli/svc_storage_object_operations.go`, `internal/cli/svc_storage.go`, `internal/cli/readonly.go`, `internal/cli/readonly_test.go`, `internal/cli/errors.go`, `internal/cli/errors_test.go`, `internal/cli/gendocs_storage_object_operations.go`, `internal/cli/gendocs.go`, `internal/cli/gendocs_test.go`|Custom command, policy, safe metadata output, error precedence, generated-reference source|
|3 Docs|Modify `README.md`, `docs/wiki/Home.md`, `docs/wiki/Storage.md`, `docs/wiki/Storage-Objects.md`; generated `docs/wiki/CLI.md`, `docs/wiki/CLI-Storage.md`|Current supported copy guidance and generated reference|
|Manager|`RELEASE_NOTES.md`, this plan, copy-related pending statements in `docs/design/README.md`, `docs/design/storage-object-operations.md`, `docs/design/storage-object-operations-api.md`, `docs/design/storage-object-operations-checks.md`|Versioned feature facts and completed scope, preserving deferred gates|

Shared-file writers run sequentially against manager-verified predecessor edits; final integration follows the tagged release bases sequentially. Tasks 2 and 3 start after their consumed interfaces exist; do not give the same file to concurrent workers. No management SDK, credentials reader, transfer-file implementation, live test, tool pin, or dependency file changes belong here.

**SDK produces:**

```go
type CopyObjectInput struct {
    SourceBucket string
    SourceKey string
    Bucket string
    Key string
    Replace bool
}
type CopyObjectOutput struct {
    ETag string
    LastModified *time.Time
    VersionID string
    SourceVersionID string
    ServerSideEncryption string
}
func (c *ObjectClient) CopyObject(ctx context.Context, in *CopyObjectInput) (*CopyObjectOutput, error)
```

`storage.ErrObjectCopyUnconfirmed` has fixed recovery text: `storage: object copy is unconfirmed; inspect the destination before deciding whether to repeat the copy`. All method errors return nil Output. Private `unconfirmedObjectCopy(err error) error` preserves only safe API/context causes and forces APIError Retryable false. Private `(c *ObjectClient) decodeCopyObjectResult(r io.Reader) (*CopyObjectOutput, error)` decodes the complete XML result; the caller owns response closure and adds filtered response headers.

**SDK consumes:** existing `(c *ObjectClient) objectURL(bucket, key string) (*url.URL, error)`, `objectEncodePath(path string) string`, `(c *ObjectClient) signObjectRequest(req *http.Request, payloadHash string, now time.Time)`, `objectAPIError`, `(c *ObjectClient) objectResponseError`, and `filterObjectMetadata`. Consume the released `newObjectXMLDecoder(r io.Reader, maxBytes int64, roots ...string) *objectXMLDecoder` and `Token() (xml.Token, error)` for byte/depth/root/namespace/EOF defense, plus `(c *ObjectClient) objectScalarHeader(h http.Header, name string, maxBytes int) string` and `(c *ObjectClient) objectTime(value string, maxBytes int, layout string) *time.Time`. Copy's query remains empty even though the shared signer now signs canonical RawQuery.

**CLI produces:** `newStorageCopyObjectCmd(e *env) *cobra.Command`, `newStorageCopyObjectCmdWithClient(e *env, newObjectClient func(storage.ObjectConfig) (objectCopyClient, error)) *cobra.Command`, and private `objectCopyClient` containing only the public `CopyObject` signature. Add `registerStorageCopyObjectFlags(cmd *cobra.Command, f *storageCopyObjectFlags)`, `storageCopyObjectNotes() string`, and `storageCopyObjectExample() string`; real command and generated reference use the same registration. `storageCopyObjectFlags` embeds released `storageObjectOperationFlags` and adds sourceBucket/sourceKey strings and replace bool. The copy flag wrapper consumes `registerStorageObjectOperationFlags(cmd *cobra.Command, f *storageObjectOperationFlags, operation string)` with fixed `copy-object`; extend its key-bearing branch without list flags. Add the copy entry to released `storageObjectOperationDocOps() []docOp`, `storageObjectOperationNotes(operation string) string`, and `storageObjectOperationExample(operation string) string`, forwarding to the copy helpers. Preserve the narrow read and transfer client interfaces.

## Task 1: SDK request, result, and outcome

- [x] Write `TestCopyObjectValidation` first: nil input, false Replace in each region, invalid buckets/keys, invalid UTF-8, oversized keys, and identical targets return ErrInvalidInput, nil Output, and zero requests. False Replace says native create-only copy is unavailable; slash-ending keys remain single objects.
- [x] Write `TestCopyObjectRequest` using an injected clock and loopback transport: one PUT to the exact destination, zero body/Content-Length, empty SHA256, empty query, and signed `x-amz-copy-source`. Independently assert source encoding and destination RequestURI for spaces, Unicode, `%2F`, `%`, controls, `+`, `?`, `#`, dot segments, and repeated slashes; assert the exact canonical request/signature with synthetic credentials.
- [x] Assert absence of conditions, source conditions, version selectors, metadata/tag directives, content metadata, ACL, encryption, retention, legal hold, governance bypass, and all other operation calls. Model concurrent name changes without adding a preflight or source DELETE.
- [x] Add labeled hand-written raw `copy_result.xml` and `copy_error.xml`; record their synthetic provenance in the fixture README. Decode fixtures through public CopyObject. Valid results have one CopyObjectResult root, required nonempty scalar ETag, and optional LastModified; test absent and S3 namespaces plus bounded unknown fields and allowed whitespace/comments/XML declaration.
- [x] Write `TestCopyObjectResultBounds`: 64 KiB exact success and cap-plus-one failure, depth 16 and 17, missing/empty ETag, duplicate recognized fields, nested scalars, wrong/qualified lookalike namespaces, invalid UTF-8, entities, directives, extra processing instructions, multiple roots, trailing data, empty/short/truncated body, read loss, and cancellation while reading. Require complete clean EOF under deadline, nil Output on failure, closure on all paths, and no partial success.
- [x] Write `TestCopyObjectMetadataSafety`: ETag display cap 1 KiB; a valid required shape with unsafe/oversize ETag stays confirmed with ETag empty. LastModified uses 128-byte RFC3339 source bounds and UTC filtering. Version/source-version headers use 1 KiB limits; encryption uses 128 bytes. Omit duplicate, unsafe, invalid, or oversized optional values wholly; never synthesize length, checksum, content type, or source ETag.
- [x] Exercise credential reflections literally, XML/JSON escaped, and with short credentials in ETag, dates, headers, Error code/message, debug and error text. Include offset timestamps whose UTC forms newly match credentials and each RFC3339Nano, MarshalText, MarshalJSON, String (`%v`, `%+v`), and GoString (`%#v`) representation. Assert nil times and absence through direct SDK access, formatting and JSON; safe times retain UTC.
- [x] Write `TestCopyObjectOutcome`: 200 Error is unconfirmed and never success; 204/206/other 2xx, redirects, 501 NotImplemented, all other 5xx, malformed result, response loss, and cancellation after possible dispatch wrap ErrObjectCopyUnconfirmed. Allowlist NotImplemented; discard remote messages. Every APIError operation is `storage.CopyObject`, malformed XML/result uses safe local InvalidResponse, transport/read loss uses RequestFailed, ambiguity Retryable is false, and safe context sentinels remain discoverable.
- [x] Definitive 400/401/403/404/409/412/429 retain normal safe status mapping, with nil Output; 412 never maps to ErrObjectExists because no destination condition was sent. Known failed dial or cancellation before possible dispatch is ordinary RequestFailed, not ambiguity. Test classification precedence and count exactly one application attempt even on 429.
- [x] Write `TestCopyObjectNoReplay` with failed dial, stale HTTP/1 connection, disconnect and deadlines. Assert bodyless PUT cannot use hidden transport replay; preserve the isolated transport and no GetBody/replay setup. Every failure leaves source and destination without client cleanup or second dispatch.
- [x] Run `go test ./storage -run 'Test(CopyObject|Object.*Error|ObjectSign)' -count=1` and record meaningful failures before implementation.
- [x] Implement the exact public surface, fixed sentinel and private helpers in owned files; extend the private APIError operation allowlist for storage.CopyObject and safe S3 codes for NotImplemented. Validate both identifiers before request construction, use encoded source URL path only, sign one bodyless PUT, trace possible dispatch conservatively, and read/close the bounded XML result under the same deadline. Reuse shared XML defenses without weakening their read behavior; require complete Error decoding under 200 before extracting only safe allowlisted Code.
- [x] Add a compile-only `ExampleObjectClient_CopyObject` with synthetic in-memory credentials, Replace true, error handling and no Output marker, IAM lookup, or real unit-test call. SDK comments state same-project scope, unconditional current-name races, diagnostic versions, metadata limits, HCM04 encryption restriction, and unverified HAN02 encryption behavior.
- [x] Run the focused command again, then `go test -race ./storage -run 'Test(CopyObject|Object.*Error|ObjectSign)' -count=1`; expect PASS and report actual results.

## Task 2: CLI consent, policy, safe output, and reference source

- [x] Write `TestStorageCopyObjectPreflight` first: required source/destination identifiers, explicit S3 region/file, identical targets, false/missing Replace, missing yes, yes alone, invalid timeout, malformed query, bad flag/profile output, positional args, `--cli-input-json`, and explicitly supplied global region/project all refuse before credential loading or SDK construction. Defaults from environment/profile region/project do not select S3 resources.
- [x] Write `TestStorageCopyObjectReadOnly` covering flag, environment, profile and invalid policy values, including consent present. Refuse before the S3 file is read. Preserve safe Linux/macOS credential-reader support and unsupported-platform refusal. Assert no IAM file, token cache, login, management request, ambient credential chain, file snapshot, or subprocess occurs.
- [x] Write `TestStorageCopyObjectDispatch`: each valid region constructs one client with exact credentials/config and invokes CopyObject once with exact literal identifiers and Replace true. Use the existing five-minute default and 1s through 1h CLI timeout; profile/output/query prevalidation precedes credentials.
- [x] Write `TestStorageCopyObjectOutput`: JSON/table/text and direct queries include only accepted CopyObjectOutput plus SourceBucket, SourceKey, Bucket and Key. Redact credential reflections in all echoed inputs; escape control characters. Unsafe normalized times stay null through every format/query. Print no content, invented lengths/checksums, raw headers, URLs, or private state.
- [x] Write `TestStorageCopyObjectOutcome`: ambiguity classifies as ObjectCopyUnconfirmed and exit 1 ahead of wrapped auth/not-found/context codes, preserving fixed recovery advice and safe operation/status. Ordinary usage/read-only/consent exit 2; credentials/401 exit 3; 404 exit 4; 403/other failures exit 1. No failed call prints a success Output.
- [x] Write `TestStorageCopyObjectCompletedOutputFailure`: runtime query and stdout failure after confirmed success say copy completed, exit 1, expose no raw writer error, and never call CopyObject again. Debug uses only safe operation, duration, status, local code and count, with no fabricated byte count from XML length; ambiguity code takes precedence over context.
- [x] Write generator/registration assertions: `copy-object` exists once, is a write, rejects input JSON, shares exact live flags, exposes metadata-only query/output, and has no move, version, body/destination-file, arbitrary-header or create-only option.
- [x] Run `go test ./internal/cli -run 'Test(StorageCopyObject|GenDocs.*Object|ExitCode|Classify|ReadOnly)' -count=1`; record failures before implementation.
- [x] Implement the narrow injected SDK command, source flags, replacement/yes and policy checks in owned paths; register outside management storageOps/runOp. Reuse released object-profile, credential reader, renderOutput and completed-operation error boundaries. Extend only copy ambiguity classification/exit/debug precedence and the object-command read-only exemption as required.
- [x] Extend storageObjectOperationDocOps and generator notes/example dispatch from the real copy flag registration. Update general custom-object input-JSON and query/output prose to include copy without altering transfer, list, HEAD or delete semantics.
- [x] Run the focused CLI command again and `go test -race ./internal/cli -run 'Test(StorageCopyObject|GenDocs.*Object|ExitCode|Classify|ReadOnly)' -count=1`; expect PASS and report actual results.

## Task 3: Supported guidance and release verification

- [x] Update current README/wiki coverage and add a copy section/example to Storage-Objects. Explain native server-side transfer, `--replace --yes`, same-project/region credentials, current-name concurrency, source retained, diagnostic versions, default provider metadata behavior, no lossless clone promise, and unconfirmed-result recovery.
- [x] State HCM04 encrypted-source 501 and possible unencrypted destination despite enabled bucket encryption; HAN02 encryption is unverified. State that AWS's 5 GB reference is not a measured GreenNode limit, service refusal has no multipart/fallback, version history is not copied, and storage/version costs follow service terms. Keep create-only copy and move unavailable.
- [x] Include CLI example `vngcloud storage copy-object --s3-region HCM04 --s3-credentials-file ./storage.credentials --source-bucket app-files --source-key archives/bundle.bin --bucket app-backups --key archives/bundle.bin --replace --yes` and the compile-only SDK example, with no secrets or live helper.
- [x] Run `make gen-docs`; inspect generated CLI/CLI-Storage pages and `go run ./cmd/vngcloud storage copy-object --help`. Generate again into a private temporary directory and compare with checked-in pages using `diff -ru`; expect no differences. Inspect wiki relative page links and absolute repository links.
- [x] Manager adds the next actual `v0.x.y` feature release facts in RELEASE_NOTES, marks only the copy scope implemented in design authorities, preserves move/create-only gates and evidence labels, and checks plan boxes only after verification. Do not hard-code a version before preceding releases land.
- [x] Run focused storage/CLI tests, `go test -race ./storage ./internal/cli`, `make check`, `bash scripts/check-sdk-deps.sh`, `make wiki-preview`, and `make semgrep`; record results and unavailable platform checks honestly. No dependency or tool changes mean no new dependency-vetting work. CI still retains vulnerability, gitleaks, connected Semgrep Code and Supply Chain, generated-doc, boundary, tidy, cross-vet, live-tag compile/vet, and Wiki preview gates.
- [x] After the final code task, manager dispatches one independent adversarial code_reviewer Sol xhigh in a disposable public-source copy. Review exact dispatch/replay boundaries, concurrent writers, XML/result bounds, timestamp normalization, credentials/echoes/debug, error precedence, no source deletion/fallback/rollback, completed-output failures, and docs/help consistency. Reviewer never edits source; report severity, concrete scenario, path:line, fix direction and skipped checks.
- [x] Independent adversarial review reported no actionable findings. No source fix or separate fix-confirmation job was needed.
- [x] Manager inspects complete diff, exact paths, `git diff --check`, lengths, links, tests, signatures and review. Commit only exact paths with SSH signing and DCO sign-off, then use standing PR/landing authority and the repository's exact-head CI procedure. Land only a descendant of current master; await every applicable successful master-push validation and disabled Wiki publication evidence before the single copy feature tag. Handle concrete available review/notification findings without waiting for optional review-only runs.
- [x] Record actual files, check results, skipped live/platform checks, independent review/fix confirmation, accepted SHAs and CI evidence. No live check is claimed; additional live work requires separate manager authority under live-data rules. Native copy completion does not unlock create-only copy or move.
