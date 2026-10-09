package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/storage"
)

// storageOps is storage's operation table. Region is a vStorage region name
// such as HCM04, not the global --region value, and an empty one maps the
// configured region, so its flag name would only collide with the global
// flag; NoFlag keeps it settable through --cli-input-json. ProjectID is a
// vStorage project ID, which the global --project-id flag supplies.
var storageOps = []Op[storage.Client]{
	Read[storage.Client, storage.ListRegionsInput, storage.ListRegionsOutput](
		kebab("ListRegions"), (*storage.Client).ListRegions),
	Read[storage.Client, storage.ListProjectsInput, storage.ListProjectsOutput](
		kebab("ListProjects"), (*storage.Client).ListProjects, NoFlag("Region")),
	Read[storage.Client, storage.ListBucketsInput, storage.ListBucketsOutput](
		kebab("ListBuckets"), (*storage.Client).ListBuckets, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Read[storage.Client, storage.GetBucketInput, storage.GetBucketOutput](
		kebab("GetBucket"), (*storage.Client).GetBucket, NoFlag("Region"), GlobalProjectID("ProjectID")),
	Write[storage.Client, storage.CreateBucketInput, storage.CreateBucketOutput](
		kebab("CreateBucket"), (*storage.Client).CreateBucket, WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.DeleteBucketInput, storage.DeleteBucketOutput](
		kebab("DeleteBucket"), (*storage.Client).DeleteBucket, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Read[storage.Client, storage.ListS3KeysInput, storage.ListS3KeysOutput](
		"list-s3-keys", (*storage.Client).ListS3Keys, NoFlag("Region"), GlobalProjectID("ProjectID")),
	createS3KeyOp(),
	Write[storage.Client, storage.DeleteS3KeyInput, storage.DeleteS3KeyOutput](
		"delete-s3-key", (*storage.Client).DeleteS3Key, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.AttachS3KeyInput, storage.AttachS3KeyOutput](
		"attach-s3-key", (*storage.Client).AttachS3Key, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.DetachS3KeyInput, storage.DetachS3KeyOutput](
		"detach-s3-key", (*storage.Client).DetachS3Key, Destructive(), WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
	Write[storage.Client, storage.EnsureServiceAccountPrincipalInput, storage.EnsureServiceAccountPrincipalOutput](
		kebab("EnsureServiceAccountPrincipal"), (*storage.Client).EnsureServiceAccountPrincipal,
		WriteNoFlag("Region"), WriteGlobalProjectID("ProjectID")),
}

func newStorageCmd(e *env) *cobra.Command {
	return Service(e, "storage", "vStorage regions, projects, and buckets", storage.New, storageOps...)
}
