package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/cdn"
)

// cdnOps is cdn's operation table. ListIPRanges reads GreenNode's public FAQ
// page, not the account API, but it is a read like any other: a read-only
// profile allows it, and its Input takes no operation flags.
var cdnOps = []Op[cdn.Client]{
	Read[cdn.Client, cdn.ListIPRangesInput, cdn.ListIPRangesOutput](
		kebab("ListIPRanges"), (*cdn.Client).ListIPRanges),
}

func newCDNCmd(e *env) *cobra.Command {
	return Service(e, "cdn", "Published CDN IP ranges", cdn.New, cdnOps...)
}
