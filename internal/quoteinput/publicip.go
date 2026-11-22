//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package quoteinput

func PublicIP(kind string) map[string]any {
	return map[string]any{"resourceType": "public-vip", "action": "create", "resourceInfo": map[string]any{"type": kind, "isBuyMorePo": false, "isPoc": false}}
}
