package cli

import (
	"context"
	"net/netip"
	"strings"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/loadbalancer"
)

// allowedCIDRsFlagName is create-listener's and update-listener's own flag
// for AllowedCIDRs: a single comma-separated value, per the vLB writes
// design's CLI table, rather than flags.go's default repeatable flag for a
// []string field. Both ops NoFlag AllowedCIDRs (createListenerOp,
// updateListenerOp) so this is its only flag.
const allowedCIDRsFlagName = "allowed-cidrs"

func registerAllowedCIDRsFlag(cmd *cobra.Command) {
	cmd.Flags().String(allowedCIDRsFlagName, "",
		"comma-separated list of IPv4 CIDR prefixes allowed to reach the listener (required for create-listener)")
}

// splitAllowedCIDRs splits value on commas and trims surrounding space from
// each entry, the reverse of the wire format CreateListenerInput's own doc
// comment describes: the SDK joins AllowedCIDRs with commas on the wire. An
// empty value yields a nil slice, so a caller who never gives
// --allowed-cidrs leaves the merged Input exactly as --cli-input-json left
// it.
func splitAllowedCIDRs(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	cidrs := make([]string, len(parts))
	for i, p := range parts {
		cidrs[i] = strings.TrimSpace(p)
	}
	return cidrs
}

// hasOpenCIDR reports whether any entry of cidrs parses as an IPv4 prefix
// with bit length 0 (0.0.0.0/0, whatever string form parses to it). An
// entry that fails to parse is skipped here: checkAllowedCIDR, run by the
// SDK itself after this guard, reports that more precisely.
func hasOpenCIDR(cidrs []string) bool {
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err == nil && prefix.Bits() == 0 {
			return true
		}
	}
	return false
}

// requireYesForOpenListener refuses op's command, per the vLB writes
// design's CLI table, when cidrs holds a /0 prefix and --yes was not given:
// such a listener serves the whole internet for as long as it stays up, the
// same risk --yes already guards for a destructive command.
func requireYesForOpenListener(cmd *cobra.Command, op string, cidrs []string) error {
	if !hasOpenCIDR(cidrs) {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError("%s: AllowedCIDRs has a /0 prefix, open to the entire internet; pass --yes to confirm", op)
}

// populateCreateListenerAllowedCIDRs is create-listener's Guard: it applies
// --allowed-cidrs, split on commas, over whatever --cli-input-json already
// set for AllowedCIDRs, the same way an ordinary flag always wins over
// --cli-input-json for its own field. It runs before checkRequiredFlags, so
// a caller who gave neither sees this guard's own message rather than the
// generic "set it through --cli-input-json" one NoFlag would otherwise
// produce.
func populateCreateListenerAllowedCIDRs(cmd *cobra.Command, in any) error {
	input, ok := in.(*loadbalancer.CreateListenerInput)
	if !ok {
		return nil
	}
	if cmd.Flags().Changed(allowedCIDRsFlagName) {
		raw, _ := cmd.Flags().GetString(allowedCIDRsFlagName)
		input.AllowedCIDRs = splitAllowedCIDRs(raw)
	}
	if len(input.AllowedCIDRs) == 0 {
		return newUsageError("--%s is required (or set AllowedCIDRs through --cli-input-json)", allowedCIDRsFlagName)
	}
	return requireYesForOpenListener(cmd, "create-listener", input.AllowedCIDRs)
}

// populateUpdateListenerAllowedCIDRs is update-listener's Guard: the same
// --allowed-cidrs merge as create-listener's, but AllowedCIDRs is optional
// here (*[]string, a nil pointer keeps the listener's own value), so an
// unset flag and an unset --cli-input-json value both leave it alone and
// need no --yes.
func populateUpdateListenerAllowedCIDRs(cmd *cobra.Command, in any) error {
	input, ok := in.(*loadbalancer.UpdateListenerInput)
	if !ok {
		return nil
	}
	if cmd.Flags().Changed(allowedCIDRsFlagName) {
		raw, _ := cmd.Flags().GetString(allowedCIDRsFlagName)
		cidrs := splitAllowedCIDRs(raw)
		input.AllowedCIDRs = &cidrs
	}
	if input.AllowedCIDRs == nil {
		return nil
	}
	return requireYesForOpenListener(cmd, "update-listener", *input.AllowedCIDRs)
}

// createListenerOp builds create-listener's Op directly, rather than through
// Write, since AllowedCIDRs needs its own comma-separated flag
// (registerAllowedCIDRsFlag) in place of flags.go's default repeatable
// []string flag, the same reason importCertificateOp (svc_loadbalancer_certificates.go)
// builds its Op by hand. CertificateIDs is NoFlag'd too, per the design:
// nested fields come through --cli-input-json.
func createListenerOp() Op[loadbalancer.Client] {
	return Op[loadbalancer.Client]{
		name:       kebab("CreateListener"),
		methodName: "CreateListener",
		kind:       kindWrite,
		noFlag:     map[string]bool{"AllowedCIDRs": true, "CertificateIDs": true},
		guard:      populateCreateListenerAllowedCIDRs,
		extraFlags: registerAllowedCIDRsFlag,
		newInput:   func() any { return new(loadbalancer.CreateListenerInput) },
		newOutput:  func() any { return new(loadbalancer.CreateListenerOutput) },
		call: func(_ *cobra.Command, client *loadbalancer.Client, ctx context.Context, in any) (any, error) {
			return client.CreateListener(ctx, in.(*loadbalancer.CreateListenerInput))
		},
	}
}

// updateListenerOp builds update-listener's Op directly, for the same
// reason createListenerOp does: AllowedCIDRs needs its own comma-separated
// flag, and CertificateIDs is NoFlag'd per the design.
func updateListenerOp() Op[loadbalancer.Client] {
	return Op[loadbalancer.Client]{
		name:       kebab("UpdateListener"),
		methodName: "UpdateListener",
		kind:       kindWrite,
		noFlag:     map[string]bool{"AllowedCIDRs": true, "CertificateIDs": true},
		guard:      populateUpdateListenerAllowedCIDRs,
		extraFlags: registerAllowedCIDRsFlag,
		newInput:   func() any { return new(loadbalancer.UpdateListenerInput) },
		newOutput:  func() any { return new(loadbalancer.UpdateListenerOutput) },
		call: func(_ *cobra.Command, client *loadbalancer.Client, ctx context.Context, in any) (any, error) {
			return client.UpdateListener(ctx, in.(*loadbalancer.UpdateListenerInput))
		},
	}
}
