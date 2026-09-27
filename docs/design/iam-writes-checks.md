# IAM Writes: Checks

Unit tests, probes, and live checks for [IAM writes](iam-writes.md). The
calls and shapes they confirm are in [IAM writes: API](iam-writes-api.md).

## Unit tests

Unit tests use `httptest`, with a fake API that records every request.

- Sanitized raw fixtures and decode tests in `testdata/iam/` for every
  read and create: a policy list with numeric, `$numberLong`, and missing
  `createdAt`; a managed and a customer policy; a group; the bare group
  arrays; a service account; `userinfo`; the action list with all four
  labels.
- Paging: `Page` 0 sends `pageNumber=0`; `Size` defaults to
  `DefaultPageSize`.
- Hosts: policies calls go to the `IAM` endpoint, accounts calls to
  `Dashboard`, and an override moves each.
- Request bodies for every write. `CreateGroup` sends `mode: "iam"` and
  no `iamUsers` or `policies`. `UpdatePolicy` fills nil fields from the
  read. `UpdateGroup` reads the name only when `Name` is nil.
  `UpdateServiceAccount` sends only non-nil fields.
- Statement shape refusals with no request: no statements, a bad effect,
  empty actions or resources, an empty string in either.
- Guards, each with no write request sent:
  - The caller as target: attach, detach, add, remove, and a service
    account update, delete, and reset when the caller is that account.
  - A user protected only through a group; a group protected only
    through a member; a service account with a managed privileged
    policy.
  - Privileged patterns: `*`, `iam:*`, `IAM:attach*`,
    `iam:CreatePolicy`, and an unknown label; not privileged: `iam:List*`
    when every match is `List`, `Read`, or `Tagging`, and any `deny`
    statement.
  - `UpdatePolicy` on a managed policy, on a privileged one, to a
    privileged document, and on a policy attached to a protected group.
  - `DeletePolicy` while attached; `DeleteGroup` with a member and with a
    policy.
  - An unknown caller user type refuses every guarded write.
  - `ErrSelfChange` wins over `ErrPrivilegedChange`.
  - No error text holds a statement, action list, or name beyond the
    rule.
- Secrets: `CreateServiceAccount` and `ResetServiceAccountSecret` set
  `Sensitive`; the capture hook never sees the response; `fmt` verbs,
  `slog`, and `json.Marshal` of the Output give `[redacted]`; a create
  response without a secret leaves `ClientSecret` empty.
- Statuses 200, 201, 204, 400, 403, 404, 409, and 5xx; no create,
  attach, add, or reset resend after a 502; `PATCH` retried as
  idempotent.
- Path ID rejection for `..`, `.`, `/`, `?`, and empty on every call.
- CLI golden tests for every command; `--document-file` with the console
  form, Go field names, an AWS-style document (exit 2), unknown keys
  (exit 2), and an oversized file (exit 2); `--secret-file` refusals and
  mode 0600; a failed file write deletes the new service account; a
  create without a secret exits 1 and keeps the account; `--yes` where
  the [CLI table](iam-writes.md#cli) says; read-only refusal with no
  request sent.

## Probes

The manager runs these before code starts, on the test account, with the
owner's approval naming the account and the resources. IAM is global, so
no region applies. Every name is `vngcloud-live-<8 hex>`; the probe
touches nothing without that prefix.

Runs log only statuses, error codes, key names and types, booleans,
counts, and timings. They never log an ID, a name past the prefix, a
client ID, a secret, or a statement. A secret stays in memory and is
dropped. Raw bodies of reads go under `examples/basic/output/raw/iam/`;
no create or reset response is written anywhere.

### Before any write

1. Quotas: `GET policies-api/v1/quotas/policy`, `quotas/group`, and
   `accounts-api/v1/quotas/user-sa`. Record limit and usage; there must
   be room for one of each.
2. `GET auth/userinfo`: record `userType`, and whether `userId` equals
   the caller's row in `GET iam-users?pageNumber=0`.
3. `GET actions?product=iam`: record the set of labels and the count per
   label.
4. `GET actions?product=vserver`: pick one action whose label is `List`;
   the policy steps use it.
5. `pageSize=10000` on `service-accounts`, `iam-users`, and `policies`:
   status and whether one page holds `totalItems`.

### Service accounts

6. Create with the prefix name and a description: status, response key
   names and types, whether `clientSecret` is present (boolean), whether
   `id` matches `^[A-Za-z0-9-]+$` (boolean), and whether it starts with
   `sa-` (boolean).
7. Create the same name again: status and code.
8. `GET service-accounts/{id}` and the list filtered by name: key names,
   `enabled`, and whether the list finds it.
9. `PATCH` with `description` only, then with `accessTokenLifeSpan` 3600:
   status, response key names, and whether the get shows each change.
   `PATCH` with a `name` field: status, and whether the name changed.
10. `POST reset-secret`: status, whether `clientSecret` is present, and
    whether it differs from step 6's (boolean, compared in memory).

### Policies

11. Create a policy with one `allow` statement for step 4's action on
    `*`: status, response keys, and the get's `manager`, `scope`, and
    whether `root` is set.
12. Create the same name again: status and code.
13. Create with an action that does not exist (`vserver:NoSuchAction`):
    status and code, to learn whether the server checks names. If it
    succeeds, delete it at once.
14. Create with an uppercase name and with only letters and digits;
    record which name forms are accepted. Delete each that succeeds.
15. `PUT` the policy with `name`, `description`, and `statements`:
    status. `PUT` with `description` only: status, and whether the get
    still holds the statements.

### Groups

16. Create a group with `name`, `description`, and `mode: "iam"`: status
    and response keys. Create the same name again: status and code.
17. `PATCH` with `description` only: status, and whether the name
    stayed. `PATCH` with `name` and `description`: status.
18. `GET groups/{id}`: key names; `iamUsers` and `policies` are empty.

### Attachments

19. Attach step 11's policy to step 6's service account: status. Repeat:
    status and code. `GET user-attachments/service-accounts/{id}/policies`
    and `GET policies/{id}/service-accounts`: whether each shows it.
20. Attach the policy to step 16's group: status. Repeat: status and
    code. `GET groups/{id}/policies` and `GET policies/{id}/groups`:
    whether each shows it.
21. `DELETE` the policy while attached: status and code. `DELETE` the
    group while it holds the policy: status and code. Each must fail; if
    either succeeds, record it and skip the matching detach.
22. `POST groups/{id}/iam-users/<random UUID>`: status and code.
    `DELETE` the same: status and code.
23. Detach from the service account and the group: status. Repeat each:
    status and code.

Not probed: any write to a managed policy, the caller's user, or a
group the caller is in. The guards cover those, and a mistake there
would change real rights.

### Cleanup and cost

24. Delete the policy, the group, and the service account: status.
    Repeat each: status and code. `GET` each: status.
25. Step 1's quotas are back to their first usage, and no resource with
    the prefix remains in the three lists.
26. The next day's bill shows no IAM line, and `billing get-balances` is
    unchanged.

The probe program deletes every prefix resource it made on any exit,
detaching first, and prints the ID of anything it could not delete to
the owner only, never to a shared log.

## Live checks

After the probes, each release adds to `make live` and the live write
test. Live write runs need the owner's approval naming the account and
resources.

- `make live` adds `GetCallerIdentity`, `ListUsers`, `ListActions`,
  `ListPolicies`, `ListGroups`, and `ListServiceAccounts`, logging
  counts and the caller's user type only.
- The live write test deletes leftover prefix resources, detaching first,
  then per release:
  - I2: creates a service account with a temp `--secret-file`, updates
    it, resets its secret to a second temp file, and deletes it.
  - I3: creates a read-only customer policy as in step 11, updates it,
    attaches it to a new service account, lists the attachment, detaches
    it, and deletes both.
  - I4: creates a group, updates it, attaches the policy, checks that
    `DeleteGroup` returns `ErrInUse` with no request, detaches, and
    deletes both. It checks that `AddUserToGroup` with the caller returns
    `ErrSelfChange` with no request.
- `t.Cleanup` is registered as each ID is known and asserts none remain.
  The test never logs an ID, name, client ID, or secret, and removes its
  temp secret files.
- A spare IAM user for a real add and remove runs only if the owner
  chooses decision 11 (b).

## Security review

The adversarial review for each write release checks the items in
[Security](iam-writes.md#security) against the code, and also:

- Every guard reads before the write in the same call and uses no cache
  across calls except caller identity and the action list.
- A guard read that fails refuses the write; it never falls through to
  sending.
- The privileged match covers case, `*` in any position, and a product
  wildcard.
- `--document-file` and `--cli-input-json` produce the same checked
  `Statements`.
- No stdout, stderr, `--debug`, error, capture, or fixture holds a client
  secret, and the secret file is created only as the file rules say.
