package cli

const discoveryOutputNote = "Returns Items and Metadata for account-scoped monthly quotes. " +
	"Unknown costs remain null in JSON and print unknown in table/text. Price per GB is the " +
	"effective rate at the quoted size, not a marginal tariff. Table/text notes print on stderr. " +
	"With --allow-partial, incomplete output prints before the error and exits 1, even with --query. " +
	"Sort before queries with --sort-by name, monthly-price, or unit-price and --descending. " +
	"Unknown numeric values sort last in either direction. Default MaxRequests is 500 (range 1..2000). " +
	"Catalog reads and quotes run serially with a ten-minute child deadline and at least 500 ms " +
	"between request starts. Quotes reserve no future price or capacity."

const volumeDiscoveryNote = discoveryOutputNote + " Size 0 uses each type's minimum. " +
	"Encryption defaults to all; none omits encryption from the quote, and an exact returned " +
	"key selects that encryption type. Disabled zones and sizes outside an offering's bounds " +
	"remain inapplicable rows without quotes. Quotes exclude snapshots, backups, and server CES."

const storageDiscoveryNote = discoveryOutputNote + " QuotaGB 0 uses the monthly package minimum. " +
	"Type is an exact catalog name. Region is a JSON-only storage region override; the global " +
	"--region still configures vServer. The default for hcm-3 is HCM04. PAYG, egress, and request " +
	"charges are excluded. Published egress rules conflict, so those costs remain unknown."
