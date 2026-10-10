package cli

// storage's doc notes, keyed "storage <op-name>" in docOpNotes
// (gendocs_notes.go).

const storageRegionNote = "`Region` is a vStorage region name such as `HCM04`, set only through " +
	"`--cli-input-json`. Empty maps the global `--region`: `hcm-3` to `HCM04` and `han-1` to `HAN02`."

const storageListProjectsNote = storageRegionNote + " A vStorage project is a paid package, so the " +
	"list is empty until one is bought in the console. Its `ID` is the value for `--project-id` in the " +
	"bucket commands."

const storageProjectPricingNote = storageRegionNote + " Prices are VND per month for one month. " +
	"Buying a project is not available yet."

const storageListProjectTypesNote = storageProjectPricingNote + " Shows each type's monthly price " +
	"at the region's minimum quota, plus the minimum and maximum quota in GB."

const storageQuoteCreateProjectNote = storageProjectPricingNote + " " +
	"`quote-create-project --type Gold --quota-gb 30` prices a package without ordering. " +
	"The quota must be within the region's limits. The type is the catalog `Name`, such as " +
	"`Gold` or `Instant-Archive-2`. `Name`, `MaxPrice`, and `NoWait` apply only to purchase " +
	"and are ignored in JSON input. Example: " +
	"`vngcloud storage quote-create-project --region hcm-3 --type Gold --quota-gb 30`."

const storageProjectIDNote = "`--project-id` is the global flag and takes a vStorage project ID from " +
	"list-projects, not the account's vServer project: the environment variable and the profile " +
	"setting do not fill it, and the command exits 2 without the flag. "

const storageListBucketsNote = storageProjectIDNote + storageRegionNote + " The API returns every bucket in " +
	"one call; a response with more fails instead of printing a partial list."

const storageGetBucketNote = storageProjectIDNote + storageRegionNote

const storageListRegionsNote = "Lists the regions vStorage serves, with their S3 hosts. Needs no project."

const storageCreateBucketNote = storageProjectIDNote + storageRegionNote + " Sends the console body for a " +
	"bucket without object lock, then reads the bucket back and prints it. A name must be lowercase letters, " +
	"digits, and hyphens; anything else the server refuses with error code 112. If the create fails in a way " +
	"that may have reached the server, the message says the bucket may exist: check with get-bucket before " +
	"running it again."

const storageDeleteBucketNote = storageProjectIDNote + storageRegionNote + " Needs `--yes`: a deleted " +
	"bucket cannot be restored. It reads the bucket first and refuses one that holds objects with error code " +
	"`BucketNotEmpty` (exit 1), sending nothing; empty the bucket with an S3 client, then run it again. " +
	"A null object count is refused the same way. There is no force option. The server deletes " +
	"asynchronously, so the command then reads the bucket every second for up to 30 seconds and returns when " +
	"it is gone; `--no-wait` skips the wait and returns once the server accepts the delete. If the bucket is " +
	"still readable when the wait ends, the error code is `NotSettled` (exit 1): the delete was accepted, so " +
	"do not repeat it, and check with get-bucket later."

const storageKeyRightsNote = "A key not attached to a service account has the rights of the IAM user that " +
	"made it on every bucket of the project. Make keys only with an IAM user scoped to vStorage. A project " +
	"holds at most ten keys."

const storageServiceAccountIDNote = "`--service-account-id` is the IAM service account ID as iam " +
	"list-service-accounts prints it, without the `sa-` prefix."

const storageAttachedKeyNote = "An attached key still lists the project's buckets and creates buckets " +
	"whatever the bucket policies say."

const storageListS3KeysNote = storageProjectIDNote + storageRegionNote + " Lists the project's keys by " +
	"`UserKeyID` and `AccessKey`. No secret is listed or printed. " + storageKeyRightsNote

const storageCreateS3KeyNote = storageProjectIDNote + storageRegionNote + " Needs `--secret-file <path>`: " +
	"the server returns the secret once, and the command writes it only to that file, at mode 0600, in the " +
	"AWS shared credentials format (`[default]` with `aws_access_key_id` and `aws_secret_access_key`), which " +
	"rclone and the AWS CLI read through `AWS_SHARED_CREDENTIALS_FILE`. The file holds no region or endpoint. " +
	"The path must not exist, symlink included, and its directory must exist; both are checked before any " +
	"request. The printed `SecretKey` is always `[redacted]` and `SecretFile` names the path. If writing the " +
	"file fails, or the response holds no secret, the command deletes the new key and exits 1 with error " +
	"code `SecretFileFailed`; if that delete also fails, the message names the key by its ID so it can be " +
	"deleted with delete-s3-key. If the create fails in a way that may have reached the server, the message " +
	"says a key may exist: list the keys and delete any `UserKeyID` you do not know. " +
	"With `--service-account-id <id>` the command creates the key, attaches it to that service account, and only " +
	"then writes the secret file, so a secret for an unrestricted key never reaches disk. " +
	storageServiceAccountIDNote + " It is checked before any request and needs no `--yes`, since the key is " +
	"new. If the attach fails for any reason, a 5xx included, the command deletes the key, writes no file, and " +
	"exits 1 with the attach's error code; if that delete also fails, the message names the key by its ID. " +
	"The printed `SubUserID` stays empty because it comes from the create response, and `ServiceAccountID` " +
	"names the account; list-s3-keys shows the attach as `SubUserID`. " + storageAttachedKeyNote +
	" Set up one bucket in this order: create the bucket, create the service account, run " +
	"ensure-service-account-principal, write a bucket policy that allows the `PrincipalARN`, then run this " +
	"command with `--service-account-id`. Without the flag the key has the IAM user's rights on every " +
	"bucket. " + storageKeyRightsNote

const storageDeleteS3KeyNote = storageProjectIDNote + storageRegionNote + " Needs `--yes`: a deleted key " +
	"stops working at once and cannot be restored. The server answers success for a `UserKeyID` it does not " +
	"know, and refuses a repeat delete of a deleted key with error code `114` (exit 1)."

const storageAttachS3KeyNote = storageProjectIDNote + storageRegionNote + " Needs `--yes`: the key loses its " +
	"creator's rights on every bucket and acts as the service account, whose rights come only from bucket " +
	"policies that name its principal (see ensure-service-account-principal), so a running app that uses the " +
	"key can lose access. " + storageServiceAccountIDNote + " " + storageAttachedKeyNote + " The data plane " +
	"follows within 3 seconds. The server refuses with error code `114` (exit 1) and says why: the key is " +
	"already attached with this service account, or with another one (checked first, so any attach of an " +
	"attached key gives one of these), `StatusCode=404` for an unknown service account, or `S3 key not " +
	"found`. The call is sent once and never retried. If it fails with a 5xx or a network error, the " +
	"message says the change may have happened: run list-s3-keys and read `SubUserID`, which is empty for " +
	"an unrestricted key. A rerun that answers \"already attached with this service account\" means the " +
	"first try took effect."

const storageDetachS3KeyNote = storageProjectIDNote + storageRegionNote + " Needs `--yes`: the key has its " +
	"creator's rights on every bucket of the project again, at once. The server refuses a key that is not " +
	"attached with error code `114` (exit 1) and the message `This S3 key is not attached to any service " +
	"account.`; an unknown key is `S3 key not found`. The call is sent once and never retried. If it fails " +
	"with a 5xx or a network error, the message says the change may have happened: run list-s3-keys and " +
	"read `SubUserID`. A rerun that answers \"not attached\" means the first try took effect."

const storageEnsurePrincipalNote = storageProjectIDNote + storageRegionNote + " " +
	storageServiceAccountIDNote + " Makes the service account's storage sub-user if it does not exist and " +
	"prints `SubUserID` and `PrincipalARN`, the value a bucket policy puts in its `Principal`. It is a " +
	"write, so a read-only profile refuses it with exit 2 before any request. It needs no `--yes`: a repeat " +
	"returns the same sub-user, and the sub-user costs nothing and has no rights until a bucket policy " +
	"names it. The console API cannot delete a sub-user. It is named from the service account's name, not " +
	"its ID, so a new service account with the name of a deleted one may get the same principal and " +
	"inherit any bucket policy that still names it: remove a service account from its bucket policies " +
	"before deleting it. A response whose sub-user is not a service account's (no `:sa-` segment) is " +
	"refused with error code `NotServiceAccountPrincipal` (exit 1) and prints nothing, so an IAM user's own " +
	"principal never reaches a policy."

const storagePolicyTemplateNote = "The policy template, which grants object work and nothing on the bucket's " +
	"settings, is on the SDK page [Bucket policy](Storage-Bucket-Policy.md)."

const storageGetBucketPolicyNote = storageProjectIDNote + storageRegionNote + " Prints " +
	"`{\"Policy\": \"<document as a string>\"}`, and an empty string for a bucket with no policy. The server " +
	"re-serializes the document, so compare decoded documents, not bytes. " + storagePolicyTemplateNote

const storagePutBucketPolicyNote = storageProjectIDNote + storageRegionNote + " `--policy` takes the document " +
	"as JSON text or as `file://<path>`. A put replaces the whole policy. A document that is not a JSON object " +
	"with a non-empty `Statement` array is refused with exit 2 before any request. The server refuses a document " +
	"it cannot parse with error code `400` or `114` (exit 1). The server does not check principals: a policy " +
	"that names no real principal is accepted and grants nothing, so copy `PrincipalARN` from " +
	"ensure-service-account-principal and check access with the attached key. Needs `--yes` when an " +
	"`Allow` statement's `Principal` contains `*` (the string, or the `AWS` string or list): that can let " +
	"anyone on the internet use the bucket as the statement allows. A `Deny` statement with `*` needs no " +
	"`--yes`. Other policies need no `--yes`, since a repeat put or a delete-bucket-policy undoes them. The data plane follows within about a second. " + storagePolicyTemplateNote

const storageDeleteBucketPolicyNote = storageProjectIDNote + storageRegionNote + " Removes the bucket's " +
	"policy; a bucket with no policy also succeeds, so a repeat delete succeeds. It needs no `--yes`, since " +
	"put-bucket-policy restores a policy. After it, a key attached to a service account has no rights in the " +
	"bucket. The data plane follows within about a second."

const storageGetBucketVersioningNote = storageProjectIDNote + storageRegionNote + " Prints `Enabled` and " +
	"`Status`, the server's `Off`, `Enabled`, or `Suspended`. A bucket reads `Off` only until the first " +
	"put-bucket-versioning; versioning never returns to `Off` once enabled, and a suspended bucket reads " +
	"`Suspended`."

const storagePutBucketVersioningNote = storageProjectIDNote + storageRegionNote + " Needs `--enabled=true` " +
	"or `--enabled=false`; without it the command exits 2 before any request, so it never suspends " +
	"versioning by default. `false` means `Suspended`, not `Off`: the stored versions stay. While versioning " +
	"is on, overwrites and deletes keep old versions, which use quota and make delete-bucket refuse the " +
	"bucket. It needs no `--yes`, since another put reverses it. The change shows on the next " +
	"get-bucket-versioning."

const storageCORSRulesNote = "Each rule has `AllowedOrigins` and `AllowedMethods` (`GET`, `PUT`, `POST`, " +
	"`DELETE`, `HEAD`), and optionally `AllowedHeaders` and `MaxAgeSeconds`. `ExposedHeaders` is read-only: " +
	"the SDK never sends it, so rules put through the CLI read back with it empty."

const storageGetBucketCORSNote = storageProjectIDNote + storageRegionNote + " Prints `{\"Rules\": [...]}`, " +
	"and an empty list for a bucket with no rules. " + storageCORSRulesNote

const storagePutBucketCORSNote = storageProjectIDNote + storageRegionNote + " `Rules` come through " +
	"`--cli-input-json`, inline or `file://<path>`, and replace every rule of the bucket. " +
	storageCORSRulesNote + " The rules are checked before any request: an empty list, a rule with no origin " +
	"or no method, an origin with more than one `*`, an unknown method, or a negative `MaxAgeSeconds` exits " +
	"2. The server's own refusals are error code `114` or `400` (exit 1), and a failed put keeps the previous " +
	"rules. It needs no `--yes`, since another put or a delete-bucket-cors undoes it. To remove every rule, " +
	"run delete-bucket-cors."

const storageDeleteBucketCORSNote = storageProjectIDNote + storageRegionNote + " Removes every CORS rule; " +
	"a bucket with none also succeeds, so a repeat delete succeeds. It needs no `--yes`, since " +
	"put-bucket-cors restores rules. A browser's next preflight is refused."

// docOpNotesStorage holds storage's entries of docOpNotes, merged into it at init so
// gendocs_notes.go stays under the length limit.
var docOpNotesStorage = map[string]string{
	"storage list-regions":                     storageListRegionsNote,
	"storage list-projects":                    storageListProjectsNote,
	"storage list-project-types":               storageListProjectTypesNote,
	"storage quote-create-project":             storageQuoteCreateProjectNote,
	"storage list-buckets":                     storageListBucketsNote,
	"storage get-bucket":                       storageGetBucketNote,
	"storage create-bucket":                    storageCreateBucketNote,
	"storage delete-bucket":                    storageDeleteBucketNote,
	"storage list-s3-keys":                     storageListS3KeysNote,
	"storage create-s3-key":                    storageCreateS3KeyNote,
	"storage delete-s3-key":                    storageDeleteS3KeyNote,
	"storage attach-s3-key":                    storageAttachS3KeyNote,
	"storage detach-s3-key":                    storageDetachS3KeyNote,
	"storage ensure-service-account-principal": storageEnsurePrincipalNote,
	"storage get-bucket-policy":                storageGetBucketPolicyNote,
	"storage put-bucket-policy":                storagePutBucketPolicyNote,
	"storage delete-bucket-policy":             storageDeleteBucketPolicyNote,
	"storage get-bucket-versioning":            storageGetBucketVersioningNote,
	"storage put-bucket-versioning":            storagePutBucketVersioningNote,
	"storage get-bucket-cors":                  storageGetBucketCORSNote,
	"storage put-bucket-cors":                  storagePutBucketCORSNote,
	"storage delete-bucket-cors":               storageDeleteBucketCORSNote,
}

func init() {
	for k, v := range docOpNotesStorage {
		docOpNotes[k] = v
	}
}
