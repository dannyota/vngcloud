package cli

import (
	"context"
	"math"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

type networkCreateEndpointInput struct {
	// Name names the endpoint.
	Name string `vngcloud:"required"`
	// VPCID identifies the VPC, in the form net-<uuid>.
	VPCID string `vngcloud:"required"`
	// SubnetID identifies the subnet, in the form sub-<uuid>.
	SubnetID string `vngcloud:"required"`
	// AvailabilityZoneID selects an enabled availability zone, default HCM03-1C.
	AvailabilityZoneID string
	// CategoryID selects a catalog category, default the sole default category.
	CategoryID string
	// ServiceID selects a Gateway service, default vstorage.hcm03.
	ServiceID string
	// PackageID selects a VND package, default Standard.
	PackageID string
	// MaxPrice must be present, finite, and nonnegative.
	MaxPrice *float64 `vngcloud:"required"`
}

func createNetworkEndpointOp() Op[network.Client] {
	op := Write[network.Client, networkCreateEndpointInput, network.CreateEndpointOutput](
		"create-endpoint", func(c *network.Client, ctx context.Context, in *networkCreateEndpointInput) (*network.CreateEndpointOutput, error) {
			return c.CreateEndpoint(ctx, &network.CreateEndpointInput{Name: in.Name, VPCID: in.VPCID, SubnetID: in.SubnetID, AvailabilityZoneID: in.AvailabilityZoneID, CategoryID: in.CategoryID, ServiceID: in.ServiceID, PackageID: in.PackageID, MaxPrice: *in.MaxPrice})
		}, Guard(requireEndpointCreateConsent))
	op.methodName = "CreateEndpoint"
	return op
}

func endpointSelectorFlags(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		for _, name := range []string{"availability-zone-id", "category-id", "service-id", "package-id"} {
			if cmd.Flags().Changed(name) {
				value, err := cmd.Flags().GetString(name)
				if err != nil || value == "" {
					return newUsageError("--%s must not be empty", name)
				}
			}
		}
		return run(cmd, args)
	}
}

func requireEndpointCreateConsent(cmd *cobra.Command, input any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); !yes {
		return newUsageError("create-endpoint buys a paid endpoint; pass --yes to confirm")
	}
	price := input.(*networkCreateEndpointInput).MaxPrice
	if price == nil {
		return newUsageError("--max-price is required; JSON MaxPrice must be non-null")
	}
	if math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 {
		return newUsageError("--max-price must be finite and nonnegative")
	}
	return nil
}
