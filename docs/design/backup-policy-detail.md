# Backup Center policy detail

Accepted addendum to [Backup Center reads](backup-services.md). Qualify
only `GetPolicy` in the regions mapped for Backup Center. Do not expand the public schedule model, add
filters or other routes, or activate or mutate resources. Full cadence
decoding needs populated evidence and separate public fields. Reusing
`Policy` leaves those settings unavailable.

## Evidence

GreenNode's [vBackup tool guide][guide] links the source recorded in the
[API companion](backup-services-api.md#sources). Its pinned
[`policy_handler.py`][handler] supplies the candidate detail route, not
proof of IAM access or other regions.

The [observed HCM detail][observed] records one matching HTTP 200 policy
from the default IAM profile, without project scope. It does not establish
filter behavior, permission boundaries, service-account or Hanoi access,
other cadence members, email contents, or failure status semantics.

## SDK and CLI contract

`(*backup.Client).GetPolicy` takes `*GetPolicyInput` after `context.Context`
and returns `(*GetPolicyOutput, error)`. The input has only `PolicyID string`
tagged `vngcloud:"required"`. The output has only `Policy Policy`.
Reject nil, empty, or invalid input with `ErrInvalidInput` using
`core.CheckPathID` (`^[A-Za-z0-9-]+$`) without echoing the rejected value.
Reject unmapped regions with `ErrInvalidConfig`, even with an endpoint
override. Validate before authentication or requests. Send
`GET /v1/backup-policies/{id}` with the ID as a separate path segment, no
query or project scope. Reuse bearer routing, retries, and refresh; do not
fetch the list or add pagination or auth.
Register `vngcloud backup get-policy --policy-id` as `Read`. Support normal
JSON input, output, and queries under `Policy`. Help states that project
selection has no effect and hourly, weekly, and monthly details are omitted.

Accept only HTTP 200 with one bare object, exact root `id` and `config` keys,
a nonempty string ID equal to `PolicyID`, and object `config`. Reject empty or
malformed bodies, arrays, wrappers, duplicate keys, and case aliases of the
required keys. Ignore other unknown fields. Fail with a fixed body-withheld
error and no output, without echoing either ID from an identity mismatch.

Return the existing `Policy`, `PolicyConfig`, and `DailyConfig` projection
unchanged, including JSON keys and scalar zero values when members are absent.
Absent or null `dailyConfig` stays nil; `{}` keeps nil member pointers;
explicit zero or empty values stay present. Keep exact whole-number schedule
decoding, rejecting fractions, strings, and overflow. Preserve supplied flags,
timestamps and timezone; infer no defaults, schedules, or next runs. Omit
`userId`, `statusSendEmail`, and hourly, weekly, and monthly child objects.

Keep the shared bounded transport, `Sensitive: true`, fixed withholding text,
and status-derived codes. Preserve HTTP status, operation, retryability, and
standard error causes, including overflow on a successful HTTP status. Reject
a retained policy string that contains a credential sent by this read, even
if the token came from an earlier attempt. Omitted fields stay omitted. Do
not expose raw bodies, arbitrary server errors, or partial policy results.
Use no new dependency.

## Fixture and checks

The [detail fixture](../../testdata/backup/get-policy.json) comes from the
raw HCM object, not the SDK output or list row. The probe preserved the
bare envelope, known keys, and field kinds in memory, replaced all scalar
values, and emptied unverified cadence objects and the email array. It
saved no raw data. Use separate synthetic tests for secret markers.

The raw `backupInstanceCount` decoded to the existing Go `int`. A
controlled GET for a distinct random UUID returned 404 in `hcm-3`.
Keep generic status mapping; this does not qualify other failures.

- Test the raw fixture through the SDK, exact path and empty query, no project
  header, every retained field, and unchanged list behavior.
- Test nil, empty, and unsafe IDs and unsupported regions with an override,
  all before auth or I/O. Reject wrong status, invalid shapes, and mismatched
  identities. Cover absent/null/empty/zero daily values and bad numbers.
- Test synthetic 401/403/404/429, retry/refresh, cancellation, and oversized
  bodies. Verify canaries never reach SDK formatting, CLI JSON/table/text,
  errors, debug logs, or capture hooks.
- Test CLI JSON/flag precedence, read-only mode, and no stdout on failure.
  Run `make check`; update the SDK and CLI wiki with implementation. Before
  release, the manager verifies the SDK IAM read against sanitized raw detail.

[guide]: https://github.com/vngcloud/docs/blob/main/English/backup-center/vbackup-mcp-server/vbackup-mcp-tools.md
[handler]: https://github.com/GreenNodeHub/greennode-mcp/blob/ca770f29e4b6eb2fe72248c6da6564a585ee4195/src/vbackup-mcp-server/greennode/vbackup_mcp_server/policy_handler.py
[gate]: backup-services.md#verification-and-ownership
[observed]: backup-services-api.md#observed-policy-detail
