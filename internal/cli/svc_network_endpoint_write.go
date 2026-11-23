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
	// MaxPrice must be present, finite, and nonnegative.
	MaxPrice *float64 `vngcloud:"required"`
}

func createNetworkEndpointOp() Op[network.Client] {
	op := Write[network.Client, networkCreateEndpointInput, network.CreateEndpointOutput](
		"create-endpoint", func(c *network.Client, ctx context.Context, in *networkCreateEndpointInput) (*network.CreateEndpointOutput, error) {
			return c.CreateEndpoint(ctx, &network.CreateEndpointInput{Name: in.Name, VPCID: in.VPCID, SubnetID: in.SubnetID, MaxPrice: *in.MaxPrice})
		}, Guard(requireEndpointCreateConsent))
	op.methodName = "CreateEndpoint"
	return op
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
