//go:build livewrite

package livetest_test

import (
	"errors"
	"math"
	"strconv"

	"danny.vn/vngcloud/portal"
)

const natHCMCap = 720000

func natRegion(value string) (string, error) {
	switch value {
	case "", "han-1":
		return "han-1", nil
	case "hcm-3":
		return value, nil
	default:
		return "", errors.New("invalid NAT region")
	}
}

func natPreflight(region, value string, hold bool) (float64, error) {
	if region == "hcm-3" && (value == "" || !hold) {
		return 0, errors.New("HCM requires a cap and hold directory")
	}
	cap, err := natMaxPrice(value)
	if err != nil || (region == "hcm-3" && cap > natHCMCap) {
		return 0, errors.New("invalid NAT cap")
	}
	return cap, nil
}

func natCreditCap(cap, credit float64) (float64, error) {
	if math.IsNaN(credit) || math.IsInf(credit, 0) || credit <= 0 {
		return 0, errors.New("invalid available credit")
	}
	return min(float64(natHCMCap), cap, credit), nil
}

func natQuotaInteger(value any) (uint64, bool) {
	switch n := value.(type) {
	case string:
		parsed, err := strconv.ParseUint(n, 10, 53)
		return parsed, err == nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1<<53-1 || math.Trunc(n) != n {
			return 0, false
		}
		return uint64(n), true
	default:
		return 0, false
	}
}

func natQuotaAvailable(rows []portal.Quota) bool {
	found := false
	available := false
	for _, row := range rows {
		if row["quotaName"] != "VPC" {
			continue
		}
		if found {
			return false
		}
		found = true
		used, usedOK := natQuotaInteger(row["used"])
		limit, limitOK := natQuotaInteger(row["limit"])
		available = usedOK && limitOK && limit > 0 && used < limit
	}
	return found && available
}
