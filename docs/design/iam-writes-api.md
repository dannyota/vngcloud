# IAM Writes: API

The GreenNode IAM calls, bodies, and responses behind
[IAM writes](iam-writes.md), and the SDK surface built on them. Tests,
probes, and live checks are in [IAM writes: checks](iam-writes-checks.md).

## Sources

- The OpenAPI specs embedded in `docs.api.greennode.ai/service-docs/`:
  `accounts-api` and `policies-api`. Both name the old
  `iamapis.vngcloud.vn` host.
- The public IAM console at `iam.console.greennode.ai`: its
  `assets/configs/config.prod.json` and its JavaScript bundles, which show
  the paths the console calls and the error codes it expects.
- The product pages "IAM Access Management" and "Service accounts" on
  `docs.greennode.ai`: a client secret is shown once and can be reset.
- Read-only calls on the test account through this repository's
  transport, on 2026-09-27. They logged only statuses, key names, types,
  counts, and booleans.

VNG Cloud's Go SDK and Terraform provider have no IAM code.

## Hosts

| API | Base | Auth |
|-|-|-|
| IAM accounts API | `https://dashboard.console.greennode.ai/accounts-api/` | IAM User token |
| IAM policies API | `https://iam.console.greennode.ai/policies-api/` | IAM User token |

The console config sets both APIs as paths relative to
`iam.console.greennode.ai`. The accounts API also answers on the
dashboard host, which [vStorage](storage.md#endpoints) already uses for
`iam`. The policies API does not: on the dashboard host it returns the
console's HTML page with status 200.

## Calls

Paths are under each API's `v1/`. "AC" is the accounts API and "PO" the
policies API.

| Call | API | Method and path | Success |
|-|-|-|-|
| Caller identity | AC | `GET auth/userinfo` | 200 |
| List IAM users | AC | `GET iam-users` | 200 |
| List service accounts | AC | `GET service-accounts` | 200 |
| Get service account | AC | `GET service-accounts/{id}` | 200 |
| Create service account | AC | `POST service-accounts` | 201 |
| Update service account | AC | `PATCH service-accounts/{id}` | 200 |
| Delete service account | AC | `DELETE service-accounts/{id}` | 204 |
| Reset client secret | AC | `POST service-accounts/{id}/reset-secret` | 200 |
| List actions | PO | `GET actions?product=iam` | 200 |
| List policies | PO | `GET policies` | 200 |
| Get policy | PO | `GET policies/{id}` | 200 |
| Create policy | PO | `POST policies` | 201 |
| Update policy | PO | `PUT policies/{id}` | 204 |
| Delete policy | PO | `DELETE policies/{id}` | 204 |
| Policy's groups, users, service accounts | PO | `GET policies/{id}/groups`, `.../iam-users`, `.../service-accounts` | 200 |
| Attach, detach to a group | PO | `POST`, `DELETE policies/{id}/groups/{groupId}` | 204 |
| Attach, detach to a user | PO | `POST`, `DELETE policies/{id}/iam-users/{userId}` | 204 |
| Attach, detach to a service account | PO | `POST`, `DELETE policies/{id}/service-accounts/{saId}` | 204 |
| List groups | PO | `GET groups` | 200 |
| Get group | PO | `GET groups/{groupId}` | 200 |
| Create group | PO | `POST groups` | 201 |
| Update group | PO | `PATCH groups/{groupId}` | 204 |
| Delete group | PO | `DELETE groups/{groupId}` | 204 |
| Group's policies | PO | `GET groups/{groupId}/policies` | 200 |
| Add, remove a user | PO | `POST`, `DELETE groups/{groupId}/iam-users/{userId}` | 204 |
| User's policies | PO | `GET user-attachments/iam-users/{userId}/policies` | 200 |
| User's groups | PO | `GET user-attachments/iam-users/{userId}/groups` | 200 |
| Service account's policies | PO | `GET user-attachments/service-accounts/{saId}/policies` | 200 |

The specs document 400, 401, 403, 404, 409, and 500 for most calls,
without bodies.

## Bodies and responses

Read shapes come from the live reads; write shapes are inferred from the
specs and the console until the [probes](iam-writes-checks.md#probes).

- Paged lists (`iam-users`, `service-accounts`, `policies`, and the
  `.../policies` attachment lists) take `pageNumber` and `pageSize` and
  return `data`, `pageNumber`, `pageSize`, `totalItems`, and
  `totalPages`. `pageNumber` starts at 0: page 1 of a one-item list is
  empty. A `pageSize` of 10000 returned every policy in one page.
- `groups`, `user-attachments/iam-users/{id}/groups`, and
  `policies/{id}/groups` return a bare array. `policies/{id}/iam-users`
  and `.../service-accounts` return arrays of IDs.
- `auth/userinfo` returns `userId`, `userType`, `accountId`, `username`,
  `rootEmail`, and `twoFactorAuth`. The console knows the user types
  `root-user`, `iam-user`, and `user-sa`; the action list also names
  `service-sa`.
- A policy list row holds `id`, `name`, and `createdAt`. A policy get adds
  `description`, `manager`, `scope`, `root`, `statements`, and `_id`.
  GreenNode-managed policies have `manager` `VNG CLOUD` and a null or
  missing `root`; a customer policy has `manager` `user` and its account
  number in `root`, sent as a JSON number, confirmed live on 2026-09-27. The
  SDK decodes `root` as a number, a string, or null or absent, and always
  exposes it as a string; `Group.root` gets the same tolerant decode on the
  same key name, though no live read has confirmed its type.
- `createdAt` arrives as an epoch-milliseconds number, as
  `{"$numberLong": "<digits>"}`, or not at all, varying by row. IAM user
  `createdAt` is an RFC 3339 string.
- A statement is `effect` (`allow` or `deny`, lower case), `actions`,
  `resources`, and an optional `condition` whose keys are the operators
  `stringEquals`, `stringNotEquals`, `numberLessThan`,
  `numberLessThanOrEquals`, `numberGreaterThan`, and
  `numberGreaterThanOrEquals`. Actions are `<product>:<Action>`, with
  `*` wildcards such as `iam:*`, `vserver:List*`, and `*`. Resources use
  the builder formats from `GET resources`, such as
  `iam::<account>:policy/<id>`, or `*`.
- A group is `id`, `name`, `description`, `mode` (`iam` or `idp`),
  `root`, `iamUsers` (user IDs), `policies` (policy IDs), `createdAt`, and
  `_id`. The list row holds only `id`, `name`, and `createdAt`.
- A service account get (spec) is `id`, `clientId`, `name`,
  `description`, `accessTokenLifeSpan` (seconds), `createdAt`, `enabled`,
  and `lastUse`.
- `GET actions?product=iam` returns 104 actions with `action`, `label`,
  `resources`, `methods`, and `userTypes`. Labels are `List`, `Read`,
  `Write`, and `Tagging`.
- Create bodies: policy `name`, `description`, `statements`; group
  `name`, `description`, `mode`, and optional `iamUsers` and `policies`;
  service account `name` and `description`. Policy and group creates
  return `{"id": ...}`. The service account create response is not
  documented; the product page says the secret is shown once.
- Update bodies: service account `description` and
  `accessTokenLifeSpan`; group `name` (required) and `description`;
  policy `name`, `description`, and `statements`.
- Reset returns `{"clientSecret": ...}`.
- Policy, group, and IAM user IDs are UUIDs. The service account ID form
  is unknown; [vStorage](storage.md#principal) saw `sa-<id>` in a
  console query.
- Quotas from the console's quota reads: 20 customer policies, 20 groups,
  and 20 service accounts per account.
- The console's policy editor names the server codes
  `POLICY_CHARACTERS_INVALID`, `POLICY_PRODUCT_INVALID`,
  `POLICY_PRODUCT_NOTFOUND`, and `POLICY_SECURITY`, and marks managed
  policies read-only: "This policy is managed by application".

## Endpoints

`endpoints.Set` and `Overrides` gain `IAM`, default
`https://iam.console.greennode.ai/`, and `internal/routes` gains
`ProductIAM`. Policies API paths are `policies-api/v1/...` under it.
Accounts API paths stay under `Dashboard`, as
[vStorage](storage.md#endpoints) set. The transport sends the token to
both hosts and follows no cross-host redirect.

## SDK

All operations live in `iam`. Operation names are `iam.<Method>`. "(r)"
marks `vngcloud:"required"`, and "L[T]" is `core.List[T]`. List Inputs
carry `Page` and `Size`, sent as `pageNumber` and `pageSize`; `Page` 0 is
the first page, and `Size` defaults to `DefaultPageSize`.

### Reads

| Operation | Input | Output |
|-|-|-|
| `GetCallerIdentity` | none | `{UserID, UserType, Username string; AccountID int64}` |
| `ListUsers` | none | L[User] |
| `ListServiceAccounts` | `Name` | L[ServiceAccount] |
| `GetServiceAccount` | `ServiceAccountID` (r) | `{ServiceAccount}` |
| `ListActions` | none | L[Action] (product `iam`) |
| `ListPolicies` | `Name` | L[PolicySummary] |
| `GetPolicy` | `PolicyID` (r) | `{Policy}` |
| `ListPolicyAttachments` | `PolicyID` (r) | `{Groups []GroupSummary; UserIDs, ServiceAccountIDs []string}` |
| `ListGroups` | none | L[GroupSummary] |
| `GetGroup` | `GroupID` (r) | `{Group}` |
| `ListGroupPolicies` | `GroupID` (r) | L[PolicySummary] |
| `ListUserPolicies` | `UserID` (r) | L[PolicySummary] |
| `ListUserGroups` | `UserID` (r) | L[Group] |
| `ListServiceAccountPolicies` | `ServiceAccountID` (r) | L[PolicySummary] |

- `GetCallerIdentity` keeps no root email and no two-factor fields.
- `ListPolicyAttachments` makes the three `policies/{id}/...` reads.
- `ListServiceAccounts` and `GetServiceAccount` move here from
  [vStorage](storage.md#iam); see [ownership](iam-writes.md#ownership).

### Writes

| Operation | Input | Output |
|-|-|-|
| `CreateServiceAccount` | `Name` (r), `Description` | `{ServiceAccount; ClientSecret vngcloud.Secret}` |
| `UpdateServiceAccount` | `ServiceAccountID` (r), `Description *string`, `AccessTokenLifeSpan *int` | `{ServiceAccount}` |
| `DeleteServiceAccount` | `ServiceAccountID` (r) | `{}` |
| `ResetServiceAccountSecret` | `ServiceAccountID` (r) | `{ClientSecret vngcloud.Secret}` |
| `CreatePolicy` | `Name` (r), `Description`, `Statements []Statement` (r) | `{Policy}` |
| `UpdatePolicy` | `PolicyID` (r), `Name *string`, `Description *string`, `Statements *[]Statement` | `{Policy}` |
| `DeletePolicy` | `PolicyID` (r) | `{}` |
| `AttachGroupPolicy`, `DetachGroupPolicy` | `PolicyID` (r), `GroupID` (r) | `{}` |
| `AttachUserPolicy`, `DetachUserPolicy` | `PolicyID` (r), `UserID` (r) | `{}` |
| `AttachServiceAccountPolicy`, `DetachServiceAccountPolicy` | `PolicyID` (r), `ServiceAccountID` (r) | `{}` |
| `CreateGroup` | `Name` (r), `Description` | `{Group}` |
| `UpdateGroup` | `GroupID` (r), `Name *string`, `Description *string` | `{Group}` |
| `DeleteGroup` | `GroupID` (r) | `{}` |
| `AddUserToGroup`, `RemoveUserFromGroup` | `GroupID` (r), `UserID` (r) | `{}` |

Every write except the two service account creates runs the
[guards](iam-writes.md#guards) first and sends nothing when one refuses.

### Models

Models keep their API JSON tags; `_id` is dropped.

- `Statement`: `Effect`, `Actions []string`, `Resources []string`, and
  `Condition map[string]map[string]any` (`condition`, omitted when
  empty).
- `Policy`: `ID`, `Name`, `Description`, `Manager`, `Scope`, `Root`,
  `Statements`, `CreatedAt`. `Managed()` is true unless `Manager` is
  `user`.
- `PolicySummary`: `ID`, `Name`, `CreatedAt`.
- `Group`: `ID`, `Name`, `Description`, `Mode`, `Root`, `UserIDs`
  (`iamUsers`), `PolicyIDs` (`policies`), `CreatedAt`. `GroupSummary`: `ID`,
  `Name`, `CreatedAt`.
- `ServiceAccount`: `ID`, `ClientID`, `Name`, `Description`,
  `AccessTokenLifeSpan`, `CreatedAt`, `Enabled`, `LastUse`. It has no
  secret field.
- `User`: `ID`, `Username`, `CreatedAt` (string).
- `Action`: `Action`, `Label`, `Resources`.
- `CreatedAt` and `LastUse` are `int64` epoch milliseconds, decoded from
  a number or a `$numberLong` string; missing is 0.

### Bodies

- `CreateGroup` always sends `mode: "iam"` and never `iamUsers` or
  `policies`, so members and policies change only through the guarded
  calls.
- `CreatePolicy` and `UpdatePolicy` send `statements` with lower-case
  keys. The [shape check](iam-writes.md#policy-documents) runs first.
- `UpdatePolicy` reads the policy and sends `name`, `description`, and
  `statements`, taking each nil field from the read, because `PUT` may
  replace the whole policy. `UpdateGroup` reads the group when `Name` is
  nil, because the `PATCH` requires `name`.
- `UpdateServiceAccount` sends only non-nil fields.
- Every update and create returns a read after the write. If that read
  fails, the error says the write happened; `CreateServiceAccount` also
  keeps a non-nil Output, carrying the create response's own ID and client
  secret, and wraps `iam.ErrCreateUnconfirmed` rather than returning nil.
  `CreatePolicy` and `UpdatePolicy` do the same for their own failed
  confirm read: their Output keeps only the policy's ID, and the error
  wraps `iam.ErrNotSettled`.

### Secrets

`CreateServiceAccount` and `ResetServiceAccountSecret` set `Sensitive` on
the request, so the capture hook never sees the response and a decode
error never quotes it. `clientSecret` decodes straight into
`vngcloud.Secret`. When a create or reset response holds no secret, the
method returns `iam.ErrNoSecret` alongside its Output instead of succeeding
silently; the CLI then fails as the [secret file](iam-writes.md#secret-file)
section says.

### Identifiers and retries

- Every path ID passes `core.CheckPathID` before any request, reads
  included. The probes confirm the service account ID form.
- `CreateServiceAccount`, `ResetServiceAccountSecret`, and `CreatePolicy`
  set `transport.Request.Once`: a resend after a 401 or a followed
  redirect would create a second account or policy, or rotate the secret a
  second time, so each is sent at most once, whatever the response.
  Attaches and adds (I3, I4) are plain `POST` instead, retried only after a
  429 or a failed dial (ADR 0002 rule 2), since a repeat is visible as a
  `Conflict`. After a 5xx or a network error from any of these, the error
  names the read that shows whether the write landed:
  `list-service-accounts --name`, `list-policies --name`, `list-groups`,
  `list-policy-attachments`, or `get-group`. A service account found that
  way lost its secret: reset it.
- A repeated attach or add returns the server's `Conflict`, which the SDK
  does not hide, as `AttachS3Key` does in [vStorage](storage.md#retries).
- `PATCH` updates send full values and are safe to repeat, so they set
  `Idempotent`. `PUT` and `DELETE` keep the transport's retries; a
  retried delete, detach, or remove that finds nothing returns `NotFound`.
- The CLI never retries a write.
