package main

import (
	"context"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/volume"
)

func showVolume(ctx context.Context, cfg vngcloud.Config, outputs *sdkOutputStore) {
	volumeClient := volume.New(cfg)

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

	defaultTypeOut, err := volumeClient.GetDefaultVolumeType(ctx, nil)
	var defaultType *volume.VolumeType
	if defaultTypeOut != nil {
		defaultType = &defaultTypeOut.VolumeType
	}
	recordOne(outputs, cfg, "volume/default_type", "default volume type", defaultType, err)

	typeZonesOut, err := volumeClient.ListVolumeTypeZones(ctx, nil)
	typeZones := []volume.VolumeTypeZone(nil)
	if typeZonesOut != nil {
		typeZones = typeZonesOut.Items
	}
	record(outputs, cfg, "volume/type_zone", "volume type zones", typeZones, err)

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
