# Developer scripts

Script tests use system `python3` and require Python 3.11 or newer with
`tomllib`. Workflow checks also require preinstalled PyYAML:

```sh
make scripts-test
make ci-workflows-check
```

CI uses its preinstalled Python packages and installs none for these suites.
Workflow checks report a fixed error when dependencies are missing and
refuse to fall back to another interpreter.

`make scripts-test` also compiles the live-write test helpers and runs only
the offline consent, privacy, and cleanup tests with the race detector.
The runner uses a temporary module root and home, public storage fixtures,
and the storage harness source needed by its checks. It passes no cloud
credentials or live-write gates to the test process. It never selects a
live write test.

## Browser login

Add `.playwright-mcp/` to `.git/info/exclude` once per clone.

The helper checks a path inside `.playwright-mcp/` with `git check-ignore -q`
before reading credentials or creating files. A failed check stops the
helper with fixed exclusion guidance.

Start `make browser-creds`, or `make browser-creds PROFILE=<name>` for a CLI
profile. The helper prints a private `browser-login.js` path inside the
repository's `.playwright-mcp/`, where Playwright MCP can read it. Pass that
path as `filename` to `browser_run_code_unsafe`. The tracked
`scripts/browser-login.js` is a template and refuses direct execution.

The helper creates `.playwright-mcp/` if missing, refuses a symlink or a
directory owned by another user, and sets its mode to 0700. Each run uses a
separate mode 0700 directory there. The temporary script contains a random
capability for this helper run and uses mode 0600. The browser sends the
capability in the Authorization header to the fixed loopback helper,
without following redirects. Missing, wrong, and stale capabilities do not
consume either endpoint. Each authenticated endpoint serves once. The
helper deletes its per-run directory and script on normal completion or
timeout.

Playwright MCP echoes the script source after a run, so the embedded
capability is single-use. The first `/creds` request spends it, before any
echo; afterwards it gets 403 on every path. That response carries a fresh
`next` capability for `/totp`. The script keeps it in memory only, and the
helper never writes it to disk.

Do not read or copy the temporary script into logs or tool output. Process
termination can leave it behind; remove that private directory after the
helper stops. The helper checks a three-minute deadline between requests.

## Live write selection

Live writes require owner approval for each run. `make live-write` selects
only the billing test and requires both `VNGCLOUD_LIVE_WRITE=1` and
`VNGCLOUD_LIVE_BILLING=1`. Other live write tests require an anchored test
selection and their own operation gate.

These tests require these operation gates in addition to the shared gate:

| Test suffix after `TestLiveWrite` | Gate suffix after `VNGCLOUD_LIVE_` |
| --- | --- |
| Empty, the billing test | `BILLING` |
| `GLB` | `GLB` |
| `Monitor` | `MONITOR` |
| `DNS` | `DNS` |
| `MonitorChannel` | `MONITOR_CHANNEL` |
| `MonitorCheckNotifications` | `MONITOR_CHECK_NOTIFICATIONS` |
| `MonitorChannelOTP` | `MONITOR_CHANNEL_OTP` |
| `StorageBucket` | `STORAGE_BUCKET` |
| `StorageS3Key` | `STORAGE_S3_KEY` |
| `StorageServiceAccountKey` | `STORAGE_SERVICE_ACCOUNT_KEY` |
| `StorageBucketPolicy` | `STORAGE_BUCKET_POLICY` |
| `StorageBucketSettings` | `STORAGE_BUCKET_SETTINGS` |
| `ContainerRegistryRepository` | `VCR_REPOSITORY` |
| `ContainerRegistryUser` | `VCR_USER` |

`TestLiveWriteGLB` also requires `VNGCLOUD_LIVE_GLB_PACKAGE_ID` naming an
approved enabled package. The manager selects the package; the test does not
compare prices.

`VNGCLOUD_LIVE_VCR` does not enable either registry test. A resource ID, URL,
quota, or shared gate grants no write consent.

## Tool freshness

`scripts/tools-outdated.sh` fails on every outdated pin and on failed
inventories. Optional `GH_TOKEN` authentication reaches curl through stdin;
the script disables tracing before reading the token and removes the token
from child environments. Tokens never enter command arguments.
