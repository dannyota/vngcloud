# vStorage access from application workloads

Status: Implemented as
[workload access guidance](../wiki/Storage-Workload-Access.md) using existing
SDK and CLI contracts. The evidence-gated
[endpoint lifecycle](network-endpoints.md) remains proposed. New API contracts
require owner approval and independent design and plan review.
The guide adds no public runtime API and enters no workload environment.

Applications on vServer and VKS can use the existing S3 key and bucket-policy
surface. The missing platform API surface is private endpoint provisioning,
subject to the [endpoint evidence gates](network-endpoints-api.md). Application
deployment and credential delivery remain the application operator's work.

## Boundary and existing support

The operator provisions platform access with the management SDK or CLI. The
application uses an S3 access key and secret for object operations. Running on
vServer or VKS does not itself grant bucket access. This design claims no
instance identity, pod identity, metadata credential source, or federation.

| Need | Existing surface | Scope |
|-|-|-|
| Workload principal | IAM service-account CRUD | Identity lifecycle |
| Management automation | Service-account login, IAM policies | Control APIs |
| Bucket principal | `storage.EnsureServiceAccountPrincipal` | Explicit write |
| Bucket rights | Bucket policy get, put, delete | Whole policy document |
| S3 credentials | S3 key create, attach, list, delete | S3 data plane |
| Object access | `storage.ObjectClient` | Separate S3 client |
| Network controls | Security-group and rule operations | Explicit resources |
| Private endpoint inventory | Endpoint list, get, tags | Reads only |

[Service-account authentication](service-account-auth.md) owns management
login. [Storage keys](storage-keys.md) owns S3 restriction and principal
semantics. IAM policy attachment does not replace the bucket policy required
by an attached console S3 key. No new cross-service binding resource is needed.

Exclude SSH, guest commands, host-file changes, cluster access, Kubernetes
Secret creation, manifests, application restarts, and credential injection.
Exclude automatic VPC creation, security-group changes, endpoint changes, and
public bucket exposure from the access workflow. Operators select any required
platform network changes as separate explicit operations.

## Setup and handoff

The user guide uses existing commands in this order:

1. Select an existing vStorage project and region, then select or create the
   bucket. The vStorage project ID differs from a vServer project ID. The
   explicit CLI `--project-id` selects the storage project; profile defaults
   do not supply it. Preserve the [region rules](storage-cli.md#commands).
2. Create or select the IAM service account. Creation uses a private
   `--secret-file` for its management client secret. Keep that secret with the
   management operator; an S3-only application does not need it.
3. Call `storage ensure-service-account-principal` for the selected storage
   project and service account. Copy its returned `PrincipalARN` exactly.
4. Read the bucket policy. Preserve unrelated statements and add the desired
   principal using the established
   [object-access template](storage-keys.md#template). Submit the complete
   policy through `storage put-bucket-policy --policy file://policy.json`.
   A put replaces the entire document; concurrent editors must coordinate.
5. Run `storage create-s3-key --service-account-id <sa>` with the storage
   project and a fresh `--secret-file` path. The existing command attaches the
   key before writing its secret to disk. Preserve its existing failure and
   cleanup behavior from [the CLI contract](storage-cli.md#create-s3-key).
6. Hand the operator the private S3 credentials file and separately record
   region, bucket, allowed operations, key ID, principal, and endpoint choice.
   Deliver these through the operator's existing deployment process. Do not
   read, print, copy into public documentation, or upload the secret here.
7. The application operator checks the intended read and write behavior from
   the workload's own network. A management read or local object test cannot
   prove vServer or pod connectivity. No SDK command enters that environment.

The guide links the template rather than inventing an IAM wildcard grant.
The template grants more than PUT and GET, including deletion and listing.
Explain those grants and let the operator narrow them to the application's
needs. A narrower policy requires its own access validation; existing tests
do not prove every minimal permission combination or prefix condition.

The handoff is documentation and existing command output, not a new export
format or command. Credentials use the existing private AWS shared credentials
file format. Metadata may contain account identifiers and stays private too.
For Go applications, reference `storage.NewObjectClient` with credentials
loaded by application code; never put secret literals in examples.

## Network choice

The regional S3 endpoints supported by the native object client are
`https://hcm04.vstorage.vngcloud.vn` and
`https://han02.vstorage.vngcloud.vn`. The client requires exact `HCM04` or
`HAN02` S3 region names. [Object transfers](storage-objects.md) owns regional
capability limits, including the HAN02 create-only PUT gate.

Public HTTPS access requires the application's existing outbound route, DNS,
and relevant network permissions. It requires no endpoint lifecycle call.
Do not create Public NAT implicitly or claim it is mandatory for all vServer
and VKS applications. Private endpoint access is a separate operator choice.

Private endpoints change network reachability, not bucket authorization. An
endpoint does not replace the S3 key or bucket policy, and its presence does
not prove public access is disabled. No source establishes a bucket policy
condition that restricts this service to one private endpoint.

The native object client pins regional hosts and has no endpoint override.
The published endpoint guide lists `hcm04.vstorage.vngcloud.vn` for HCM04
and describes service-domain override with Private DNS. This establishes a
documented hostname, not live DNS, TLS, signing, routing, or VKS behavior.
Bounded IAM-user catalog reads in Hanoi also map the selected vStorage
service to the native/public HAN02 hostname. That catalog observation does
not establish private connectivity or service-account authorization.
Do not claim native private access until the
[compatibility evidence](network-endpoints-api.md#private-data-plane-gate)
is complete. Do not introduce an arbitrary URL, redirect, hosts-file helper,
or disabled TLS verification to bypass this gap.

## Rotation and retirement

Rotate an application's S3 key by overlap:

1. Confirm capacity for one additional key and identify the old `UserKeyID`.
   The [recorded limit](storage-api.md#cost) is account-specific evidence;
   do not delete the working key to make space without a maintenance plan.
2. Create a new S3 key attached to the same service account, writing to a new
   private file. Keep the old attached key working during the handoff.
3. The application operator deploys the new key and verifies the intended
   access. Retain both keys until the operator confirms the switch.
4. Delete the old key directly while it is attached, using
   `storage delete-s3-key --user-key-id <old-id> --yes`. Read the list to
   confirm its absence. A missing output or error requires reconciliation;
   do not assume deletion or remove unrelated keys.
5. The operator removes obsolete secret copies through the deployment process.

Never detach a key as a revocation or rotation step. Detach restores its
creator's project-wide rights. An ambiguous key creation needs manual
reconciliation against known IDs, not a sweep deleting every unfamiliar key.

IAM client-secret reset is separate from S3 key rotation. Existing
`iam reset-service-account-secret` uses `--yes` and a fresh `--secret-file`;
the old client secret stops authenticating immediately. A previously issued
token survived reset in the
[recorded test](service-account-auth-api.md#recorded-live-results).
Reset is neither a token revocation guarantee nor an S3 key rotation method.

To retire a workload identity, remove its grants from all bucket policies and
delete its S3 keys while attached, then remove remaining management policy
attachments and delete the service account through existing guards. Preserve
other principals' policy statements. If no statements remain, use policy
delete instead of an empty put. The principal depends on the account name;
reusing that name can inherit forgotten grants. Do not delete a shared account
or an endpoint still used by another workload.

## Limits, errors, and checks

The [storage key contract](storage-keys.md#wiki-rules) records unavoidable
scope limits: attached keys can list project buckets, create otherwise
unusable buckets, and infer object existence outside their granted bucket.
Do not describe this as complete project isolation.

Existing write/read-only classification, `--yes` gates, exit codes, retry
rules, ambiguous-write errors, and private-file handling remain unchanged.
Authentication, policy propagation, routing, DNS, TLS, and application rollout
are separate failure domains. A 403 is not a reason to grant `*`, detach a
key, publish a bucket, or switch endpoints automatically.

The [workload access page](../wiki/Storage-Workload-Access.md) and its
navigation links document these existing contracts. They introduce no SDK
methods, CLI flags, dependencies, runtime probes, or generated commands.
Review each command against current source and each policy claim against its
authority. Check links and 80-column wrapping, run `make wiki-preview`, and
run the required documentation checks in
[testing](../../instructions/testing.md#required-checks).

## Alternatives and owner decisions

Recommend composing existing primitives and documenting rotation and handoff.
This gives application teams useful support without a second ownership model
for accounts, policies, keys, or deployment secrets.

An all-in-one binding command would need rollback and partial-success
contracts across IAM, storage, network, and local files. A credential exporter
would add a second secret delivery surface. Neither is required by the stated
workflow; defer both.

The owner must approve the proposed endpoint contract separately. Endpoint
API evidence, paid-run authority, and private data-plane compatibility remain
independent gates. The existing-contract guide remains usable while those
are open.
