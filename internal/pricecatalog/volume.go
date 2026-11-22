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

func VolumeParents(c *core.Client, s *discovery.Session, project string) ([]VolumeParent, error) {
	const op = "volume.ListVolumeTypeZones"
	var values []VolumeParent
	err := regionalRead(c, s, op, []string{project, "volume_type_zones"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[VolumeParent](op, status, raw, "volumeTypeZones")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, p := range values {
			if core.CheckPathID(op, "VolumeTypeZoneID", p.ID) != nil || seen[p.ID] {
				return Invalid(op, status)
			}
			seen[p.ID] = true
		}
		return s.AddRows(len(values))
	})
	return values, err
}

func VolumeTypes(c *core.Client, s *discovery.Session, project, parent string, details ...bool) ([]VolumeType, error) {
	return volumeTypes(c, s, project, parent, len(details) > 0 && details[0], nil)
}

func volumeTypes(c *core.Client, s *discovery.Session, project, parent string, details bool, validate func(int, []VolumeType) error) ([]VolumeType, error) {
	const op = "volume.ListVolumeTypes"
	var values []VolumeType
	err := regionalRead(c, s, op, []string{project, parent, "volume_types"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[VolumeType](op, status, raw, "volumeTypes")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, v := range values {
			id := v.QuoteID()
			if core.CheckPathID(op, "VolumeTypeID", id) != nil || seen[id] || v.MinSize <= 0 || v.MaxSize < v.MinSize || (v.VolumeTypeZoneID != "" && v.VolumeTypeZoneID != parent) {
				return Invalid(op, status)
			}
			if details && (v.Name == "" || v.IOPS < 0) {
				return Invalid(op, status)
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

func Encryptions(c *core.Client, s *discovery.Session, project string) ([]Encryption, error) {
	const op = "volume.ListEncryptionTypes"
	var values []Encryption
	err := regionalRead(c, s, op, []string{project, "volumes", "encryption_types"}, nil, func(status int, raw json.RawMessage) error {
		var err error
		values, err = Array[Encryption](op, status, raw, "")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, v := range values {
			if v.Key == "" || v.Key == "none" || v.Key == "all" || core.CheckTypeID(op, "Encryption", v.Key) != nil || seen[v.Key] {
				return Invalid(op, status)
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
