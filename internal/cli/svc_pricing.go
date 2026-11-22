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
	return Service(e, "pricing", "Price quotes for resources", pricing.New, pricingOps...)
}
