# Database

`danny.vn/vngcloud/database` reads relational database catalogs and inventory
in `hcm-3`, and creates and deletes one kind of instance: a private,
single-node MySQL 8.0 instance with automatic backup off. Creating one
spends credits. See [CLI: Database](CLI-Database.md) for every command.

## Reads

```go
client := database.New(cfg)
versions, err := client.ListDatabaseEngineVersions(ctx, nil)
zones, err := client.ListDatabaseZones(ctx, nil)
flavors, err := client.ListDatabaseFlavors(ctx, &database.ListDatabaseFlavorsInput{Engine: "MySQL", EngineVersion: "8.0", ZoneID: zoneID})
types, err := client.ListDatabaseStorageTypes(ctx, &database.ListDatabaseStorageTypesInput{ZoneID: zoneID})
networks, err := client.ListDatabaseNetworks(ctx, &database.ListDatabaseNetworksInput{ZoneID: zoneID})
page, err := client.ListDatabaseInstances(ctx, nil)
one, err := client.GetDatabaseInstance(ctx, &database.GetDatabaseInstanceInput{ID: id})
```

Each read sends one GET with the IAM bearer token. `ListDatabaseInstances`
returns one page (defaults: page 1, size 100) and never pages on its own.
A flavor's `monthlyCost` is a catalog estimate, not a quote.

## Create

```go
out, err := client.CreateDatabaseInstance(ctx, &database.CreateDatabaseInstanceInput{
    Name: "demo-db", ZoneID: zoneID, FlavorID: 211,
    StorageTypeID: "Gen2-NVMe2-IOPS3000", StorageSize: 20,
    VPCID: vpcID, SubnetID: subnetID,
    DatabaseName: "appdb", Username: "admin_user", Password: password,
})
```

Every choice comes from the catalogs above for one zone: `ZoneID` is a zone
UUID, `FlavorID` a flavor `id`, `StorageTypeID` a storage type token with
`StorageSize` in GB inside its bounds, and `SubnetID` a subnet nested under
`VPCID`. The engine is fixed to MySQL 8.0 and must appear in
`ListDatabaseEngineVersions`. The call validates every input, and the
catalogs, before it sends anything.

- No price is checked. The service offers no usable quote API, so the call
  sends the create with IAM Auto Payment (`user-type: IAM_USER`, `poc: false`)
  and debits credits. The CLI requires `--yes`.
- `Name` is 1 to 20 letters, digits, hyphens, or underscores, starting with a
  letter or digit; a longer name returns `vngcloud.ErrInvalidInput` before any
  request.
- A 4xx reply returns a `vngcloud.APIError` with fixed SDK text, never a
  provider message or an echoed value. When the body lists `fieldError`
  entries, the message names the matching input fields, such as
  `invalid input rejected by the API: Name, Password`; otherwise it reads
  `request rejected by the API`.
- The request is a single node with `publicAccess: false` and
  `backupAuto: false`, one logical database, and the master user.
- The request is sent once and never resent after any failure, including a
  401, 429, 5xx, redirect, or lost connection.
- Writes require SDK IAM-user login with the default IAM, database, and
  VServer (project discovery) endpoints in `hcm-3`; otherwise they return `vngcloud.ErrInvalidConfig` before any request.
- `Password` must be 8 to 32 characters of letters, digits, and `$ ^ _ < >`,
  starting with a letter and ending with a letter or digit; otherwise the call
  returns `vngcloud.ErrInvalidInput` naming `Password`, without the value, before
  any request. It is a `vngcloud.Secret`. It appears only in the create request
  body, never in outputs, errors, logs, or captures. Every database read used
  to confirm a write is rejected as malformed when any decoded string contains
  the password or the bearer token, including the one sent with the create.
- A returned instance ID must match the path-safe ID pattern. The reply is
  rejected when any decoded string, including the ID, contains the password or
  the bearer token, even in a JSON-escaped form. Anything else returns
  `database.ErrNotSettled`.
- The call then reads the instance at once and every 10 seconds, for at most
  30 minutes or the caller's earlier deadline. Reads never follow redirects,
  and a redirect returns `database.ErrNotSettled`. A 404 right after the
  create is treated as not yet visible and keeps the wait going. It succeeds
  only when the state is `ACTIVE`, with matching engine, version, flavor,
  backup off, and no public access.
- A state of `ERROR` or `FAILED` returns `database.ErrFailed`. An unknown
  state, a failed read, a timeout, a cancelled context, or an uncertain send
  returns `database.ErrNotSettled`. Both return an `Output` holding only the
  instance ID when known; the full instance is returned only once it is
  confirmed `ACTIVE`.
  Inspect inventory and billing before any further mutation.

## Delete

```go
out, err := client.DeleteDatabaseInstance(ctx, &database.DeleteDatabaseInstanceInput{ID: id})
```

The call reads the exact ID first and sends nothing, returning
`database.ErrDeleteRefused`, unless the instance is an `ACTIVE` MySQL instance
in the client's project, in a zone that `ListDatabaseZones` returns, not shared
(`owner` is not false), with no replication and no backups. The project is
proven from the complete instance list: every page is read, the list's
`projectId` must equal the client's project, and the ID must appear exactly
once. A partial or inconsistent page set refuses. A detail `projectId`, when
present, must match too. A different project or an unknown zone is refused. It then sends one delete with no final
backup and no backup purge, accepts any 2xx reply except one echoing a
credential or an explicit `success: false` row, and waits up to 30 minutes for
a 404 or a `DELETED` state; redirects are never followed. `Deleted` is true only after that. A timeout or unknown state
returns `database.ErrNotSettled` with the ID. Deleting does not promise a
refund.
