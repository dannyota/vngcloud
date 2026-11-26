package cli

const endpointScopeHelp = "Supports Gateway endpoints in hcm-3 on an existing VPC and subnet, with private DNS off. " +
	"It needs SDK IAM-user login and default endpoints; anything else returns InvalidConfig (exit 2). " +
	"An endpoint changes network reachability; it does not grant bucket access and does not prove S3 connectivity."

const endpointOfferHelp = "Select catalog UUIDs with --availability-zone-id, --category-id, --service-id, and --package-id. " +
	"Omitted selectors default to HCM03-1C, the sole default category, vstorage.hcm03, and Standard. " +
	"The catalog and placement must offer the selection; lifecycle behavior for other offers remains unverified."

func init() {
	docOpNotes["network quote-create-endpoint"] = "Reads a fresh monthly quote in VND per month for one Gateway endpoint without creating it. " +
		"Read-only profiles can run this command; no --yes or --max-price is needed.\n" +
		endpointScopeHelp + "\n" + endpointOfferHelp + "\n" +
		"A quote is not a price lock and not proof of the debit."
	docOpNotes["network create-endpoint"] = "Buys one Gateway endpoint with one request and waits for ACTIVE. The body sets isEnableAutoRenew false to request auto-renew off, and private DNS stays off; " +
		"No endpoint read returns the renewal setting, and billing list-resources reported a created endpoint's RenewType as AUTO-RENEW despite that request. Check billing list-resources after create. Use a fresh, unique name for each create.\n\n" +
		endpointScopeHelp + "\n" + endpointOfferHelp + "\n" +
		"Requires --yes and --max-price (VND per month, or non-null JSON MaxPrice) before any request. Read-only profiles refuse this command. " +
		"The cap is compared to a fresh quote taken just before create; it is not a server price lock. Quote and debit can differ. " +
		"Unpriced or PriceAboveMax stops before purchase. A name that already exists in the VPC is refused.\n" +
		"Waits up to 15 minutes after the request, polling every 5 seconds; a shorter context deadline wins. " +
		"There is no --no-wait, DNS, renewal, tag, or raw request option.\n" +
		"NotSettled prints safe partial output on stdout, ignoring --query. After it, run network list-endpoints and network get-endpoint in the same scope, " +
		"then inspect billing. Do not repeat create while the outcome is unresolved. See [Network endpoints](Network-Endpoints.md)."
	docOpNotes["network delete-endpoint"] = "Requires --yes. Deleting an endpoint that a cluster uses breaks that cluster's private access. " +
		"The command refuses an endpoint with an attached resource or domain, but those fields do not prove that no cluster uses it.\n" +
		endpointScopeHelp + "\n" +
		"Requires explicit --endpoint-id and --vpc-id, refuses read-only profiles before any request, and sends one delete for an ACTIVE endpoint of the named VPC. " +
		"Waits up to 10 minutes, polling every 5 seconds, until a complete list omits the ID. There is no --no-wait or --force. " +
		"Deletion promises no refund, and a refund can stay pending after delete. In one probe, an endpoint that reused a just-deleted endpoint's name was deleted but had not been refunded hours after delete, and billing still listed it as active. Check billing list-transactions for the refund after delete. " +
		"NotFound exits 4. After NotSettled, run network list-endpoints in the same scope before another action. See [Network endpoints](Network-Endpoints.md)."
	docExampleOverride["network quote-create-endpoint"] = "vngcloud network quote-create-endpoint --region hcm-3 --name demo \\\n" +
		"  --vpc-id <net-id> --subnet-id <sub-id>"
	docExampleOverride["network create-endpoint"] = "vngcloud network create-endpoint --region hcm-3 --name demo \\\n" +
		"  --vpc-id <net-id> --subnet-id <sub-id> --max-price <VND-per-month> --yes"
	docExampleOverride["network delete-endpoint"] = "vngcloud network delete-endpoint --region hcm-3 \\\n" +
		"  --endpoint-id <enp-id> --vpc-id <net-id> --yes"
}
