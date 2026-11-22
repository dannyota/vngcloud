package cli

const vatInclusivePriceNote = "Prices are VND totals that include VAT; the API gives no VAT breakdown."

const signedResizeQuoteNote = "OptimumPrice is the signed quoted adjustment in VND for the remaining period. " +
	"A negative value is an expected credit, not a confirmed refund. JSON, table, and text retain the minus sign."

const estimateHelp = `Estimate specified monthly setup costs without buying resources.

Supply a strict JSON setup with required Quantity on every row. Servers include
their root and bundled data disk; Volumes count additional disks independently.
Unverified IP kinds stay unknown. Known subtotal and nullable requested total
exclude usage and unsupported products. Inspect Coverage and RequestedComplete;
processing can succeed with unknown prices. Partial errors exit 1.

Prices are VND, with VAT as returned, for independent units in this account.
Quotes reserve no price or capacity and never represent a complete bill.
See the pricing CLI reference for the full JSON schema and examples.`

func init() {
	docOpNotes["pricing get-quote"] = vatInclusivePriceNote + " " +
		"Empty --action sends create. Only exact --action resize accepts a negative OptimumPrice, for any resource type. " +
		"Missing, null, malformed, and nonfinite prices are refused; zero is a valid quote read. " +
		"Responses are limited to 1 MiB. Quotes refuse redirects. " +
		"Malformed successful replies return InvalidResponse with the observed HTTP status. " + signedResizeQuoteNote
	docOpNotes["pricing estimate"] = estimateCommandNote
	docExampleOverride["pricing estimate"] = "vngcloud pricing estimate --cli-input-json file://setup.json --allow-partial --max-requests 100 --output table"
}

const estimateCommandNote = `Prices specified recurring components without ordering resources. Read-only
profiles can run this command. Quotes reserve neither prices nor capacity.
Quantities project independent monthly units, without bulk discounts or shared
allowances. All sums use VND, with VAT as returned; no extra VAT or discount is
applied. This is an account-scoped estimate, not a commitment or a complete bill.

Supply one JSON object, inline or with --cli-input-json file://setup.json.
Both forms have a 1 MiB cap. Files must be regular files; FIFOs and directories
refuse before authentication. Duplicate, unknown, and case-mismatched fields at
every depth, trailing values, and nesting beyond 16 levels refuse with exit 2.
JSON cannot supply credentials, endpoints, headers, or order options. Explicit
--allow-partial and --max-requests override valid JSON controls.

Each row needs a unique Key and Quantity from 1 to 1000. At most 1000 rows and
10000 summed quantities are allowed. Servers need explicit zone, flavor, image,
root size, and root type IDs. Pricing requires unlicensed Ubuntu with a usable
minimum root size; other images can remain unknown. Disabled zones are
inapplicable. Use returned encryption type keys, never encryption key material.
DataDiskSize and DataDiskTypeID specify one bundled disk per server unit,
already included in its quote. Volumes are additional disks counted once;
their Quantity is never multiplied by a server group's Quantity. ServerKey
records intended association only, requiring the same zone and an encrypted
server root for an encrypted associated volume. No attachment occurs.

PublicIPs Kind is public-vip, floating-ip, external-interface, or reservation.
Only public-vip accepts and requires Type: public, public-vm, or public-mkp.
Other IP products remain unknown, without a free-IP deduction. Storage uses an
exact returned Type, positive QuotaGB per package, and optional Region. Omitted
Region uses configured resolution, normally HCM04 for hcm-3 or HAN02 for han-1.

Example setup.json; replace synthetic selectors with returned catalog IDs:

` + "```json\n" + `{
  "Servers": [{"Key": "web", "Quantity": 2, "ZoneID": "zone-1",
    "FlavorID": "flavor-1", "ImageID": "image-1", "RootDiskSize": 20,
    "RootDiskTypeID": "type-1", "DataDiskSize": 30,
    "DataDiskTypeID": "type-1"}],
  "Volumes": [{"Key": "extra", "Quantity": 3, "ServerKey": "web",
    "ZoneID": "zone-1", "VolumeTypeID": "type-1", "Size": 10}],
  "PublicIPs": [{"Key": "ip", "Kind": "floating-ip", "Quantity": 2}],
  "Storage": [{"Key": "objects", "Quantity": 1, "Type": "Gold",
    "QuotaGB": 30}]
}
` + "```\n\n" + `JSON prints the full output. Table/text show unit prices, extended amounts,
quantities, and separate per-unit breakdowns labeled included in server quote.
Nil amounts display as unknown. Known monthly subtotal sums valid amounts;
Requested monthly total (specified components only) stays unknown unless every
requested row is quoted and processing completes. Display rounds to two
decimals only. Inspect Total.RequestedComplete and Coverage in automation;
Metadata.Complete describes processing, while Total.CostComplete is always
false. Exit 0 includes deliberate unknown and inapplicable results.

Bandwidth packages, vServer traffic, object egress/requests, software licenses,
NAT, load balancers, backups, snapshots, PAYG, and negotiated discounts/credits
remain unknown or excluded. Unspecified IP costs are excluded. Table/text and
every query, including JSON queries, print coverage and the estimate caveat on
stderr so a query cannot hide those limits.

Errors normally return no output. --allow-partial preserves surviving rows,
prints partial output on stdout and a structured error on stderr, and uses
exit 1 even with --query. Failed rows retain unknown amounts. Authentication,
throttling, cancellation, and runtime limits stop further work; invalid setup
input always refuses the whole call. Arithmetic overflow also uses exit 1.
The request budget defaults to 500, accepts 1..2000, and counts logical reads
and quotes rather than units or authentication. One invocation shares a
ten-minute deadline and serial request pacing. Shared authentication or cache
waits can outlast a caller deadline; cancellation is checked after acquisition.
See [Billing and Pricing](Billing-and-Pricing.md) for the SDK estimate.`
