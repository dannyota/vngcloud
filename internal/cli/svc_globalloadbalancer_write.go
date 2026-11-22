package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/globalloadbalancer"
)

func createGlobalLoadBalancerOp() Op[globalloadbalancer.Client] {
	op := Write[globalloadbalancer.Client, globalloadbalancer.CreateLoadBalancerInput, globalloadbalancer.CreateLoadBalancerOutput](
		kebab("CreateLoadBalancer"), (*globalloadbalancer.Client).CreateLoadBalancer, Guard(globalLoadBalancerCreateGuard))
	op.short = "Create using credits-only automated payment; a prepaid three-day hold and later connection and traffic charges apply. Listener CIDR must be restricted."
	op.extraFlags = func(cmd *cobra.Command) {
		cmd.Flags().String("listener-name", "", "TCP listener name")
		cmd.Flags().Int("listener-port", 0, "TCP listener port")
		cmd.Flags().StringArray("listener-allowed-cidrs", nil, "restricted canonical IPv4 CIDR, repeatable; /0 is refused")
		cmd.Flags().String("pool-name", "", "TCP pool name")
		cmd.Flags().String("member-region", "", "backend region")
		cmd.Flags().String("member-vpc-id", "", "backend VPC ID")
		cmd.Flags().String("member-subnet-id", "", "backend subnet ID")
		cmd.Flags().String("member-address", "", "backend IPv4 address")
		cmd.Flags().Int("member-port", 0, "backend port")
	}
	return op
}

func globalLoadBalancerCreateGuard(cmd *cobra.Command, value any) error {
	yes, _ := cmd.Flags().GetBool("yes")
	if !yes {
		return newUsageError("create-load-balancer requires --yes to confirm charges")
	}
	in := value.(*globalloadbalancer.CreateLoadBalancerInput)
	f := cmd.Flags()
	if f.Changed("listener-name") || f.Changed("listener-port") || f.Changed("listener-allowed-cidrs") {
		if in.Listener == nil {
			in.Listener = &globalloadbalancer.CreateListenerInput{}
		}
		if f.Changed("listener-name") {
			in.Listener.Name, _ = f.GetString("listener-name")
		}
		if f.Changed("listener-port") {
			in.Listener.Port, _ = f.GetInt("listener-port")
		}
		if f.Changed("listener-allowed-cidrs") {
			in.Listener.AllowedCIDRs, _ = f.GetStringArray("listener-allowed-cidrs")
		}
	}
	if f.Changed("pool-name") || f.Changed("member-region") || f.Changed("member-vpc-id") || f.Changed("member-subnet-id") || f.Changed("member-address") || f.Changed("member-port") {
		if in.Pool == nil {
			in.Pool = &globalloadbalancer.CreatePoolInput{}
		}
		if f.Changed("pool-name") {
			in.Pool.Name, _ = f.GetString("pool-name")
		}
		if f.Changed("member-region") || f.Changed("member-vpc-id") || f.Changed("member-subnet-id") || f.Changed("member-address") || f.Changed("member-port") {
			if len(in.Pool.Members) == 0 {
				in.Pool.Members = []globalloadbalancer.PoolMemberInput{{}}
			}
			m := &in.Pool.Members[0]
			if f.Changed("member-region") {
				m.Region, _ = f.GetString("member-region")
			}
			if f.Changed("member-vpc-id") {
				m.VPCID, _ = f.GetString("member-vpc-id")
			}
			if f.Changed("member-subnet-id") {
				m.SubnetID, _ = f.GetString("member-subnet-id")
			}
			if f.Changed("member-address") {
				m.Address, _ = f.GetString("member-address")
			}
			if f.Changed("member-port") {
				m.Port, _ = f.GetInt("member-port")
			}
		}
	}
	return nil
}
