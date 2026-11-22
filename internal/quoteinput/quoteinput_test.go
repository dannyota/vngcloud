package quoteinput

import (
	"encoding/json"
	"testing"
)

func TestPricedSerialization(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func() (any, error)
		want  string
	}{
		{"server plain", func() (any, error) {
			return ServerInfo(Server{ZoneID: "z-1", FlavorID: "f-1", ImageID: "i-1", RootDiskSize: 20, RootDiskTypeID: "t-1"})
		}, `{"encryptionVolume":false,"flavorId":"f-1","imageId":"i-1","isPoc":false,"period":1,"rootDiskSize":20,"rootDiskTypeId":"t-1","zoneId":"z-1"}`},
		{"server root encryption", func() (any, error) {
			return ServerInfo(Server{ZoneID: "z-1", FlavorID: "f-1", ImageID: "i-1", RootDiskSize: 20, RootDiskTypeID: "t-1", RootDiskEncryptionTypeID: "aes_256"})
		}, `{"encryptionVolume":true,"flavorId":"f-1","imageId":"i-1","isPoc":false,"period":1,"rootDiskSize":20,"rootDiskTypeId":"t-1","zoneId":"z-1"}`},
		{"server bundled encryption", func() (any, error) {
			return ServerInfo(Server{ZoneID: "z-1", FlavorID: "f-1", ImageID: "i-1", RootDiskSize: 20, RootDiskTypeID: "t-1", DataDiskSize: 10, DataDiskTypeID: "t-2", DataDiskEncryptionTypeID: "aes_256"})
		}, `{"dataDiskSize":10,"dataDiskTypeId":"t-2","encryptionVolume":true,"flavorId":"f-1","imageId":"i-1","isPoc":false,"period":1,"rootDiskSize":20,"rootDiskTypeId":"t-1","zoneId":"z-1"}`},
		{"volume plain", func() (any, error) { return VolumeInfo(Volume{ZoneID: "z-1", VolumeTypeID: "t-1", Size: 10}) }, `{"isPoc":false,"period":1,"size":10,"volumeTypeId":"t-1","zoneId":"z-1"}`},
		{"volume encryption", func() (any, error) {
			return VolumeInfo(Volume{ZoneID: "z-1", VolumeTypeID: "t-1", Size: 10, EncryptionTypeID: "aes_256"})
		}, `{"encryptionType":"aes_256","isPoc":false,"period":1,"size":10,"volumeTypeId":"t-1","zoneId":"z-1"}`},
		{"storage", func() (any, error) { return Storage(30, 4, 1), nil }, `{"resourceType":"object_storage","action":"create","resourceInfo":{"quota":30,"purchaseTypeId":4,"projectType":1}}`},
		{"public", func() (any, error) { return PublicIP("public"), nil }, `{"action":"create","resourceInfo":{"isBuyMorePo":false,"isPoc":false,"type":"public"},"resourceType":"public-vip"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := tc.build()
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != tc.want {
				t.Fatalf("body %s, want %s", raw, tc.want)
			}
		})
	}
}
