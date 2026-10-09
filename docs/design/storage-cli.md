# vStorage: CLI

The CLI commands for [vStorage](storage.md): the command table, the
per-bucket key setup, and `create-s3-key`. The SDK surface, errors, and
releases are in that design and [vStorage: keys](storage-keys.md).

## Commands

`svc_storage.go` registers the table, the service account key commands
included. Flags follow the Input fields; `Rules` comes through
`--cli-input-json`, and `Policy` accepts `file://` like `--cli-input-json`.

| Command | Kind | Needs `--yes` |
|-|-|-|
| `storage list-regions`, `list-projects`, `list-buckets`, `get-bucket` | Read | No |
| `storage create-bucket` | Write | No |
| `storage delete-bucket` | Write, destructive | Yes |
| `storage list-s3-keys` | Read | No |
| `storage create-s3-key` | Write | No |
| `storage delete-s3-key` | Write, destructive | Yes |
| `storage attach-s3-key`, `detach-s3-key` | Write | Yes |
| `storage ensure-service-account-principal` | Write | No |
| `storage get-bucket-policy` | Read | No |
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
- Attach and detach can be undone by one more command, so ADR 0002 rule 6
  does not require `--yes`. They need it anyway: a detach widens a key to
  its creator's rights on the whole project at once, and an attach cuts
  off a running app's bucket access.
- `ensure-service-account-principal` is a write, so a read-only profile
  refuses it. It prints `SubUserID` and `PrincipalARN`.
- An attach or detach that the server refuses prints code `114` and the
  server's message, which says what state the key is in
  ([Service account keys](storage-keys.md#service-account-keys)).

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
   its own short timeout. Success or envelope code 114, which the server
   gives for a key already deleted, counts as done.
5. Stdout gets the key without the secret: `SecretKey` prints as
   `[redacted]`, and a `SecretFile` field names the path.

With `--service-account-id <sa>`, the CLI calls `AttachS3Key` after the
create and before step 2, so the secret reaches disk only for a key
already restricted to that service account. If the attach fails for any
reason, an ambiguous 5xx included, the CLI writes no file, deletes the key
with the same cleanup as step 4, and exits 1 with the attach's code. If
that delete fails, the error names the `UserKeyID`. Stdout is as in step
5 plus a `ServiceAccountID` field. Its `SubUserID` is empty, since it comes
from the create response; `list-s3-keys` shows the attach. The flag needs
no `--yes`: the key is new, so the attach cuts off nothing.

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
vngcloud storage ensure-service-account-principal --project-id <p> \
  --service-account-id <sa>
vngcloud storage put-bucket-policy --project-id <p> --bucket <b> \
  --policy file://policy.json
vngcloud storage create-s3-key --project-id <p> \
  --service-account-id <sa> --secret-file <key-path>
```

The order matters:

- The principal exists before the policy names it.
- The policy exists before the key, since an attached key has no rights in
  the bucket until a policy names its principal.
- The key is attached before its secret is written; a key is unrestricted
  until its attach. To restrict a key made earlier, run
  `storage attach-s3-key --project-id <p> --user-key-id <k>
  --service-account-id <sa> --yes`.

An attached key can still list the project's buckets and create buckets.
`put-bucket-policy` ships in S5, after the live check that the policy
scopes the key to its bucket
([key live checks](storage-keys.md#live-checks)). Until then an attached
key reaches no bucket, and `create-s3-key` without `--service-account-id`
makes a project-wide key with the IAM user's rights on every bucket of the
project. The wiki says so and tells the reader to make keys only with an
IAM user scoped to vStorage.

Before deleting a service account, remove its principal from every bucket
policy: a new service account with the same name may get the same
principal ([Principal](storage-keys.md#principal)).
