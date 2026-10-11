package pricing

// DiscoveryNotes returns an independent copy of reviewed pricing context.
// These sources describe coverage, not a fresh account quote or eligibility.
func DiscoveryNotes() []PricingNote {
	return []PricingNote{
		{Topic: "account-scope", Scope: "quotes", Text: "Quotes reflect the current account. Returned discounts do not establish eligibility for a program.", SourceURL: "https://docs.greennode.ai/partner-portal-user-guide/registration-of-partner-discount.md", Evidence: "published"},
		{Topic: "terms", Scope: "monthly", Text: "Tested monthly quotes showed no term discount on the observed account. This is not a universal discount policy or proof that a term was honored.", Evidence: "observed"},
		{Topic: "units", Scope: "compute", Text: "The byte definition of raw RAM is unverified. GiB amounts and rates remain unknown.", SourceURL: "https://docs.greennode.ai/vserver/compute-hcm03-1a/instance/flavor.md", Evidence: "unverified"},
		{Topic: "exclusions", Scope: "compute", Text: "Instance prices exclude root and data disks, CES, licensing, IPs, bandwidth, traffic, snapshots, backups, and NAT.", SourceURL: "https://docs.greennode.ai/vserver/compute-hcm03-1a/pricing.md", Evidence: "published"},
		{Topic: "effective-rate", Scope: "storage", Text: "Price per GB is the effective rate at the quoted size, not a marginal tariff.", SourceURL: "https://docs.greennode.ai/vstorage/object-storage/cach-tinh-phi.md", Evidence: "published"},
		{Topic: "egress", Scope: "object-storage", Text: "Published egress rules conflict. Egress and request charges remain unknown and are excluded from quota quotes.", SourceURL: "https://docs.greennode.ai/vstorage/object-storage/cach-tinh-phi.md", Evidence: "conflicting"},
		{Topic: "bandwidth", Scope: "network", Text: "Bandwidth tariffs and capacity units are unverified and excluded from public-IP prices.", SourceURL: "https://docs.greennode.ai/vserver/compute-hcm03-1a/vpc/bandwidth.md", Evidence: "unverified"},
	}
}
