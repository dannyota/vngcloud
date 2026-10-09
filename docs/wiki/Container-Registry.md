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
if err != nil {
	log.Fatal(err)
}
log.Println(created.SecretKey.Reveal())
```

The response carries only a secret key, no user id, so `CreateUser` finds
the new user with the same exact-name listing: a live capture shows the
server applies no account prefix. One match fills `User`; zero or more
than one returns an error wrapping `ErrUserNotFound`, naming `list-users
--name <name>` to check by hand, while `SecretKey` on the Output is still
set either way, since the create itself already succeeded. Both this
lookup and the pre-create check walk every page of the list and fail
closed, returning an error rather than a guess, when a page's own totals
do not add up. `SecretKey` is a `vngcloud.Secret`: printing, logging, or
JSON-encoding the Output gives `[redacted]`, and `Reveal()` is the only way
to read it back. `CreateUser` sends the create with `Once`: a followed
redirect is refused, since it would resend the same create and its secret
a second time. A 401 is not the same risk: the API gateway rejects a stale
or invalid token before the request reaches the create logic, so a 401
always creates nothing. A 5xx or a network error is still never resent,
since either can mean the server already acted; a user found afterward by
`list-users --name <name>` has already lost its secret and should be
deleted before creating again. `DurationDays` left nil creates a user with
no expiration; set one for a pull user meant to be temporary.

`DeleteUser` sends the delete directly: a user holds no data of its own,
so there is no pre-delete guard.

Repository and user names, registry URLs, and ids are account data.
`docker login vcr.vngcloud.vn -u <login name> --password-stdin` takes the
secret as the password; whether `<login name>` is the repository user's
own name or the repository's name is unverified.
