package pricing

import (
	"errors"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/pricecatalog"
	"danny.vn/vngcloud/internal/quoteinput"
	"danny.vn/vngcloud/internal/transport"
)

func estimateCached[T any](d *estimateRun, key string, load func() (T, error)) (T, error) {
	if cached, ok := d.catalogs[key]; ok {
		var zero T
		if cached.err != nil {
			return zero, cached.err
		}
		return cached.value.(T), nil
	}
	value, err := load()
	d.catalogs[key] = estimateCatalogResult{value: value, err: err}
	return value, err
}

func (d *estimateRun) project() (string, error) {
	return estimateCached(d, "project", func() (string, error) { return d.client.RequireDiscoveryProjectID(d.session) })
}

func (d *estimateRun) regional(zone string) ([]pricecatalog.VolumeType, error) {
	project, err := d.project()
	if err != nil {
		return nil, err
	}
	zones, err := estimateCached(d, "zones", func() ([]pricecatalog.Zone, error) { return pricecatalog.Zones(d.client, d.session, project) })
	if err != nil {
		return nil, err
	}
	var selected *pricecatalog.Zone
	for i := range zones {
		if zones[i].ID == zone {
			selected = &zones[i]
			break
		}
	}
	if selected == nil {
		return nil, estimateInvalid("ZoneID")
	}
	return estimateCached(d, "volume types", func() ([]pricecatalog.VolumeType, error) {
		return pricecatalog.VolumeCatalog(d.client, d.session, project)
	})
}

func estimateDisk(types []pricecatalog.VolumeType, id string, size int) error {
	for _, typ := range types {
		if typ.QuoteID() == id {
			if size < typ.MinSize || size > typ.MaxSize {
				return estimateInvalid("disk size")
			}
			return nil
		}
	}
	return estimateInvalid("disk type")
}

func (d *estimateRun) encryption(ids ...string) error {
	needed := false
	for _, id := range ids {
		needed = needed || id != ""
	}
	if !needed {
		return nil
	}
	project, err := d.project()
	if err != nil {
		return err
	}
	keys, err := estimateCached(d, "encryption", func() ([]pricecatalog.Encryption, error) {
		return pricecatalog.Encryptions(d.client, d.session, project)
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		found := false
		for _, key := range keys {
			if key.Key == id {
				found = true
				break
			}
		}
		if !found {
			return estimateInvalid("encryption type")
		}
	}
	return nil
}

func (d *estimateRun) image(server EstimateServer) (bool, error) {
	project, err := d.project()
	if err != nil {
		return false, err
	}
	images, err := estimateCached(d, "images/"+server.ZoneID, func() ([]pricecatalog.Image, error) {
		return pricecatalog.Images(d.client, d.session, project, server.ZoneID)
	})
	if err != nil {
		return false, err
	}
	for _, image := range images {
		if image.ID != server.ImageID {
			continue
		}
		if !image.UnlicensedUbuntu() || image.PackageLimit.DiskSize == nil || *image.PackageLimit.DiskSize <= 0 {
			return false, nil
		}
		if server.RootDiskSize < *image.PackageLimit.DiskSize {
			return false, estimateInvalid("image root minimum")
		}
		return true, nil
	}
	return false, estimateInvalid("ImageID")
}

func (d *estimateRun) status(i int, status, reason string) {
	d.output.Items[i].Price.Status = status
	d.output.Coverage = append(d.output.Coverage, EstimateCoverage{Topic: d.output.Items[i].Kind, Scope: d.output.Items[i].Key, Status: status, Reason: reason})
}

func (d *estimateRun) prepareServer(i int, server EstimateServer) (*transport.Request, error) {
	types, err := d.regional(server.ZoneID)
	if err != nil {
		return nil, err
	}
	if err = estimateDisk(types, server.RootDiskTypeID, server.RootDiskSize); err != nil {
		return nil, err
	}
	if server.DataDiskSize > 0 {
		if err = estimateDisk(types, server.DataDiskTypeID, server.DataDiskSize); err != nil {
			return nil, err
		}
	}
	if err = d.encryption(server.RootDiskEncryptionTypeID, server.DataDiskEncryptionTypeID); err != nil {
		return nil, err
	}
	usable, err := d.image(server)
	if err != nil {
		return nil, err
	}
	if !usable {
		d.status(i, "unknown", "image-price-unverified")
		return nil, nil
	}
	info, err := quoteinput.ServerInfo(quoteinput.Server{ZoneID: server.ZoneID, FlavorID: server.FlavorID, ImageID: server.ImageID, RootDiskSize: server.RootDiskSize, RootDiskTypeID: server.RootDiskTypeID, RootDiskEncryptionTypeID: server.RootDiskEncryptionTypeID, DataDiskSize: server.DataDiskSize, DataDiskTypeID: server.DataDiskTypeID, DataDiskEncryptionTypeID: server.DataDiskEncryptionTypeID})
	if err != nil {
		return nil, err
	}
	req := pricecatalog.RegionalQuote(d.client, estimateOperation, quoteBody{ResourceType: ResourceServer, Action: ActionCreate, ResourceInfo: info})
	return &req, nil
}

func (d *estimateRun) prepareVolume(volume EstimateVolume) (*transport.Request, error) {
	types, err := d.regional(volume.ZoneID)
	if err != nil {
		return nil, err
	}
	if err = estimateDisk(types, volume.VolumeTypeID, volume.Size); err != nil {
		return nil, err
	}
	if err = d.encryption(volume.EncryptionTypeID); err != nil {
		return nil, err
	}
	info, err := quoteinput.VolumeInfo(quoteinput.Volume{ZoneID: volume.ZoneID, VolumeTypeID: volume.VolumeTypeID, Size: volume.Size, EncryptionTypeID: volume.EncryptionTypeID})
	if err != nil {
		return nil, err
	}
	req := pricecatalog.RegionalQuote(d.client, estimateOperation, quoteBody{ResourceType: ResourceVolume, Action: ActionCreate, ResourceInfo: info})
	return &req, nil
}

func (d *estimateRun) prepareStorage(i int, storage EstimateStorage) (*transport.Request, error) {
	regions, err := estimateCached(d, "storage regions", func() ([]pricecatalog.StorageRegion, error) { return pricecatalog.StorageRegions(d.client, d.session) })
	if err != nil {
		return nil, err
	}
	id, name, err := pricecatalog.ResolveStorageRegion(estimateOperation, d.client.Region(), storage.Region, regions)
	if err != nil {
		if errors.Is(err, core.ErrInvalidInput) {
			return nil, estimateInvalid("storage region")
		}
		return nil, err
	}
	d.output.Items[i].Region = name
	if _, ok := d.catalogs["storage/"+id]; !ok {
		count := 0
		for key := range d.catalogs {
			if len(key) > 8 && key[:8] == "storage/" {
				count++
			}
		}
		if count >= 32 {
			return nil, estimateInvalid("storage region count")
		}
	}
	catalog, err := estimateCached(d, "storage/"+id, func() (*pricecatalog.StorageCatalog, error) {
		return pricecatalog.ReadStorage(d.client, d.session, estimateOperation, id)
	})
	if err != nil {
		return nil, err
	}
	spec, err := catalog.Resolve(estimateOperation, storage.Type, storage.QuotaGB)
	if err != nil {
		return nil, estimateInvalid("storage type or quota")
	}
	req := pricecatalog.StorageQuote(d.client, estimateOperation, id, quoteinput.Storage(spec.Quota, spec.PurchaseTypeID, spec.ProjectTypeID))
	return &req, nil
}

func (d *estimateRun) prepareItem(i int) (*transport.Request, error) {
	if i < len(d.input.Servers) {
		return d.prepareServer(i, d.input.Servers[i])
	}
	n := i - len(d.input.Servers)
	if n < len(d.input.Volumes) {
		return d.prepareVolume(d.input.Volumes[n])
	}
	n -= len(d.input.Volumes)
	if n < len(d.input.PublicIPs) {
		ip := d.input.PublicIPs[n]
		if ip.Kind != "public-vip" {
			d.status(i, "unknown", "ip-price-unverified")
			return nil, nil
		}
		if _, err := d.project(); err != nil {
			return nil, err
		}
		req := pricecatalog.RegionalQuote(d.client, estimateOperation, quoteinput.PublicIP(ip.Type))
		return &req, nil
	}
	n -= len(d.input.PublicIPs)
	return d.prepareStorage(i, d.input.Storage[n])
}
