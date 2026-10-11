package compute

import (
	"encoding/json"
	"net/url"
	"slices"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/volume"
)

func (d *flavorDiscovery) zones(project, selected string) ([]flavorDiscoveryZone, error) {
	op := "portal.ListZones"
	var zones []flavorDiscoveryZone
	err := d.read(op, d.client.computeURL("v1", []string{project, "zones"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		zones, e = flavorDiscoveryArray[flavorDiscoveryZone](op, status, raw, "data")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, z := range zones {
			if core.CheckPathID(op, "ZoneID", z.ID) != nil || z.Enabled == nil || seen[z.ID] {
				return invalidFlavorDiscovery(op, status)
			}
			seen[z.ID] = true
		}
		return d.session.AddRows(len(zones))
	})
	if err != nil {
		return nil, err
	}
	if selected != "" {
		zones = slices.DeleteFunc(zones, func(z flavorDiscoveryZone) bool { return z.ID != selected })
		if len(zones) == 0 {
			return nil, core.ErrInvalidInput
		}
	}
	return zones, nil
}

func (d *flavorDiscovery) platforms(project string) ([]flavorDiscoveryPlatform, error) {
	op := "compute.ListFlavorPlatforms"
	var values []flavorDiscoveryPlatform
	err := d.read(op, d.client.computeURL("v1", []string{project, "flavor_zones", "codes"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		values, e = flavorDiscoveryArray[flavorDiscoveryPlatform](op, status, raw, "")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, v := range values {
			if core.CheckPathID(op, "Platform", v.Key) != nil || seen[v.Key] {
				return invalidFlavorDiscovery(op, status)
			}
			seen[v.Key] = true
		}
		return d.session.AddRows(len(values))
	})
	return values, err
}

func (d *flavorDiscovery) consoleCatalog(project string, zone flavorDiscoveryZone, platforms []flavorDiscoveryPlatform, in *ListFlavorPricesInput) error {
	op := "compute.ListFlavorFamilies"
	var families []flavorDiscoveryFamily
	err := d.read(op, d.client.computeURL("v1", []string{project, "flavor_zones", "families"}, url.Values{"zoneId": {zone.ID}}), func(status int, raw json.RawMessage) error {
		var e error
		families, e = flavorDiscoveryArray[flavorDiscoveryFamily](op, status, raw, "")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, family := range families {
			if core.CheckPathID(op, "Family", family.Key) != nil || family.Condition.Codes == nil || seen[family.Key] {
				return invalidFlavorDiscovery(op, status)
			}
			seen[family.Key] = true
			codes := map[string]bool{}
			for _, code := range family.Condition.Codes {
				if codes[code] || !slices.ContainsFunc(platforms, func(p flavorDiscoveryPlatform) bool { return p.Key == code }) {
					return invalidFlavorDiscovery(op, status)
				}
				codes[code] = true
			}
		}
		return d.session.AddRows(len(families))
	})
	if err != nil {
		return err
	}
	for _, family := range families {
		if family.Key == "custom" || (in.Family != "" && in.Family != family.Key) {
			continue
		}
		for _, platform := range family.Condition.Codes {
			if in.Platform != "" && in.Platform != platform {
				continue
			}
			op = "compute.ListFamilyFlavors"
			scope := zone.ID + "/" + family.Key + "/" + platform
			err = d.flavors(op, d.client.computeURL("v1", []string{project, "flavors", "families", family.Key, "platforms", platform}, url.Values{"zoneId": {zone.ID}}), "", zone, family.Key, family.Value, platform, "console-family", in)
			if err != nil {
				if !d.record(op, scope, err) || d.stop(err) {
					return err
				}
			}
		}
	}
	return nil
}

func (d *flavorDiscovery) flavors(op, url, key string, zone flavorDiscoveryZone, family, familyName, platform, visibility string, in *ListFlavorPricesInput) error {
	var rows []FlavorPrice
	err := d.read(op, url, func(status int, raw json.RawMessage) error {
		flavors, e := decodeDiscoveryFlavors(op, status, raw, key)
		if e != nil {
			return e
		}
		if visibility == "console-family" && family == "gpu" && len(flavors) > 0 {
			return invalidFlavorDiscovery(op, status)
		}
		identities := map[string]Flavor{}
		tuples := map[string]Flavor{}
		rows = nil
		for _, f := range flavors {
			if core.CheckPathID(op, "FlavorID", f.FlavorID) != nil || core.CheckPathID(op, "FlavorZoneID", f.FlavorZoneID) != nil || f.Name == "" || f.CPU < 0 || f.Memory < 0 || f.GPU > 0 || (f.ZoneID != "" && f.ZoneID != zone.ID) {
				return invalidFlavorDiscovery(op, status)
			}
			row := FlavorPrice{Flavor: f, ZoneID: zone.ID, ZoneName: zone.Name, ZoneEnabled: zone.Enabled, Family: family, FamilyName: familyName, Platform: platform, Visibility: visibility, RAMUnit: "unknown"}
			row.Price.Status = "unknown"
			row.Price.Source = "pricing.GetQuote"
			identity := zone.ID + "/" + f.FlavorID
			for _, seen := range []map[string]Flavor{d.identities, identities} {
				if previous, ok := seen[identity]; ok && !sameDiscoveryFlavor(previous, f) {
					return invalidFlavorDiscovery(op, status)
				}
			}
			identities[identity] = f
			tuple := flavorDiscoveryKey(row)
			if previous, ok := d.tuples[tuple]; ok {
				if previous != f {
					return invalidFlavorDiscovery(op, status)
				}
				continue
			}
			if previous, ok := tuples[tuple]; ok {
				if previous != f {
					return invalidFlavorDiscovery(op, status)
				}
				continue
			}
			tuples[tuple] = f
			if (in.Family != "" && in.Family != family) || (in.Platform != "" && in.Platform != platform) || (in.Name != "" && in.Name != f.Name) || (in.MinRAM > 0 && f.Memory < in.MinRAM) || (in.MaxRAM > 0 && f.Memory > in.MaxRAM) || (in.AvailableOnly && (f.IsSoldOut || f.RemainingVMs <= 0)) {
				continue
			}
			if !*zone.Enabled {
				row.Price.Status = "inapplicable"
			}
			rows = append(rows, row)
		}
		if e = d.session.AddRows(len(flavors) + len(rows)); e != nil {
			return e
		}
		for id, f := range identities {
			d.identities[id] = f
		}
		for key, f := range tuples {
			d.tuples[key] = f
		}
		return nil
	})
	if err == nil {
		d.out.Items = append(d.out.Items, rows...)
	}
	return err
}

func (d *flavorDiscovery) volumeCatalog(project string) ([]volume.VolumeType, error) {
	op := "volume.ListVolumeTypeZones"
	var parents []volume.VolumeTypeZone
	err := d.read(op, d.client.computeURL("v1", []string{project, "volume_type_zones"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		parents, e = flavorDiscoveryArray[volume.VolumeTypeZone](op, status, raw, "volumeTypeZones")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, parent := range parents {
			if core.CheckPathID(op, "VolumeTypeZoneID", parent.ID) != nil || seen[parent.ID] {
				return invalidFlavorDiscovery(op, status)
			}
			seen[parent.ID] = true
		}
		return d.session.AddRows(len(parents))
	})
	if err != nil {
		return nil, err
	}
	types := []volume.VolumeType{}
	seen := map[string]volume.VolumeType{}
	for _, parent := range parents {
		op = "volume.ListVolumeTypes"
		var values []volume.VolumeType
		err = d.read(op, d.client.computeURL("v1", []string{project, parent.ID, "volume_types"}, nil), func(status int, raw json.RawMessage) error {
			var e error
			values, e = flavorDiscoveryArray[volume.VolumeType](op, status, raw, "volumeTypes")
			if e != nil {
				return e
			}
			local := map[string]bool{}
			for _, v := range values {
				id := v.VolumeTypeID
				if id == "" {
					id = v.ID
				}
				if core.CheckPathID(op, "VolumeTypeID", id) != nil || local[id] || v.MinSize <= 0 || v.MaxSize < v.MinSize || (v.VolumeTypeZoneID != "" && v.VolumeTypeZoneID != parent.ID) {
					return invalidFlavorDiscovery(op, status)
				}
				local[id] = true
				if previous, ok := seen[id]; ok && previous != v {
					return invalidFlavorDiscovery(op, status)
				}
			}
			return d.session.AddRows(len(values))
		})
		if err != nil {
			return nil, err
		}
		for _, v := range values {
			id := v.VolumeTypeID
			if id == "" {
				id = v.ID
			}
			if _, ok := seen[id]; !ok {
				types = append(types, v)
				seen[id] = v
			}
		}
	}
	return types, nil
}

func (d *flavorDiscovery) images(project, zone string) ([]flavorReferenceImage, error) {
	op := "compute.ListOSImages"
	var images []flavorReferenceImage
	err := d.read(op, d.client.computeURL("v1", []string{project, "images", "os"}, url.Values{"zoneId": {zone}}), func(status int, raw json.RawMessage) error {
		var e error
		images, e = flavorDiscoveryArray[flavorReferenceImage](op, status, raw, "images")
		if e != nil {
			return e
		}
		if e = validateReferenceImages(op, status, images); e != nil {
			return e
		}
		return d.session.AddRows(len(images))
	})
	return images, err
}

func (d *flavorDiscovery) nonPublicCatalog(project string, zones []flavorDiscoveryZone, in *ListFlavorPricesInput) error {
	op := "compute.ListFlavorZones"
	var groups []FlavorZone
	err := d.read(op, d.client.computeURL("v1", []string{project, "flavor_zones", "product"}, nil), func(status int, raw json.RawMessage) error {
		var e error
		groups, e = flavorDiscoveryArray[FlavorZone](op, status, raw, "flavorZones")
		if e != nil {
			return e
		}
		seen := map[string]bool{}
		for _, group := range groups {
			if core.CheckPathID(op, "FlavorZoneID", group.ID) != nil || core.CheckPathID(op, "ZoneID", group.ZoneID) != nil || seen[group.ID] {
				return invalidFlavorDiscovery(op, status)
			}
			seen[group.ID] = true
		}
		return d.session.AddRows(len(groups))
	})
	if err != nil {
		return err
	}
	count := len(groups)
	d.out.Metadata.ExcludedGroups = &count
	d.note("non-public", "compute", "Product group count describes the excluded console-family scope. Populated custom and GPU schemas remain unverified.")
	for _, zone := range zones {
		for _, path := range [][]string{{project, "flavor_zones", "customs"}, {project, "flavors", "customs"}} {
			op = "compute.ListCustomFlavors"
			err = d.read(op, d.client.computeURL("v1", path, url.Values{"zoneId": {zone.ID}}), func(status int, raw json.RawMessage) error {
				values, e := flavorDiscoveryArray[json.RawMessage](op, status, raw, "")
				if e != nil {
					return e
				}
				if len(values) > 0 {
					return invalidFlavorDiscovery(op, status)
				}
				return nil
			})
			if err != nil {
				if !d.record(op, zone.ID, err) || d.stop(err) {
					return err
				}
			}
		}
		for _, group := range groups {
			if group.ZoneID != zone.ID {
				continue
			}
			op = "compute.ListFlavors"
			err = d.flavors(op, d.client.computeURL("v1", []string{project, group.ID, "flavors"}, nil), "flavors", zone, "", "", "", "unclassified", in)
			if err != nil {
				if !d.record(op, zone.ID+"/"+group.ID, err) || d.stop(err) {
					return err
				}
			}
		}
	}
	return nil
}

func sameDiscoveryFlavor(a, b Flavor) bool {
	a.FlavorZoneID, b.FlavorZoneID = "", ""
	a.ZoneID, b.ZoneID = "", ""
	return a == b
}
