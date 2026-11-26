package globalloadbalancer

import (
	"encoding/json"
	"strings"
	"unicode"
)

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

func foldAlias(value string) string {
	return strings.Map(func(r rune) rune {
		minRune := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minRune {
				minRune = next
			}
		}
		return minRune
	}, value)
}

func packageSupports(p wirePackage, configured, regionID string, regions []wireRegion) bool {
	if p.Enabled == nil || !*p.Enabled {
		return false
	}
	aliases := make(map[string]int, len(regions)*2)
	for i, region := range regions {
		for _, value := range []string{region.Name, region.ID} {
			if value == "" {
				continue
			}
			key := foldAlias(value)
			if previous, ok := aliases[key]; ok && previous != i {
				aliases[key] = -1
			} else if !ok {
				aliases[key] = i
			}
		}
	}
	matched := false
	ids := make(map[int]string)
	for _, v := range p.VLBPackages {
		if v.GLBPackageID != nil {
			var id string
			if json.Unmarshal(v.GLBPackageID, &id) != nil || id != p.ID {
				return false
			}
		}
		index, found := aliases[foldAlias(v.Region)]
		if v.VLBPackageID != "" {
			if !found || index < 0 {
				return false
			}
			if prior := ids[index]; prior != "" && prior != v.VLBPackageID {
				return false
			}
			ids[index] = v.VLBPackageID
		}
		if strings.EqualFold(v.Region, configured) || strings.EqualFold(v.Region, regionID) {
			if !found || index < 0 {
				return false
			}
			matched = true
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
