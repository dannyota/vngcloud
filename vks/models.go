package vks

type Cluster struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Description          string `json:"description"`
	Status               string `json:"status"`
	ReleaseChannel       string `json:"releaseChannel"`
	Version              string `json:"version"`
	AZStrategy           string `json:"azStrategy"`
	CreatedAt            string `json:"createdAt"`
	UpdatedAt            string `json:"updatedAt"`
	NumNodes             int64  `json:"numNodes"`
	EnablePrivateCluster bool   `json:"enablePrivateCluster"`
}

type ClusterVersion struct {
	Version      string `json:"version"`
	Enable       bool   `json:"enable"`
	Stage        string `json:"stage"`
	DeprecatedAt string `json:"deprecatedAt"`
}

type Quota struct {
	MaxClusters             int `json:"maxClusters"`
	NumClusters             int `json:"numClusters"`
	MaxNodeGroupsPerCluster int `json:"maxNodeGroupsPerCluster"`
	MaxNodesPerNodeGroup    int `json:"maxNodesPerNodeGroup"`
}
