package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/loadbalancer"
)

// requireYesForInternetLoadBalancer is create-load-balancer's Guard, per the
// vLB writes design's CLI table: it refuses the command when Scheme reads as
// loadbalancer.SchemeInternet, matched case-insensitively since the SDK
// sends Scheme to the server as given rather than restricting it to a fixed
// value set, and --yes was not given. An Internet load balancer gets a
// public address for as long as it exists; Scheme has no default, so the
// caller who wrote it must also confirm it.
//
// It runs on the merged Input, after --cli-input-json and every flag are
// applied, so a Scheme set either way is checked the same way. A Scheme
// that is not Internet, case-insensitively, needs no --yes from this guard,
// whatever it is; the SDK's own required-field check reports an empty one.
func requireYesForInternetLoadBalancer(cmd *cobra.Command, in any) error {
	create, ok := in.(*loadbalancer.CreateLoadBalancerInput)
	if !ok {
		return nil
	}
	if !strings.EqualFold(create.Scheme, loadbalancer.SchemeInternet) {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"create-load-balancer: Scheme %s gets a public address reachable from the entire internet; pass --yes to confirm",
		create.Scheme)
}
