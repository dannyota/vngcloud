package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/dns"
)

// dnsOps is dns's operation table. CreateHostedZone and UpdateHostedZone are
// Write; DeleteHostedZone is Write and Destructive: a deleted zone and its
// history cannot be restored by one more command, so it needs --yes. A
// read-only profile refuses all three, before any request. VPCIDs (on both
// CreateHostedZoneInput and UpdateHostedZoneInput) has no flag-settable
// type, so it reaches a command only through --cli-input-json; every other
// field of the three Inputs gets a flag from flags.go's reflection. Record
// writes (CreateRecord, UpdateRecord, DeleteRecord) are not part of this
// release.
var dnsOps = []Op[dns.Client]{
	Read[dns.Client, dns.ListHostedZonesInput, dns.ListHostedZonesOutput](
		kebab("ListHostedZones"), (*dns.Client).ListHostedZones),
	Read[dns.Client, dns.GetHostedZoneInput, dns.GetHostedZoneOutput](
		kebab("GetHostedZone"), (*dns.Client).GetHostedZone),
	Write[dns.Client, dns.CreateHostedZoneInput, dns.CreateHostedZoneOutput](
		kebab("CreateHostedZone"), (*dns.Client).CreateHostedZone),
	Write[dns.Client, dns.UpdateHostedZoneInput, dns.UpdateHostedZoneOutput](
		kebab("UpdateHostedZone"), (*dns.Client).UpdateHostedZone),
	Write[dns.Client, dns.DeleteHostedZoneInput, dns.DeleteHostedZoneOutput](
		kebab("DeleteHostedZone"), (*dns.Client).DeleteHostedZone, Destructive()),
	Read[dns.Client, dns.ListRecordsInput, dns.ListRecordsOutput](
		kebab("ListRecords"), (*dns.Client).ListRecords),
	Read[dns.Client, dns.GetRecordInput, dns.GetRecordOutput](
		kebab("GetRecord"), (*dns.Client).GetRecord),
}

func newDNSCmd(e *env) *cobra.Command {
	return Service(e, "dns", "Hosted zones and DNS records", dns.New, dnsOps...)
}
