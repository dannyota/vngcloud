package globalloadbalancer

import "strings"

type stateClass int

const (
	stateUnknown stateClass = iota
	stateSuccess
	stateTransitional
	stateFailed
)

func createState(status string) stateClass {
	switch strings.ToUpper(status) {
	case "ACTIVE":
		return stateSuccess
	case "CREATING", "PENDING", "PROVISIONING", "UPDATING":
		return stateTransitional
	case "ERROR":
		return stateFailed
	}
	return stateUnknown
}

func memberHealth(status string) bool {
	switch strings.ToUpper(status) {
	case "ONLINE", "OFFLINE", "HEALTHY", "UNHEALTHY":
		return true
	}
	return false
}

func memberState(status string) stateClass {
	if memberHealth(status) {
		return stateSuccess
	}
	if c := createState(status); c == stateTransitional || c == stateFailed {
		return c
	}
	return stateUnknown
}

func deleteEligible(status string) bool {
	return strings.EqualFold(status, "ACTIVE")
}

func deleteInProgress(status string) bool {
	switch strings.ToUpper(status) {
	case "ACTIVE", "DELETING", "PENDING", "ONLINE", "OFFLINE", "HEALTHY", "UNHEALTHY":
		return true
	}
	return false
}

func parentEligible(status string) bool {
	return strings.EqualFold(status, "ACTIVE")
}

func regionMatches(configured string, region wireRegion) bool {
	return region.Status != nil && (*region.Status == "" || strings.EqualFold(*region.Status, "ACTIVE")) && strings.EqualFold(region.Name, configured) &&
		region.VServerEndpoint == "https://"+configured+".api.vngcloud.vn/vserver/vserver-gateway"
}

func packageSupports(p wirePackage, configured, regionID string, regions []wireRegion) bool {
	if p.Enabled == nil || !*p.Enabled {
		return false
	}
	matched := false
	ids := make(map[int]string)
	for _, v := range p.VLBPackages {
		if v.GLBPackageID != "" && v.GLBPackageID != p.ID {
			return false
		}
		index := -1
		for i, region := range regions {
			if v.Region != "" && (strings.EqualFold(v.Region, region.Name) || strings.EqualFold(v.Region, region.ID)) {
				if index != -1 {
					if strings.EqualFold(v.Region, configured) || strings.EqualFold(v.Region, regionID) {
						return false
					}
					index = -2
					break
				}
				index = i
			}
		}
		if index >= 0 && v.VLBPackageID != "" {
			if prior := ids[index]; prior != "" && prior != v.VLBPackageID {
				return false
			}
			ids[index] = v.VLBPackageID
		}
		if strings.EqualFold(v.Region, configured) || strings.EqualFold(v.Region, regionID) {
			matched = index >= 0
		}
	}
	return matched
}

const (
	wireProtocol     = "TCP"
	wireAlgorithm    = "ROUND_ROBIN"
	wireGroupType    = "PRIVATE"
	wireHealthy      = 3
	wireUnhealthy    = 3
	wireInterval     = 30
	wireHealthTime   = 5
	wireTimeoutCli   = 50
	wireTimeoutConn  = 5
	wireTimeoutMem   = 50
	wireTrafficDial  = 100
	wireWeight       = 1
	wireProviderText = "request rejected by the API"
)
