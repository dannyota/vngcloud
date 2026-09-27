package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/pricing"
)

// loadbalancerOps is loadbalancer's operation table. Every read here needs
// no NoFlag or Redact: the CLI reads design's "loadbalancer" section notes
// that Certificate has no key or PEM field, and a typed model drops every
// field it does not declare, so a private key in a response could never
// reach output. import-certificate and delete-certificate are the two
// writes, per the vLB certificate design: import-certificate is built by
// hand (importCertificateOp, in svc_loadbalancer_certificates.go) since
// Certificate and CertificateChain need NoFlag and the certificate, chain,
// key, and passphrase all arrive through its own file flags rather than
// flags.go's reflection; delete-certificate is Destructive, since a deleted
// certificate needs its key again to re-import and the key may no longer
// exist anywhere else, and needs no Guard of its own: DeleteCertificate
// itself refuses a certificate a listener still uses before sending
// anything.
//
// quote-create-load-balancer and quote-resize-load-balancer are the vLB
// writes design's L1 release: Read, per ADR 0002 rule 1, since a quote
// orders nothing; both hide MaxPrice and NoWait with NoFlag since both
// govern only an actual create or resize, which this SDK does not send yet.
//
// create-load-balancer and delete-load-balancer are the design's L2
// release. create-load-balancer takes requireYesForInternetLoadBalancer
// (svc_loadbalancer_create.go) as its Guard: Scheme has no default, so the
// design's own --yes rule applies only once the caller actually names
// Internet. delete-load-balancer is Destructive: a deleted load balancer
// loses its address and its prepaid time for good.
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
	importCertificateOp(),
	Write[loadbalancer.Client, loadbalancer.DeleteCertificateInput, loadbalancer.DeleteCertificateOutput](
		kebab("DeleteCertificate"), (*loadbalancer.Client).DeleteCertificate, Destructive()),
	Read[loadbalancer.Client, loadbalancer.CreateLoadBalancerInput, pricing.GetQuoteOutput](
		kebab("QuoteCreateLoadBalancer"), (*loadbalancer.Client).QuoteCreateLoadBalancer,
		NoFlag("MaxPrice", "NoWait")),
	Read[loadbalancer.Client, loadbalancer.ResizeLoadBalancerInput, pricing.GetQuoteOutput](
		kebab("QuoteResizeLoadBalancer"), (*loadbalancer.Client).QuoteResizeLoadBalancer,
		NoFlag("MaxPrice", "NoWait")),
	Write[loadbalancer.Client, loadbalancer.CreateLoadBalancerInput, loadbalancer.CreateLoadBalancerOutput](
		kebab("CreateLoadBalancer"), (*loadbalancer.Client).CreateLoadBalancer,
		Guard(requireYesForInternetLoadBalancer)),
	Write[loadbalancer.Client, loadbalancer.DeleteLoadBalancerInput, loadbalancer.DeleteLoadBalancerOutput](
		kebab("DeleteLoadBalancer"), (*loadbalancer.Client).DeleteLoadBalancer, Destructive()),
}

func newLoadBalancerCmd(e *env) *cobra.Command {
	return Service(e, "loadbalancer", "Regional load balancers, listeners, pools, and certificates", loadbalancer.New, loadbalancerOps...)
}
