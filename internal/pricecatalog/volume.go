//revive:disable:exported Internal contracts live in docs/design/cost-estimate.md.
package pricecatalog

import (
	"encoding/json"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/discovery"
)

type parentZone struct {
	UUID          string   `json:"uuid"`
	Name          string   `json:"name,omitempty"`
	ZoneType      string   `json:"zoneType"`
	IsDefault     bool     `json:"isDefault"`
	Description   string   `json:"description"`
	IsEnabled     bool     `json:"isEnabled"`
	OpenStackZone string   `json:"openstackZone"`
	IPRanges      []string `json:"ipRanges"`
	ServerCount   int      `json:"serverCount"`
	VolumeCount   int      `json:"volumeCount"`
}

type VolumeParent struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Zone        parentZone `json:"zone"`
}

type VolumeType struct {
	ID               string `json:"id"`
	VolumeTypeID     string `json:"volumeTypeId"`
	Name             string `json:"name"`
	IOPS             int    `json:"iops"`
	MaxSize          int    `json:"maxSize"`
	MinSize          int    `json:"minSize"`
	Throughput       int    `json:"throughPut"`
	ZoneID           string `json:"zoneId"`
	VolumeTypeZoneID string `json:"volumeTypeZoneId"`
}

func (v VolumeType) QuoteID() string {
	if v.VolumeTypeID != "" {
		return v.VolumeTypeID
	}
	return v.ID
}

type Encryption struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name,omitempty"`
	Value       string `json:"value,omitempty"`
	DisplayKey  string `json:"displayKey,omitempty"`
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
}

func VolumeParents(c *core.Client, s *discovery.Session, project string, validationOp ...string) ([]VolumeParent, error) {
	const op = "volume.ListVolumeTypeZones"
	checkOp := op
	if len(validationOp) > 0 {
		checkOp = validationOp[0]
	}
	var values []VolumeParent
	err := regionalRead(c, s, op, []string{project, "volume_type_zones"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[VolumeParent](checkOp, status, raw, "volumeTypeZones")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, p := range values {
			if core.CheckPathID(checkOp, "VolumeTypeZoneID", p.ID) != nil || seen[p.ID] {
				return Invalid(checkOp, status)
			}
			seen[p.ID] = true
		}
		return s.AddRows(len(values))
	})
	return values, err
}

type VolumeTypeOptions struct {
	RequireDetails      bool
	ValidationOperation string
}

func VolumeTypes(c *core.Client, s *discovery.Session, project, parent string, options ...VolumeTypeOptions) ([]VolumeType, error) {
	var option VolumeTypeOptions
	if len(options) > 0 {
		option = options[0]
	}
	return volumeTypes(c, s, project, parent, option.RequireDetails, nil, option.ValidationOperation)
}

func volumeTypes(c *core.Client, s *discovery.Session, project, parent string, details bool, validate func(int, []VolumeType) error, validationOp ...string) ([]VolumeType, error) {
	const op = "volume.ListVolumeTypes"
	checkOp := op
	if len(validationOp) > 0 && validationOp[0] != "" {
		checkOp = validationOp[0]
	}
	var values []VolumeType
	err := regionalRead(c, s, op, []string{project, parent, "volume_types"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[VolumeType](checkOp, status, raw, "volumeTypes")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, v := range values {
			id := v.QuoteID()
			if core.CheckPathID(checkOp, "VolumeTypeID", id) != nil || seen[id] || v.MinSize <= 0 || v.MaxSize < v.MinSize || (v.VolumeTypeZoneID != "" && v.VolumeTypeZoneID != parent) {
				return Invalid(checkOp, status)
			}
			if details && (v.Name == "" || v.IOPS < 0) {
				return Invalid(checkOp, status)
			}
			seen[id] = true
		}
		if validate != nil {
			if err := validate(status, values); err != nil {
				return err
			}
		}
		return s.AddRows(len(values))
	})
	return values, err
}

func Encryptions(c *core.Client, s *discovery.Session, project string, validationOp ...string) ([]Encryption, error) {
	const op = "volume.ListEncryptionTypes"
	checkOp := op
	if len(validationOp) > 0 {
		checkOp = validationOp[0]
	}
	var values []Encryption
	err := regionalRead(c, s, op, []string{project, "volumes", "encryption_types"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[Encryption](checkOp, status, raw, "")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, v := range values {
			if v.Key == "" || v.Key == "none" || v.Key == "all" || core.CheckTypeID(checkOp, "Encryption", v.Key) != nil || seen[v.Key] {
				return Invalid(checkOp, status)
			}
			seen[v.Key] = true
		}
		return s.AddRows(len(values))
	})
	return values, err
}

func VolumeCatalog(c *core.Client, s *discovery.Session, project string) ([]VolumeType, error) {
	parents, err := VolumeParents(c, s, project)
	if err != nil {
		return nil, err
	}
	values := []VolumeType{}
	seen := map[string]VolumeType{}
	for _, parent := range parents {
		rows, err := volumeTypes(c, s, project, parent.ID, false, func(status int, rows []VolumeType) error {
			for _, v := range rows {
				if previous, ok := seen[v.QuoteID()]; ok && previous != v {
					return Invalid("volume.ListVolumeTypes", status)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		for _, v := range rows {
			if _, ok := seen[v.QuoteID()]; !ok {
				values = append(values, v)
				seen[v.QuoteID()] = v
			}
		}
	}
	return values, nil
}
