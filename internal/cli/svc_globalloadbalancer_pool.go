package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/globalloadbalancer"
)

func globalLoadBalancerYesGuard(command string) writeOption {
	return Guard(func(cmd *cobra.Command, _ any) error {
		if yes, _ := cmd.Flags().GetBool("yes"); !yes {
			return newUsageError("%s requires --yes to confirm", command)
		}
		return nil
	})
}

func globalLoadBalancerChildOps() []Op[globalloadbalancer.Client] {
	createPool := Write[globalloadbalancer.Client, globalloadbalancer.CreatePoolInput, globalloadbalancer.CreatePoolOutput](
		kebab("CreatePool"), (*globalloadbalancer.Client).CreatePool, globalLoadBalancerYesGuard("create-pool"), WriteGlobalProjectID("BackendProjectID"))
	createPool.short = "Create a TCP pool with one private member group and one vServer backend."
	createListener := Write[globalloadbalancer.Client, globalloadbalancer.CreateListenerInput, globalloadbalancer.CreateListenerOutput](
		kebab("CreateListener"), (*globalloadbalancer.Client).CreateListener, globalLoadBalancerYesGuard("create-listener"), WriteGlobalProjectID("BackendProjectID"))
	createListener.short = "Create a TCP listener bound to the pool, limited to the allowed source CIDRs."
	deleteListener := Write[globalloadbalancer.Client, globalloadbalancer.DeleteListenerInput, globalloadbalancer.DeleteListenerOutput](
		kebab("DeleteListener"), (*globalloadbalancer.Client).DeleteListener, Destructive())
	deleteListener.short = "Delete the listener only; the pool and its member group stay."
	deletePool := Write[globalloadbalancer.Client, globalloadbalancer.DeletePoolInput, globalloadbalancer.DeletePoolOutput](
		kebab("DeletePool"), (*globalloadbalancer.Client).DeletePool, Destructive())
	deletePool.short = "Delete a listener-free pool with its member group and backend row; the vServer stays."
	return []Op[globalloadbalancer.Client]{createPool, createListener, deleteListener, deletePool}
}
