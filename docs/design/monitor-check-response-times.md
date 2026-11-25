# vMonitor check response times

Proposed. The manager probe below fixed the point formats.

Read server-averaged HTTP response time by probe location. Callers can join
by `LocationID` with [check alarm state and history][alarms]. This is the
fixed HTTP average slice of the held [location metrics design][metrics].

## SDK and CLI

`monitor.Client.ListCheckResponseTimes(ctx, *ListCheckResponseTimesInput)`
returns `(*ListCheckResponseTimesOutput, error)`. Register `monitor
list-check-response-times` as a Read with `--check-id`, `--start`, and
`--end`. It works on read-only profiles with the existing output and query
options. It adds no dependencies or changes to existing operations.

| Input | Type | Rule |
|-|-|-|
| `CheckID` | `string` | Required, validated with `core.CheckPathID` |
| `End` | `string` | Optional RFC 3339, defaults to the injected clock |
| `Start` | `string` | Optional RFC 3339, defaults to resolved End minus 24 hours |

Validate before requests. Require a positive range of at most 24 hours
at millisecond precision. This local limit matches the observed window.
Return the resolved request bounds as `Start, End time.Time` in UTC.

Output also carries `Items []CheckResponseTimeSeries`. Each series has
`LocationID string` and
`Points []CheckResponseTimePoint`. Decode `key` and `value` as JSON
strings. Project `key`, an RFC 3339 timestamp, to a UTC `Timestamp
time.Time`, and `value`, an unsigned decimal string, to a finite,
nonnegative `ValueMillis float64`. The console labels this metric in
milliseconds. Group `name` is the metric name, not a location label;
callers get location names from `get-check-alarm`.

## Requests and response checks

1. Reuse the bounded private uptime snapshot read from `checkalarms.go`
   (`GET vmonitor-uptime-manager/v1/uptimes/{checkID}`). Require a matching
   `id`, a nonempty `subtype`, a string `history_alarm`, and valid, unique
   location IDs in `locations`. A well-formed non-`HTTP` subtype returns
   `ErrInvalidInput`. An empty alarm reference on an HTTP check returns
   `ErrNotFound`. An unsafe alarm ID or malformed snapshot returns
   `InvalidResponse`. This read needs neither the alarm definition nor
   `test_frequency`.
2. Use the existing Monitor routing prefix (`alarmRoute`) and URL-encode
   the query on `GET vmonitor-api/api/v1/statistics/synthetics`.

   | Key | Value |
   |-|-|
   | `name` | `syntt.http.response_time` |
   | `dimensions` | `syntt_id:{checkID}` |
   | `statistics` | `avg` |
   | `start_time`, `end_time` | Resolved epoch milliseconds |
   | `group_by` | `location_id` |
   | `alarm` | The snapshot's `history_alarm` |

Require HTTP 200 and a bare array. Require exact spelling, presence, and
JSON types for fields used in the projection. Reject duplicate known keys,
missing or null arrays, missing or null required strings, malformed or
out-of-range points, duplicate bucket timestamps within a series, and
missing, duplicate, or foreign location IDs. Scope each location to the
snapshot. Accept points from 5 minutes before `Start` through `End`; the
server aligns the first bucket before `Start`. Reject other points.
Ignore unmodeled
fields. Sort series by location ID and points by timestamp. A valid empty
array yields an empty slice. Output contains only returned buckets.

Reuse the checks for sensitive monitor reads in `checkalarms.go`. Cap the
snapshot at 1 MiB and the statistics body at 16 MiB before decoding. Cap
results at 64 series and 100,000 points per call. Keep TLS, redirect,
cancellation, and normal bounded read retry behavior. Suppress routine
captures and withhold response text. Check every reused or projected
response string against all credentials sent across both GETs and all
attempts, including refresh. Check IDs before the next request and point
strings before conversion. Fail closed on a reflection or exhausted
credential tracking. Return nil Output on any failure with fixed
`InvalidResponse`, `ResponseLimitExceeded`, or existing status-derived
errors. Never truncate or log the response or query.

Out of scope are per-run results, probe execution, other check subtypes and
metrics, alarm joins in the SDK, interval controls, paging, and gap filling.
Server cadence, point limits, and empty-window and error behavior remain
unverified.

## Evidence and manager probe

The [routing bundle][routing], [chart bundle][chart], and [GreenNode MCP][mcp]
provide route evidence. The chart bundle supplies the exact query. An IAM
read in `hcm-3` over 24 hours returned 200 with two groups. Each group held
`dimensions.location_id`, `name`, and a `statistics` array of string
`key`/`value` pairs. Two additional group fields remain unmodeled.

A reviewed private classifier probe then read the account's only check,
an HTTP check with two locations, over 24 hours. It printed only classes:

- HTTP 200, two groups, no foreign location IDs.
- Every `key` parsed as RFC 3339, with no duplicate or unsorted buckets.
  Buckets were 5 minutes apart, with one 10-minute gap. One point per
  location fell within 5 minutes before `start_time`; none fell after
  `end_time`.
- Every `value` was an unsigned decimal, between 100 and 10,000. The
  console bundle prints this metric with " ms " and lists
  `response_time: "ms"` among its assertion units.
- Group `name` equalled `syntt.http.response_time` in both groups.

Unverified: scope separation between two checks sharing a location (the
account has one check), bucket width for windows other than 24 hours,
empty windows, and error envelopes.

[alarms]: monitor-quotas.md#check-state-and-history
[metrics]: monitor-console-reads.md#check-per-location-diagnostics
[live]: ../../instructions/live-data.md
[routing]: https://vmonitor.console.greennode.ai/245.db0f3f6e59fd44cf.js
[chart]: https://vmonitor.console.greennode.ai/478.527c60df14590d84.js
[mcp]: https://github.com/GreenNodeHub/greennode-mcp/blob/main/src/vmonitor-mcp-server/greennode/vmonitor_mcp_server/statistic_handler.py#L110-L152
