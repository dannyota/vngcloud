package cli

import (
	"github.com/spf13/cobra"

	"danny.vn/vngcloud/portal"
)

// portalOps is portal's operation table. Every Output here is map-backed
// (portal.UserInfo, portal.Zone, portal.Quota, portal.TagQuota), so its keys
// are the API's own names, not Go field names, and redact.go's redactMaps
// covers all four before render.go encodes them.
var portalOps = []Op[portal.Client]{
	Read[portal.Client, portal.GetUserInfoInput, portal.GetUserInfoOutput](
		kebab("GetUserInfo"), (*portal.Client).GetUserInfo),
	Read[portal.Client, portal.ListZonesInput, portal.ListZonesOutput](
		kebab("ListZones"), (*portal.Client).ListZones),
	Read[portal.Client, portal.ListQuotaUsedInput, portal.ListQuotaUsedOutput](
		kebab("ListQuotaUsed"), (*portal.Client).ListQuotaUsed),
	Read[portal.Client, portal.GetQuotaInput, portal.GetQuotaOutput](
		kebab("GetQuota"), (*portal.Client).GetQuota),
	Read[portal.Client, portal.GetTagQuotaInput, portal.GetTagQuotaOutput](
		kebab("GetTagQuota"), (*portal.Client).GetTagQuota),
}

func newPortalCmd(e *env) *cobra.Command {
	return Service(e, "portal", "Account info, zones, and quotas", portal.New, portalOps...)
}
