package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/dns"
)

// dnsOps is dns's operation table. CreateHostedZone, UpdateHostedZone,
// CreateRecord, and UpdateRecord are Write; DeleteHostedZone and
// DeleteRecord are Write and Destructive: a deleted zone or record and its
// history cannot be restored by one more command, so each needs --yes. A
// read-only profile refuses all six, before any request. UpdateHostedZoneInput's
// VPCIDs (a pointer to a slice) and Values on both CreateRecordInput and
// UpdateRecordInput have no flag-settable type, so each reaches a command
// only through --cli-input-json. CreateHostedZoneInput's own VPCIDs is
// []string, a type flags.go can bind a repeatable flag to (compute's
// CreateServerInput.SecurityGroupIDs needs it), but WriteNoFlag keeps it
// JSON-only here too, matching UpdateHostedZoneInput's own VPCIDs and since
// giving create-hosted-zone a new flag is outside the design that added
// []string support. Every other field of the six Inputs gets a flag from
// flags.go's reflection.
var dnsOps = []Op[dns.Client]{
	Read[dns.Client, dns.ListHostedZonesInput, dns.ListHostedZonesOutput](
		kebab("ListHostedZones"), (*dns.Client).ListHostedZones),
	Read[dns.Client, dns.GetHostedZoneInput, dns.GetHostedZoneOutput](
		kebab("GetHostedZone"), (*dns.Client).GetHostedZone),
	Write[dns.Client, dns.CreateHostedZoneInput, dns.CreateHostedZoneOutput](
		kebab("CreateHostedZone"), (*dns.Client).CreateHostedZone, WriteNoFlag("VPCIDs")),
	Write[dns.Client, dns.UpdateHostedZoneInput, dns.UpdateHostedZoneOutput](
		kebab("UpdateHostedZone"), (*dns.Client).UpdateHostedZone),
	Write[dns.Client, dns.DeleteHostedZoneInput, dns.DeleteHostedZoneOutput](
		kebab("DeleteHostedZone"), (*dns.Client).DeleteHostedZone, Destructive()),
	Read[dns.Client, dns.ListRecordsInput, dns.ListRecordsOutput](
		kebab("ListRecords"), (*dns.Client).ListRecords),
	Read[dns.Client, dns.GetRecordInput, dns.GetRecordOutput](
		kebab("GetRecord"), (*dns.Client).GetRecord),
	Write[dns.Client, dns.CreateRecordInput, dns.CreateRecordOutput](
		kebab("CreateRecord"), (*dns.Client).CreateRecord),
	Write[dns.Client, dns.UpdateRecordInput, dns.UpdateRecordOutput](
		kebab("UpdateRecord"), (*dns.Client).UpdateRecord),
	Write[dns.Client, dns.DeleteRecordInput, dns.DeleteRecordOutput](
		kebab("DeleteRecord"), (*dns.Client).DeleteRecord, Destructive()),
}

func newDNSCmd(e *env) *cobra.Command {
	return Service(e, "dns", "Hosted zones and DNS records", dns.New, dnsOps...)
}
