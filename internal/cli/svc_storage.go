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
}

func newStorageCmd(e *env) *cobra.Command {
	return Service(e, "storage", "vStorage regions, projects, and buckets", storage.New, storageOps...)
}
