package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/pricing"
)

const estimateCaveat = "monthly, VAT as returned, account-scoped estimate; not a commitment"

func estimateRead() Op[pricing.Client] {
	op := Read("estimate", (*pricing.Client).Estimate, NoFlag("Servers", "Volumes", "PublicIPs", "Storage"))
	op.estimate = true
	op.short = "Estimate specified monthly setup costs"
	op.call = func(_ *cobra.Command, client *pricing.Client, ctx context.Context, in any) (any, error) {
		return client.Estimate(ctx, in.(*pricing.EstimateInput))
	}
	return op
}

func renderEstimate(stdout, stderr io.Writer, format, query string, out *pricing.EstimateOutput) error {
	if format != outputJSON || query != "" {
		if err := renderEstimateCoverage(stderr, out); err != nil {
			return err
		}
	}
	if format == outputJSON || query != "" {
		return renderOutput(stdout, format, query, out, false)
	}
	rows := make([]any, 0, len(out.Items))
	breakdown := []any{}
	for _, item := range out.Items {
		rows = append(rows, map[string]any{
			"Key": item.Key, "Kind": item.Kind, "Region": discoveryText(item.Region),
			"ZoneID": discoveryText(item.ZoneID), "Quantity": item.Quantity,
			"Status": item.Price.Status, "UnitMonthlyPrice": discoveryAmount(item.Price.MonthlyPrice),
			"MonthlyAmount": discoveryAmount(item.MonthlyAmount),
			"Currency":      discoveryText(item.Price.Currency), "Tax": discoveryText(item.Price.Tax),
		})
		if item.Kind == "server" {
			for _, charge := range item.Breakdown {
				breakdown = append(breakdown, map[string]any{
					"Key": item.Key, "Charge": charge.Name, "UnitMonthlyPrice": discoveryAmount(charge.MonthlyPrice),
					"Currency": discoveryText(item.Price.Currency), "Tax": discoveryText(item.Price.Tax),
				})
			}
		}
	}
	render := renderText
	if format == outputTable {
		render = renderTable
	}
	if err := render(stdout, rows); err != nil {
		return err
	}
	if len(breakdown) != 0 {
		if _, err := fmt.Fprintln(stdout, "Breakdown per unit (included in server quote)"); err != nil {
			return err
		}
		if err := render(stdout, breakdown); err != nil {
			return err
		}
	}
	for _, sum := range []struct {
		label string
		value *float64
	}{
		{"Known monthly subtotal", out.Total.KnownMonthlySubtotal},
		{"Requested monthly total (specified components only)", out.Total.RequestedMonthlyTotal},
	} {
		if _, err := fmt.Fprintf(stdout, "%s\t%s %s\t%s\n", sum.label, discoveryAmount(sum.value), escapeControlChars(out.Total.Currency), estimateCaveat); err != nil {
			return err
		}
	}
	return nil
}

func renderEstimateCoverage(w io.Writer, out *pricing.EstimateOutput) error {
	if _, err := fmt.Fprintln(w, estimateCaveat); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Processing complete: %t; requested complete: %t; cost complete: %t\n", out.Metadata.Complete, out.Total.RequestedComplete, out.Total.CostComplete); err != nil {
		return err
	}
	for _, coverage := range out.Coverage {
		if _, err := fmt.Fprintf(w, "Coverage\t%s\t%s\t%s\t%s\n", escapeControlChars(coverage.Topic), escapeControlChars(coverage.Scope), escapeControlChars(coverage.Status), escapeControlChars(coverage.Reason)); err != nil {
			return err
		}
	}
	for _, note := range out.Metadata.Notes {
		if _, err := fmt.Fprintf(w, "%s: %s\n", escapeControlChars(note.Topic), escapeControlChars(note.Text)); err != nil {
			return err
		}
	}
	return nil
}
