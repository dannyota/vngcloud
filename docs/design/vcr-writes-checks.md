# vCR Writes: Checks

Status: Proposed, with [vCR writes](vcr-writes.md).

The unit tests, cost probe, and live checks for [vCR writes](vcr-writes.md).
Terms and sentinels are defined there.

## Unit tests

Unit tests use `httptest` and an injected clock.

- Sanitized raw fixtures and decode tests in `testdata/containerregistry/`
  for the repository create, get, and delete responses, a user list row
  with two repository permissions, the permission list, and a user create
  whose `secretKey` is `<secret>`. The existing list fixtures move to the
  typed models.
- Request bodies: repository create always sends `isPublic` false and the
  given `quotaLimit`; user create maps each action to its policy ID and
  sends `duration` only when `DurationDays` is set.
- Shape refusals with no request: empty name, quota 0, no permission, a
  permission with no action, a bad repository ID, `DurationDays` 0, an
  action absent from the permission list (after that one read only).
- Repository delete: `ImageCount` above 0 sends no `DELETE`; attached
  users do not block it.
- User create lookup: one match fills `User`; none and two matches return
  the Output with `SecretKey` and `ErrUserNotFound`; a name that only
  contains the input as a substring is not a match.
- Waits: create to `ACTIVE`, a 404 then `ACTIVE`, delete until absent, the
  bound, `NoWait`, poll spacing, and a cancelled context.
- Statuses 200, 202, 400, 404, 409, and 5xx; no create resend after a
  502; a user create 200 with an empty `secretKey` fails; the not-found
  mapping the probe chooses.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every operation
  that takes an ID.
- Secret: `fmt` verbs, `slog`, and `json.Marshal` of `CreateUserOutput`
  give `[redacted]`; `--debug` output, stdout, stderr, and every error
  never hold the fixture secret.
- CLI golden tests for every command; no `--public` flag; `--yes` on both
  deletes; read-only refusal with no request; the `--secret-file` cases of
  [vStorage](storage.md#testing), plus a secret file written after
  `ErrUserNotFound` and a failed write with no user ID naming the user.

## Cost probe

The probe decides whether a repository costs money. It runs once, before
R1's code merges, on the test account, following
[live data](../../instructions/live-data.md), with the owner's approval
naming the account, the vCR endpoint, and the resources. It logs only
statuses, field names and types, booleans, counts, and timings, and
writes raw captures only under `examples/basic/output/`.

The test account is prepaid with no credit. The survey's assumption is
that a paid create then fails rather than bills; the vDNS probes found
this, and this probe relies on it for vCR.

Baseline, read-only:

1. `billing get-balances`, `billing get-cost-overview` and
   `list-cost-resources` for the current period, and
   `portal list-quota-used`: record the vCR rows, if any.
2. `pricing get-quote` with resource types `vcr` and `container-registry`
   and `resourceInfo` `{"quotaLimit": 1}`: status and price, or the
   refusal. A found type is recorded in the design.
3. `list-repositories` and `list-users`: row counts only.

One write session:

4. Create one repository through a throwaway script: name
   `vngcloud-live-<8 hex>`, `isPublic` false, `quotaLimit` 1.
   - A refusal (any 4xx naming balance, credit, payment, or order): the
     repository is paid. Log the status and code, send nothing more, and
     stop. Never retry the create.
   - 202: log the response field names and types, and `name` against the
     input (prefix shape, logged as a pattern such as `<n>-<input>`).
     Poll `GET` every 2 seconds: the `status` values and the time to the
     settled one. Capture one list row's field names and types.
5. While the repository exists, and only if step 4 returned 202:
   - `GET v1/user/permissions`: the action strings (these are not account
     data) and the count.
   - Create a user with a pull action on the repository and no duration:
     status, response field names, a boolean for a non-empty `secretKey`
     and its length. The secret stays in memory and is never logged or
     written. List users by name: row field names and types, whether
     `name` or `backendName` carries the prefix, and whether the filter
     matches by substring. Create the same name again: status and
     message.
   - Delete the user: status. Repeat delete: status.
6. Delete the repository: status and response field names; poll until it
   leaves the list, with the time. Repeat delete: status. `GET` after
   delete: status.
7. Whether a `GET` or `DELETE` of an unknown ID, such as
   `repo-00000000-0000-0000-0000-000000000000`, answers 404, 400, or 500.

Next day, read-only:

8. `get-balances`, `get-cost-overview`, and `list-cost-resources` show no
   vCR line and no change. Any new line means paid.

Outcomes:

| Result | Meaning | Next |
|-|-|-|
| Step 4 refused | Paid | Build and unit-test R1 and R2 from the reference and these results; their live checks and tags wait for credit; design the quote |
| Step 4 accepted, step 8 shows a line | Paid, billed despite no credit | Stop and tell the owner at once: the no-credit assumption failed. Same gate as above |
| Step 4 accepted, step 8 clean | Free | R1 and R2 ship in order |

If step 4 is refused, steps 5 to 7 are unknown until credit exists, and
the typed models come from the reference alone: every field is kept, and
the first live check after credit compares them against a capture.

## Live checks

Each release's checks pass on the test account before its code merges,
and only once the cost probe says free or the owner has added credit.
Each write run needs the owner's approval naming the account and
resources. Names are `vngcloud-live-<8 hex>`.

### R1 repositories

1. `CreateRepository` with `QuotaLimitGB` 1: the Output after the wait;
   the capture decodes into `Repository` with no field lost (compare the
   raw and SDK captures).
2. `GetRepository`: fields match the list row.
3. Create the same name again: status and message.
4. `DeleteRepository`: the wait until absent; repeat delete through the
   SDK: `NotFound`.
5. `portal list-quota-used`: the vCR row before and after, if any.

Not checkable without pushing an image: the images guard against a real
repository. The pre-read covers it; the server's own behavior on a
non-empty delete stays unknown.

### R2 users

On a repository the run creates:

1. `ListPermissions`: decodes into `Permission`.
2. `CreateUser` with a pull action and `DurationDays` 1: `User` filled by
   the lookup; `SecretKey` non-empty (boolean only).
3. `docker login` is not run: it would put the secret in a credential
   store. The login name stays an open question until the owner tries it.
4. `ListRepositoryUsers` on the repository: the user appears.
5. `DeleteUser`; repeat delete: `NotFound`.
6. The CLI `create-user` with `--secret-file` in a temporary directory:
   file mode 0600, one line, stdout holds `[redacted]`; then
   `delete-user --yes`.

### Cleanup

The live write test first deletes users, then repositories, whose names
end with a `vngcloud-live-` name and that hold no images. It registers
`t.Cleanup` as soon as each ID is known, deletes users before
repositories with its own context, and asserts none remain. If a create
fails, it lists by name and deletes a match. It never touches a
repository or user without the prefix, and never a repository with
images.
