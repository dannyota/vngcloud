# vCR Writes Design

Status: Accepted (2026-09-27). The owner approved every recommendation
under [Owner decisions](#owner-decisions).

This design adds vContainer Registry (vCR) repository create and delete and
repository user create and delete to `containerregistry`, beside the
existing `ListRepositories` and `ListUsers`, with CLI commands. It is pick 8
of the [free writes survey](free-writes-survey.md). A user create returns a
registry secret, which follows the `vngcloud.Secret` and `--secret-file`
contract of [vStorage](storage.md#secrets) and
[create-ssh-key](vserver-writes.md#create-ssh-key).

Whether a repository costs money is unknown. The releases are gated on the
[cost probe](vcr-writes-checks.md#cost-probe). Tests, the probe, and live
checks are in [vCR writes: checks](vcr-writes-checks.md).

## Source

Public sources only; no write was sent.

- The vCR API reference on `docs.api.greennode.ai` (`service-docs/vcr.html`),
  tags `Repository` and `Repository User`.
- The product pages under "vCR" on `docs.greennode.ai`: Getting Started,
  Create a repository, Edit quota limit, Repository user, Create
  repository user, Refresh secret key, and Pull and Push image with Docker.
- A read-only `list-repositories` on the test account, through this
  repository's CLI: it succeeds on the IAM vCR endpoint
  (`vcr.<console domain>/vcr-api/`) and returns no rows.

Neither VNG Cloud's Go SDK nor its Terraform provider covers vCR. The
reference documents `https://vcr.api.vngcloud.vn` (HCM) with the same
`v1/...` paths; that the writes exist on the IAM endpoint is part of the
cost probe.

### Calls

| Call | Method and path | Success |
|-|-|-|
| List repositories | `GET v1/repository` | 200 |
| Get repository | `GET v1/repository/{repoId}` | 200 |
| Create repository | `POST v1/repository` | 202 |
| Delete repository | `DELETE v1/repository/{repoId}` | 202 |
| List users of a repository | `GET v1/repository/{repoId}/user` | 200 |
| List users | `GET v1/user` | 200 |
| List permissions | `GET v1/user/permissions` | 200 |
| Create user | `POST v1/user` | 200 |
| Delete user | `DELETE v1/user/{repoUserId}` | 200 |

The reference documents only 200 or 202, 401, and 500 for each call.

### Bodies and responses (inferred)

- Repository create: `repoName`, `isPublic`, and `quotaLimit` (all
  required). `quotaLimit` is an integer in GB. The console says a name the
  caller leaves out is generated, and every name gets the account ID as a
  prefix.
- Repository responses (create, get, delete, and list rows) share one bare
  shape (`RepositoryDto`): `uuid`, `name`, `backendName`, `accessLevel`,
  `registryUrl`, `quotaLimit`, `quotaUsed`, `imageCount`, `attachedUser`,
  and `createdAt`. A live capture confirms these are the only keys: there
  is no `status` field on any of the four responses, so `CreateRepository`
  and `DeleteRepository` confirm by reading the repository rather than
  waiting on a status (see [Waits](#waits)). The reference names the list
  key `listData`; the fixture has `data`, and `core.DecodeFlexibleList`
  reads both.
- User create: `name` and `permissionRequestList` (required),
  `description`, and `duration` in days. Each permission is `repoId` and
  `policyIdList`. The response is only `{"secretKey": "..."}`: no user ID.
- User list rows are `RobotAccountDto`: `uuid`, `name`, `backendName`,
  `description`, `disable`, `expiredAt`, `createdAt`, `numberOfRepo`,
  `userId`, and `repoPermissionList` (each `repoId`, `repoName`,
  `backendRepoName`, and `policyDtoList` of `uuid` and `action`). No field
  holds a secret.
- The permission list is `[{uuid, action}]`. The console offers "Push &
  Pull" and "Pull Only"; the action strings are a probe item.
- ID examples in the reference are `repo-<uuid>` and `ra-<uuid>`, which
  `core.CheckPathID` allows.

### Server rules (from the product docs)

- A public repository lets anyone pull, and the docs say push too,
  without a repository user. A private one needs a user and its secret.
- `quotaLimit` caps storage. It can be raised later, never below use.
- A user may expire. An expired user is locked until extended. A user may
  be disabled and enabled.
- The secret is shown once. "Refresh secret key" makes a new one.
- `docker login vcr.vngcloud.vn -u <repository user>` with the secret as
  the password. One console page names the repository name as the
  username instead; the probe settles which name works.

### Cost

No pricing page, calculator item, or quote resource type names vCR. A
repository reserves `quotaLimit` GB, which suggests storage billing, so
the cost is unknown until the [cost probe](vcr-writes-checks.md#cost-probe)
settles it. Users hold no storage and are assumed free once repositories
are. If repositories are paid, ADR 0002 rule 8 requires a quote before
`CreateRepository` ships, and that quote is designed then.

## Non-goals

- The HAN region (`han-1.api.vngcloud.vn/vcr-api/`): the SDK has one vCR
  endpoint.
- Quota change, user update, permission change, attach, detach, enable,
  and disable. Each is small and can follow once create and delete are
  proven.
- Secret refresh. It is a `GET` that changes the secret, so the
  transport's `GET` retry could rotate it twice and lose the first new
  value. It needs its own design using `Once` from
  [ADR 0003](../adr/0003-toggle-writes.md).
- Image and artifact reads and deletes, and repository history.
- Public repositories; see [decision 4](#owner-decisions).

## Decisions

- `Repository` and `User` become typed models (breaking), so a write's
  Output never passes an unseen key through, and `list-users` can ship.
- A user's secret is a `vngcloud.Secret`, written only to `--secret-file`.
- Repositories are created private. A repository with images is not
  deleted.
- Two releases, repositories then users, both behind the cost probe.

## SDK

All methods live in `containerregistry`.

```go
func (c *Client) GetRepository(ctx context.Context, in *GetRepositoryInput) (*GetRepositoryOutput, error)
func (c *Client) CreateRepository(ctx context.Context, in *CreateRepositoryInput) (*CreateRepositoryOutput, error)
func (c *Client) DeleteRepository(ctx context.Context, in *DeleteRepositoryInput) (*DeleteRepositoryOutput, error)
func (c *Client) ListRepositoryUsers(ctx context.Context, in *ListRepositoryUsersInput) (*ListRepositoryUsersOutput, error)
func (c *Client) ListPermissions(ctx context.Context, in *ListPermissionsInput) (*ListPermissionsOutput, error)
func (c *Client) CreateUser(ctx context.Context, in *CreateUserInput) (*CreateUserOutput, error)
func (c *Client) DeleteUser(ctx context.Context, in *DeleteUserInput) (*DeleteUserOutput, error)

var (
	ErrRepositoryNotEmpty = errors.New("containerregistry: repository holds images")
	ErrNotSettled         = errors.New("containerregistry: write accepted but not settled")
	ErrUserNotFound       = errors.New("containerregistry: created user not found")
)
```

"(r)" marks `vngcloud:"required"`.

| Operation | Input | Output |
|-|-|-|
| `GetRepository` | `RepositoryID` (r) | `{Repository}` |
| `CreateRepository` | `Name` (r), `QuotaLimitGB` (r), `NoWait` | `{Repository}` |
| `DeleteRepository` | `RepositoryID` (r), `NoWait` | `{}` |
| `ListRepositoryUsers` | `RepositoryID` (r), `Name`, `Page`, `Size` | paged `User` |
| `ListPermissions` | none | `Items []Permission` |
| `CreateUser` | `Name` (r), `Description`, `DurationDays *int`, `Permissions []UserPermission` (r) | `{User; SecretKey vngcloud.Secret}` |
| `DeleteUser` | `UserID` (r) | `{}` |

`UserPermission` is `RepositoryID` and `Actions []string`. `Permission` is
`ID` and `Action`.

### Typed models

`Repository` and `User` change from `map[string]any` to structs with the
reference's fields, Go-named (`ID` for `uuid`, `QuotaLimitGB`,
`QuotaUsed`, `ImageCount`, `AttachedUsers`, `RegistryURL`; `User.Disabled`,
`ExpiredAt`, `Repositories []RepositoryPermission`). A live capture
confirms `Repository`'s fields and drops `Status`, which no response
carries. `User`'s fields still await a live capture before its fixtures are
written; a field the probe does not see stays out. `User` has no secret
field, so no read can print one. This lifts the hold on `list-users` in
[CLI reads](cli-reads.md#secrets).

The change breaks callers that index the maps. The release notes say so.

### Repositories

- `CreateRepository` sends `repoName`, `quotaLimit`, and `isPublic`
  false. `Name` is required even though the API would generate one: after
  a 5xx a generated name cannot be found. `QuotaLimitGB` must be at least
  1 (`ErrInvalidInput`); the upper bound stays on the server.
- A live 400 confirmed `repoName`'s own rule: 6 to 20 characters, only
  `a-z`, `0-9`, `_`, and `-`, starting with a letter or digit. `Name` is
  checked against this rule before any request (`ErrInvalidInput`).
- A live capture shows the server applies no account prefix: the Output's
  `Name` equals the Input's `Name` exactly. `BackendName`'s own relation to
  the account is unconfirmed. The wiki says so.
- Create is `POST` and is never resent after a 5xx or network error (ADR
  0002 rule 2). The error names `list-repositories --name <name>` and says
  to match the exact name.
- `CreateRepository` waits unless `NoWait` (see [Waits](#waits)).
- `DeleteRepository` reads first and returns `ErrRepositoryNotEmpty`,
  sending nothing, when `ImageCount` is above 0. Emptying a repository is
  image work for `docker` or the console. Attached users do not block the
  delete: they keep existing and lose access to it. The wiki says to
  delete a user made only for that repository first.
- A retried delete that finds the repository gone returns `NotFound`.
  The reference documents no 404; how a missing repository reads is a
  probe item. If it is not a 404, the SDK confirms by
  `ListRepositories` before mapping it to `NotFound`.

### Users

`CreateUser` steps:

1. Check shape: `Name` required; at least one permission; each has a
   path-safe `RepositoryID` and at least one action; `DurationDays`, when
   set, at least 1. `ErrInvalidInput` otherwise, nothing sent.
2. Read `ListPermissions` and map each action to its policy ID, matching
   the server's `action` string exactly. An unknown action is
   `ErrInvalidInput` naming the known ones, nothing sent. The server's
   list is the authority, so this is a lookup, not a value rule (ADR 0002
   rule 5).
3. Send the create with `Sensitive` set, so the capture hook never sees
   the response and a decode error never quotes it. The secret is decoded
   straight into `vngcloud.Secret`. A 200 with an empty `secretKey` is an
   `*APIError` whose message says a user may exist and names
   `list-users --name <name>`.
4. Find the user: `ListUsers` with the name filter, then keep rows whose
   `name` equals the input or ends with it after the account prefix. One
   row fills `User`. None or several: the Output still holds `SecretKey`,
   and the error wraps `ErrUserNotFound`, whose message names the list to
   check. The secret is never dropped because a lookup failed.

- The create is `POST`, never resent after a 5xx or network error. A user
  found by list after such an error has lost its secret: delete it.
- `DurationDays` nil sends no `duration`, which the console calls "no
  expiration". The wiki recommends an expiry.
- `DeleteUser` sends the `DELETE`. A user holds no data, so it has no
  guard. A retried delete that finds the user gone returns `NotFound`,
  mapped as for repositories.

### Waits

Per ADR 0002 rule 7, the 202 writes wait unless `NoWait` is set. They poll
every 2 seconds, honour `ctx`, and use an injected clock. A live capture
confirms create, get, delete, and list responses carry no `status` field,
so neither wait polls one:

| Write | Settled | Bound |
|-|-|-|
| Repository create | `GetRepository` succeeds (finds the uuid) | 60 s |
| Repository delete | `GetRepository` reports `NotFound` | 60 s |

A 404 during the create wait keeps polling, tolerating a repository that is
not yet readable. A live create completed in about 4 seconds, with the
repository already visible through `GetRepository` by the time the `POST`
returned, so the confirm read normally succeeds on its first try; the poll
exists only as a bound against that not holding every time. A live delete
settled within the 60-second bound, confirmed at the time through the
SDK's prior list-based wait; whether a deleted repository's own `GET`
answers a plain 404 or an ambiguous 5xx needing the `ListRepositories`
fallback (see [Repositories](#repositories)) is unconfirmed, so
`DeleteRepository` relies on `GetRepository`'s own handling of both. The
bound returns the Output and an error wrapping `ErrNotSettled`: for a
create, the repository exists and the create must not be repeated; for a
delete, a rerun is safe. User writes answer 200 and have no wait unless the
probe shows one.

### Identifiers and retries

- Every operation that puts an ID in a path checks it with
  `core.CheckPathID` before any request.
- Deletes keep the transport's retries. The CLI never retries a write.

## Errors

| Case | Result | CLI code and exit |
|-|-|-|
| Missing field, bad ID, quota below 1, no permission, unknown action | `ErrInvalidInput`, no request | `InvalidUsage`, 2 |
| `--secret-file` exists or its directory is missing; missing `--yes` | No request | `InvalidUsage`, 2 |
| Unknown repository or user | `NotFound` | `NotFound`, 4 |
| Delete of a repository with images | `ErrRepositoryNotEmpty`, no request | `RepositoryNotEmpty`, 1 |
| Repository not confirmed or not gone within the wait | `ErrNotSettled`, with Output | `NotSettled`, 1 |
| User created but not found by list | `ErrUserNotFound`, with Output | `UserNotFound`, 1 |
| Secret file write failed after create | User deleted | `SecretFileFailed`, 1 |
| Duplicate name, quota, a refusal for no credit | The server's `*APIError` | 1 |
| 5xx or network error on a create | The error; the message names the list | 1 |

The CLI list in [CLI](cli.md#errors-and-exit-codes) gains
`RepositoryNotEmpty` and `UserNotFound`. `UserNotFound` exits 1, not 4:
the create succeeded.

## CLI

| Command | Kind | `--yes` | Release |
|-|-|-|-|
| `containerregistry get-repository` | Read | No | R1 |
| `containerregistry create-repository` | Write | No | R1 |
| `containerregistry delete-repository` | Write, destructive | Yes | R1 |
| `containerregistry list-users`, `list-repository-users`, `list-permissions` | Read | No | R2 |
| `containerregistry create-user` | Write | No | R2 |
| `containerregistry delete-user` | Write, destructive | Yes | R2 |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- `create-repository` takes `--name` and `--quota-limit-gb`. It has no
  `--public` flag.
- `create-user` takes `Permissions` through `--cli-input-json`, inline or
  `file://`; the value is not secret.
- A deleted user's secret cannot be restored, and a deleted repository's
  ID is gone from every user that named it (ADR 0002 rule 6).

```sh
vngcloud containerregistry create-repository --name app --quota-limit-gb 1
vngcloud containerregistry create-user --name app-ci --duration-days 90 \
  --cli-input-json '{"Permissions":[{"RepositoryID":"<id>","Actions":["pull"]}]}' \
  --secret-file ./vcr-secret
docker login vcr.vngcloud.vn -u <login name> --password-stdin < ./vcr-secret
```

The action strings in the example are placeholders until the probe names
them.

### create-user

`create-user` needs `--secret-file <path>` and cannot print the secret. It
follows [CLI secret files](cli.md#secret-files) and the steps of
[create-ssh-key](vserver-writes.md#create-ssh-key):

1. Before any request, the parent directory must exist and nothing may
   exist at the path, symlinks included; otherwise it exits 2.
2. After the create, including one that returns `ErrUserNotFound`, it
   opens the path with `O_CREATE|O_EXCL|O_NOFOLLOW` and mode 0600 and
   writes the secret with one trailing newline, which `docker login
   --password-stdin` strips.
3. If the write fails, it removes any partial file, deletes the new user,
   and exits 1 with `SecretFileFailed`. Without a user ID (after
   `ErrUserNotFound`) or if that delete fails, the error names the user
   name so a person can delete it.
4. Stdout gets the user without the secret: `SecretKey` prints as
   `[redacted]`, and a `SecretFile` field names the path. The wiki names
   the field that is the `docker login` username.

## Security

- The adversarial review checks: the secret never reaches stdout, stderr,
  `--debug`, an error, a response capture, a fixture, or a log; it is
  written to `--secret-file` even when the lookup fails; the file rules of
  create-ssh-key hold; the orphan user is deleted after a failed write;
  `User` has no secret field; `isPublic` is always false; the images
  pre-read sends nothing; no create resend after a 5xx; path ID checks on
  every call; `--yes` on deletes; read-only refusal of every write.
- A repository user is a credential that can push images other systems
  run. The wiki shows a pull-only user with an expiry first.
- Repository and user names, registry URLs, and IDs are account data.
  Fixtures use `<id>`, `<name>`, `<account>`, `<hostname>`, and
  `<secret>`.

## Releases

| Release | Content |
|-|-|
| R1 | Typed `Repository` (breaking); `GetRepository`, `CreateRepository`, `DeleteRepository`, the waits, `ErrRepositoryNotEmpty`, `ErrNotSettled`; CLI commands |
| R2 | Typed `User` (breaking); `ListRepositoryUsers`, `ListPermissions`, `CreateUser`, `DeleteUser`, `ErrUserNotFound`; `list-users` and the other CLI commands |

Each is numbered when it ships, after its live checks and an adversarial
review. `Services.md` and `CLI-ContainerRegistry.md` in the wiki gain the
writes, the prefix note, the retry advice, and the `docker login` steps.

The cost probe gates both. If it shows repositories are free, R1 and R2
ship in order as usual. If it shows them paid, each release is still built
and unit-tested and may merge, but its live checks and its tag wait until
the owner adds credit, and R1 also gains the quote ADR 0002 rule 8
requires. Nothing is tagged on an unverified write.

## Owner decisions

1. Designing before the probe. The survey's decision 2 kept vCR out of any
   design until a quote or probe showed zero cost. Options: approve this
   design now with the probe as the gate; run the probe first, then
   review the design. Recommend approve now: the design needs the probe's
   captures anyway, and building and unit-testing costs nothing.
2. Cost probe. Options: one private repository create with
   `quotaLimit` 1 and delete, relying on the no-credit refusal and the
   next-day bill; a `pricing.GetQuote` guess only. Recommend the create:
   no quote resource type for vCR is known, and the test account's
   prepaid balance of zero turns a paid create into a refusal.
3. Typed models. Options: type `Repository` and `User` now (breaking);
   add typed write Outputs and keep the maps for reads; keep maps.
   Recommend type both: one model per resource, and `list-users` can ship.
4. Public repositories. Options: always private, no `--public`;
   `--public` behind `--yes`. Recommend always private: the docs say a
   public repository accepts anonymous push, which lets anyone store images
   on the owner's quota and serve them under the owner's name.
5. Repository delete guard. Options: refuse while images remain, allow
   attached users; also refuse while users are attached; no guard.
   Recommend the first: images are the data, and a user can be attached
   to other repositories, so forcing its delete would be wrong.
6. Permission input. Options: action names the SDK resolves through
   `ListPermissions`; raw policy IDs. Recommend action names: IDs are
   opaque, and the lookup uses the server's own list.
7. User expiry. Options: optional with no default; required. Recommend
   optional, with the wiki recommending one: the API allows none, and a
   required field would block a long-lived pull user a person chooses on
   purpose.
8. Release split. Options: R1 repositories then R2 users; one release.
   Recommend two: R2 carries the secret handling.

## Open questions

- Whether a repository costs money, and on what (quota or use).
- Whether the IAM vCR endpoint accepts the writes.
- How a missing repository or user reads (the reference lists only 500):
  unconfirmed whether a deleted repository's own `GET` answers a plain 404
  or an ambiguous 5xx.
- The permission action strings.
- Whether names are unique, and whether `BackendName` carries an account
  prefix; a live capture shows `Name` does not.
- Which name `docker login` takes.
