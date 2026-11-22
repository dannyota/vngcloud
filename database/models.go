package database

// EngineVersion is a documented datastore catalog entry.
type EngineVersion struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	VersionName string `json:"versionName"`
	Version     string `json:"version"`
	LicenseName string `json:"licenseName"`
}

// Zone is a documented database zone catalog entry.
type Zone struct {
	UUID     string `json:"uuid"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	ZoneType string `json:"zoneType"`
}

// Flavor is a documented database flavor catalog entry.
type Flavor struct {
	ID           int32  `json:"id"`
	Name         string `json:"name"`
	FlavorID     string `json:"flavorId"`
	VCPUs        int32  `json:"vcpus"`
	RAM          int32  `json:"ram"`
	PackageSKU   string `json:"packageSku"`
	LocateZoneID string `json:"locateZoneId"`
}

// StorageType is a documented database volume type catalog entry.
type StorageType struct {
	ID               int32  `json:"id"`
	Type             string `json:"type"`
	MinVolumeSize    int32  `json:"minVolumeSize"`
	MaxVolumeSize    int32  `json:"maxVolumeSize"`
	ZoneID           string `json:"zoneId"`
	VolumeTypeZoneID string `json:"volumeTypeZoneId"`
	VolumeTypeSKU    string `json:"volumeTypeSku"`
}

// Subnet is a documented database network subnet.
type Subnet struct {
	UUID        string `json:"uuid"`
	NetworkUUID string `json:"networkUuid"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	ZoneID      string `json:"zoneId"`
}

// Network is a documented database network catalog entry.
type Network struct {
	UUID        string   `json:"uuid"`
	DisplayName string   `json:"displayName"`
	Status      string   `json:"status"`
	ZoneID      string   `json:"zoneId"`
	Subnets     []Subnet `json:"subnets"`
}

// Instance is a documented non-secret projection of a relational database.
type Instance struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Status           string   `json:"status"`
	DatastoreType    string   `json:"datastoreType"`
	DatastoreVersion string   `json:"datastoreVersion"`
	ZoneID           string   `json:"zoneId"`
	ProjectID        string   `json:"projectId"`
	SubnetID         string   `json:"subnetId"`
	VolumeType       string   `json:"volumeType"`
	VolumeSize       int32    `json:"volumeSize"`
	QuotaPackageID   string   `json:"quotaPackageId"`
	PublicAccess     *bool    `json:"publicAccess"`
	BackupAuto       *bool    `json:"backupAuto"`
	Owner            *bool    `json:"owner"`
	Role             string   `json:"role"`
	ReplicaSourceID  string   `json:"replicaSourceId"`
	Replicas         []string `json:"replicas"`
	Deleted          *bool    `json:"deleted"`
}

// Page contains the pagination metadata documented by the service.
type Page struct {
	TotalPages    int32 `json:"totalPages"`
	TotalElements int32 `json:"totalElements"`
	Size          int32 `json:"size"`
	Number        int32 `json:"number"`
	MaxSize       int32 `json:"maxSize"`
}

// ListDatabaseEngineVersionsInput selects the datastore catalog.
type ListDatabaseEngineVersionsInput struct{}

// ListDatabaseEngineVersionsOutput contains the datastore catalog.
type ListDatabaseEngineVersionsOutput struct{ Items []EngineVersion }

// ListDatabaseZonesInput selects the database zones catalog.
type ListDatabaseZonesInput struct{}

// ListDatabaseZonesOutput contains the database zones catalog.
type ListDatabaseZonesOutput struct{ Items []Zone }

// ListDatabaseFlavorsInput selects flavors for an engine and zone.
type ListDatabaseFlavorsInput struct {
	// Engine is the datastore type.
	Engine string `vngcloud:"required"`
	// EngineVersion is the datastore version.
	EngineVersion string `vngcloud:"required"`
	// ZoneID identifies the selected database zone.
	ZoneID string `vngcloud:"required"`
}

// ListDatabaseFlavorsOutput contains the matching flavors.
type ListDatabaseFlavorsOutput struct{ Items []Flavor }

// ListDatabaseStorageTypesInput selects storage types for a zone.
type ListDatabaseStorageTypesInput struct {
	// ZoneID identifies the selected database zone.
	ZoneID string `vngcloud:"required"`
}

// ListDatabaseStorageTypesOutput contains the matching storage types.
type ListDatabaseStorageTypesOutput struct {
	ProjectID string
	Items     []StorageType
}

// ListDatabaseNetworksInput selects networks for a zone.
type ListDatabaseNetworksInput struct {
	// ZoneID identifies the selected database zone.
	ZoneID string `vngcloud:"required"`
}

// ListDatabaseNetworksOutput contains the matching networks.
type ListDatabaseNetworksOutput struct{ Items []Network }

// ListDatabaseInstancesInput selects one page of database instances.
type ListDatabaseInstancesInput struct {
	// PageNumber is the one-based page number; zero selects one.
	PageNumber int32
	// PageSize is the requested page size; zero selects 100.
	PageSize int32
}

// ListDatabaseInstancesOutput contains one page of database instances.
type ListDatabaseInstancesOutput struct {
	ProjectID string
	Items     []Instance
	Page      Page
}

// GetDatabaseInstanceInput selects an instance by ID.
type GetDatabaseInstanceInput struct {
	// ID is the exact instance ID.
	ID string `vngcloud:"required"`
}

// GetDatabaseInstanceOutput contains the requested instance.
type GetDatabaseInstanceOutput struct{ Instance Instance }
