package compute

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/volume"
)

// Private selectors preserve presence and never retain license keys.
type flavorReferenceImage struct {
	ID           string `json:"id"`
	ImageType    string `json:"imageType"`
	ImageVersion string `json:"imageVersion"`
	Licence      *bool  `json:"licence"`
	PackageLimit struct {
		CPU      *int64 `json:"cpu"`
		Memory   *int64 `json:"memory"`
		DiskSize *int   `json:"diskSize"`
	} `json:"packageLimit"`
}

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

func asciiUbuntu(value string) bool {
	if len(value) != 6 {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != "ubuntu"[i] {
			return false
		}
	}
	return true
}

func selectFlavorReference(ctx context.Context, flavor Flavor, images []flavorReferenceImage, types []volume.VolumeType) (*FlavorPriceReference, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	support := flavorImageSupport(flavor.Metadata)
	var best FlavorPriceReference
	found := false
	for _, image := range images {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limits := image.PackageLimit
		if !slices.Contains(support, image.ImageType) || !asciiUbuntu(image.ImageType) || image.Licence == nil || *image.Licence || limits.CPU == nil || limits.Memory == nil || limits.DiskSize == nil || *limits.CPU < 0 || *limits.Memory < 0 || *limits.CPU > flavor.CPU || *limits.Memory > flavor.Memory || *limits.DiskSize <= 0 {
			continue
		}
		for _, disk := range types {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if disk.IOPS != 3000 || disk.MinSize <= 0 || disk.MaxSize < disk.MinSize {
				continue
			}
			size := max(disk.MinSize, *limits.DiskSize)
			if size > disk.MaxSize {
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
		return nil, nil
	}
	return &best, nil
}

func compareFlavorReferences(a, b FlavorPriceReference) int {
	return cmp.Or(cmp.Compare(a.RootDiskSizeGB, b.RootDiskSizeGB), cmp.Compare(a.ImageVersion, b.ImageVersion), cmp.Compare(a.ImageID, b.ImageID), cmp.Compare(a.RootDiskTypeID, b.RootDiskTypeID))
}

func flavorDiscoveryBody(row FlavorPrice) (map[string]any, error) {
	reference := row.Reference
	info, err := buildServerQuoteInfo("compute.ListFlavorPrices", &CreateServerInput{ZoneID: row.ZoneID, FlavorID: row.Flavor.FlavorID, ImageID: reference.ImageID, RootDiskSize: reference.RootDiskSizeGB, RootDiskTypeID: reference.RootDiskTypeID})
	return map[string]any{"resourceType": "server", "action": "create", "resourceInfo": info}, err
}

func validateReferenceImages(op string, status int, images []flavorReferenceImage) error {
	seen := map[string]bool{}
	for _, image := range images {
		if core.CheckPathID(op, "ImageID", image.ID) != nil || image.ImageType == "" || image.ImageVersion == "" || seen[image.ID] {
			return invalidFlavorDiscovery(op, status)
		}
		seen[image.ID] = true
	}
	return nil
}

func (image *flavorReferenceImage) UnmarshalJSON(raw []byte) error {
	type identity struct {
		ID           string                     `json:"id"`
		ImageType    string                     `json:"imageType"`
		ImageVersion string                     `json:"imageVersion"`
		Licence      json.RawMessage            `json:"licence"`
		Limits       map[string]json.RawMessage `json:"packageLimit"`
	}
	var fields identity
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*image = flavorReferenceImage{ID: fields.ID, ImageType: fields.ImageType, ImageVersion: fields.ImageVersion}
	// An unusable selector excludes the candidate, rather than inventing zeros.
	if json.Unmarshal(fields.Licence, &image.Licence) != nil {
		image.Licence = nil
	}
	if json.Unmarshal(fields.Limits["cpu"], &image.PackageLimit.CPU) != nil {
		image.PackageLimit.CPU = nil
	}
	if json.Unmarshal(fields.Limits["memory"], &image.PackageLimit.Memory) != nil {
		image.PackageLimit.Memory = nil
	}
	if json.Unmarshal(fields.Limits["diskSize"], &image.PackageLimit.DiskSize) != nil {
		image.PackageLimit.DiskSize = nil
	}
	return nil
}
