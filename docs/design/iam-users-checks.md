# IAM users: checks

Unit tests, probe evidence, fixture provenance, and live checks for [IAM
users](iam-users.md). [IAM users: API](iam-users-api.md) owns the wire
evidence and SDK contracts these checks enforce; [IAM user administration:
API and checks](iam-users-admin-api.md) owns the reset and MFA
checks that run on the same disposable user.

## Unit tests

Use synthetic canaries and `httptest`, never real credentials or live calls.
Each operation tests its exact path, body/query, success status, and every error
in the [calls table](iam-users-api.md#calls), plus unexpected responses.

- Decode `create_user.json`, `get_user.json`, `get_user_mfa.json`, and
  `list_users_filtered.json` through the SDK, plus an empty-bytes create and a
  get with an extra synthetic null key inside a factor. Check both `createdAt`
  forms, `LastUse` 0 when absent, and omitted, null, wrong-type, and duplicate
  identity/metadata keys.
- Check generator alphabet, default and chosen lengths, the 8 and 50 bounds, all
  four classes, unbiased sampling, fresh values, entropy failure, cancellation,
  bounded exhaustion, and the supplied-plus-length refusal; supplied-password
  shape and immutable Input.
- Count create POSTs for 401, 429, 500/502, all redirects, partial bodies, body
  overflow, dropped connections, and cancellation before/after send. Distinguish
  proven NotSent from custom-transport dial-shaped errors.
- Test every outcome-table branch with generated and supplied passwords, both
  sentinels via `errors.Is`, API errors via `errors.As`, and context identities.
  Test 201 with empty body, null/array/malformed JSON, missing, invalid, or
  mismatched ID, failed confirmation, duplicate matches, and the duplicate 400.
  A zero-length body sets `BodyWasEmpty` and permits the lookup, as does `{}`;
  whitespace only, `null`, an array, or malformed JSON is a withheld decode
  failure with no lookup and no CandidateID. Only a valid ID with failed
  confirmation is `ErrCreateUnconfirmed`; a missing, empty, invalid,
  non-string (`{"id":17}`), or credential-bearing ID and a name-only match are
  `ErrUserCreateUncertain`, CandidateID only for the last, never a nil error.
  A 201 `id`, or a list or get identity after a 401 refresh, that equals a
  sent bearer token is refused with no follow-up and no token in any error
  node, output, or CLI line.
- Exercise missing/null/negative/inconsistent list metadata, multi-page results,
  substring and case-only names, wrong get identity, caps, and public ListUsers
  compatibility without Name. Verify CreatedID provenance.
- Test self before the target read; unknown/root caller; missing caller ID;
  service-account caller; non-iam, missing, null, and wrong-type origin;
  mismatched target ID; failed target read. Assert zero mutations on refusal,
  not zero reads. An attached user is not refused; its 409 stays `Conflict`.
- Test DELETE 204, 404, 409, retries ending in 404, exhausted 5xx, no redirects,
  cancellation, and dependency 404 labels. Send zero DELETEs for a duplicate
  `userId` or `userType`, or a `USERID` alias after the real key, in the caller
  identity, and for a duplicate `id`, `username`, or `from` in the target user.
- Put a canary in a `$numberLong` timestamp, a `root`, and a malformed strict
  body on every strict read, and check that no SDK error node, `APIError` field,
  or CLI stderr line carries it. Canary-check stdout, stderr, debug, capture,
  every reachable error node, and supported fmt/slog/JSON paths, including
  nested escaped password echoes in error codes/messages and success bodies;
  unsafe formatting is forbidden, not claimed safe. Confirm requests serialize
  the actual value.
- CLI tests cover exactly-one flag, empty file, private file mode, symlinks,
  FIFO/device/directory refusal, terminal stdin, CRLF/spaces, overflow, stalled
  input deadline, and cancellation. `beforeInput` runs before any
  `--cli-input-json` read: `--password-file -` with `file://` JSON exits 2 while
  stdin is a never-closing pipe; a FIFO at `file://` exits 2 for any command;
  read-only refuses before any stdin read, proven by a stdin that fails the test
  on `Read`; cancellation during a stalled stdin exits within the deadline with
  no request. Delete without `--yes` sends nothing.
- Inject file open/write/sync/close/removal failures. Check exclusive 0600
  creation, existing-file preservation, saved passwords on partial output, no
  request of any kind after a file failure, the `CreatedID` or username and the
  `delete-user` command in the message, and exit precedence.

## Probes

The base probe ran twice under owner approval, after adversarial review of its
credential-free source, with `vngcloud-probe-<8 hex>` names, an in-memory
password, a private ledger of returned IDs, and deletion of attributable IDs
only. For the test account it established create 201 `{"id"}`, the get and
list shapes above, the case-insensitive substring filter, the empty result for
an absent name, empty default attachments, the duplicate 400, delete 204 with
404 on repeat and on get, and the quota shape with usage restored after each
base run. The Google-only probe has separate
[cleanup and quota evidence](iam-users-admin-api.md#probes); it does not inherit
that restoration claim. Sign-in as the new user through the SDK's IAM user login
succeeded with no MFA challenge. The caller identity matched the created ID,
and the get then carried `lastUse`. The base administration checks used that
disposable user; the Google-only probe used its own user.

Still unverified: delete 409 meaning, custom password-policy breadth,
concurrent races, token revocation after deletion, and accounts with other
controls. A repeat run keeps the same rules: owner approval naming the account,
resources, sign-in scope, cost assumption, and cleanup authority;
serialization with other IAM writers; an absent-name precheck; no change to
policy or account security settings; no deletion of the caller, an attached or
pre-existing user, or an ID the ledger does not hold; no short-name or
weak-password negatives, which custom policy may accept while creating extra
identities; and a stop with a private leftover report on ambiguity. A prefix
alone never authorizes cleanup.

### Fixture provenance

`testdata/iam/create_user.json`, `get_user.json`, `get_user_mfa.json`, and
`list_users_filtered.json` are synthetic rebuilds of the recorded keys, types,
and booleans. A rebuild that keeps the withheld factor key matches the observed
get lengths; the published files omit that key and are 11 bytes shorter, as
[fixture provenance](../../testdata/iam/user-provenance.md) records. Raw
user-write bytes stayed in memory; only credential-free list, attachment, and
quota bodies persisted privately. Tests
that build adversarial variants label them synthetic; an account-bearing raw
read stays under the private paths in [live
data](../../instructions/live-data.md) until the manager sanitizes it.

## Live checks

Add GetUser and filtered ListUsers to the authorized read suite. Gate
`TestLiveWriteIAMUser` on both `VNGCLOUD_LIVE_WRITE=1` and
`VNGCLOUD_LIVE_IAM_USER=1`. Use a unique `vngcloud-live-<8 hex>` name and only
resources recorded by this run; never sweep all prefix users. Exercise
generation, create, private temporary secret-file persistence, get, filtered
list, self refusal with no DELETE, guarded delete, and only separately opted-in
[administration live checks](iam-users-admin-api.md#live-checks) on the same
user. Register cleanup before create using a ledger, never a name-based delete;
on uncertain ownership, report a leftover and stop. Cleanup deletes only this
run's `CreatedID` through `DeleteUser`, with no attachment precheck: the server
decides about attachments, and a 409 or any other failure is reported as a
leftover, never retried by name. Remove temporary secrets
and emit only fixed labels, statuses, counts, and booleans.

### Google MFA live checks

The Google branch uses test-only set-up and verify helpers on the attributable,
identity-confirmed user. Reuse the reviewed probe's bounded in-memory enrolment
and TOTP logic. Set-up sends once; verification allows at most two attempts,
with only one next-window retry after the probe's definite 400 rejection, no
retry after any other error, and context-bounded waiting. Refuse redirects.
Never fetch the QR URL, persist the seed or code, sign in, alter policies, or
enrol any other user.
Suppress captures and arbitrary server text before set-up and on every later
request, including cleanup and quota reads. Offline canaries cover unknown
secret-bearing fields as well as the known seed, code, QR data, and credentials.

After verification, require verified and enabled true. Exercise public SDK
enable as a guarded no-op, disable as a mutation and then a no-op, enable as a
mutation and then a no-op, and final disable as a mutation. Check Enabled and
Changed, verification retention, and identity/origin after each step. A failure
stops the sequence and leaves only attributable cleanup, never a repeat setup
or blind mutation retry. No public enrolment method or command is added.

Read the published IAM-user quota before create and after confirmed deletion
through a test-only helper, never a new public SDK method. Require 200, strict
JSON within 256 KiB, and explicit numeric usage and limit; refuse redirects.
Compare usage in memory, emit only a restoration boolean, and fail the live
check on an unavailable or unequal result. Keep quota comparison separate from
DELETE/GET absence evidence. This helper and the Google branch require
independent adversarial review and explicit run authority before execution.

The independently reviewed helper passed authorized verification on its own
disposable user. The IAM read suite passed user detail and filtered identity
checks. The Google SDK cycle passed every mutation and no-op, retained verified
state, and ended disabled. Guarded deletion and a subsequent get confirmed
absence; the in-memory before/after quota comparison confirmed restoration.
Reset remained disabled. The helper persisted no enrolment secret or code,
and temporary password storage was removed. These checks establish this run's
results without supplying an enrolled raw fixture or resolving the older
probe's unavailable quota comparison.
