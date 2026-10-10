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
}

func newCDNCmd(e *env) *cobra.Command {
	return Service(e, "cdn", "Published CDN IP ranges, vCDN certificates, and API keys", cdn.New, cdnOps...)
}
