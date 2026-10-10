package cli

const billingListResourcesNote = "Lists billing resource renewal state across products without quoting prices. " +
	"Tables show raw renewal and billing timestamps in epoch milliseconds. Cost has unknown meaning " +
	"and currency; it is not a price or next charge and is omitted from the table. " +
	"Use storage get-project-auto-renew for a project's joined state and fresh price estimate."

func init() {
	docOpNotes["billing list-resources"] = billingListResourcesNote
}
