package cli

const vatInclusivePriceNote = "Prices are VND totals that include VAT; the API gives no VAT breakdown."

const signedResizeQuoteNote = "OptimumPrice is the signed quoted adjustment in VND for the remaining period. " +
	"A negative value is an expected credit, not a confirmed refund. JSON, table, and text retain the minus sign."

func init() {
	docOpNotes["pricing get-quote"] = vatInclusivePriceNote + " " +
		"Empty --action sends create. Only exact --action resize accepts a negative OptimumPrice, for any resource type. " +
		"Missing, null, malformed, and nonfinite prices are refused; zero is a valid quote read. " +
		"Responses are limited to 1 MiB. " + signedResizeQuoteNote
}
