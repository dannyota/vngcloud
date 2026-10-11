package cli

import (
	"context"
	"errors"
	"math"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/network"
)

// A pointer distinguishes an explicit zero price cap from omitted consent.
type networkCreateNATInput struct {
	Name               string   `vngcloud:"required"`
	ZoneID             string   `vngcloud:"required"`
	AvailabilityZoneID string   `vngcloud:"required"`
	PackageID          string   `vngcloud:"required"`
	VPCID              string   `vngcloud:"required"`
	MaxPrice           *float64 `vngcloud:"required"`
}

func createNetworkNATOp() Op[network.Client] {
	op := Write[network.Client, networkCreateNATInput, network.CreateNATInstanceOutput](
		"create-nat-instance", func(c *network.Client, ctx context.Context, in *networkCreateNATInput) (*network.CreateNATInstanceOutput, error) {
			return c.CreateNATInstance(ctx, &network.CreateNATInstanceInput{
				Name: in.Name, ZoneID: in.ZoneID, AvailabilityZoneID: in.AvailabilityZoneID,
				PackageID: in.PackageID, VPCID: in.VPCID, MaxPrice: *in.MaxPrice,
			})
		}, Guard(requireNATCreateConsent))
	op.methodName = "CreateNATInstance"
	return op
}

func requireNATCreateConsent(cmd *cobra.Command, input any) error {
	if yes, _ := cmd.Flags().GetBool("yes"); !yes {
		return newUsageError("create-nat-instance changes VPC egress; pass --yes to confirm")
	}
	price := input.(*networkCreateNATInput).MaxPrice
	if price == nil {
		return newUsageError("--max-price is required; JSON MaxPrice must be non-null")
	}
	if math.IsNaN(*price) || math.IsInf(*price, 0) || *price < 0 {
		return newUsageError("--max-price must be finite and nonnegative")
	}
	return nil
}

// The NAT write contract groups read-only refusal under InvalidUsage.
func natReadOnlyUsage(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := run(cmd, args)
		var readOnly readOnlyError
		if errors.As(err, &readOnly) {
			return newUsageError("%s", err)
		}
		return err
	}
}
