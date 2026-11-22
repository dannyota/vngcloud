package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/pricing"
)

func estimateTestOutput(state string) *pricing.EstimateOutput {
	out := &pricing.EstimateOutput{
		Items: []pricing.EstimateItem{
			{Key: "web", Kind: "server", Region: "han-1", ZoneID: "zone-1", Quantity: 2,
				Price: pricing.DiscoveryPrice{Status: "quoted", Currency: "VND", Tax: "as-returned", MonthlyPrice: vngcloud.Ptr(100.125)}, MonthlyAmount: vngcloud.Ptr(200.25),
				Breakdown: []pricing.EstimateCharge{{Name: "ROOT DISK", MonthlyPrice: vngcloud.Ptr(20.0)}, {Name: "CES\n\t\x1b", MonthlyPrice: nil}}},
			{Key: "data", Kind: "volume", ServerKey: "web", Region: "han-1", ZoneID: "zone-1", Quantity: 3,
				Price: pricing.DiscoveryPrice{Status: "quoted", Currency: "VND", Tax: "as-returned", MonthlyPrice: vngcloud.Ptr(10.0)}, MonthlyAmount: vngcloud.Ptr(30.0)},
		},
		Total: pricing.EstimateTotal{KnownMonthlySubtotal: vngcloud.Ptr(230.25), RequestedMonthlyTotal: vngcloud.Ptr(230.25), RequestedComplete: true,
			Currency: "VND", Tax: "as-returned", Basis: "sum-of-independent-monthly-quotes", Scope: "specified-recurring-components"},
		Coverage: []pricing.EstimateCoverage{{Topic: "vserver-traffic", Scope: "web", Status: "unknown", Reason: "usage-not-priced"}, {Topic: "nat", Scope: "setup", Status: "excluded", Reason: "outside-estimate-scope"}},
		Metadata: pricing.DiscoveryMetadata{Region: "han-1", Complete: true, QuotedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Failures: []pricing.DiscoveryFailure{}, Notes: []pricing.PricingNote{{Topic: "quantity", Text: "Independent unit quotes; no bulk discounts."}}},
	}
	switch state {
	case "partial":
		out.Items[1].Price = pricing.DiscoveryPrice{Status: "failed"}
		out.Items[1].MonthlyAmount = nil
		out.Total.KnownMonthlySubtotal = vngcloud.Ptr(200.25)
		out.Total.RequestedMonthlyTotal = nil
		out.Total.RequestedComplete = false
		out.Metadata.Complete = false
		out.Metadata.Failures = []pricing.DiscoveryFailure{{Operation: "pricing.Estimate", ItemKey: "data", Code: "InternalServerError", Status: 500}}
	case "unknown":
		out.Items = []pricing.EstimateItem{{Key: "ip", Kind: "floating-ip", Region: "han-1", Quantity: 2, Price: pricing.DiscoveryPrice{Status: "unknown"}}}
		out.Total.KnownMonthlySubtotal = nil
		out.Total.RequestedMonthlyTotal = nil
		out.Total.RequestedComplete = false
	}
	return out
}

func estimateTestCommand(t *testing.T, stdout, stderr io.Writer, format, query string, out *pricing.EstimateOutput, callErr error) *cobra.Command {
	t.Helper()
	newSvcRoot(t, newSvcFixture(nil))
	for _, op := range pricingOps {
		if op.name != "estimate" {
			continue
		}
		op.call = func(_ *cobra.Command, _ *pricing.Client, _ context.Context, _ any) (any, error) { return out, callErr }
		e := &env{stdout: stdout, stderr: stderr, flags: &globalFlags{region: "hcm-3", output: format, query: query}}
		cmd := newOpCmd(e, "pricing", pricing.New, op)
		cmd.SetOut(stdout)
		cmd.SetErr(stderr)
		cmd.SetArgs([]string{"--cli-input-json", unknownEstimateSpec})
		return cmd
	}
	t.Fatal("pricing estimate is not registered")
	return nil
}

func TestGoldenEstimate(t *testing.T) {
	for _, state := range []string{"full", "partial", "unknown"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(state+"/"+format, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				var callErr error
				if state == "partial" {
					callErr = &pricing.DiscoveryError{Errors: []error{vngcloud.ErrAuth}}
				}
				cmd := estimateTestCommand(t, &stdout, &stderr, format, "", estimateTestOutput(state), callErr)
				err := cmd.ExecuteContext(context.Background())
				if !errors.Is(err, callErr) || (state == "partial" && exitCode(err) != 1) {
					t.Fatalf("error=%v want=%v", err, callErr)
				}
				if format != "json" && (!strings.Contains(stderr.String(), "usage-not-priced") || !strings.Contains(stderr.String(), "not a commitment")) {
					t.Fatalf("missing coverage/caveat: %s", &stderr)
				}
				name := "pricing-estimate"
				if state != "full" {
					name += "-" + state
				}
				path := goldenPath(name + "." + format + ".golden")
				if *updateGolden {
					if err := os.WriteFile(path, stdout.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, stdout.Bytes()) {
					t.Fatalf("golden mismatch %s:\n%s", path, &stdout)
				}
			})
		}
	}
}

func TestEstimateQueryAndRenderPreservePartialFailure(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"Total.KnownMonthlySubtotal", "abs(Items)"} {
			t.Run(format+"/"+query, func(t *testing.T) {
				var stdout, stderr bytes.Buffer
				cause := &pricing.DiscoveryError{Errors: []error{vngcloud.ErrNotFound}}
				cmd := estimateTestCommand(t, &stdout, &stderr, format, query, estimateTestOutput("partial"), cause)
				err := cmd.ExecuteContext(context.Background())
				if !errors.Is(err, cause) || exitCode(err) != 1 || !strings.Contains(stderr.String(), "usage-not-priced") || !strings.Contains(stderr.String(), "not a commitment") {
					t.Fatalf("err=%v stderr=%s", err, &stderr)
				}
			})
		}
	}
	for _, partial := range []bool{false, true} {
		var stderr bytes.Buffer
		var cause error
		if partial {
			cause = &pricing.DiscoveryError{Errors: []error{vngcloud.ErrAuth}}
		}
		cmd := estimateTestCommand(t, discoveryFailWriter{}, &stderr, "text", "", estimateTestOutput("full"), cause)
		err := cmd.ExecuteContext(context.Background())
		if err == nil || exitCode(err) != 1 || (partial && !errors.Is(err, cause)) {
			t.Fatalf("render failure: %v", err)
		}
	}
}
