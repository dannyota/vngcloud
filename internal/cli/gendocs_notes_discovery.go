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

const computeDiscoveryNote = discoveryOutputNote + " Family, platform, and name are exact matches. " +
	"RAM bounds compare raw catalog memory with unknown units. Use --sort-by ram for raw RAM " +
	"or unit-price for the raw RAM rate. GiB values remain unknown. Each quote reports only " +
	"the INSTANCE TYPE component of its recorded supported unlicensed Ubuntu reference and " +
	"3000 IOPS root configuration. Root disks, data disks, CES, licensing, IPs, bandwidth, " +
	"traffic, snapshots, backups, and NAT are excluded. Disabled zones remain inapplicable; " +
	"sold-out flavors remain visible unless --available-only. Missing reference support " +
	"leaves unknown prices. A refused reference fails without fallback. Non-public populated " +
	"custom and GPU branches remain fixture-gated."

func init() { docOpNotes["compute list-flavor-prices"] = computeDiscoveryNote }

const networkDiscoveryNote = "Returns Items and Metadata for account-scoped public-IP quotes. " +
	"Quotes public-vip types public, public-vm, and public-mkp separately without a period. " +
	"Equal amounts do not establish interchangeable products. IP-month rows retain unknown " +
	"Floating IP, external-interface, and reservation charges. Bandwidth tariffs and capacity " +
	"units remain unverified and excluded. NAT costs are excluded; use list-nat-zones, " +
	"list-nat-packages, and quote-create-nat-instance separately. Unknown costs are null in " +
	"JSON and print unknown in table/text. Table/text notes print on stderr. " +
	"With --allow-partial, incomplete output prints before the error and exits 1, even with " +
	"--query. Sort before queries with --sort-by name or monthly-price and --descending; " +
	"unknown numeric values sort last in either direction. MaxRequests defaults to 500 " +
	"(range 1..2000). Three quotes plus an uncached project read must fit before quotes start. " +
	"Requests run serially with a ten-minute child deadline and at least 500 ms between " +
	"starts, including retries. Quotes reserve no future price or capacity."

func init() { docOpNotes["network list-network-prices"] = networkDiscoveryNote }
