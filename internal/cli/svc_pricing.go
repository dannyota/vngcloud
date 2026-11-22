package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/pricing"
)

var pricingOps = []Op[pricing.Client]{
	Read[pricing.Client, pricing.GetQuoteInput, pricing.GetQuoteOutput](
		kebab("GetQuote"), (*pricing.Client).GetQuote),
	estimateRead(),
}

func newPricingCmd(e *env) *cobra.Command {
	cmd := Service(e, "pricing", "Price quotes for resources", pricing.New, pricingOps...)
	for _, child := range cmd.Commands() {
		if child.Name() == "get-quote" {
			child.Long = docOpNotes["pricing get-quote"]
		}
	}
	return cmd
}
