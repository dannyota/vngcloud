# Single-object deletion implementation plan

Ship the accepted current-key DELETE as one SDK, CLI, help, and wiki release after the reviewed ListObjects/HeadObject release is verified, landed, and tagged.

For workers: use test-first development under the project's brief, worktree, ownership, routing, and review rules; the manager dispatches workers and owns Git state.

**Goal:** Delete one current object name with explicit CLI destructive consent, bounded safe metadata, and a distinct unconfirmed outcome.

**Architecture:** Extend the existing standard-library `storage.ObjectClient` with one bodyless signed DELETE and a handwritten CLI command that uses its public SDK. Reuse the landed read release's metadata and command foundations without adding future copy or move methods.

**Tech stack:** Repository-pinned Go, standard-library SDK, existing Cobra and rendering dependencies, deterministic synthetic responses and loopback HTTP tests; no dependency additions.

**Authorities:** [Accepted operations](../design/storage-object-operations.md#deleteobject), [DELETE protocol](../design/storage-object-operations-api.md#single-object-delete), [checks](../design/storage-object-operations-checks.md), [transfer boundaries](../design/storage-objects-api.md), [workflow](../../instructions/workflow.md), and [releases](../../instructions/release.md).

## Constraints and execution

- [ ] Begin SDK work only after independent plan review and manager acceptance of the shared read foundation; CLI work requires manager-verified read CLI helpers and deletion SDK interfaces. Consume verified files, never an active writer's unfinished files. Before complete-slice verification and independent code review, integrate only this feature onto the tagged read release; deletion landing still waits for the read feature tag and successful master validation.
- [ ] Preserve supplied design acceptance edits; owner approval and selected parallel execution already cover this release, so do not ask another approval or execution-method question.
- [ ] Dispatch `implementer` on `gpt-6.1-sol` medium, failures to `troubleshooter` on `gpt-6.1-sol` xhigh, and independent reviews to the appropriate reviewer on `gpt-6.1-sol` xhigh; set model and effort explicitly.
- [ ] Give each worker a credential-free `.claude/worktrees/<slice>` worktree with disjoint owned paths; run at most three workers across all slices, including reviewers.
- [ ] Each brief states exact paths, authorities, interfaces, checks and forbidden actions; workers start with `git status --short`, preserve others' edits, make no Git mutations, and report conflicts instead of widening scope.
- [ ] Forbid secret/private-config reads, `.env`, private captures, live calls, unrelated network, dependencies, and edits outside the brief; permit loopback tests and required-check network only under project sandbox/cache settings.
- [ ] The manager inspects worker diffs and reruns focused checks before accepting results or starting dependent work; run one independent adversarial review after all code and docs, then confirm fixes independently, without per-task review gates.
- Preserve `storage.Client`, IAM management transport, transfer behavior, root re-exports, `go.mod`, and `go.sum`; no HEAD, GET, list, version selection, preconditions, MFA, retention bypass, verification request, retry, rollback, copy or move implementation.
- SDK `ObjectConfig` remains unchanged: exact HCM04/HAN02, fixed TLS endpoints, no proxy or redirect, five-minute default deadline and one-hour maximum, earlier caller deadline, standard-library dependencies only.
- DELETE removes the current name unconditionally; `s3:DeleteObject` is the action requirement, not a live-proven minimal-grant combination, and no read or console grant is required by the client.
- Neither 204 nor optional marker/version metadata proves prior existence, permanent erasure, reclaimed quota, empty bucket, or safety against concurrent writers; repeated deletion can target new data or add another marker.
- Human docs wrap at 80 columns; this plan uses one line per paragraph/list item, no dates or em dashes; maintain Markdown/source length limits.

## Files and dependencies

|Task|Exact owned paths|Dependency|
|-|-|-|
|1 SDK|New `storage/objects_delete.go`, `storage/objects_delete_test.go`, `storage/objects_delete_security_test.go`; modify `storage/object_errors.go`, `storage/object_errors_test.go`, `storage/example_object_operations_test.go`; new `testdata/storage/objects/delete_204.http`; modify `testdata/storage/objects/README.md`|Manager-verified shared read foundation; integrate onto tagged read release before complete-slice checks/review|
|2 CLI|New `internal/cli/svc_storage_objects_delete.go`, `internal/cli/svc_storage_objects_delete_test.go`; modify `internal/cli/svc_storage.go`, `internal/cli/svc_storage_object_operations.go` only shared flag registration, `internal/cli/svc_storage_objects.go` only debug classification, `internal/cli/errors.go`, `internal/cli/errors_test.go`, `internal/cli/gendocs_storage_object_operations.go`, `internal/cli/gendocs.go`, `internal/cli/gendocs_test.go`|Task 1 public types, then SDK checks before integrated checks|
|3 Docs|Modify `README.md`, `docs/wiki/Storage-Objects.md`, `docs/wiki/CLI.md`, `docs/wiki/CLI-Storage.md`|Task 2 generator; generated pages only through generator|
|Manager|`RELEASE_NOTES.md`, `docs/design/storage-object-operations.md`, `docs/design/storage-object-operations-api.md`, `docs/design/storage-object-operations-checks.md`, `docs/design/README.md`, this plan|Whole release verified; preserve evidence limits|

SDK authors may start core method/error/fixture work from the verified shared foundation while the read release proceeds. The SDK example append waits for the read CLI author to finish and the manager to hand off the verified example file; never assign that shared path to both active writers. CLI authors may start after manager acceptance of read CLI helpers, using the exact public types below; Task 2 behavior-test execution waits for accepted Task 1 public interfaces, and missing-method compilation is an unavailable dependency rather than behavioral red; never add production stubs. Docs can follow Task 2 while the manager verifies SDK integration, with generated output owned only by the docs worker.

Existing SDK seams consumed unchanged are `(*ObjectClient).objectURL(bucket, key string) (*url.URL, error)`, `(*ObjectClient).signObjectRequest(req *http.Request, payloadHash string, now time.Time)`, `(*ObjectClient).filterObjectMetadata(value string, maxBytes int) string`, `objectAPIError(op string, status int, code string, cause error) *vngcloud.APIError`, and `(*ObjectClient).objectResponseError(op string, resp *http.Response, createOnly bool) error`; DELETE always passes false to the last helper.

The landed read foundation also supplies `(*ObjectClient).objectScalarHeader(h http.Header, name string, maxBytes int) string` in `storage/object_metadata.go`; consume it for duplicate-filtered optional headers, and leave read timestamp/user-metadata helpers unchanged.

Read-release CLI interfaces consumed are `storageObjectOperationFlags`, `registerStorageObjectOperationFlags(cmd *cobra.Command, f *storageObjectOperationFlags, operation string)`, `storageObjectOperationDocOps() []docOp`, `storageObjectOperationNotes(operation string) string`, and `storageObjectOperationExample(operation string) string` in `internal/cli/svc_storage_object_operations.go` and `internal/cli/gendocs_storage_object_operations.go`; extend their fixed operation cases for delete rather than create a second common flag/doc foundation; preserve narrow read/transfer test interfaces.

Existing CLI seams consumed unchanged are `loadObjectProfile(*globalFlags) (objectProfile, error)`, `readOnlyPreConfig`, `readOnlyProfileValue`, `readS3CredentialsFile`, `objectFilesSupported`, `compileQuery`, `renderOutput`, `objectCompletedError`, `logStorageObject`, `classify`, and `exitCode`; never call `loadConfig`, management clients, IAM login or token-cache loaders.

## Review focus

1. A zero-body DELETE can be transparently replayed on a reused connection; Task 1 counts raw HTTP requests and verifies the production no-reuse/HTTP1 transport.
2. HTTP 204 is bodyless and net/http can suppress content bytes; Task 1 proves no body draining and classifies only transport-surfaced invalid framing as unconfirmed.
3. Duplicate or normalized header text can leak credentials or invent a marker; Task 1 tests whole-field filtering before parsing, duplicate omission, and every retained representation.
4. A 404 or context sentinel inside an ambiguous write can hide the operation outcome; Tasks 1 and 2 check sentinel/error envelope precedence and nil failure outputs.
5. Query/stdout failure after confirmed deletion can induce a destructive rerun; Task 2 proves the completed message and exactly one dispatch.

## Task 1: Public DELETE and safe outcome

**Produces:** `DeleteObjectInput{Bucket string, Key string}`, `DeleteObjectOutput{DeleteMarker *bool, VersionID string}`, `(*ObjectClient).DeleteObject(ctx context.Context, in *DeleteObjectInput) (*DeleteObjectOutput, error)`, and `storage.ErrObjectDeleteUnconfirmed`.

**Private interface:** `unconfirmedObjectDelete(err error) error` returns the DELETE sentinel wrapping only a safe APIError and its safe causes, forces Retryable false, and never reuses upload recovery text.

- [ ] Write `TestDeleteObjectValidation`: nil input/client, invalid bucket, empty/invalid UTF-8/over-1,024-byte key return nil Output plus `ErrInvalidInput` and zero requests; canceled context before dispatch preserves its sentinel, is not unconfirmed, and sends zero requests.
- [ ] Write `TestDeleteObjectRequest`: both exact regions send one DELETE to the literal encoded key with empty RawQuery, empty payload SHA256, correct signing region/headers, nil or NoBody body and zero content length; assert no If-Match, versionId, bypass, MFA, retention, idempotency or content-transfer header, no HEAD/GET/list, and no path cleaning for slash-ending/control/Unicode/space/percent/plus/query-like keys.
- [ ] Write `TestDeleteObjectSuccessFixture`: decode synthetic raw `delete_204.http` through the SDK transport, accept 204 with no optional headers, true/false marker pointers and safe VersionID; absent/filtered marker remains nil and version remains empty; a missing-name 204 succeeds without an existence claim.
- [ ] Write `TestDeleteObjectBodylessSuccess`: real loopback 204 returns without reading or waiting for a declared length; injected 204 with a body whose Read fails the test closes exactly once and performs zero reads, including arbitrary supplied content, because 204 is bodyless; optional malformed metadata never changes confirmed success.
- [ ] Write `TestDeleteObjectStatusOutcomes`: 400/401/403/404/409/412/429 produce definitive safe APIErrors and appropriate shared sentinels, including bodyless 404; 200/201/202/206, 3xx and 5xx are unconfirmed with Retryable false; bound XML error reads to 64 KiB plus a probe and do not retain raw XML/URLs/transport errors; never retry 429 or treat 412 as `ErrObjectExists`.
- [ ] Write `TestDeleteObjectDispatchBoundary`: failed dial/TLS and pre-dispatch deadline are ordinary safe failures; disconnect/cancel/deadline after connection acquisition are unconfirmed, preserve safe context sentinels and Operation `storage.DeleteObject`, and return nil Output; malicious error text never reaches formatting or JSON.
- [ ] Write `TestDeleteObjectNoTransportReplay`: raw loopback dropped response, dropped connection before response, and server-advertised stale keep-alive fixtures count exactly one received DELETE and no auxiliary requests; a preceding request cannot make production DELETE reuse that connection; inspect production DisableKeepAlives true, HTTP1-only, no GetBody or idempotency key, and count accepted connections as well as requests.
- [ ] Write `TestDeleteObjectMalformedWire`: raw malformed framing or truncated headers surfaced as a transport error yield nil Output/unconfirmed once dispatch may have occurred; characterize net/http's actual 204 NoBody behavior separately from injected responses, without claiming suppressed wire bytes are exposed or drained.
- [ ] Write `TestDeleteObjectMetadataSafety`: omit observable duplicate scalar headers, conflicting case variants, marker values other than exact true/false, controls, malformed UTF-8 and oversize values; version max is 1 KiB and marker raw max is five bytes; filter before bool parsing so credentials `true` or `false` remove the marker, and reflected literal/escaped/short credentials remove VersionID wholly.
- [ ] Exercise safe output and errors through direct fields, `%v`, `%+v`, `%#v`, JSON and supported CLI projections; accepted marker bool/VersionID representations must not reflect credentials; no unsafe optional metadata revokes confirmed success or triggers another request.
- [ ] Run `go test ./storage -run 'TestDeleteObject|TestObjectErrors' -count=1` before implementation and record the expected missing-method/behavior failure.
- [ ] Implement the exact public method in `storage/objects_delete.go`, reuse existing URL/signer/transport/deadline boundaries, validate locally, track conservative dispatch with the existing httptrace connection-acquisition pattern, send one bodyless DELETE, close accepted 204 without draining, classify definitive 4xx before ambiguity, and return nil Output on every error.
- [ ] Extend `storage/object_errors.go` with the DELETE sentinel, local operation allowlist and fixed message `storage: object delete is unconfirmed; inspect the current key and version history before deciding whether to delete again`; preserve APIError/status sentinels and safe context causes, and keep Retryable false for the ambiguity wrapper.
- [ ] Add `ExampleObjectClient_DeleteObject` to `storage/example_object_operations_test.go` with in-memory synthetic credentials, unconditional current-key/versioning caveat and explicit unconfirmed recovery handling; no Output directive or live execution.
- [ ] Record `delete_204.http` as a synthetic protocol/header fixture in `testdata/storage/objects/README.md`, not a provider capture; repeat focused checks and run `go test -race ./storage`, expecting PASS.

## Task 2: CLI command, outcome precedence and generated-help source

**Produces:** `newStorageDeleteObjectCmd(e *env) *cobra.Command`, `newStorageDeleteObjectCmdWithClient(e *env, factory func(storage.ObjectConfig) (objectDeleteClient, error)) *cobra.Command`, and private `objectDeleteClient` with only `DeleteObject(context.Context, *storage.DeleteObjectInput) (*storage.DeleteObjectOutput, error)`.

**Consumes:** Task 1 public method/types/sentinel and the landed read-release common flags, policy and generator interfaces; use the fixed operation string `delete-object`.

**Display type:** private delete metadata has `Bucket string`, `Key string`, `DeleteMarker *bool`, and `VersionID string`; keep nil marker visible as JSON null, filter echoed Bucket/Key with both credentials, and render only this envelope through the shared renderer.

- [ ] Write `TestStorageDeleteObjectPolicy`: each flag/environment/profile read-only source, missing `--yes`, invalid region/bucket/key/timeout, explicit global `--region`/`--project-id`, invalid output/query/profile, `--cli-input-json`, unsupported platform and unsupported body/destination/replace/version/header flags fail before S3 credential loading and client construction; valid --yes permits exactly one DELETE, never a prompt.
- [ ] Write `TestStorageDeleteObjectIsolation`: require explicit --s3-region/--s3-credentials-file/--bucket/--key, retain --profile and --transfer-timeout default 5m/range 1s..1h; ignore management environment/profile region/project/auth and ambient S3 credentials, use existing private-file reader on Linux/macOS, and assert no IAM/management request, credential/token-cache access, subprocess or auxiliary SDK method.
- [ ] Write `TestStorageDeleteObjectOutput`: JSON/table/text and direct-field query show only accepted output plus redacted inputs, escape key controls, print no content/progress and emit no success envelope on error; debug has only operation/duration/status/bytes/local code, success status 204 and zero content bytes, with no path/header/URL/key/credential.
- [ ] Write `TestStorageDeleteObjectErrorPrecedence` and extend shared `TestClassify`/`TestExitCode` tables: success 0; usage/config/input/read-only/consent 2; credentials/401 3; 404 4; 403/ordinary failure 1; context anywhere 1; DELETE ambiguity always code `ObjectDeleteUnconfirmed`, exit 1 and fixed recovery ahead of wrapped 401/404/context/APIError code, including debug ambiguity ahead of context.
- [ ] Write `TestStorageDeleteObjectCompletedError`: valid syntax with runtime query failure or failing stdout after SDK 204 exits 1, explicitly says delete completed and do not retry, retains no unsafe writer/query text, and counts one DELETE.
- [ ] Write `TestGenDocsDeleteObject`: generator lists Write requiring --yes, exact required flags and metadata fields, current-key/concurrent-writer/versioning caveats, 204 not prior-existence proof, no version/precondition/body/replace/CLI JSON input and no move command; existing read/transfer docs remain accurate.
- [ ] Run `go test ./internal/cli -run 'Test(StorageDeleteObject|GenDocsDeleteObject|Classify|ExitCode)' -count=1` and record red before implementation.
- [ ] Implement manual command in `internal/cli/svc_storage_objects_delete.go`, register outside management `storageOps`/`runOp` in `newStorageCmd`, resolve policy and validate consent/options/output/query before credentials, call public SDK once and render the display envelope; reuse `storageObjectOperationFlags` and `registerStorageObjectOperationFlags` with `delete-object`, registering bucket/key and common S3 options only; --yes stays inherited, and transfer/read interfaces remain unchanged.
- [ ] Add early DELETE ambiguity branches to `classify`, `exitCode` and `logStorageObject`; successful rendering failures use `objectCompletedError("delete", "metadata output failed; do not retry the operation", err)` without resend.
- [ ] Extend `storageObjectOperationDocOps()`, `storageObjectOperationNotes(operation string) string`, and `storageObjectOperationExample(operation string) string` in `gendocs_storage_object_operations.go` and their existing `gendocs.go` routing, sharing delete flag registration with doc-field generation; add root ambiguity-code prose without manually editing generated pages.
- [ ] Repeat focused tests and run `go test -race ./internal/cli`, expecting PASS; report policy ordering, isolation, envelope/debug precedence and one-dispatch output-failure evidence.

## Task 3: Wiki and release facts

- [ ] Update `docs/wiki/Storage-Objects.md` and README coverage for SDK/CLI current-key deletion, --yes, all read-only sources, explicit private S3 credentials/regions, timeout, metadata-only output/query, exact exit codes and distinct ambiguity recovery; retain native copy and move as unavailable until their separate releases.
- [ ] Explain no concurrent-writer protection, no existence proof, enabled/suspended versioning behavior with retained versions/markers, no permanent-erasure/quota guarantee and why repeat DELETE needs inspection first; name s3:DeleteObject without claiming proven minimal grants or adding console/read requirements.
- [ ] Run `make gen-docs` to regenerate only `docs/wiki/CLI.md` and `docs/wiki/CLI-Storage.md`, compare second generation in a private temporary directory with `diff -ru`, and inspect `go run ./cmd/vngcloud storage delete-object --help`; expect no generation differences and accurate consent/caveats.
- [ ] Run `make wiki-preview`, `git diff --check`, and `bash scripts/check-lengths.sh`; inspect touched links/anchors, using Page-Name.md wiki links and absolute repository URLs for source links; no wiki publication.

## Manager verification, adversarial review and release

- [ ] Inspect all owned diffs, run both focused suites and `make check` before every commit, including plan/docs commits; send failures to a briefed troubleshooter instead of weakening/retrying checks.
- [ ] Run `bash scripts/check-sdk-deps.sh`, `GOOS=windows go vet ./...`, `GOOS=darwin go vet ./...`, `go vet -tags live ./...`, `go vet -tags livewrite ./...`, `go test -tags live -run '^$' ./livetest/`, and `go test -tags livewrite -run '^$' ./livetest/`; confirm unchanged dependencies and distinguish cross-vet/compile-only from native execution/live proof.
- [ ] Verify generated-doc comparison/help and wiki preview, source lengths and diff whitespace; repeat checks only after relevant changes or unresolved failures, not as duplicate worker jobs.
- [ ] Prepare a disposable public-source review copy with reviewed base/diff/new-file inventory and no secrets/private state; dispatch one independent `code_reviewer` Sol xhigh adversarial release review after all tasks to trace endpoint trust, credentials/reflections, transport replay, bodyless-204/raw-wire behavior, bounded error bodies and metadata, status/context/ambiguity precedence, versioning/concurrency claims and completed output failure.
- [ ] Brief fixes to separate implementer/troubleshooter jobs, verify their checks, and obtain independent reviewer fix confirmation; a reviewer never edits reviewed source or reviews its own work.
- [ ] Reuse recorded HCM04 delete/versioning observations and published HAN02 compatibility; synthetic tests establish client mechanics only, not server conditions, marker races or minimal permissions; conditional-delete/move evidence and a new live matrix are not release prerequisites.
- [ ] No live call is authorized by this plan; any separately authorized targeted observation belongs to the manager under live-data/testing rules, with named resources/cost/cleanup and sanitized provenance, not worker implementation scope.
- [ ] Update RELEASE_NOTES for the whole deletion release and current-state design/index coverage after shipment, preserving pending native copy and gated move/create-only copy; do not overwrite accepted evidence distinctions or ADRs.
- [ ] Stage only exact authorized paths; preserve ignore rules; manager uses owner SSH-signed commits with DCO sign-off, signed tags, plain change/purpose messages and standing Git/landing authority; no worker Git operations or partial SDK-only tag.
- [ ] Open the PR, inspect complete ancestry/diff and signatures, require all applicable actual exact-head PR validation including connected security scans, handle available reviews and repository notifications, and land a descendant of current master under workflow's approved admin bypass; red validation blocks and is fixed forward.
- [ ] Require every applicable landed-head master validation, confirm master still names that head and wiki publication remains disabled, then create one signed `v0.x.y` feature tag for deletion before native copy proceeds; successful preview is not publication.
- [ ] Report exact new/changed/deleted files, checks/results, skipped checks/reasons, confirmed review invariants, evidence limits and remaining copy/move gates; mark boxes only after manager verification.
