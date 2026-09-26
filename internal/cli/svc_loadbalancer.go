package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/loadbalancer"
)

// loadbalancerOps is loadbalancer's operation table. Every operation reads.
// No Input field here
// needs NoFlag or Redact: the CLI reads design's "loadbalancer" section
// notes that Certificate has no key or PEM field, and a typed model drops
// every field it does not declare, so a private key in a response could
// never reach output.
var loadbalancerOps = []Op[loadbalancer.Client]{
	Read[loadbalancer.Client, loadbalancer.ListLoadBalancersInput, loadbalancer.ListLoadBalancersOutput](
		kebab("ListLoadBalancers"), (*loadbalancer.Client).ListLoadBalancers),
	Read[loadbalancer.Client, loadbalancer.GetLoadBalancerInput, loadbalancer.GetLoadBalancerOutput](
		kebab("GetLoadBalancer"), (*loadbalancer.Client).GetLoadBalancer),
	Read[loadbalancer.Client, loadbalancer.ListPackagesInput, loadbalancer.ListPackagesOutput](
		kebab("ListPackages"), (*loadbalancer.Client).ListPackages),
	Read[loadbalancer.Client, loadbalancer.ListCertificatesInput, loadbalancer.ListCertificatesOutput](
		kebab("ListCertificates"), (*loadbalancer.Client).ListCertificates),
	Read[loadbalancer.Client, loadbalancer.GetCertificateInput, loadbalancer.GetCertificateOutput](
		kebab("GetCertificate"), (*loadbalancer.Client).GetCertificate),
	Read[loadbalancer.Client, loadbalancer.ListListenersInput, loadbalancer.ListListenersOutput](
		kebab("ListListeners"), (*loadbalancer.Client).ListListeners),
	Read[loadbalancer.Client, loadbalancer.GetListenerInput, loadbalancer.GetListenerOutput](
		kebab("GetListener"), (*loadbalancer.Client).GetListener),
	Read[loadbalancer.Client, loadbalancer.ListPoolsInput, loadbalancer.ListPoolsOutput](
		kebab("ListPools"), (*loadbalancer.Client).ListPools),
	Read[loadbalancer.Client, loadbalancer.GetPoolInput, loadbalancer.GetPoolOutput](
		kebab("GetPool"), (*loadbalancer.Client).GetPool),
	Read[loadbalancer.Client, loadbalancer.GetPoolHealthMonitorInput, loadbalancer.GetPoolHealthMonitorOutput](
		kebab("GetPoolHealthMonitor"), (*loadbalancer.Client).GetPoolHealthMonitor),
	Read[loadbalancer.Client, loadbalancer.ListPoolMembersInput, loadbalancer.ListPoolMembersOutput](
		kebab("ListPoolMembers"), (*loadbalancer.Client).ListPoolMembers),
	Read[loadbalancer.Client, loadbalancer.ListPoliciesInput, loadbalancer.ListPoliciesOutput](
		kebab("ListPolicies"), (*loadbalancer.Client).ListPolicies),
	Read[loadbalancer.Client, loadbalancer.GetPolicyInput, loadbalancer.GetPolicyOutput](
		kebab("GetPolicy"), (*loadbalancer.Client).GetPolicy),
	Read[loadbalancer.Client, loadbalancer.ListTagsInput, loadbalancer.ListTagsOutput](
		kebab("ListTags"), (*loadbalancer.Client).ListTags),
}

func newLoadBalancerCmd(e *env) *cobra.Command {
	return Service(e, "loadbalancer", "Regional load balancers, listeners, pools, and certificates", loadbalancer.New, loadbalancerOps...)
}
