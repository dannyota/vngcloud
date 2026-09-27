package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/loadbalancer"
)

// requireYesUnlessSchemeInternal is create-load-balancer's Guard, per the
// vLB writes design's CLI table: it refuses the command unless Scheme,
// trimmed of surrounding space and matched case-insensitively, reads as
// exactly loadbalancer.SchemeInternal, and --yes was not given. Every other
// value needs --yes, including one the SDK will itself go on to refuse as
// neither SchemeInternet nor SchemeInternal: this guard does not rely on
// that separate check, since a value it does not recognize as Internal may
// still reach the server with a public address, whatever the SDK later
// decides. Scheme has no default, so the caller who wrote it must also
// confirm it.
//
// It runs on the merged Input, after --cli-input-json and every flag are
// applied, so a Scheme set either way is checked the same way.
func requireYesUnlessSchemeInternal(cmd *cobra.Command, in any) error {
	create, ok := in.(*loadbalancer.CreateLoadBalancerInput)
	if !ok {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(create.Scheme), loadbalancer.SchemeInternal) {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError(
		"create-load-balancer: Scheme %q may get a public address reachable from the entire internet; pass --yes to confirm",
		create.Scheme)
}
