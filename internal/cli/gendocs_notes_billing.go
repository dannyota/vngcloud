package cli

const billingListResourcesNote = "Lists billing resource renewal state across products without quoting prices. " +
	"Tables show raw renewal and billing timestamps in epoch milliseconds. Cost has unknown meaning " +
	"and currency; it is not a price or next charge and is omitted from the table. " +
	"Use storage get-project-auto-renew for a project's joined state and fresh price estimate."

const billingHistoryNote = `Reads account history. Requests send no project or region filters.
JSON, text, and explicit queries can contain account data, including
descriptions and references. These reads work with read-only profiles and
need no consent prompt.

Dates are inclusive YYYY-MM-DD in fixed UTC+07:00, a manager choice fitting
observed filtering; empty end means today. The wire end is the next calendar
day. Size defaults to 50 and is capped at 50. --all starts at page 1 and
rejects --page above 1. Queries use Items and the Page, PageSize, TotalPage,
and TotalItem metadata.

A complete walk is limited to 100 pages, at most 100 times the verified page
size rows, 8 MiB per response, 32 MiB of decoded JSON values, and two minutes
including authentication and retries. Surrounding whitespace does not count
toward the walk limit. Changed coverage or a walk limit returns
HistoryIncomplete, exit 1, with no output. A complete walk is not an atomic
account snapshot; a narrower range may help but cannot guarantee success.

Values retain the API's monetary basis; historical VAT normalization and
service-account access are unverified. Runtime query failures return
QueryFailed, exit 1, with no report.`

func init() {
	docOpNotes["billing list-resources"] = billingListResourcesNote
	docOpNotes["billing list-transactions"] = billingHistoryNote + "\n\nDefault table columns: CreatedAt, Type, TypeCode, Direction, Amount, Balance."
	docOpNotes["billing list-payments"] = billingHistoryNote + "\n\nDefault table columns: CreatedAt, Gateway, Status, TypeCode, Amount.\nGateway, status, and payment type codes retain unknown values."
	docExampleOverride["billing list-transactions"] = "vngcloud billing list-transactions --start-date 2024-11-01 \\\n  --end-date 2024-11-30 --all \\\n  --query 'Items[].{Time:CreatedAt,Type:Type,Amount:Amount}' \\\n  --output table"
	docExampleOverride["billing list-payments"] = "vngcloud billing list-payments --start-date 2024-11-01 \\\n  --end-date 2024-11-30 \\\n  --query 'Items[].{Time:CreatedAt,Gateway:Gateway,Amount:Amount}' \\\n  --output table"
}
