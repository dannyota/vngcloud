//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package pricecatalog

import (
	"encoding/json"
	"net/url"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
)

type Image struct {
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

func Ubuntu(value string) bool {
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

func ValidateImages(op string, status int, images []Image) error {
	seen := map[string]bool{}
	for _, image := range images {
		if core.CheckPathID(op, "ImageID", image.ID) != nil || image.ImageType == "" || image.ImageVersion == "" || seen[image.ID] {
			return Invalid(op, status)
		}
		seen[image.ID] = true
	}
	return nil
}

func (image *Image) UnmarshalJSON(raw []byte) error {
	if err := ValidateFields(raw, (*Image)(nil)); err != nil {
		return err
	}
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
	*image = Image{ID: fields.ID, ImageType: fields.ImageType, ImageVersion: fields.ImageVersion}
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

func (image Image) UnlicensedUbuntu() bool {
	return Ubuntu(image.ImageType) && image.Licence != nil && !*image.Licence
}

func Images(c *core.Client, s *discovery.Session, project, zone string) ([]Image, error) {
	const op = "compute.ListOSImages"
	var values []Image
	err := regionalRead(c, s, op, []string{project, "images", "os"}, url.Values{"zoneId": {zone}}, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[Image](op, status, raw, "images")
		if err != nil {
			return err
		}
		if err := ValidateImages(op, status, values); err != nil {
			return err
		}
		return s.AddRows(len(values))
	})
	return values, err
}
