# Service-account authentication: evidence and probes

Evidence for the accepted [authentication design](service-account-auth.md).
Published sources and manager-recorded live results are separate evidence.
The designer used no credentials or authenticated APIs and executed no
public downloads. Downloads stayed under `/tmp`. Live facts below cite the
manager's private `sa-probe-results.md`, read in full; no raw capture or
account identifiers are reproduced. The report is not a public fixture.

## First-party sources

Repository links below pin the inspected public source revisions. Console
asset names identify the fetched bundles. These links are evidence, not
dependencies or code to execute.

- [Accounts OpenAPI][accounts] and [API overview][overview].
- Service accounts: [English][sa-en] and [Vietnamese][sa-vi].
- [vStorage authorization][storage-auth], [policy management][policy], and
  [managed policies][managed].
- Terraform: [auth client][tf-auth] and [provider][tf-provider].
- Official Go SDK: [token method][go-auth], [request][go-request],
  [response][go-response], [HTTP layer][go-http], [error shape][go-errors],
  and [auth handlers][go-auth-errors].
- IAM console: [entry page][console], [config][console-config],
  [main bundle][main], [service-account view][sa-bundle], and
  [service API bundle][sa-api-bundle].

Source access limits: the [service-account page][docs-page] could not be
read; the first-party docs repository supplied its text. Only the linked
`.html` Accounts URL loaded its OpenAPI object. Access failures prove no
missing API facts.

## Wire evidence

### Token exchange

The manager verified this Accounts-spec request with created credentials:
Basic authentication, JSON `grant_type` only, and no scope. It returned 200
without redirect and with `application/json`. `Accept` remains an SDK
choice. Credentials stay in memory.

```http
POST https://iamapis.vngcloud.vn/accounts-api/v2/auth/token
Authorization: Basic base64(clientID + ":" + clientSecret)
Content-Type: application/json
Accept: application/json

{"grant_type":"client_credentials"}
```

The recorded response contains `access_token` and `token_type` strings and
numeric `expires_in`. The observed default lifespan is 1800 seconds. Neither
`refresh_token` nor `refresh_expires_in` is present. This is an observed
default, not a universal lifespan limit or a substitute for returned expiry.

The spec declares HTTP 200 with `access_token`, `refresh_token`,
`expires_in`, and `refresh_expires_in`, without requiring those fields or
specifying `token_type`. It documents errors 400, 401, 403, 404, 409, and
500 without body schemas. The request schema has no scope field. SDK
response validation is defined in the [design](service-account-auth.md).

The overview and vStorage guide also show `token_type` as `bearer` or
`Bearer`, `scope=email`, and numeric expiry. The vStorage guide's complete
curl example uses JSON and returns `refresh_expires_in=0`. Its 1800-second
example differs from the overview's 7200; neither proves a default or limit.

The official Go SDK's `GetAccessToken` calls `Post`, sets Basic auth, sets
`application/x-www-form-urlencoded`, but passes a struct through
`WithJsonBody` and then `SetBodyJsonMarshal`. The struct has `ClientId` and
`ClientSecret` without JSON exclusion tags as well as `grant_type`. Do not
copy its body or treat it as proof of a correctly encoded form request.
Its response model reads `access_token`, `expires_in`, `token_type`, and
`refresh_expires_in`, then calculates expiry from `expires_in`.

The Terraform provider configures `golang.org/x/oauth2/clientcredentials`
with client ID, client secret, email scope, and the same v2 URL. It does not
set `AuthStyle`. This proves intended use of client credentials, not which
wire authentication style succeeded on the server. No downloaded dependency
or provider code was run. The selected JSON/Basic flow follows the Accounts
spec and the recorded exchange. Form acceptance remains unverified; the
verified request needs no email scope.

### Console boundary, lifespan, and rotation

The unauthenticated console config declares `ACCOUNT_API_URL=/accounts-api`
and `ROOT_URL=https://iam.console.greennode.ai`. The main bundle posts code
and refresh grants to `/v1/auth/token`, using camel-case fields such as
`grantType`, `redirectUri`, and `refreshToken`. The Basic username there is
the console's public client ID, not a created service account's client ID.
No `client_credentials` exchange was found in the downloaded main or lazy
bundles. This console login must not be substituted for machine auth.

The service-account bundles read `clientSecret` after creation and reset;
the API bundle posts to `/v1/service-accounts/{id}/reset-secret`. The
Accounts spec leaves create's HTTP 201 body unspecified; the manager
verified creation in [recorded results](#recorded-live-results).

`accessTokenLifeSpan` is an integer in seconds on service-account get/update;
the spec's update example is 3600. The console lifespan validator requires
digits and a minimum of zero, with no maximum in that validator. Neither
source establishes server min/max, zero semantics, or whether a changed
lifespan affects already-issued tokens. Do not accept zero expiry
as an indefinitely valid token.

The product guide documents resetting the secret and toggling activation.
The repository's reset method says the old secret stops working immediately.
The [recorded results](#recorded-live-results) establish reset behavior for
the tested account. Activation remains outside this slice; do not infer its
revocation behavior.

### Policies, limits, and errors

Product docs support policies attached to service accounts with selected
actions/resources. They do not establish whether a server list filters
ungranted rows or requires a project-wide grant. Exact server resource
syntax and server discovery permissions remain live gates; security-group
results below do not establish them. Never fill that gap with `*`.

The existing [IAM API evidence](iam-writes-api.md#bodies-and-responses)
reports a quota of 20 service accounts from console quota reads. Treat that
as account-specific evidence, not a platform-wide constant. No universal
client-secret lifetime, token issuance rate, failed-login threshold, or
maximum token lifespan was established by the public sources inspected.
Check quota before creating one account; do not probe limits by exhaustion.

The official Go SDK models IAM failures as
`{"errors":[{"code":"...","message":"..."}]}` and recognizes
`AUTHENTICATION_FAILED` and `TOO_MANY_FAILED_LOGINS`. Those handlers do not
prove HTTP status mappings or exhaust the server's shapes. OAuth-style
`error`/`error_description` is not established by the Accounts spec. The
SDK returns fixed local errors and status only, avoiding a public
dependency on any unverified server message or code.

## Repository evidence

- [auth.go](../../internal/core/auth.go), [iamuser
  login](../../internal/iamuser/login.go): IAM user uses browser sign-in, PKCE,
  optional TOTP, v1 exchange, and age-limited invalidation; do not reuse that
  flow.
- [service-account auth](../../internal/core/service_account_auth.go),
  [exchange](../../internal/core/service_account_exchange.go),
  [client.go](../../internal/core/client.go),
  [options](../../internal/core/client_options.go),
  [config](../../internal/core/config.go), [public aliases](../../vngcloud.go):
  Auth selection, shared token sources, and error-only `Authenticate`.
- [load.go](../../internal/core/load.go): Whole-source loading, explicit
  profiles and file checks; service-account
  [tests](../../internal/core/load_service_account_test.go).
- [cache](../../internal/tokencache/cache.go), [service-account
  cache](../../internal/tokencache/service_account.go), [cache
  tests](../../internal/tokencache/cache_test.go): IAM key fields exclude the
  password and TOTP; preserve that hash. The service-account key is separate.
- [transport](../../internal/transport/transport.go), [hardening
  tests](../../internal/core/transport_hardening_test.go): Token required; one
  401 recovery, `Once`, sensitive capture suppression, redirect guards.
- [auth tests](../../internal/core/auth_test.go), [IAM security
  tests](../../internal/iamuser/security_test.go): Fixed `LoginError` boundary
  and redirect leak tests to retain.
- [Secret](../../secret.go): Redacting defined string type with explicit
  formatting/reflection limitations.
- [project discovery](../../internal/core/project.go),
  [endpoints](../../internal/endpoints/endpoints.go),
  [compute](../../compute/compute.go): Console endpoints, optional explicit
  project, current `GetServer` route.
- [service-account writes](../../iam/service_accounts_write.go),
  [models](../../iam/models.go): Create has partial-output/secret preservation;
  client ID comes from confirm read; reset/delete already exist.
- [IAM CLI](../../internal/cli/svc_iam.go), [secret
  files](../../internal/cli/secretfile.go): Create/reset require exclusive 0600
  secret files and never print secret.
- [configure](../../internal/cli/configure.go), [CLI
  config](../../internal/cli/config.go), [errors](../../internal/cli/errors.go):
  stdin secrets, read-only latch, CLI disk cache, existing error/exit
  precedence.
- [caller identity](../../iam/caller.go), [guards](../../iam/guard.go), [policy
  guard](../../iam/policy_guard.go): Existing caller classification and
  conservative write restrictions remain unchanged.
- [auth kind](../../internal/core/auth_kind.go), [NAT
  guard](../../network/nat_write_guards.go), [NAT
  design](network-writes-nat.md): NAT create/delete require SDK IAM-user login
  in Hanoi. [Quote tests](../../network/nat_write_test.go) preserve broader
  auth and region support.
- [IAM design](iam-writes.md), [API](iam-writes-api.md),
  [checks](iam-writes-checks.md): Management contracts and existing live-probe
  rules remain authorities.
- [Authentication](../wiki/Authentication.md),
  [Configuration](../wiki/Configuration.md): Public auth and cache guidance
  reflects the implemented behavior.
- [ADRs](../adr/README.md), [security](../../instructions/security.md), [live
  data](../../instructions/live-data.md): Preserve retries, guards, secret
  handling, approvals, and sanitation.

IAM authentication exists in the [Go SDK][go-auth] and [Terraform
provider][tf-auth]. The [configuration cache docs][cache-docs] state that IAM
keys exclude password and TOTP; preserve the hash and keep the service-account
cache key separate.

## Recorded live results

Source: the manager's private `sa-probe-results.md`. The completed probe used
`hcm-3`, an IAM-user manager, two disposable unattached security groups,
one service account, and one narrow policy. Service-account product calls
in this probe used `WithStaticToken`; these results establish server policy
behavior, not SDK token exchange.
All resources were deleted, confirmed absent, and quota usage returned to
baseline. These results establish behavior only for this test scope.

- IAM quota bodies use integer `usage` and `limit`; Portal quota `used` is a
  decimal string. Security-group creation returned 201, IDs shaped
  `secg-<uuid>`, and two default rules per group. Probe validation must accept
  that ID shape; it does not change the SDK's general safe-path rule.
- Service-account creation returned 201 with a secret; confirm read was 200.
  These credentials passed the [token exchange](#token-exchange).
- The catalog identifies `GetSecurityGroup` as Read/GET and
  `UpdateSecurityGroup` as Write/PUT, with resource type `vserver:secgroup`
  and builder `vserver:{zone}:{root account id}:secgroups/{id}`. Catalog
  caller types include `user-sa` and `service-sa`.
- The policy had one allow statement for `vserver:GetSecurityGroup` and
  `vserver:*:<account id>:secgroups/<allowed group id>`. Here `<account id>`
  means the numeric root account, not the service-account ID; `*` occupies
  only the region component. Create returned 201 and attachment returned 204.
  Complete attachment checks found exactly this policy and statement, with
  only the disposable account in the reciprocal service-account array.
- A fresh token read the allowed group with 200 and the matching ID. The
  excluded group returned a non-disclosing 403: a JSON array of objects with
  string `code` and `message`, no detail object, group name, or description.
  The report masks those strings; their exact values are not established.
- The manager's description update and restoration both returned 200. The
  service-account SDK call sent exactly one PUT on the expected route with
  retries disabled. GreenNode returned 403, mapped to `ErrPermission`, and
  the description stayed unchanged. This proves GreenNode enforces both
  the resource boundary and read-only action restriction for these groups.
- `project.ListProjects` returned 200 and one project matching the manager's.
  The group read with project discovery returned 200, the matching group,
  and the same selected project. `iam.GetCallerIdentity` returned 200 with
  `userType` equal to `user-sa`; `UserID` equals the created service-account
  ID, not its client ID or the numeric root account ID.
- Setting `AccessTokenLifeSpan` to 3600 returned 200, read back correctly,
  and produced a new token with `expires_in` 3600. Expiry wait was skipped.
- Secret reset returned 200. The new secret exchanged successfully; the old
  secret immediately returned 401 with `errors[]` code
  `AUTHENTICATION_FAILED`. A token issued before reset still read the
  allowed group with 200. Reset rejects the old secret but does not revoke
  issued tokens; no longer-term survival measurement was made.
- Deleted credentials returned 401 with the same authentication error code.
  The retained-token read after deletion yielded no HTTP status in the
  harness, and the policy had already been detached. Neither observation
  establishes token revocation on deletion. Cleanup deletes returned 204
  and by-ID absence checks returned 404, including recovered resources.

## Live probes

Only the manager runs these under [live verification][live-checks]. Standing
approval covers the free, cleanable test resources above, updates, and
cleanup. It covers no paid server or broader policy. The account has no
servers; server scope and enumeration remain blocked. The manager supplies
the project and region and uses an IAM-user config for provisioning/cleanup.

Use a private ignored helper, runtime credentials, and a 120-second context
per call with a separate bounded cleanup context. Keep secrets/tokens in
memory, never argv, environment additions, logs, captures, stdout, or raw
files. Emit only statuses, schema metadata, counts, timings, and booleans.
Give each resource a distinct `vngcloud-probe-<8 hex>` name and record
baseline absence. Preserve IDs from partial outputs and register cleanup
immediately. Never repeat uncertain creation or guess a deletion target.

For paged account/policy recovery and service-account `.../policies`
attachment lookups, read raw pages from page 0, size 100, at most 50 pages.
Require data and pagination metadata, stable totals, unique IDs, and an
accumulated count equal to `totalItems`; fail on incomplete evidence.
`ListPolicies` drops metadata, so one SDK page cannot prove completeness.
[IAM response evidence][iam-pages] owns the shapes. For reciprocal
`policies/<id>/groups`, `.../iam-users`, and `.../service-accounts`, make one
request each without pagination. Require strict non-null arrays with the
documented item types; reject missing bodies, objects, wrong types, and
malformed/trailing JSON. Explicit `[]` is empty. Require no metadata. Follow
[guard decoding](../../iam/guard.go), not the tolerant public decoder.

1. Check `accounts-api/v1/quotas/user-sa` on the Dashboard host and
   `policies-api/v1/quotas/policy` on the IAM host from [IAM hosts][iam-hosts].
   Call `portal.ListQuotaUsed` for the project. Require spare capacity for
   the disposable resources and default rules; never exhaust quota.
2. Recheck `policies-api/v1/actions?product=vserver` and
   `policies-api/v1/resources` against [SDK reads][sg-reads],
   [writes][sg-writes], and the recorded mapping above. Stop on mismatch.
   These are manager metadata probes; SDK `iam.ListActions` fixes product
   to `iam`. Do not infer server mappings from security groups.
3. Call `network.CreateSecurityGroup` twice with unique names and synthetic
   descriptions. Attach neither and change no rules, under the accepted
   [cost contract][sg-cost]. On uncertain create, use complete
   `ListSecurityGroups` pagination and exact-name baseline comparison to
   recover one new ID. Confirm ACTIVE, non-system groups and raw explicit
   empty attachments through `GetSecurityGroup` and
   `ListServersBySecurityGroup`; an empty SDK slice alone is insufficient.
4. Call `iam.CreateServiceAccount` with its unique name. Preserve partial ID
   and secret; recover client ID with `GetServiceAccount`. On uncertainty,
   use complete exact-name account lookup and accept one new baseline-absent
   match. Missing secret stops token probes but never skips cleanup.
5. Send the [token request](#token-exchange) with a private TLS client,
   no cookie jar or retries, and a 64 KiB response bound. `CheckRedirect`
   returns `http.ErrUseLastResponse`. Record schema/status metadata only.
   A redirect or incompatible response stops the probe; do not fall back.
6. Call `iam.CreatePolicy` with its unique name and the single allow
   statement recorded above. Retain partial IDs, including `ErrNotSettled`.
   On network failure, 5xx, missing ID, or other uncertain outcome, recover
   through complete raw pages of
   `GET policies-api/v1/policies?name=<name>&pageNumber=<page>&pageSize=100`.
   Accept one exact baseline-absent match. If absent, retry only lookup at
   most twice at one-second intervals. Multiple/unresolved matches stop the
   probe and leave a private recovery record. Never retry creation.
7. Call `AttachServiceAccountPolicy`. Read the complete paged
   `policies-api/v1/user-attachments/service-accounts/<id>/policies`
   collection and `GetPolicy` for every ID. Require exactly the intended
   policy and statement, with no broader actions, resources, or conditions.
   Check all reciprocal arrays: only the disposable account ID, empty
   users/groups. Repeat immediately before and after the write-denial probe;
   any change invalidates the result.
8. Issue a fresh token after attachment. Use `WithStaticToken`, region, and
   explicit project with unchanged SDK endpoints. Call
   `network.GetSecurityGroup` for allowed and excluded groups, checking the
   recorded outcomes. The implemented `WithServiceAccount` and
   `Authenticate` path repeated both reads with the same outcomes.
9. Reconfirm the allowed group is owned, non-system, and unattached. As
   manager, save its description, update to a synthetic value, confirm,
   restore, and confirm. Repeat `network.UpdateSecurityGroup` with the
   service-account token and `WithRetry(0, 0)`. Observe only method,
   expected-route equality, send count, and status. Require one actual PUT
   to GreenNode and its 403 mapped to `ErrPermission`; a CLI/guard refusal,
   pre-read failure, 401, 404, or input rejection proves nothing. Confirm
   unchanged description as manager. Restore after unexpected success or
   uncertainty, fail the probe, and clean up.
10. Call `project.ListProjects`, then read the allowed group without
    `WithProjectID`. Record status, count, selected-project equality. Call
    `iam.GetCallerIdentity`; compare IDs only in memory. Never silently add
    permissions or relax guards to make these checks pass.
11. Read `AccessTokenLifeSpan`, update only that field to the recorded tested
    value, confirm, and exchange again. Optional expiry verification waits
    until returned expiry plus 30 seconds and attempts one old-token read.
12. Retain a token, reset the secret, and exchange once with each new/old
    secret. Check the recorded outcomes and old/new-token reads; record the
    interval. The implemented disk cache kept the old and new secrets'
    entries apart: the new secret made its own exchange, and the old secret
    failed login with 401 before any product request.
13. Detach/delete the policy, then delete the account. Try one exchange with
    deleted credentials and one retained-token group read. Policy detachment
    prevents any denial from proving revocation. Delete both groups and
    confirm absence and baseline quotas. Run cleanup on every exit using
    only recorded or uniquely recovered owned IDs. Reconcile uncertain
    attachments before deleting parents; stop on unknown ownership or
    attachments. Keep recovery IDs private. Remove helper/cache/secret files
    and check billing evidence under the [cost contract][sg-cost].

Sanitize raw token JSON in memory before writing fixtures. Replace tokens
and unknown sensitive fields, preserving types/enums/expiry; inspect the
sanitized result before `testdata/`. Never save raw create/reset/token bodies
or treat SDK output as raw evidence. Keep unknown errors private and extract
schema metadata first. The manager report alone is not a raw JSON fixture.

## Open evidence

The fixed-host exchange and current SDK security-group read gates are met.
Remaining checks must not be presented as verified:

- Token revocation on deletion, expiry-wait behavior, lifespan extrema/zero
  semantics, and changed-lifespan effects on issued tokens remain unknown.
- Other regions and services, activation, issuance limits, failed-login
  thresholds, and universal secret lifetime remain unverified.
- Server scope and enumeration need approved existing allowed/excluded
  servers, verified server mappings, detail reads, and enumeration checks
  for excluded IDs. Security groups cannot substitute; create no paid server.

[live-checks]: ../../instructions/testing.md#live-verification
[iam-pages]: iam-writes-api.md#bodies-and-responses
[iam-hosts]: iam-writes-api.md#hosts
[sg-reads]: ../../network/network.go
[sg-writes]: ../../network/security_groups_write.go
[sg-cost]: vserver-writes.md#cost
[cache-docs]: ../wiki/Configuration.md#token-cache

[accounts]: https://docs.api.greennode.ai/service-docs/accounts-api.html
[overview]: https://docs.api.greennode.ai/
[docs-page]: https://docs.greennode.ai/identity-and-access-management-iam/cac-loai-dinh-danh-iam/tai-khoan-service-accounts
[sa-en]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d5348dab82937443ed27f784d73c/English/identity-and-access-management-iam/cac-loai-dinh-danh-iam/tai-khoan-service-accounts.md
[sa-vi]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d5348dab82937443ed27f784d73c/Vietnamese/identity-and-access-management-iam/cac-loai-dinh-danh-iam/tai-khoan-service-accounts/README.md
[storage-auth]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d5348dab82937443ed27f784d73c/English/vstorage/object-storage/vstorage-hcm03/identity-and-access-management/managing-vstorage-access-account/service-account/authorization-with-service-account.md
[policy]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d5348dab82937443ed27f784d73c/English/identity-and-access-management-iam/quan-ly-truy-cap-iam/quan-ly-truy-cap-qua-policy.md
[managed]: https://github.com/vngcloud/docs/blob/0c10ca7a4521d5348dab82937443ed27f784d73c/English/identity-and-access-management-iam/quan-ly-truy-cap-iam/chinh-sach-vng-managed-policy.md
[tf-auth]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/client/authen/client.go
[tf-provider]: https://github.com/vngcloud/terraform-provider-vngcloud/blob/230bd7d346853b2aba33a9ec06f8c544b06fa8a2/provider/provider.go
[go-auth]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/services/identity/v2/identity.go
[go-request]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/services/identity/v2/identity_request.go
[go-response]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/services/identity/v2/identity_response.go
[go-http]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/client/http.go
[go-errors]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/sdk_error/errors.go
[go-auth-errors]: https://github.com/vngcloud/vngcloud-go-sdk/blob/a76d5fc0fdbd31663c51a7ba5cb71c15b3604188/vngcloud/sdk_error/identity.go
[console]: https://iam.console.greennode.ai/
[console-config]: https://iam.console.greennode.ai/assets/configs/config.prod.json
[main]: https://iam.console.greennode.ai/main.a0cfd100731832eb.js
[sa-bundle]: https://iam.console.greennode.ai/616.6b1acf8d5038308b.js
[sa-api-bundle]: https://iam.console.greennode.ai/867.39145ed302e52dc3.js
