//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package quoteinput

type StorageBody struct {
	ResourceType string      `json:"resourceType"`
	Action       string      `json:"action"`
	ResourceInfo StorageInfo `json:"resourceInfo"`
}

type StorageInfo struct {
	Quota          int64 `json:"quota"`
	PurchaseTypeID int   `json:"purchaseTypeId"`
	ProjectType    int   `json:"projectType"`
}

func Storage(quota int64, purchaseTypeID, projectTypeID int) StorageBody {
	return StorageBody{ResourceType: "object_storage", Action: "create", ResourceInfo: StorageInfo{Quota: quota, PurchaseTypeID: purchaseTypeID, ProjectType: projectTypeID}}
}
