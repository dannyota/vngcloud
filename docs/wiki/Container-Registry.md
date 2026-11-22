# Container Registry

`containerregistry` is `danny.vn/vngcloud/containerregistry`, with its own
`New(cfg)`. See [Services](Services.md) for the shared `Config` and the
read method shape every package uses.

```go
vcrClient := containerregistry.New(cfg)
vcrClient.ListRepositories(ctx, in)     // AccessLevel, Name
vcrClient.GetRepository(ctx, in)        // RepositoryID (required)
vcrClient.CreateRepository(ctx, in)     // Name, QuotaLimitGB (both required), NoWait
vcrClient.DeleteRepository(ctx, in)     // RepositoryID (required), NoWait
vcrClient.ListUsers(ctx, in)            // Name, Page, Size
vcrClient.ListRepositoryUsers(ctx, in)  // RepositoryID (required), Name, Page, Size
vcrClient.ListPermissions(ctx, nil)
vcrClient.CreateUser(ctx, in)           // Name, Permissions (both required), Description, DurationDays
vcrClient.DeleteUser(ctx, in)           // UserID (required)
```

`Repository` and `User` are both typed structs, matching their live bodies.
`Repository`'s fields are `ID`, `Name`, `BackendName`, `AccessLevel`,
`RegistryURL`, `QuotaLimitGB`, `QuotaUsed`, `ImageCount`, `AttachedUsers`,
and `CreatedAt`; there is no `Status` field, since no response carries one.
`User`'s fields are `ID`, `Name`, `BackendName`, `Description`, `Disabled`,
`ExpiredAt`, `CreatedAt`, `NumberOfRepositories`, `UserID`, and
`Repositories` (each a `RepositoryPermission` of `RepositoryID`,
`RepositoryName`, `BackendRepositoryName`, and `Policies`, each a
`Permission` of `ID` and `Action`). `User.ID` is the id `DeleteUserInput`
and a `RepositoryPermission.RepositoryID` take; a live delete confirms
this, not `UserID`, is what the server expects. `User.UserID` decodes the
server's numeric `userId` to a string and is otherwise a separate,
unconfirmed field. A field the reference does not document is dropped
rather than kept for either type. This breaks code that indexed
`Repository` or `User` as a map.

`CreateRepository` always creates a private repository; there is no
`Public` option, since a public repository accepts anonymous push. The
server applies no account prefix, so the created `Repository.Name` equals
the Input's `Name` exactly. The server requires `Name` to be 6 to 20
characters, only `a-z`, `0-9`, `_`, and `-`, starting with a letter or
digit, and returns 400 naming the rule otherwise; the SDK sends `Name` as
given and does not check its shape. `CreateRepository` is a `POST` and is
never retried after a failure that may have already reached the server,
including a 408 or 499: after such a failure, list repositories with
`Name` set and match a row whose name equals the input exactly before
creating again.

`DeleteRepository` reads the repository first and returns
`ErrRepositoryNotEmpty`, sending nothing, when it still holds images;
delete the images with `docker` or the console first. A repository user
attached to it is not affected by the delete. That same read must confirm
the repository: a response with no image count, or one naming a different
repository, also sends nothing and returns an error.

The create and delete responses carry no status to wait on. Without
`NoWait`, `CreateRepository` confirms the new repository with
`GetRepository`, and `DeleteRepository` waits for `GetRepository` to
report it gone, each polling every 2 seconds for up to 60 seconds. A live
create was visible through `GetRepository` at once, so the confirm read
usually succeeds on its first try. Past the bound, or on a canceled
context, the returned error wraps `ErrNotSettled`: for a create, the
repository exists and must not be created again; for a delete, the delete
was sent and a rerun is safe.

`CreateUser` takes a permission's actions as names, such as `"Pull
Images"`, not raw policy ids: it reads `ListPermissions` and maps each name
to its policy id, matching the server's own list exactly (a live capture
shows the three actions "Pull Images", "Push Images", and "All"), so an
unknown action fails before any create is sent. The server's own name rule
for a user is 6 to 14 characters, only `a-z`, `A-Z`, `0-9`, `_`, and `-`,
starting with a letter or digit; the SDK sends `Name` as given and does not
check this. Before sending the create, `CreateUser` also lists users by the
exact input name and refuses with `vngcloud.ErrInvalidInput`, sending
nothing, if a user is already named that: the create response carries no
user id, so a lookup after the fact could otherwise resolve to an older
user that just happens to share the name.

A repository user is a credential that can push images other systems run.
The example below creates a pull-only user with an expiry, rather than an
unrestricted, permanent one:

```go
created, err := vcrClient.CreateUser(ctx, &containerregistry.CreateUserInput{
	Name:         "app-ci",
	DurationDays: vngcloud.Ptr(90),
	Permissions: []containerregistry.UserPermission{
		{RepositoryID: "<repository-id>", Actions: []string{"Pull Images"}},
	},
})
if created != nil && created.SecretKey != "" {
	saveSecret(created.SecretKey.Reveal())
}
if err != nil {
	log.Fatal(err)
}
```

The create response carries a secret key but no attributable user ID. An
exact-name lookup can find a concurrently recreated account, so even one match
proves no ownership. `CreateUser` keeps `User` empty, retains `SecretKey`, and
returns `ErrUserNotFound` after an accepted secret-only response. This is a
breaking change: inspect both output and error, and persist the secret before
handling the error. The CLI writes the secret file and exits 1. It cannot
automatically delete a candidate after a file failure. Reconcile ownership with
other administrators before any cleanup.

If a saved-file path contains the issued secret, that value is redacted in
diagnostics and `SecretFile` output. Use the original `--secret-file`
argument to locate the saved file.

Both name lookups walk the established user `page` and `size` parameters.
Missing or conflicting list envelopes, missing or invalid row names, invalid or
changing totals, repeated IDs, and exhausted page limits fail instead of proving
absence. No create is sent on incomplete name evidence. Dependent reads
withhold captures and reject reflections of issued secrets and every credential
sent during the operation. `SecretKey` remains a `vngcloud.Secret`; never log
`Reveal()`.

`CreateUser` sends once, including after redirects or authentication failure.
After an uncertain create, inspect `list-users --name` and reconcile ownership
before another create or delete. A name match alone never authorizes cleanup.
`DurationDays` left nil creates a user with no expiration; set one for a
temporary user.

`ListRepositories` returns partial output with an explicit incomplete-list error
when metadata reports more pages. Repository continuation parameters remain
unverified, so the SDK adds none. Inspect both output and error; never treat
that partial list as a complete inventory. User lists retain their established
`Page` and `Size` inputs. A malformed list cannot turn a server error into
`NotFound`.

Repository confirmation requires the requested ID. Polling requests and sleeps
share the earlier caller or operation deadline, and a response received after
that deadline cannot confirm success.

`DeleteUser` sends the delete directly: a user holds no data of its own,
so there is no pre-delete guard.

Repository and user names, registry URLs, and ids are account data.
`docker login vcr.vngcloud.vn -u <login name> --password-stdin` takes the
secret as the password; whether `<login name>` is the repository user's
own name or the repository's name is unverified.
