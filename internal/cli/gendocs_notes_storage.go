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
