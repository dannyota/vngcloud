# Release Notes

## v0.85.0 - IAM User Create, Delete, and Reads

- SDK `GetUser` and CLI `iam get-user --user-id` read one IAM user:
  `CreatedAt`, `LastUse` (epoch milliseconds; `LastUse` stays 0 until the
  user first signs in), `Enabled`, `From`, `ID`, and `Username`.
  `ListUsersInput.Name` and `iam list-users --name` filter by the server's
  case-insensitive substring match.
- SDK `CreateUser` and CLI `iam create-user --username` create an IAM user
  with no rights and no MFA. Give exactly one of `--password-file <path>`
  (or `-` for stdin) or `--secret-file <new-path>`. With `--secret-file`,
  the CLI generates the password, 32 characters by default (choose 8 to 50
  with `--password-length`), always with uppercase, lowercase, digits, and
  `@`, and saves it to a new file at mode 0600. A password is never taken
  from argv or `--cli-input-json` and never printed.
- Create sends once and never retries. An uncertain outcome returns
  `ErrUserCreateUncertain` (exit 1) and keeps a generated password saved; a
  user found only by name is reported as `CandidateID`, never as proof that
  the password belongs to it. A confirmed create with failed confirmation
  reads returns `ErrCreateUnconfirmed`. If saving the generated password
  fails, the CLI sends no cleanup request and prints the exact `delete-user`
  command.
- SDK `DeleteUser` and CLI `iam delete-user --user-id --yes` refuse the
  caller's own user, root or unknown callers, and a user whose origin is not
  `iam` (`ErrOriginNotIAM`). The server decides about attached policies and
  groups; its 409 stays `Conflict`.
- Behavior change for every command: a `--cli-input-json file://` path
  that is not a regular file (a FIFO, a device, or a directory) now fails
  with exit 2 instead of waiting.
- Compatibility: unkeyed `ListUsersInput` literals no longer compile.
  `ListUsers` and `list-users` now cap a response at 4 MiB and refuse a
  larger one, and they refuse a response whose user ID or username contains
  a credential the request sent.

## v0.84.2 - Strict, Bounded IAM Guard Reads

- Fix: every read an IAM write guard depends on now rejects a duplicate
  JSON key, two keys that would fill the same field (such as `actions`
  and `Actions`), malformed JSON, trailing data, and an empty body. Before,
  such a response could hide a privileged grant, so a guarded write could
  go through against a protected user, group, or service account.
- Guard reads are capped at 256 KiB for single objects and 4 MiB for
  lists; a larger response fails without decoding. Before, they had no
  size limit.
- The covered reads are caller identity, the action list, policy and group
  gets, policy pages, attachment lists, and a user's groups. A failure
  refuses the guarded write before any write request.
- Compatibility: `iam get-policy`, `get-group`, `list-actions`, and
  `get-caller-identity` share these reads, so they now fail on the same
  ambiguous, empty, or oversized responses. Policy `condition` maps still
  accept keys that differ only by case.

## v0.84.1 - IAM Guard Fails Closed on Malformed Statements

- Fix: the IAM write guards now treat a malformed policy statement as
  privileged instead of as granting nothing. A null statement, a statement
  whose effect is neither `allow` nor `deny`, and an `allow` statement with
  a missing, null, or empty action list each count as privileged. Before,
  a policy holding only such statements could leave a protected user,
  group, or service account unprotected.
- Compatibility: guarded writes that read such a policy now refuse with
  `PrivilegedChange` before any write request. `create-policy` and
  `update-policy` refuse a document with an `allow` statement that lists
  no actions. A `deny` statement is still ignored.

## v0.84.0 - vMonitor SMS Orders and Quota Lifecycle

- SMS quota reads, quotes, and orders now work with
  `--kind SMS`, under the same guards as Email: an existing quota refuses
  the order, and a paid package needs a `--max-price` covering the higher
  of its quote and list price (the paid SMS package lists 55000 VND). SMS
  usage reads stay refused locally (exit 2) until verified.
- SDK `SetNotificationQuotaAutoRenew` and CLI
  `monitor set-notification-quota-auto-renew --kind Email|SMS
  --enabled=false` turn notification auto-renew off. Turning it on is
  refused locally (exit 2) until the renewal price is verified.
- SDK `SetSyntheticQuotaAutoRenew` and CLI
  `monitor set-synthetic-quota-auto-renew --enabled=true --max-price <VND>`
  quote the renewal, check the budget, and turn synthetic auto-renew on.
  The server refuses auto-renew on the free quota with a visible
  `BadRequest` (400, exit 1). Turning synthetic auto-renew off is not
  available.
- SDK `DeleteNotificationQuota` and CLI `monitor delete-notification-quota
  --kind Email|SMS --yes` delete an ACTIVE quota with auto-renew off. The
  deleted quota does not go to trash and cannot be recovered by these
  commands. Alerts that draw on it are expected to stop. SDK
  `DeleteSyntheticQuota` always refuses with `ErrInvalidInput`, and the
  CLI has no synthetic delete command.
- Each lifecycle write re-reads the quota just before sending, sends once,
  never retries, and confirms the result. `--expected-quota-id` refuses
  when the current quota has a different ID. An uncertain outcome prints
  partial output with `Stage` and returns `NotSettled` (exit 1). Read-only
  settings refuse every lifecycle write before any request.
- A quota whose `updatedAt` is null, as a new quota's is while it is
  being created, now decodes with a zero `UpdatedAt` instead of failing,
  so SMS orders settle.
- Compatibility: `QuotaUsageDetail.Counts` is now tagged `omitzero`, so
  its JSON encoding omits zero counts. Notification usage output in the
  CLI no longer prints an empty `Counts`.

## v0.83.0 - vMonitor Email Notification Quota Order

- SDK `GetNotificationQuota` and `GetNotificationQuotaUsage`, with CLI
  `monitor get-notification-quota --kind Email` and
  `get-notification-quota-usage --kind Email`, read the email quota, its
  expiry, and the emails sent this month. A missing quota is `NotFound`
  (exit 4).
- SDK `QuoteCreateNotificationQuota` and `CreateNotificationQuota`, with CLI
  `quote-create-notification-quota` and `create-notification-quota --kind
  Email`, order an email notification quota. The free `emailfree` package
  (0 VND, 20 emails a month) is the default. A paid package is ordered only
  when `--max-price` covers the higher of its quote and list price, both
  before VAT. A positive `--max-price` prints a stderr warning that the
  order may debit the balance.
- The order refuses when an email quota already exists. It re-checks the
  quota just before sending, sends once, never retries, and waits up to
  120 seconds for the quota to become active (`--no-wait` skips the wait).
  After an uncertain outcome, read the quota before ordering again.
- SMS quota reads, quotes, and orders are refused locally until SMS
  behavior is verified. Listing SMS packages still works.
- Each alert email consumes the email quota. Free quotas renew only in
  their last 7 days; renewal commands come in a later release.
- Fix for every single-send write (paid creates, resizes, server actions,
  orders): Go's HTTP/2 transport could replay the request body after a
  refused stream or GOAWAY, and HTTP/1 after a dead reused connection,
  despite the SDK's own no-retry rule. Those writes now go out once; a
  refused stream is reported as an uncertain outcome. Empty-body single-send
  PUTs use chunked framing, verified against the vMonitor and vServer APIs.
- The transport reports when a request was never sent, so an order whose
  login failed first leaves no unresolved attempt and keeps its login error.

## v0.82.0 - vMonitor Check Alarm and Quota Reads

- `monitor.Check` adds `MonitorStatus` (`OK`, `ALARM`, or `UNDETERMINED`),
  the check's alarm state, and `HistoryAlarmID`. `Status` stays the
  `ENABLED` or `DISABLED` run state. CLI `get-check` and `list-checks`
  print both new fields.
- SDK `GetCheckAlarm` and CLI `monitor get-check-alarm` read a check's
  current alarm state per probe location. SDK `ListCheckAlarmHistory` and
  CLI `monitor list-check-alarm-history` list state transitions over an
  RFC 3339 range (default: the last 24 hours). History holds only the
  records the server returned: paging and truncation are unknown, and an
  empty result proves nothing.
- SDK `GetQuotaUsage`, `ListNotificationPackages`, `GetSyntheticQuota`, and
  `GetSyntheticQuotaUsage`, with CLI `get-quota-usage`,
  `list-notification-packages --kind Email|SMS`, `get-synthetic-quota`, and
  `get-synthetic-quota-usage`, read composite usage, notification packages,
  and the synthetic quota with its expiry. Read the expiry alone with
  `vngcloud monitor get-synthetic-quota --query Quota.EndTime`. Free
  quotas renew only in their last 7 days; ordering and renewal commands
  come in later releases.
- The wiki no longer claims 20 free alert emails. Allowances depend on the
  account and package: a synthetic-only account has none until it orders
  an email package. See Monitor Quotas.
- Compatibility: unkeyed `monitor.Check` struct literals no longer
  compile, and exact snapshots of `Check` JSON or CLI output gain the two
  fields.

## v0.81.3 - Credential Redaction Across Retries

- Requests that retried or refreshed their token redacted only the last
  credential they sent. A server that echoed an earlier token in a response
  could get it into SDK output, errors, captures, or logs. Every credential
  a request sends is now redacted. This affects billing, billing resources,
  NAT, and vStorage project operations. Overlapping credentials, where an
  earlier token is a prefix of a later one, are redacted in full.
- Billing and vStorage decode errors no longer keep the JSON decoder's
  error, which could carry a value the server echoed back.
- An oversized error response now keeps its HTTP status and public error
  identity (`ErrAuth`, `ErrPermission`, `ErrNotFound`, `ErrRateLimited`)
  instead of status 0, with the body withheld. The server console log keeps
  its documented rule: a body over 8 MiB on any status is
  `ResponseTooLarge` with status 0.
- No public SDK or CLI surface changes.

## v0.81.2 - Billing Cost Resource Fixes

- `billing.ListCostResources` and CLI `billing list-cost-resources` now
  return the first page. The cost explorer API pages from 0; the SDK sent
  the 1-based `Page` unchanged, so it skipped the first page and returned no
  rows at all when every row fit on one page. `Page` stays 1-based: the SDK
  sends `Page - 1` and reports the server's page plus 1. A response whose
  page is negative or the largest int returns an `InvalidResponse` API
  error.
- `CostResource.Cost` now reads the API's `totalCost`, the resource's total
  cost over the requested range in VND. It previously read `cost`, which the
  API never sends, so every row showed 0.
- `CostResource` adds `DailyAverage` (`dailyAvg`) and `StartDate`
  (`startDate`, `YYYY-MM-DD`). The account ID and region in each item stay
  omitted.
- Compatibility: SDK JSON that serialized `CostResource` with the old `cost`
  key no longer decodes into `Cost`; re-encode it with `totalCost`. Unkeyed
  `CostResource` struct literals no longer compile. CLI JSON keeps the `Cost`
  key and adds `DailyAverage` and `StartDate`; table and text output gain two
  columns, so scripts that read text columns by position must be updated.

## v0.81.1 - User Guides and Storage Workload Access

- Document application access to vStorage from vServer and VKS: dedicated
  identities, bucket grants, private S3 key handoff, rotation, retirement,
  and network prerequisites. See [Storage Workload Access](
  docs/wiki/Storage-Workload-Access.md).
- Clarify key retirement and ambiguous-create recovery: delete identified
  keys while attached, reconcile uncertain results with known inventory and
  other creators, and preserve unrelated keys and policy grants.
- Improve README and Wiki navigation, put credential prerequisites before
  first-use examples, keep quick starts read-only, and identify SDK server
  counts as one page. Add CDN, Monitor, and current object operations to the
  service overview.
- Correct guidance on secret redaction, Windows file permissions, endpoint
  overrides, and operation-specific retries. Clarify the CLI read-only guard
  and quote checks without implying IAM restrictions or final billing locks.
- This patch changes documentation only. It adds no SDK or CLI operations or
  endpoint APIs.

## v0.81.0 - Native vStorage Server-Side Copy

- SDK `storage.ObjectClient.CopyObject` and CLI `storage copy-object` copy one
  current object directly within vStorage, retaining the source. Both buckets
  must belong to the same project and region.
- The SDK requires `Replace: true`; the CLI requires `--replace --yes`.
  Read-only settings refuse before credentials load. Source and destination
  names remain subject to concurrent changes; no version selector or
  create-only destination guarantee is provided.
- One bodyless signed PUT uses `x-amz-copy-source`. There is no local spool,
  download/upload fallback, source deletion, multipart copy, or retry.
  A complete bounded result is required; HTTP 200 with an Error is not success.
- Ambiguous results use `ErrObjectCopyUnconfirmed` and CLI
  `ObjectCopyUnconfirmed` with exit 1. Inspect the destination before deciding
  whether to repeat the copy. Failed metadata output after confirmed success
  reports completion and never resends.
- Provider metadata defaults apply without a lossless clone or encryption
  inheritance guarantee. Recorded HCM04 encrypted sources return 501, and a
  plain-source copy can remain unencrypted despite destination bucket
  encryption. HAN02 encryption and GreenNode's exact copy size limit remain
  unverified. No new live verification is claimed.
- Existing transfers, listing, HEAD, and deletion remain available. Move/rename
  stays gated on safe source removal. See [copy guidance](
  docs/wiki/Storage-Objects-Copy.md).

## v0.80.0 - Native vStorage Object Deletion

- SDK `storage.ObjectClient.DeleteObject` and CLI `storage delete-object`
  delete one current object name. The CLI requires `--yes`; every read-only
  setting refuses before S3 credentials load.
- Each invocation sends one unconditional DELETE. HTTP 204 confirms acceptance,
  without proving prior existence, permanent erasure, or reclaimed quota.
  Concurrent writers remain unprotected, and versioning can retain data or add
  a delete marker. No version selector, precondition, retry, or cleanup is added.
- Ambiguous results return `ErrObjectDeleteUnconfirmed` and CLI
  `ObjectDeleteUnconfirmed` with exit 1. Inspect the current key and version
  history before another deletion. Failed metadata output after confirmed
  success reports completion and never resends.
- Explicit S3 region and private credentials-file flags, safe metadata output,
  and the five-minute default timeout remain. No new live verification is
  claimed. Native copy is a separate release; move/rename remains gated.
  See [object guidance](docs/wiki/Storage-Objects.md#current-name-deletion).

Older releases are in [docs/release-notes-archive-v0.79.md](docs/release-notes-archive-v0.79.md).
