package compute

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"

	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/volume"
)

type flavorReferenceImage = pricecatalog.Image

var errFlavorRootBounds = errors.New("root disk size outside type bounds")

func flavorImageSupport(metadata string) []string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(metadata), &fields) != nil || fields == nil {
		return nil
	}
	var support []string
	if json.Unmarshal(fields["imageTypeSupport"], &support) != nil || len(support) == 0 {
		return nil
	}
	for _, value := range support {
		if value == "" {
			return nil
		}
	}
	return support
}

func selectFlavorReference(ctx context.Context, flavor Flavor, images []flavorReferenceImage, types []volume.VolumeType) (*FlavorPriceReference, error) {
	return selectFlavorReferenceWithDisk(ctx, flavor, images, types, false)
}

func selectFlavorReferenceWithDisk(ctx context.Context, flavor Flavor, images []flavorReferenceImage, types []volume.VolumeType, custom bool) (*FlavorPriceReference, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	support := flavorImageSupport(flavor.Metadata)
	var best FlavorPriceReference
	found := false
	outOfBounds := false
	for _, image := range images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limits := image.PackageLimit
		if !slices.Contains(support, image.ImageType) || !image.UnlicensedUbuntu() || limits.CPU == nil || limits.Memory == nil || limits.DiskSize == nil || *limits.CPU < 0 || *limits.Memory < 0 || *limits.CPU > flavor.CPU || *limits.Memory > flavor.Memory || *limits.DiskSize <= 0 {
			continue
		}
		for _, disk := range types {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if (!custom && disk.IOPS != 3000) || disk.MinSize <= 0 || disk.MaxSize < disk.MinSize {
				continue
			}
			size := max(disk.MinSize, *limits.DiskSize)
			if size > disk.MaxSize {
				outOfBounds = true
				continue
			}
			id := disk.VolumeTypeID
			if id == "" {
				id = disk.ID
			}
			candidate := FlavorPriceReference{ImageID: image.ID, ImageType: image.ImageType, ImageVersion: image.ImageVersion, RootDiskTypeID: id, RootDiskSizeGB: size}
			if !found || compareFlavorReferences(candidate, best) < 0 {
				best, found = candidate, true
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !found {
		if custom && outOfBounds {
			return nil, errFlavorRootBounds
		}
		return nil, nil
	}
	return &best, nil
}

func validateFlavorRootQuote(status int, raw json.RawMessage) error {
	var result struct {
		Properties []struct {
			Name string `json:"name"`
		} `json:"propertiesPrice"`
	}
	if json.Unmarshal(raw, &result) != nil || len(result.Properties) != 2 {
		return invalidFlavorDiscovery("pricing.GetQuote", status)
	}
	counts := map[string]int{}
	for _, line := range result.Properties {
		counts[line.Name]++
	}
	if counts["INSTANCE TYPE"] != 1 || counts["ROOT DISK"] != 1 {
		return invalidFlavorDiscovery("pricing.GetQuote", status)
	}
	return nil
}

func compareFlavorReferences(a, b FlavorPriceReference) int {
	return cmp.Or(cmp.Compare(a.RootDiskSizeGB, b.RootDiskSizeGB), cmp.Compare(a.ImageVersion, b.ImageVersion), cmp.Compare(a.ImageID, b.ImageID), cmp.Compare(a.RootDiskTypeID, b.RootDiskTypeID))
}

func flavorDiscoveryBody(row FlavorPrice) (map[string]any, error) {
	reference := row.Reference
	info, err := buildServerQuoteInfo("compute.ListFlavorPrices", &CreateServerInput{ZoneID: row.ZoneID, FlavorID: row.Flavor.FlavorID, ImageID: reference.ImageID, RootDiskSize: reference.RootDiskSizeGB, RootDiskTypeID: reference.RootDiskTypeID})
	return map[string]any{"resourceType": "server", "action": "create", "resourceInfo": info}, err
}
