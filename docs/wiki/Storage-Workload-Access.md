# Storage Workload Access

Prepare vStorage access for an application on vServer or VKS with a dedicated
IAM service account, a bucket policy, and an attached S3 key. The application
operator delivers the key through the application's deployment process and
validates access from the workload's network.

This guide covers cloud-side setup and private handoff. It configures no VM or
cluster, runs no SSH or Kubernetes API commands, and deploys no secrets or
host entries. Running on vServer or VKS grants no bucket access by itself.
This SDK and CLI workflow provides no workload identity federation or
automatic credential source.

## Select the project, region, and bucket

Use an existing CLI profile authenticated as an authorized IAM user. The
provisioner needs the relevant IAM and vStorage rights; create S3 keys only
with an IAM user scoped to vStorage. A read-only profile refuses the setup
writes. See [CLI configuration](CLI.md#configure) and
[IAM guards](IAM.md#service-account-writes).

The examples select management region `hcm-3` and exact storage region
`HCM04`. For Hanoi, change both to `han-1` and `HAN02`. Storage management
accepts `Region` through `--cli-input-json`; global `--region` selects the
management region. Object clients require exact `HCM04` or `HAN02` through
their separate S3 settings.

Replace the synthetic profile and IDs with your selected resources. Keep
command output and metadata private: they contain account identifiers.

```sh
provisioner_profile="storage-provisioner"
vngcloud --profile "$provisioner_profile" --region hcm-3 \
  storage list-projects --cli-input-json '{"Region":"HCM04"}'
```

Select the returned vStorage project ID, which differs from a vServer project
ID. Supply it explicitly on every bucket, policy, and key operation; profile
and environment project defaults do not select a storage project. If none
exists, arrange a package separately under
[Storage Projects](Storage-Projects.md).

The following shell helper retains that selection for storage management
commands only:

```sh
storage_project_id="<storage-project-id>"
bucket="app-files"
storage_admin() {
  vngcloud --profile "$provisioner_profile" --region hcm-3 \
    --project-id "$storage_project_id" --output json storage "$@" \
    --cli-input-json '{"Region":"HCM04"}'
}

storage_admin list-buckets
storage_admin get-bucket --bucket "$bucket"
```

Inspect the selected bucket before proceeding. If a new bucket is required,
create it explicitly instead of running the `get-bucket` above:

```sh
storage_admin create-bucket --bucket "$bucket"
```

See [bucket creation](Storage.md#create-and-delete) for naming, encryption,
and ambiguous results. Bucket creation adds no public-read policy.

## Create or select the workload identity

Use a dedicated service account whose ownership and existing grants are
known. Check available IAM quota before creating one; do not exhaust quota
to discover its limit. Keep the authorized IAM-user provisioner for this
workflow; the SDK refuses service-account callers for guarded account and
policy-attachment changes. IAM management policies and bucket policies are
separate: attaching an IAM policy does not replace the bucket policy required
by an attached S3 key.

Choose a fresh private directory under an existing trusted parent. Keep it
outside the repository. Each secret-file path must be new, with nothing
already there, including a symlink. The CLI creates secret files with mode
0600 and refuses to overwrite them.

```sh
private_dir="/secure/path/workload-access"
umask 077
mkdir -m 700 "$private_dir"
vngcloud --profile "$provisioner_profile" iam create-service-account \
  --name app-files-workload \
  --secret-file "$private_dir/iam-client-secret"
```

Record `ServiceAccount.ID` from the output. For an existing dedicated account,
select its exact ID and inspect it instead of creating a replacement:

```sh
service_account_id="<service-account-id>"
vngcloud --profile "$provisioner_profile" iam get-service-account \
  --service-account-id "$service_account_id"
```

The IAM client ID and client secret authenticate management API calls. Keep
the management secret with the provisioner. An S3-only application needs the
separate S3 access key and secret key created below. Do not hand it the IAM
client secret. See [Authentication](Authentication.md#service-account).

A failed create may still have made the account or written its secret file.
Follow [IAM create recovery](IAM.md#service-account-writes) and reconcile the
returned ID with `get-service-account` or `list-service-accounts`. Do not
repeat creation blindly.

## Resolve the principal and preserve the bucket policy

```sh
storage_admin ensure-service-account-principal \
  --service-account-id "$service_account_id"
storage_admin get-bucket-policy --bucket "$bucket" \
  > "$private_dir/bucket-policy-before.json"
```

`ensure-service-account-principal` is a write even though its HTTP method is
GET. Copy the returned `PrincipalARN` exactly into the policy. Do not derive
it from the account ID or name. The server accepts an incorrect principal
without granting access, so policy acceptance alone proves nothing.

The read output is an envelope with a `Policy` field containing JSON text;
`""` means no policy. Decode that text before editing. Create a separate
`$private_dir/bucket-policy.json` containing the complete policy document,
not the read-output envelope. Preserve every unrelated statement and grant.

Start from the [object-access template](Storage-Bucket-Policy.md#template)
when the bucket has no policy, or merge its statements into the existing
document. Fill in the exact principal and bucket. The template grants GET,
PUT, and DELETE of objects, bucket listing and location reads, plus multipart
upload listing, part listing, and abort. It grants more than simple upload
and download. Choose the application's required operations explicitly;
avoid `s3:*`, which includes bucket deletion and policy changes.

A narrower policy needs its own access validation. The recorded tests use
broader grants and do not prove every minimal-action combination or prefix
condition. See [object permissions](Storage-Objects.md#credentials-and-regions).

Coordinate with every policy editor before reading, editing, and applying.
A put replaces the entire policy; there is no merge or concurrent-edit
protection. Re-read before the put and reconcile any changes. Preserve
unrelated grants throughout setup, rotation, and retirement.

```sh
storage_admin put-bucket-policy --bucket "$bucket" \
  --policy "file://$private_dir/bucket-policy.json"
storage_admin get-bucket-policy --bucket "$bucket"
```

Compare decoded documents because the server can change whitespace and key
order. A preserved public principal requires `--yes` under the existing
[public-principal guard](Storage-Bucket-Policy.md#public-read); review that
exposure explicitly before applying. This workflow requires no public grant.

## Create the attached S3 key and hand it off privately

```sh
storage_admin list-s3-keys
storage_admin create-s3-key --service-account-id "$service_account_id" \
  --secret-file "$private_dir/s3.credentials"
storage_admin list-s3-keys
```

The create command attaches the new key before writing its secret file.
Record its `UserKeyID`, which is distinct from the S3 access key. Confirm its
attachment with `list-s3-keys`: the list's `SubUserID` must match the resolved
principal's `SubUserID`. The create output's `SubUserID` remains empty because
it comes from the pre-attachment response.

The new mode-0600 file uses the AWS shared credentials format with a
`[default]` section, access key, and secret key. It contains no region or
endpoint. The secret never appears in normal command output and cannot be
retrieved later. Do not print the file or place its contents in arguments,
logs, tickets, source control, or public documentation.

If attachment or file publication fails, the CLI attempts to delete the new
key and names its ID if cleanup fails. Follow the
[key-create contract](https://github.com/dannyota/vngcloud/blob/master/docs/design/storage-cli.md#create-s3-key).
An uncertain create may leave a key whose secret is lost. Reconcile the
before/after inventory and known IDs with other key creators; never delete
every unfamiliar key or retry the create blindly.

Hand off the credential file privately through the operator's existing
deployment process. Keep the following metadata private too:

- Storage project ID, exact S3 region, and bucket name.
- Permitted operations and any independently validated prefix restrictions.
- Service account ID, exact principal, and `UserKeyID` for future rotation.
- Regional HTTPS endpoint and the selected network path.
- Credential-file location and application ownership, without secret values.

For Go applications, use `storage.NewObjectClient` with credentials loaded
by application code; see [Storage Objects](Storage-Objects.md).
The application operator must confirm deployment and validate intended reads,
writes, and denied operations from the actual VM or pod network. Successful
management reads or local object calls do not prove workload connectivity.

## Choose the network path

Public HTTPS access uses the regional endpoints:

| S3 region | Endpoint |
|-|-|
| `HCM04` | `https://hcm04.vstorage.vngcloud.vn` |
| `HAN02` | `https://han02.vstorage.vngcloud.vn` |

The workload needs an existing outbound route, DNS resolution, and relevant
network permissions. Public NAT is an explicit option when the network needs
it, not a mandatory prerequisite or an implicit step. This guide changes no
VPC, subnet, route, security group, or cluster. See [Network](Network.md).

The SDK and CLI support private endpoint inventory only. Inspect a selected
vServer project separately from the storage project:

```sh
vserver_project_id="<vserver-project-id>"
vpc_id="<vpc-id>"
endpoint_id="<endpoint-id>"
vngcloud --profile "$provisioner_profile" --region hcm-3 \
  --project-id "$vserver_project_id" network list-endpoints --vpc-id "$vpc_id"
vngcloud --profile "$provisioner_profile" --region hcm-3 \
  --project-id "$vserver_project_id" network get-endpoint \
  --endpoint-id "$endpoint_id"
```

Endpoint presence or status does not prove private S3 connectivity, replace
bucket authorization, or disable public access. No verified bucket policy
condition restricts access to one private endpoint. Do not delete shared or
VKS-managed endpoints as workload cleanup.

The published endpoint guide lists the HCM04 hostname and a service-domain
override with Private DNS. That product documentation does not establish live
DNS, TLS, signing, routing, or VKS compatibility. The native object client
pins the regional HTTPS hosts and exposes no endpoint override. Native
private access remains unverified under the
[private data-plane gate](https://github.com/dannyota/vngcloud/blob/master/docs/design/network-endpoints-api.md#private-data-plane-gate).
Keep TLS verification enabled; do not bypass this gap with host entries,
arbitrary URLs, or redirects. Object operations retain their
[regional limits](Storage-Objects.md#cli-file-transfers), including the
HAN02 create-only PUT restriction.

## Rotate by overlapping attached keys

1. Identify the old `UserKeyID` and confirm capacity for one additional key.
   The recorded ten-key limit is account-specific evidence. Do not delete the
   working key to make space without an agreed maintenance plan.
2. Create another attached key for the same service account at a fresh path:

   ```sh
   storage_admin list-s3-keys
   storage_admin create-s3-key --service-account-id "$service_account_id" \
     --secret-file "$private_dir/s3-next.credentials"
   storage_admin list-s3-keys
   ```

3. Hand off the new file privately. Keep both keys attached until the
   application operator confirms deployment and workload access validation.
4. Delete only the identified old key while it is still attached:

   ```sh
   old_key_id="<old-user-key-id>"
   storage_admin delete-s3-key --user-key-id "$old_key_id" --yes
   storage_admin list-s3-keys
   ```

5. Confirm the old ID is absent. The operator removes obsolete secret copies
   through the deployment process. Errors or missing output require
   reconciliation; do not assume completion or delete unrelated keys.

Never detach as revocation or rotation. Detach restores the key's creator
rights across the project. Read [S3 key errors](Storage.md#s3-keys) before
recovering an ambiguous write.

IAM management-secret reset is separate. `iam reset-service-account-secret`
requires the account ID, `--yes`, and a fresh `--secret-file`. It invalidates
the old client secret immediately. A previously issued token survived reset
in the [recorded test](https://github.com/dannyota/vngcloud/blob/master/docs/design/service-account-auth-api.md#recorded-live-results).
Reset provides no issued-token revocation guarantee and does not rotate S3
keys.

## Retire the workload identity

Coordinate retirement with the application operator and all policy editors.
Retire only a dedicated identity whose consumers are known:

1. Remove its principal from every bucket policy that grants it access,
   preserving other principals and statements. Apply the complete remaining
   document. If no statements remain, use
   `storage_admin delete-bucket-policy --bucket "$bucket"` instead of an empty
   put. Read back each affected policy.
2. Identify all its S3 keys and delete them while attached with
   `storage_admin delete-s3-key --user-key-id "<user-key-id>" --yes`.
   Confirm their absence in each relevant storage project and region.
3. Inspect remaining management IAM policy attachments. Read every page and
   remove each known attachment before deleting the identity:

   ```sh
   vngcloud --profile "$provisioner_profile" \
     iam list-service-account-policies \
     --service-account-id "$service_account_id" --page 0 --size 100
   policy_id="<attached-policy-id>"
   vngcloud --profile "$provisioner_profile" \
     iam detach-service-account-policy --policy-id "$policy_id" \
     --service-account-id "$service_account_id" --yes
   ```

   Repeat the detach for each attachment, then delete the account:

   ```sh
   vngcloud --profile "$provisioner_profile" iam delete-service-account \
     --service-account-id "$service_account_id" --yes
   ```

   Follow [IAM guards](IAM.md#service-account-writes); do not bypass a refused
   change or delete a policy used by another identity.
4. The application operator removes the deployed credentials and obsolete
   private copies through the deployment process.

Deleting the service account does not remove bucket grants or attached S3
keys. The storage principal depends on the account's name; reusing that name
can inherit forgotten grants. Remove every grant before deleting the account.
Never detach its keys as cleanup.

Attached keys still list project buckets, create otherwise unusable buckets,
and reveal whether object names exist outside granted buckets. This is not
complete project isolation; see [policy rules](Storage-Bucket-Policy.md#rules).
A 403 requires investigation of credentials, policies, and the network path,
not broader wildcard grants or bucket publication.
