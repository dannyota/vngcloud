package cli

const vatInclusivePriceNote = "Prices are VND totals that include VAT; the API gives no VAT breakdown."

func init() {
	docOpNotes["pricing get-quote"] = vatInclusivePriceNote
}
