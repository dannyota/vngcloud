# Backup Center

`danny.vn/vngcloud/backup` reads Backup Center backends and policies in
`hcm-3`. The client shares the configuration's IAM credential provider or
static bearer token with other SDK clients.

```go
import "danny.vn/vngcloud/backup"

client := backup.New(cfg)
backends, err := client.ListBackends(ctx, nil)
policies, err := client.ListPolicies(ctx, &backup.ListPoliciesInput{
    Page: 1,
    Size: 200,
})
```

`ListBackends` sends no query parameters. Its output contains `Items`,
`Page *int`, `PageSize *int`, `TotalPage int`, and `TotalItem int`. Null
backend page metadata stays nil. Each backend has `ID` and `Name`.

`ListPolicies` reads one page and accepts only `Page` and `Size`. Nil input
or zero values select page 1 and size 200. Negative values return
`ErrInvalidInput` before authentication or a request. The output contains
`Items`, `Page`, `PageSize`, `TotalPage`, and `TotalItem`, with integer
metadata. Call again with the next page to read more results.

Each policy has `ID`, `BackendID`, `ProjectID`, `Product`, `Name`,
`IsDefault`, `BackupInstanceCount`, `Config`, `CreatedAt`, and `UpdatedAt`.
`Config` contains `Hour`, `Minute`, `TimeZone`, `IsProtectedServer`,
`HourlyEnabled`, `DailyEnabled`, `WeeklyEnabled`, `MonthlyEnabled`, and
`DailyConfig`. Daily settings use optional `Retention`, `BackupType`, and
`IncrementalQuantity` pointers. Absent or null daily configuration stays
nil; an empty object has nil members. Timestamps and timezones stay as
received.

Hourly, weekly, and monthly configuration details are omitted. Their enable
flags do not establish those schedule details. Account `userId`,
`statusSendEmail`, credentials, and unknown fields are also omitted.

Both reads use token scope. Configuration `ProjectID` does not scope them;
neither method sends a project ID or discovers projects. Returned project
IDs identify policies and do not prove filtering or cross-account access.

`EndpointOverrides.BackupCenter` replaces the verified endpoint
`https://hcm-3.api.vngcloud.vn/vbackup-gateway/`. Every other region returns
`ErrInvalidConfig` before authentication or network access, even with an
endpoint override. Backup Center remains separate from vServer volume
snapshots in [Volume](Volume.md).

Response capture hooks receive no Backup Center bodies. Errors and debug
logs withhold response bodies and server error text, including server error
codes. Errors preserve HTTP status, operation, standard error classes, and
GET retry behavior. A 401, 403, or 404 fails rather than returning an empty
list or activating the service. See [Errors](Errors.md).
