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

func deleteEligible(status string) bool {
	return strings.EqualFold(status, "ACTIVE")
}

func deleteInProgress(status string) bool {
	switch strings.ToUpper(status) {
	case "ACTIVE", "DELETING", "PENDING":
		return true
	}
	return false
}

func parentEligible(status string) bool {
	return strings.EqualFold(status, "ACTIVE")
}

func regionMatches(configured string, region wireRegion) bool {
	return (region.Status == "" || strings.EqualFold(region.Status, "ACTIVE")) && strings.EqualFold(region.Name, configured) &&
		region.VServerEndpoint == "https://"+configured+".api.vngcloud.vn/vserver/vserver-gateway"
}

func packageSupports(p wirePackage, configured, regionID string) bool {
	if p.Enabled == nil || !*p.Enabled {
		return false
	}
	for _, v := range p.VLBPackages {
		if strings.EqualFold(v.Region, configured) || strings.EqualFold(v.Region, regionID) {
			return true
		}
	}
	return false
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
