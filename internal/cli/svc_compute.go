package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/compute"
)

// computeOps is compute's operation table. Every operation reads; compute
// writes are not part of this release.
var computeOps = []Op[compute.Client]{
	Read[compute.Client, compute.ListServersInput, compute.ListServersOutput](
		kebab("ListServers"), (*compute.Client).ListServers),
	Read[compute.Client, compute.GetServerInput, compute.GetServerOutput](
		kebab("GetServer"), (*compute.Client).GetServer),
	Read[compute.Client, compute.ListSSHKeysInput, compute.ListSSHKeysOutput](
		kebab("ListSSHKeys"), (*compute.Client).ListSSHKeys),
	Read[compute.Client, compute.ListServerGroupsInput, compute.ListServerGroupsOutput](
		kebab("ListServerGroups"), (*compute.Client).ListServerGroups),
	Read[compute.Client, compute.ListServerSecurityGroupsInput, compute.ListServerSecurityGroupsOutput](
		kebab("ListServerSecurityGroups"), (*compute.Client).ListServerSecurityGroups),
	Read[compute.Client, compute.ListServerGroupMembersInput, compute.ListServerGroupMembersOutput](
		kebab("ListServerGroupMembers"), (*compute.Client).ListServerGroupMembers),
	Read[compute.Client, compute.ListServerGroupPoliciesInput, compute.ListServerGroupPoliciesOutput](
		kebab("ListServerGroupPolicies"), (*compute.Client).ListServerGroupPolicies),
	Read[compute.Client, compute.ListOSImagesInput, compute.ListOSImagesOutput](
		kebab("ListOSImages"), (*compute.Client).ListOSImages),
	Read[compute.Client, compute.ListGPUImagesInput, compute.ListGPUImagesOutput](
		kebab("ListGPUImages"), (*compute.Client).ListGPUImages),
	Read[compute.Client, compute.ListUserImagesInput, compute.ListUserImagesOutput](
		kebab("ListUserImages"), (*compute.Client).ListUserImages),
}

func newComputeCmd(e *env) *cobra.Command {
	return Service(e, "compute", compute.New, computeOps...)
}
