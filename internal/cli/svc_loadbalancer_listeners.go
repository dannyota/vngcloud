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

// registerAllowedCIDRsFlag adds --allowed-cidrs to cmd. required marks it
// "(required)" in the flag's own usage text, the literal suffix gen-docs'
// extraDocFields looks for: true for create-listener, where AllowedCIDRs is
// a required field, and false for update-listener, where a nil value keeps
// the listener's own.
func registerAllowedCIDRsFlag(cmd *cobra.Command, required bool) {
	usage := "comma-separated list of IPv4 CIDR prefixes allowed to reach the listener"
	if required {
		usage += " (required)"
	}
	cmd.Flags().String(allowedCIDRsFlagName, "", usage)
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

// privateCIDRRanges are the IPv4 ranges create-listener's and
// update-listener's own --yes guard treats as private, per the vLB writes
// design's CLI table. A listener whose AllowedCIDRs stays entirely inside
// these reaches only a private network and needs no --yes; anything else,
// including a broad prefix that only overlaps one of them (0.0.0.0/1), does.
var privateCIDRRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"),
}

// isPrivateCIDR reports whether cidr parses as an IPv4 prefix wholly
// contained in one of privateCIDRRanges: its own bits must be at least as
// long as the range's, so a broader prefix that merely overlaps it (such as
// 0.0.0.0/1 over 10.0.0.0/8) does not count, and its address must fall
// inside the range. An entry that fails to parse, or parses as anything but
// an IPv4 prefix (including an IPv6 one), is never private: this guard
// cannot verify it safe, so it falls to requireYesForPublicListener the same
// as a confirmed public address; checkAllowedCIDR, run by the SDK itself
// after this guard, reports a malformed entry more precisely.
func isPrivateCIDR(cidr string) bool {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil || !prefix.Addr().Is4() {
		return false
	}
	for _, r := range privateCIDRRanges {
		if prefix.Bits() >= r.Bits() && r.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

// hasPublicCIDR reports whether any entry of cidrs is not confirmed private
// by isPrivateCIDR.
func hasPublicCIDR(cidrs []string) bool {
	for _, cidr := range cidrs {
		if !isPrivateCIDR(cidr) {
			return true
		}
	}
	return false
}

// requireYesForPublicListener refuses op's command, per the vLB writes
// design's CLI table, when cidrs holds an entry outside every private range
// and --yes was not given: such a listener may be reachable from outside the
// account's own network for as long as it stays up, the same risk --yes
// already guards for a destructive command.
func requireYesForPublicListener(cmd *cobra.Command, op string, cidrs []string) error {
	if !hasPublicCIDR(cidrs) {
		return nil
	}
	if yes, _ := cmd.Flags().GetBool("yes"); yes {
		return nil
	}
	return newUsageError("%s: AllowedCIDRs has an entry outside every private range "+
		"(10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 100.64.0.0/10); pass --yes to confirm", op)
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
	return requireYesForPublicListener(cmd, "create-listener", input.AllowedCIDRs)
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
	return requireYesForPublicListener(cmd, "update-listener", *input.AllowedCIDRs)
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
		extraFlags: func(cmd *cobra.Command) { registerAllowedCIDRsFlag(cmd, true) },
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
		extraFlags: func(cmd *cobra.Command) { registerAllowedCIDRsFlag(cmd, false) },
		newInput:   func() any { return new(loadbalancer.UpdateListenerInput) },
		newOutput:  func() any { return new(loadbalancer.UpdateListenerOutput) },
		call: func(_ *cobra.Command, client *loadbalancer.Client, ctx context.Context, in any) (any, error) {
			return client.UpdateListener(ctx, in.(*loadbalancer.UpdateListenerInput))
		},
	}
}
