# vStorage: CLI

The CLI commands for [vStorage](storage.md): the command table, the
per-bucket key setup, and `create-s3-key`. The SDK surface, errors, and
releases are in that design.

## Commands

`svc_storage.go` registers the table, and `svc_iam.go` registers the
service account key commands once S4 names them. Flags follow the Input
fields; `Rules` comes through `--cli-input-json`, and `Policy` accepts
`file://` like `--cli-input-json`.

| Command | Kind | Needs `--yes` |
|-|-|-|
| `storage list-regions`, `list-projects`, `list-buckets`, `get-bucket` | Read | No |
| `storage create-bucket` | Write | No |
| `storage delete-bucket` | Write, destructive | Yes |
| `storage list-s3-keys` | Read | No |
| `storage create-s3-key` | Write | No |
| `storage delete-s3-key` | Write, destructive | Yes |
| `storage get-bucket-policy`, `get-service-account-principal` | Read | No |
| `storage put-bucket-policy`, `delete-bucket-policy` | Write | No |
| `storage get-bucket-versioning`, `get-bucket-cors`, `get-bucket-public-access` | Read | No |
| `storage put-bucket-versioning`, `put-bucket-cors`, `delete-bucket-cors` | Write | No |
| `storage put-bucket-public-access` | Write | Yes when `--public` |

- A [read-only](cli.md#read-only) profile refuses every write with exit 2
  before any request.
- A deleted bucket or key cannot be restored by one more command (ADR 0002
  rule 6); a deleted policy or CORS set can, by a put.
- Making a bucket public needs `--yes`: exposure cannot be undone, because
  anyone may copy the objects while it lasts.
- The service account key commands wait on the
  [S4 probes](storage.md#live-checks-before-code).

The global `--project-id` flag supplies a vStorage `ProjectID`. Only the flag
counts: the environment and profile project is the vServer project and is
never used here. The flag overrides a `ProjectID` in `--cli-input-json`, and a
missing one exits 2 before any request.

## create-s3-key

`storage create-s3-key --project-id <p>` needs `--secret-file <path>` and
cannot print the secret:

1. Before any request, the parent directory must exist and nothing may exist
   at the path, symlinks included; otherwise it exits 2.
2. After the create, it opens the path with `O_CREATE|O_EXCL|O_NOFOLLOW`
   and mode 0600, writes, syncs, and closes. The file is an AWS shared
   credentials file, which rclone and the AWS CLI read through
   `AWS_SHARED_CREDENTIALS_FILE`:

   ```ini
   [default]
   aws_access_key_id = <access key>
   aws_secret_access_key = <secret key>
   ```

3. If the write fails, the CLI removes any partial file, deletes the new key
   (the secret is lost anyway), and exits 1. If that delete fails, the
   error names the `UserKeyID` so a person can delete it.
4. If the create returns `storage.ErrNoSecret`, the CLI writes no file,
   deletes the key, which is unusable, and exits 1 with `SecretFileFailed`.
   The cleanup delete runs on a context detached from the command's, with
   its own short timeout; a `NotFound` from it counts as done.
5. Stdout gets the key without the secret: `SecretKey` prints as
   `[redacted]`, and a `SecretFile` field names the path.

The file holds no region or endpoint. The wiki shows the rest of the
client setup: endpoint `https://hcm04.vstorage.vngcloud.vn` and region
`HCM04` for that region's projects.

## Per-bucket key

The target setup for aboutme, per bucket. `policy.json` allows the
principal `s3:*` on `arn:aws:s3:::<b>` and `arn:aws:s3:::<b>/*`; the wiki
gives the template.

```sh
vngcloud storage create-bucket --project-id <p> --bucket <b>
vngcloud iam create-service-account --name <b>-app --secret-file <path>
vngcloud storage get-service-account-principal --project-id <p> \
  --service-account-id <sa>
vngcloud storage put-bucket-policy --project-id <p> --bucket <b> \
  --policy file://policy.json
```

The last step, a key that acts as the service account, waits on the
[S4 probes](storage.md#live-checks-before-code), and so does whether the
principal exists before that key. The design names the command once the
probes answer.

Until then, `storage create-s3-key` makes a project-wide key with the IAM
user's rights on every bucket of the project. The wiki says so and tells
the reader to make keys only with an IAM user scoped to vStorage.
