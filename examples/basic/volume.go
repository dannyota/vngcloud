package main

import (
	"context"
	"fmt"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/volume"
)

func showVolume(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	volumeClient := volume.New(cfg)
	prices, priceErr := volumeClient.ListVolumePrices(ctx, &volume.ListVolumePricesInput{MaxRequests: 100, AllowPartial: true})
	recordOne(outputs, cfg, "volume/discovery", "volume prices", prices, priceErr)
	showSnapshotPolicies(ctx, cfg, outputs)

	volumes, err := collectPaged(vngcloud.DefaultPageSize, func(page, size int) ([]volume.Volume, int, error) {
		out, err := volumeClient.ListVolumes(ctx, &volume.ListVolumesInput{Page: page, Size: size})
		if err != nil {
			return nil, 0, err
		}
		return out.Items, out.TotalPage, nil
	})
	record(outputs, cfg, "volume/volume", "volumes", volumes, err)

	underlyingVolumes, underlyingErr := collectDetails(volumes,
		func(v volume.Volume) string { return v.UUID },
		func(id string) (*volume.Volume, error) {
			out, err := volumeClient.GetUnderlyingVolume(ctx, &volume.GetUnderlyingVolumeInput{VolumeID: id})
			if err != nil {
				return nil, err
			}
			return &out.Volume, nil
		},
	)
	if err != nil {
		underlyingErr = err
	}
	record(outputs, cfg, "volume/underlying_volume", "underlying volumes", underlyingVolumes, underlyingErr)

	typeZonesOut, err := volumeClient.ListVolumeTypeZones(ctx, nil)
	typeZones := []volume.VolumeTypeZone(nil)
	if typeZonesOut != nil {
		typeZones = typeZonesOut.Items
	}
	record(outputs, cfg, "volume/type_zone", "volume type zones", typeZones, err)

	zonesOut, zonesErr := portal.New(cfg).ListZones(ctx, nil)
	zoneID := ""
	if zonesOut != nil {
		for _, zone := range zonesOut.Items {
			if zone.IsEnabled != nil && *zone.IsEnabled && zone.UUID != nil && *zone.UUID != "" {
				zoneID = *zone.UUID
				break
			}
		}
	}
	switch {
	case zonesErr != nil:
		fmt.Println("default volume type: skipped (portal zone lookup failed)")
	case zoneID == "":
		fmt.Println("default volume type: skipped (no enabled portal zone with a zone ID)")
	default:
		out, callErr := volumeClient.GetDefaultVolumeType(ctx, &volume.GetDefaultVolumeTypeInput{ZoneID: zoneID})
		var defaultType *volume.VolumeType
		if out != nil {
			defaultType = &out.VolumeType
		}
		recordOne(outputs, cfg, "volume/default_type", "default volume type", defaultType, callErr)
	}

	typesOut, err := volumeClient.ListVolumeTypes(ctx, nil)
	types := []volume.VolumeType(nil)
	if typesOut != nil {
		types = typesOut.Items
	}
	record(outputs, cfg, "volume/type", "volume types", types, err)

	typeDetails, typeDetailErr := collectDetails(types,
		func(vt volume.VolumeType) string { return vt.ID },
		func(id string) (*volume.VolumeType, error) {
			out, err := volumeClient.GetVolumeType(ctx, &volume.GetVolumeTypeInput{VolumeTypeID: id})
			if err != nil {
				return nil, err
			}
			return &out.VolumeType, nil
		},
	)
	if err != nil {
		typeDetailErr = err
	}
	record(outputs, cfg, "volume/type_detail", "volume type details", typeDetails, typeDetailErr)

	encryptionTypesOut, err := volumeClient.ListEncryptionTypes(ctx, nil)
	encryptionTypes := []volume.EncryptionType(nil)
	if encryptionTypesOut != nil {
		encryptionTypes = encryptionTypesOut.Items
	}
	record(outputs, cfg, "volume/encryption_type", "encryption types", encryptionTypes, err)

	snapshotsOut, err := volumeClient.ListAllSnapshots(ctx, nil)
	snapshots := []volume.Snapshot(nil)
	if snapshotsOut != nil {
		snapshots = snapshotsOut.Items
	}
	record(outputs, cfg, "volume/snapshot", "snapshots", snapshots, err)
}

func showSnapshotPolicies(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	if cfg.Region() != "hcm-3" {
		return
	}
	client := volume.New(cfg)
	backends, err := client.ListSnapshotBackends(ctx, &volume.ListSnapshotBackendsInput{Name: "HCM-03"})
	var items []volume.SnapshotBackend
	if backends != nil {
		items = backends.Items
	}
	record(outputs, cfg, "volume/snapshot_backend", "snapshot backends", items, err)
	if err != nil {
		return
	}
	// Each returned backend keeps its own scope; no first-match selection.
	for _, backend := range items {
		policies, err := collectPaged(10, func(page, size int) ([]volume.SnapshotPolicy, int, error) {
			out, err := client.ListSnapshotPolicies(ctx, &volume.ListSnapshotPoliciesInput{BackendID: backend.ID, Page: page, Size: size})
			if err != nil {
				return nil, 0, err
			}
			return out.Items, out.TotalPage, nil
		})
		record(outputs, cfg, "volume/snapshot_policy/"+backend.ID, "snapshot policies", policies, err)
	}
}
