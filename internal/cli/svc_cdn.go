package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/cdn"
)

// cdnOps is cdn's operation table. ListIPRanges reads GreenNode's public FAQ
// page, not the account API, but it is a read like any other: a read-only
// profile allows it, and its Input takes no operation flags. The other
// reads call the vCDN API with the vCDN API key; the SDK drops the private
// keys and tokens those answers carry, so no read needs a Redact.
var cdnOps = []Op[cdn.Client]{
	Read[cdn.Client, cdn.ListIPRangesInput, cdn.ListIPRangesOutput](
		kebab("ListIPRanges"), (*cdn.Client).ListIPRanges),
	Read[cdn.Client, cdn.ListCertificatesInput, cdn.ListCertificatesOutput](
		kebab("ListCertificates"), (*cdn.Client).ListCertificates),
	Read[cdn.Client, cdn.GetCertificateInput, cdn.GetCertificateOutput](
		kebab("GetCertificate"), (*cdn.Client).GetCertificate),
	Read[cdn.Client, cdn.ListAPIKeysInput, cdn.ListAPIKeysOutput](
		kebab("ListAPIKeys"), (*cdn.Client).ListAPIKeys),
	Read[cdn.Client, cdn.ListWebAcceleratorsInput, cdn.ListWebAcceleratorsOutput](
		kebab("ListWebAccelerators"), (*cdn.Client).ListWebAccelerators),
	Read[cdn.Client, cdn.GetWebAcceleratorInput, cdn.GetWebAcceleratorOutput](
		kebab("GetWebAccelerator"), (*cdn.Client).GetWebAccelerator),
	Read[cdn.Client, cdn.GetTrafficInput, cdn.GetTrafficOutput](
		kebab("GetTraffic"), (*cdn.Client).GetTraffic, NoFlag("CDNDomains")),
	Read[cdn.Client, cdn.GetRequestRateInput, cdn.GetRequestRateOutput](
		kebab("GetRequestRate"), (*cdn.Client).GetRequestRate, NoFlag("CDNDomains")),
	Read[cdn.Client, cdn.GetCacheStatusInput, cdn.GetCacheStatusOutput](
		kebab("GetCacheStatus"), (*cdn.Client).GetCacheStatus, NoFlag("CDNDomains")),
	Read[cdn.Client, cdn.GetHTTPCodesInput, cdn.GetHTTPCodesOutput](
		kebab("GetHTTPCodes"), (*cdn.Client).GetHTTPCodes, NoFlag("CDNDomains")),
	Read[cdn.Client, cdn.GetTrafficReportInput, cdn.GetTrafficReportOutput](
		kebab("GetTrafficReport"), (*cdn.Client).GetTrafficReport, NoFlag("CDNDomains")),
	Write[cdn.Client, cdn.UpdateWebAcceleratorInput, cdn.UpdateWebAcceleratorOutput](
		kebab("UpdateWebAccelerator"), (*cdn.Client).UpdateWebAccelerator,
		WriteNoFlag("RemoveRuleActions", "FailOverErrorCodes", "CNames")),
	Write[cdn.Client, cdn.DeleteWebAcceleratorInput, cdn.DeleteWebAcceleratorOutput](
		kebab("DeleteWebAccelerator"), (*cdn.Client).DeleteWebAccelerator, Destructive()),
	Write[cdn.Client, cdn.EnableWebAcceleratorInput, cdn.EnableWebAcceleratorOutput](
		kebab("EnableWebAccelerator"), (*cdn.Client).EnableWebAccelerator),
	Write[cdn.Client, cdn.DisableWebAcceleratorInput, cdn.DisableWebAcceleratorOutput](
		kebab("DisableWebAccelerator"), (*cdn.Client).DisableWebAccelerator, Destructive()),
}

func newCDNCmd(e *env) *cobra.Command {
	return Service(e, "cdn", "CDN IP ranges and vCDN resources, analytics, and writes", cdn.New, cdnOps...)
}
