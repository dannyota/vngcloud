package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/globalloadbalancer"
)

func createGlobalLoadBalancerOp() Op[globalloadbalancer.Client] {
	op := Write[globalloadbalancer.Client, globalloadbalancer.CreateLoadBalancerInput, globalloadbalancer.CreateLoadBalancerOutput](
		kebab("CreateLoadBalancer"), (*globalloadbalancer.Client).CreateLoadBalancer, Guard(globalLoadBalancerCreateGuard))
	op.short = "Create using credits-only automated payment; a prepaid three-day hold and later connection and traffic charges apply."
	return op
}

func globalLoadBalancerCreateGuard(cmd *cobra.Command, _ any) error {
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		return newUsageError("create-load-balancer requires --yes to confirm charges")
	}
	return nil
}
