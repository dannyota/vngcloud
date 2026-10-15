# IAM user fixture provenance

These fixtures are synthetic bodies built from response shapes that the
reviewed IAM user probe recorded on the owner's test account, in two runs that
each created, exercised, and deleted one disposable `vngcloud-probe-<8 hex>`
user. The probe kept every password, token, and create, reset, and rename body
in memory only. It recorded statuses, body lengths, key names, JSON types, and
booleans, and kept only credential-free list, attachment, and quota bodies in
private storage. Every ID, name, and timestamp below is synthetic. The
[IAM user API](../../docs/design/iam-users-api.md) and
[administration API](../../docs/design/iam-users-admin-api.md) designs record
what the probe showed.

Paths are relative to `https://dashboard.console.greennode.ai/accounts-api/v1`
for user calls and the IAM policies endpoint for attachment reads.

|Fixture|Request|Status|Observed shape|Synthetic values|
|-|-|-|-|-|
|`create_user.json`|POST `/iam-users` with `username` and `password`|201|Object with one key, `id` (string); 45 bytes, so a 36-character UUID|`id` is a synthetic UUID|
|`get_user.json`|GET `/iam-users/{id}` right after create|200|Object with `createdAt` (number, epoch milliseconds), `enabled` (boolean, `true`), `from` (string, `iam`), `id`, `username`, and `twoFactorAuth`; no `lastUse`; 321 bytes|`id`, `username`, and `createdAt` are synthetic|
|`get_user_mfa.json`|GET `/iam-users/{id}` after one sign-in as the user|200|The same object plus `lastUse` (number, epoch milliseconds); 345 bytes|`lastUse` is synthetic|
|`list_users_filtered.json`|GET `/iam-users?name=<value>`|200|Object with `data`, `pageSize`, `pageNumber` (0), `totalItems`, and `totalPages`; each row holds `id`, `createdAt` (RFC 3339 string with milliseconds and `Z`), and `username`|Row values are synthetic|

`twoFactorAuth` holds three factor objects. `email` has `address` (null),
`enabled`, and `verified`. `googleAuthenticator` has `enabled` and
`verified`. `sms` has `enabled`, `verified`, and one more key: a
three-character key whose value was null, which the probe withheld by name.
The fixtures omit that key. Both booleans were `false` for every factor on a
new user. A compact rebuild of the recorded keys and types, with a
three-character placeholder for that key, matches the observed lengths
exactly, 321 and 345 bytes; the published fixtures, without it, compact to 310
and 334 bytes.

Shapes recorded without a fixture:

- The `name` filter on GET `/iam-users` matched a substring and ignored case.
  An absent name returned `data: []` with `totalItems` 0.
- A new user's direct-policy page was empty (`totalItems` 0), and its group
  list was the bare array `[]`.
- A second create with an existing username returned 400.
- POST `/iam-users/{id}/reset-password` returned 204 with an empty body for
  generated passwords of 32, 8, and 50 characters. A fresh sign-in with the new
  password succeeded and one with the old password failed.
- POST `/iam-users/{id}/activate-email`, `deactivate-email`,
  `activate-google`, and `deactivate-google` each returned 400 for a user with
  no factor set up.
- PUT `/iam-users/{id}` with a `username` body returned 403 to the IAM user
  caller, with a top-level array of one object holding one string field. The
  account's action list names no IAM user update action.
- DELETE `/iam-users/{id}` returned 204; a repeat DELETE and a GET then
  returned 404.
- GET `/quotas/iam-user` returned `{"usage": <number>, "limit": <number>}`,
  with a limit of 20 and the same values before and after each run.
- A sign-in as the new user through the SDK's IAM user login succeeded with no
  MFA challenge, and the caller identity it returned matched the created ID.
- A read of GET `actions?product=iam` on the test account returned 104 actions.
  The user-related names were `CreateIamUser`, `DeleteIamUser`, `GetIamUser`,
  `ListIamUsers`, `ChangeIamUserPassword`, `ActivateIamUser2FA`,
  `DeactivateIamUser2FA`, `SetUpIamUser2FA`, `ActivateIamUserConsoleAccess`,
  and `DeactivateIamUserConsoleAccess`, with no IAM user update or rename
  action.

Tests that build adversarial variants from these files label them synthetic.
