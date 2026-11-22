package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/vks"
)

var vksOps = []Op[vks.Client]{
	Write[vks.Client, vks.CreateClusterInput, vks.CreateClusterOutput]("create-cluster", (*vks.Client).CreateCluster, Guard(requireVKSCreateConsent)),
	Write[vks.Client, vks.DeleteClusterInput, vks.DeleteClusterOutput]("delete-cluster", (*vks.Client).DeleteCluster, Destructive()),
	Read[vks.Client, vks.ListClustersInput, vks.ListClustersOutput](
		kebab("ListClusters"), (*vks.Client).ListClusters),
	Read[vks.Client, vks.ListClusterVersionsInput, vks.ListClusterVersionsOutput](
		kebab("ListClusterVersions"), (*vks.Client).ListClusterVersions),
	Read[vks.Client, vks.GetQuotaInput, vks.GetQuotaOutput](
		kebab("GetQuota"), (*vks.Client).GetQuota),
}

func requireVKSCreateConsent(cmd *cobra.Command, _ any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); !yes {
		return newUsageError("create-cluster incurs charges; pass --yes to confirm")
	}
	return nil
}

func newVKSCmd(e *env) *cobra.Command {
	cmd := Service(e, "vks", "Kubernetes clusters and node groups", vks.New, vksOps...)
	for _, child := range cmd.Commands() {
		if note, ok := docOpNotes["vks "+child.Name()]; ok {
			child.Long = note
		}
	}
	return cmd
}
