package cli

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"danny.vn/vngcloud"
)

type flagsTestInput struct {
	Name       string
	Enabled    *bool
	Count      *int
	Limit      int64
	Price      float64
	MaxPrice   *float64
	Extra      map[string]any // unsupported type: only settable via --cli-input-json
	unexported string         //nolint:unused // proves flagSpecsFor skips unexported fields
}

func TestFlagSpecsForSkipsUnsupportedAndUnexported(t *testing.T) {
	specs, err := flagSpecsFor(&flagsTestInput{})
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	names := make(map[string]bool, len(specs))
	for _, s := range specs {
		names[s.flagName] = true
	}
	for _, want := range []string{"name", "enabled", "count", "limit", "price", "max-price"} {
		if !names[want] {
			t.Errorf("missing flag %q in %v", want, names)
		}
	}
	if len(specs) != 6 {
		t.Fatalf("got %d specs, want 6 (Extra and unexported must be skipped): %+v", len(specs), specs)
	}
}

// secretFlagsTestInput stands in for an Input carrying a vngcloud.Secret
// field, the shape loadbalancer.ImportCertificateInput's PrivateKey and
// Passphrase take: without the rule flags.go enforces, PlainSecret's
// underlying reflect.Kind (String) would make flagSpecsFor give it an
// ordinary "--plain-secret" flag.
type secretFlagsTestInput struct {
	Name        string
	PlainSecret vngcloud.Secret
	PtrSecret   *vngcloud.Secret
}

// TestFlagSpecsForSkipsSecretFields checks flags.go's rule that a
// vngcloud.Secret field, plain or pointer, never becomes a flag: only Name
// gets one.
func TestFlagSpecsForSkipsSecretFields(t *testing.T) {
	specs, err := flagSpecsFor(&secretFlagsTestInput{})
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	if len(specs) != 1 || specs[0].fieldName != "Name" {
		t.Fatalf("specs = %+v, want only Name (PlainSecret and PtrSecret must be skipped)", specs)
	}
}

// TestNoRealOpEverGetsAFlagForASecretField sweeps every registered
// operation table (svc_test.go's assertNoSecretFieldGetsAFlag) so a Secret
// field added to any real Input, now or later, is caught here rather than
// only where it happens to be introduced.
func TestNoRealOpEverGetsAFlagForASecretField(t *testing.T) {
	assertNoSecretFieldGetsAFlag(t, "billing", billingOps)
	assertNoSecretFieldGetsAFlag(t, "pricing", pricingOps)
	assertNoSecretFieldGetsAFlag(t, "compute", computeOps)
	assertNoSecretFieldGetsAFlag(t, "network", networkOps)
	assertNoSecretFieldGetsAFlag(t, "dns", dnsOps)
	assertNoSecretFieldGetsAFlag(t, "cdn", cdnOps)
	assertNoSecretFieldGetsAFlag(t, "monitor", monitorOps)
	assertNoSecretFieldGetsAFlag(t, "project", projectOps)
	assertNoSecretFieldGetsAFlag(t, "portal", portalOps)
	assertNoSecretFieldGetsAFlag(t, "loadbalancer", loadbalancerOps)
	assertNoSecretFieldGetsAFlag(t, "volume", volumeOps)
	assertNoSecretFieldGetsAFlag(t, "containerregistry", containerRegistryOps)
	assertNoSecretFieldGetsAFlag(t, "globalloadbalancer", globalLoadBalancerOps)
}

func TestFlagSpecsForRejectsNonStructPointer(t *testing.T) {
	if _, err := flagSpecsFor("not a pointer"); err == nil {
		t.Fatalf("expected an error for a non-pointer input")
	}
}

// sliceFlagsTestInput stands in for an Input carrying a []string field, the
// shape compute.CreateServerInput.SecurityGroupIDs takes: flags.go gives it
// a repeatable flag, bound with pflag's StringArrayVar so a value is never
// split on a comma the way StringSliceVar would.
type sliceFlagsTestInput struct {
	Name string
	Tags []string `vngcloud:"required"`
}

// TestFlagSpecsForSupportsStringSliceFields checks that a []string field
// gets its own flag, unlike Extra (flagsTestInput's map[string]any) above.
func TestFlagSpecsForSupportsStringSliceFields(t *testing.T) {
	specs, err := flagSpecsFor(&sliceFlagsTestInput{})
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("got %d specs, want 2: %+v", len(specs), specs)
	}
	var found bool
	for _, s := range specs {
		if s.fieldName == "Tags" {
			found = true
			if s.kind != reflect.Slice {
				t.Fatalf("Tags spec.kind = %v, want reflect.Slice", s.kind)
			}
			if s.isPointer {
				t.Fatalf("Tags spec.isPointer = true, want false")
			}
		}
	}
	if !found {
		t.Fatalf("no spec for Tags: %+v", specs)
	}
}

// TestRegisterFlagsBindsARepeatableStringSliceFlag checks that a []string
// flag can be given more than once on one command line, and that each
// occurrence is kept whole rather than split on a comma: pflag's
// StringSliceVar would split "a,b" into two values, which would silently
// break a security group ID or any other value that happens to contain a
// comma.
func TestRegisterFlagsBindsARepeatableStringSliceFlag(t *testing.T) {
	in := &sliceFlagsTestInput{}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, bound := newTestCmd(t, specs)
	if err := cmd.ParseFlags([]string{"--tags", "sg-1", "--tags", "sg-2,not-split"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	applyChangedFlags(cmd, in, bound)
	want := []string{"sg-1", "sg-2,not-split"}
	if !equalStringSlices(in.Tags, want) {
		t.Fatalf("Tags = %v, want %v", in.Tags, want)
	}
}

// TestApplyChangedFlagsLeavesStringSliceUntouchedWhenNotGiven checks that a
// []string field --cli-input-json already set survives when the flag is
// never given on the command line, the same rule every other field type
// already follows.
func TestApplyChangedFlagsLeavesStringSliceUntouchedWhenNotGiven(t *testing.T) {
	in := &sliceFlagsTestInput{Tags: []string{"from-json"}}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, bound := newTestCmd(t, specs)
	if err := cmd.ParseFlags(nil); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	applyChangedFlags(cmd, in, bound)
	if !equalStringSlices(in.Tags, []string{"from-json"}) {
		t.Fatalf("Tags = %v, want the JSON value left untouched", in.Tags)
	}
}

// TestCheckRequiredFlagsNamesAMissingStringSliceFlag checks that a required
// []string field with no value (a nil slice, the zero value for Slice)
// fails checkRequiredFlags the same way a missing required string does.
func TestCheckRequiredFlagsNamesAMissingStringSliceFlag(t *testing.T) {
	err := checkRequiredFlags(&sliceFlagsTestInput{}, nil)
	if err == nil {
		t.Fatalf("expected an error for a missing required []string field")
	}
	if got := err.Error(); got != "--tags is required" {
		t.Fatalf("error = %q, want it to name the flag", got)
	}
}

func newTestCmd(t *testing.T, specs []flagSpec) (*cobra.Command, []boundFlag) {
	t.Helper()
	cmd := &cobra.Command{Use: "test", RunE: func(*cobra.Command, []string) error { return nil }}
	bound := registerFlags(cmd, specs)
	return cmd, bound
}

func TestApplyChangedFlagsOnlyAppliesGivenFlags(t *testing.T) {
	in := &flagsTestInput{Name: "from-json", Limit: 42}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, bound := newTestCmd(t, specs)
	if err := cmd.ParseFlags([]string{"--enabled=false"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	applyChangedFlags(cmd, in, bound)

	if in.Name != "from-json" {
		t.Fatalf("Name = %q, want the JSON value left untouched (flag not given)", in.Name)
	}
	if in.Limit != 42 {
		t.Fatalf("Limit = %d, want the JSON value left untouched", in.Limit)
	}
	if in.Enabled == nil || *in.Enabled != false {
		t.Fatalf("Enabled = %v, want a pointer to false", in.Enabled)
	}
}

func TestApplyChangedFlagsSetsPointerFields(t *testing.T) {
	in := &flagsTestInput{}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, bound := newTestCmd(t, specs)
	if err := cmd.ParseFlags([]string{"--count=7"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	applyChangedFlags(cmd, in, bound)
	if in.Count == nil || *in.Count != 7 {
		t.Fatalf("Count = %v, want a pointer to 7", in.Count)
	}
}

// TestApplyChangedFlagsSetsFloat64Fields checks a plain float64 field
// (Price, the shape monitor.CreateLogProjectInput.MaxPrice takes) and a
// pointer-to-float64 field both parse and apply correctly: float64 has no
// other Input field in the SDK to exercise this path against.
func TestApplyChangedFlagsSetsFloat64Fields(t *testing.T) {
	in := &flagsTestInput{}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, bound := newTestCmd(t, specs)
	if err := cmd.ParseFlags([]string{"--price=917000", "--max-price=1500.5"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	applyChangedFlags(cmd, in, bound)
	if in.Price != 917000 {
		t.Fatalf("Price = %v, want 917000", in.Price)
	}
	if in.MaxPrice == nil || *in.MaxPrice != 1500.5 {
		t.Fatalf("MaxPrice = %v, want a pointer to 1500.5", in.MaxPrice)
	}
}

func TestEnabledFalseSendsFalse(t *testing.T) {
	in := &flagsTestInput{}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, bound := newTestCmd(t, specs)
	if err := cmd.ParseFlags([]string{"--enabled=false"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	if args := cmd.Flags().Args(); len(args) != 0 {
		t.Fatalf("leftover positional args = %v, want none", args)
	}
	applyChangedFlags(cmd, in, bound)
	if in.Enabled == nil || *in.Enabled != false {
		t.Fatalf("Enabled = %v, want a pointer to false", in.Enabled)
	}
}

func TestEnabledSpaceFalseLeavesAStrayPositionalArg(t *testing.T) {
	// "--enabled false" cannot mean "set Enabled to false": pflag's bool
	// flags never consume a following bare value, so "false" here is left as
	// a positional argument. noArgs (used by every real operation command)
	// turns that leftover into a usage error, which is asserted in
	// TestOpEnabledSpaceValueExitsUsageError against a real command.
	in := &flagsTestInput{}
	specs, err := flagSpecsFor(in)
	if err != nil {
		t.Fatalf("flagSpecsFor: %v", err)
	}
	cmd, _ := newTestCmd(t, specs)
	if err := cmd.ParseFlags([]string{"--enabled", "false"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	args := cmd.Flags().Args()
	if len(args) != 1 || args[0] != "false" {
		t.Fatalf("leftover positional args = %v, want [\"false\"]", args)
	}
	if !cmd.Flags().Changed("enabled") {
		t.Fatalf("enabled should be Changed (bare --enabled means true)")
	}
}

type requiredTestInput struct {
	BudgetUUID string `vngcloud:"required"`
	Name       string
}

func TestCheckRequiredFlagsNamesTheFlag(t *testing.T) {
	err := checkRequiredFlags(&requiredTestInput{}, nil)
	if err == nil {
		t.Fatalf("expected an error for a missing required field")
	}
	var usageErr usageError
	if !errors.As(err, &usageErr) {
		t.Fatalf("error is not a usageError: %v (%T)", err, err)
	}
	if got := err.Error(); got != "--budget-uuid is required" {
		t.Fatalf("error = %q, want it to name the flag", got)
	}
}

func TestCheckRequiredFlagsPassesWhenSet(t *testing.T) {
	if err := checkRequiredFlags(&requiredTestInput{BudgetUUID: "b-1"}, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckRequiredFlagsCanBeSatisfiedAfterJSONMerge(t *testing.T) {
	in := &requiredTestInput{}
	if err := applyCLIInputJSON(`{"BudgetUUID":"b-1"}`, in); err != nil {
		t.Fatalf("applyCLIInputJSON: %v", err)
	}
	if err := checkRequiredFlags(in, nil); err != nil {
		t.Fatalf("unexpected error after JSON supplied the required field: %v", err)
	}
}

// requiredNoFlagTestInput stands in for an Input whose required field is
// NoFlag (op.go), the shape project.ListProjectsInput.Region would have if
// it were ever marked required: no flag exists for it, so the error must
// name the Go field name a --cli-input-json key uses instead.
type requiredNoFlagTestInput struct {
	Region string `vngcloud:"required"`
}

// TestCheckRequiredFlagsNamesTheJSONFieldForANoFlagField checks that a
// required field marked NoFlag gets an error naming the JSON field ("Region"),
// never a flag ("--region") that flags.go never registered for it.
func TestCheckRequiredFlagsNamesTheJSONFieldForANoFlagField(t *testing.T) {
	err := checkRequiredFlags(&requiredNoFlagTestInput{}, map[string]bool{"Region": true})
	if err == nil {
		t.Fatalf("expected an error for a missing required NoFlag field")
	}
	got := err.Error()
	if got != "Region is required; set it with --cli-input-json" {
		t.Fatalf("error = %q, want it to name the JSON field", got)
	}
	if strings.Contains(got, "--region") {
		t.Fatalf("error = %q, names a flag that does not exist", got)
	}
}

func TestGlobalFlagNamesCoversEveryPersistentFlag(t *testing.T) {
	root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !globalFlagNames[f.Name] {
			t.Errorf("global flag %q is missing from globalFlagNames", f.Name)
		}
	})
}
