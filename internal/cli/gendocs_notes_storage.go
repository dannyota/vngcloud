package cli

// storage's doc notes, keyed "storage <op-name>" in docOpNotes
// (gendocs_notes.go).

const storageRegionNote = "`Region` is a vStorage region name such as `HCM04`, set only through " +
	"`--cli-input-json`. Empty maps the global `--region`: `hcm-3` to `HCM04` and `han-1` to `HAN02`."

const storageListProjectsNote = storageRegionNote + " A vStorage project is a paid package, so the " +
	"list is empty until one is bought in the console. Its `ID` is the value for `--project-id` in the " +
	"bucket commands."

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
