package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/vks"
)

var vksOps = []Op[vks.Client]{
	Read[vks.Client, vks.ListClustersInput, vks.ListClustersOutput](
		kebab("ListClusters"), (*vks.Client).ListClusters),
	Read[vks.Client, vks.ListClusterVersionsInput, vks.ListClusterVersionsOutput](
		kebab("ListClusterVersions"), (*vks.Client).ListClusterVersions),
	Read[vks.Client, vks.GetQuotaInput, vks.GetQuotaOutput](
		kebab("GetQuota"), (*vks.Client).GetQuota),
}

func newVKSCmd(e *env) *cobra.Command {
	return Service(e, "vks", "Kubernetes clusters and node groups", vks.New, vksOps...)
}
