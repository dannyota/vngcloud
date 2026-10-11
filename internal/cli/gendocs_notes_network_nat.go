package cli

func init() {
	docOpNotes["network list-nat-instances"] = "Reads one page in `hcm-3` or `han-1`. Read-only profiles can run this command.\n" +
		"`--page` defaults to 1 and `--size` defaults to 10. No automatic paging occurs.\n" +
		"Live multi-page behavior remains unverified. An omitted `--zone-id` requires\n" +
		"a unique zone mapping for the selected region. Status describes provisioning.\n" +
		"See [Network NAT](Network-NAT.md) for fields and routing."
	docExampleOverride["network list-nat-instances"] = "vngcloud network list-nat-instances --page 1 --size 10 \\\n" +
		"  --query 'Items[].{ID:UUID,Name:NATName,Status:Status}' --output table"
}

const natScopeHelp = "--zone-id is the required region-level vNetwork ID. " +
	"vngcloud network list-v-network-regions lists it: use the uuid of the selected region's row."

const natPlacementHelp = "--availability-zone-id is a distinct availability-zone UUID, for example HAN01-1B, " +
	"from vngcloud network list-nat-zones. --package-id is the package UUID from list-nat-packages, " +
	"not the offer's separate packageId field."

const natWriteSupportHelp = "NAT writes support HAN (han-1) only and IAM-user login only. " +
	"Unsupported auth, region, or NAT V3 mode returns InvalidConfig (exit 2)."

func init() {
	docOpNotes["network list-nat-zones"] = "Reads the unpaged Public NAT availability-zone catalog. Read-only profiles can run this command.\n" +
		"--zone-id is the optional region-level vNetwork ID, discovered when omitted.\n" +
		"Use an enabled AVAILABILITY row's UUID as --availability-zone-id, not as --zone-id.\n" +
		"See [Network NAT](Network-NAT.md) for fields and routing."
	docOpNotes["network list-nat-packages"] = "Reads the unpaged Public NAT package catalog. Read-only profiles can run this command.\n" +
		"--zone-id is the optional region-level vNetwork ID, discovered when omitted.\n" +
		natPlacementHelp + "\nCatalog prices do not replace a fresh purchase quote."
	docOpNotes["network quote-create-nat-instance"] = "Reads a fresh one-month prepaid VND quote without placing an order. " +
		"Read-only profiles can run this command; no --yes or --max-price is needed.\n" +
		natScopeHelp + "\n" + natPlacementHelp + "\n" +
		"Requires NAT V3 enabledForAll mode. No subnet fallback exists. MaxPrice in JSON does not cap the quote."
	docOpNotes["network create-nat-instance"] = "Creates a 0.0.0.0/0 route in the named VPC. Every VM in that VPC uses this NAT for egress. " +
		"Use a disposable VPC for testing; never test in a production VPC.\n\n" +
		"The purchase starts with auto-renew enabled. After the NAT is ACTIVE, this command disables renewal through billing and " +
		"confirms it. The command waits for both steps. If it fails after purchase, renewal may remain enabled; inspect the NAT and billing before taking another action.\n\n" +
		natScopeHelp + "\n" + natPlacementHelp + "\n" + natWriteSupportHelp + "\n" +
		"Requires --yes and an explicit --max-price (or non-null JSON MaxPrice) before any request. Read-only profiles refuse this command. " +
		"Buys one month prepaid in VND, using a fresh quote. Unpriced or PriceAboveMax stops before purchase. The cap is not a server price lock. " +
		"Waits up to 15 minutes after the order attempt, including renewal confirmation; a shorter context deadline wins. " +
		"There is no --no-wait, period, renewal toggle, or raw request body.\n" +
		"NotSettled or WriteFailed prints safe partial output on stdout, ignoring --query. " +
		"Run network list-nat-instances in the same scope and billing list-resources, matching the known NAT ID. " +
		"If no NAT appears, inspect payment history without ordering again. If renewal is true or unknown, renewal may remain enabled; " +
		"use the billing console for a deliberate disable and confirm by a billing read. Do not repeat create while purchase or renewal is unresolved. " +
		"Deletion is a separate deliberate action. See [Network NAT](Network-NAT.md)."
	docOpNotes["network delete-nat-instance"] = "Requires --yes: egress can stop for every VM in the named VPC, and prior routes are not restored by the CLI.\n" +
		natScopeHelp + "\n" + natWriteSupportHelp + "\n" +
		"Requires explicit --vpc-id and --nat-id and refuses read-only profiles before any request. " +
		"Confirms the NAT belongs to the named VPC, then sends one delete. " +
		"Waits for absence, polling every 5 seconds for up to 10 minutes; a shorter context deadline wins. " +
		"--no-wait confirms acceptance only, not absence, refund, or route restoration. " +
		"NotFound exits 4. After NotSettled, inspect network list-nat-instances in the same scope before taking another action."
	docExampleOverride["network list-nat-zones"] = "vngcloud network list-nat-zones --region han-1 --output table"
	docExampleOverride["network list-nat-packages"] = "vngcloud network list-nat-packages --region han-1 --availability-zone-id HAN01-1B --output table"
	docExampleOverride["network quote-create-nat-instance"] = "vngcloud network list-v-network-regions --region han-1\n" +
		"vngcloud network list-nat-zones --region han-1\n" +
		"vngcloud network list-nat-packages --region han-1 --availability-zone-id HAN01-1B\n" +
		"vngcloud network quote-create-nat-instance --region han-1 --name example \\\n" +
		"  --zone-id <region-uuid> --availability-zone-id HAN01-1B \\\n" +
		"  --package-id <package-uuid> --vpc-id <disposable-vpc-id>"
	docExampleOverride["network create-nat-instance"] = "vngcloud network create-nat-instance --region han-1 --name example \\\n" +
		"  --zone-id <region-uuid> --availability-zone-id HAN01-1B \\\n" +
		"  --package-id <package-uuid> --vpc-id <disposable-vpc-id> --max-price <VND> --yes"
	docExampleOverride["network delete-nat-instance"] = "vngcloud network delete-nat-instance --region han-1 --zone-id <region-uuid> \\\n" +
		"  --vpc-id <disposable-vpc-id> --nat-id <nat-id> --yes"
}
