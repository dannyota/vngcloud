package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/pricing"
)

// pricingOps is pricing's operation table. get-quote is a Read even though
// it sends an HTTP POST: kind follows the side effect, and a quote changes
// nothing and places no order.
var pricingOps = []Op[pricing.Client]{
	Read[pricing.Client, pricing.GetQuoteInput, pricing.GetQuoteOutput](
		kebab("GetQuote"), (*pricing.Client).GetQuote),
}

func newPricingCmd(e *env) *cobra.Command {
	return Service(e, "pricing", "Price quotes for resources", pricing.New, pricingOps...)
}
