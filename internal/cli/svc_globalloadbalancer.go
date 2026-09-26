package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/globalloadbalancer"
)

// globalLoadBalancerOps is globalloadbalancer's operation table. The test
// account holds no global load balancer, so get-load-balancer and every
// child read below is Shape, not Live; gen-docs marks each with
// globalLoadBalancerShapeUnverifiedNote, per the CLI reads design's
// globalloadbalancer section.
var globalLoadBalancerOps = []Op[globalloadbalancer.Client]{
	Read[globalloadbalancer.Client, globalloadbalancer.ListPackagesInput, globalloadbalancer.ListPackagesOutput](
		kebab("ListPackages"), (*globalloadbalancer.Client).ListPackages),
	Read[globalloadbalancer.Client, globalloadbalancer.ListRegionsInput, globalloadbalancer.ListRegionsOutput](
		kebab("ListRegions"), (*globalloadbalancer.Client).ListRegions),
	Read[globalloadbalancer.Client, globalloadbalancer.ListLoadBalancersInput, globalloadbalancer.ListLoadBalancersOutput](
		kebab("ListLoadBalancers"), (*globalloadbalancer.Client).ListLoadBalancers),
	Read[globalloadbalancer.Client, globalloadbalancer.GetLoadBalancerInput, globalloadbalancer.GetLoadBalancerOutput](
		kebab("GetLoadBalancer"), (*globalloadbalancer.Client).GetLoadBalancer),
	Read[globalloadbalancer.Client, globalloadbalancer.ListPoolsInput, globalloadbalancer.ListPoolsOutput](
		kebab("ListPools"), (*globalloadbalancer.Client).ListPools),
	Read[globalloadbalancer.Client, globalloadbalancer.ListListenersInput, globalloadbalancer.ListListenersOutput](
		kebab("ListListeners"), (*globalloadbalancer.Client).ListListeners),
	Read[globalloadbalancer.Client, globalloadbalancer.GetListenerInput, globalloadbalancer.GetListenerOutput](
		kebab("GetListener"), (*globalloadbalancer.Client).GetListener),
	Read[globalloadbalancer.Client, globalloadbalancer.ListPoolMembersInput, globalloadbalancer.ListPoolMembersOutput](
		kebab("ListPoolMembers"), (*globalloadbalancer.Client).ListPoolMembers),
	Read[globalloadbalancer.Client, globalloadbalancer.GetPoolMemberInput, globalloadbalancer.GetPoolMemberOutput](
		kebab("GetPoolMember"), (*globalloadbalancer.Client).GetPoolMember),
	Read[globalloadbalancer.Client, globalloadbalancer.ListUsageHistoriesInput, globalloadbalancer.ListUsageHistoriesOutput](
		kebab("ListUsageHistories"), (*globalloadbalancer.Client).ListUsageHistories),
}

func newGlobalLoadBalancerCmd(e *env) *cobra.Command {
	return Service(e, "globalloadbalancer", "Global load balancers, pools, and listeners", globalloadbalancer.New, globalLoadBalancerOps...)
}
