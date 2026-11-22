package pricing

import (
	"fmt"
	"regexp"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/pricecatalog"
)

var estimateKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func estimateInvalid(field string) error {
	return fmt.Errorf("%w: pricing.Estimate: invalid %s", core.ErrInvalidInput, field)
}

func validateEstimate(in *EstimateInput, region string) error {
	if in == nil {
		return estimateInvalid("setup")
	}
	if in.MaxRequests < 0 || in.MaxRequests > 2000 {
		return estimateInvalid("MaxRequests")
	}
	rows := 0
	for _, n := range []int{len(in.Servers), len(in.Volumes), len(in.PublicIPs), len(in.Storage)} {
		if n > 1000-rows {
			return estimateInvalid("component count")
		}
		rows += n
	}
	if rows == 0 {
		return estimateInvalid("setup")
	}
	keys := map[string]bool{}
	quantity := 0
	check := func(key string, n int) error {
		if !estimateKeyPattern.MatchString(key) || keys[key] {
			return estimateInvalid("Key")
		}
		keys[key] = true
		if n < 1 || n > 1000 || n > 10000-quantity {
			return estimateInvalid("Quantity")
		}
		quantity += n
		return nil
	}
	servers := map[string]EstimateServer{}
	for _, s := range in.Servers {
		if err := check(s.Key, s.Quantity); err != nil {
			return err
		}
		for field, id := range map[string]string{"ZoneID": s.ZoneID, "FlavorID": s.FlavorID, "ImageID": s.ImageID, "RootDiskTypeID": s.RootDiskTypeID} {
			if core.CheckPathID("pricing.Estimate", field, id) != nil {
				return estimateInvalid(field)
			}
		}
		if s.RootDiskSize <= 0 || s.DataDiskSize < 0 || (s.DataDiskSize > 0) != (s.DataDiskTypeID != "") {
			return estimateInvalid("disk size/type pair")
		}
		if s.DataDiskTypeID != "" && core.CheckPathID("pricing.Estimate", "DataDiskTypeID", s.DataDiskTypeID) != nil {
			return estimateInvalid("DataDiskTypeID")
		}
		if s.DataDiskEncryptionTypeID != "" && s.DataDiskSize == 0 {
			return estimateInvalid("DataDiskEncryptionTypeID")
		}
		for _, id := range []string{s.RootDiskEncryptionTypeID, s.DataDiskEncryptionTypeID} {
			if id != "" && core.CheckTypeID("pricing.Estimate", "encryption", id) != nil {
				return estimateInvalid("encryption")
			}
		}
		servers[s.Key] = s
	}
	for _, v := range in.Volumes {
		if err := check(v.Key, v.Quantity); err != nil {
			return err
		}
		if v.Size <= 0 {
			return estimateInvalid("Size")
		}
		if core.CheckPathID("pricing.Estimate", "ZoneID", v.ZoneID) != nil || core.CheckPathID("pricing.Estimate", "VolumeTypeID", v.VolumeTypeID) != nil {
			return estimateInvalid("volume selectors")
		}
		if v.EncryptionTypeID != "" && core.CheckTypeID("pricing.Estimate", "EncryptionTypeID", v.EncryptionTypeID) != nil {
			return estimateInvalid("EncryptionTypeID")
		}
		if v.ServerKey != "" {
			s, ok := servers[v.ServerKey]
			if !ok || v.ZoneID != s.ZoneID || (v.EncryptionTypeID != "" && s.RootDiskEncryptionTypeID == "") {
				return estimateInvalid("ServerKey association")
			}
		}
	}
	for _, ip := range in.PublicIPs {
		if err := check(ip.Key, ip.Quantity); err != nil {
			return err
		}
		switch ip.Kind {
		case "public-vip":
			if ip.Type != "public" && ip.Type != "public-vm" && ip.Type != "public-mkp" {
				return estimateInvalid("IP Type")
			}
		case "floating-ip", "external-interface", "reservation":
			if ip.Type != "" {
				return estimateInvalid("IP Type")
			}
		default:
			return estimateInvalid("IP Kind")
		}
	}
	for _, s := range in.Storage {
		if err := check(s.Key, s.Quantity); err != nil {
			return err
		}
		if s.Type == "" || s.QuotaGB <= 0 {
			return estimateInvalid("storage type or quota")
		}
		if pricecatalog.ValidateStorageRegion("pricing.Estimate", region, s.Region) != nil {
			return estimateInvalid("storage region")
		}
	}
	return nil
}
