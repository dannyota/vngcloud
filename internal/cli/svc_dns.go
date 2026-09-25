package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/dns"
)

// dnsOps is dns's operation table. Every operation reads; dns record writes
// are not part of this release.
var dnsOps = []Op[dns.Client]{
	Read[dns.Client, dns.ListHostedZonesInput, dns.ListHostedZonesOutput](
		kebab("ListHostedZones"), (*dns.Client).ListHostedZones),
	Read[dns.Client, dns.GetHostedZoneInput, dns.GetHostedZoneOutput](
		kebab("GetHostedZone"), (*dns.Client).GetHostedZone),
	Read[dns.Client, dns.ListRecordsInput, dns.ListRecordsOutput](
		kebab("ListRecords"), (*dns.Client).ListRecords),
	Read[dns.Client, dns.GetRecordInput, dns.GetRecordOutput](
		kebab("GetRecord"), (*dns.Client).GetRecord),
}

func newDNSCmd(e *env) *cobra.Command {
	return Service(e, "dns", dns.New, dnsOps...)
}
