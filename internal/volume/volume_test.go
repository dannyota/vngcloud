package volume

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestVolumeListVolumes(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "data" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/list_volumes.json")
	}))

	volumes, err := service.ListVolumes(context.Background(), &ListVolumesOptions{Name: "data", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if len(volumes.Items) != 1 || volumes.Items[0].UUID != "volume-1" {
		t.Fatalf("unexpected volumes: %+v", volumes)
	}
	if !volumes.Items[0].IsInUse() || !volumes.Items[0].AttachedToServer("server-1") || volumes.Items[0].CanDelete() {
		t.Fatalf("unexpected volume helpers: %+v", volumes.Items[0])
	}
}

func TestVolumeListVolumesDefaultPageSize(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := service.ListVolumes(context.Background(), nil); err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
}

func TestVolumeGetVolume(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/volume-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/get_volume.json")
	}))

	volume, err := service.GetVolume(context.Background(), "volume-1")
	if err != nil {
		t.Fatalf("GetVolume() error = %v", err)
	}
	if volume.UUID != "volume-1" || !volume.IsAvailable() || !volume.CanDelete() {
		t.Fatalf("unexpected volume: %+v", volume)
	}
}

func TestVolumeGetUnderlyingVolume(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/volume-1/mapping" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/get_underlying_volume.json")
	}))

	volume, err := service.GetUnderlyingVolume(context.Background(), "volume-1")
	if err != nil {
		t.Fatalf("GetUnderlyingVolume() error = %v", err)
	}
	if volume.UUID != "<volume-id>" || volume.ProjectID != "<project-id>" || !volume.AttachedToServer("<server-id>") {
		t.Fatalf("unexpected underlying volume: %+v", volume)
	}
}

func TestVolumeListSnapshots(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/volume-1/snapshots" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "25" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/list_snapshots.json")
	}))

	snapshots, err := service.ListSnapshots(context.Background(), "volume-1", &ListSnapshotsOptions{Page: 2, Size: 25})
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(snapshots.Items) != 1 || snapshots.Items[0].ID != "snapshot-1" || snapshots.Page.PageSize != 25 {
		t.Fatalf("unexpected snapshots: %+v", snapshots)
	}
}

func TestVolumeListVolumeTypeZones(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_type_zones" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zoneId") != "zone-a" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/list_volume_type_zones.json")
	}))

	zones, err := service.ListVolumeTypeZones(context.Background(), &ListVolumeTypeZonesOptions{ZoneID: "zone-a"})
	if err != nil {
		t.Fatalf("ListVolumeTypeZones() error = %v", err)
	}
	if len(zones) != 1 || zones[0].ID != "volume-zone-1" || zones[0].PoolName[0] != "<name>" {
		t.Fatalf("unexpected zones: %+v", zones)
	}
}

func TestVolumeListVolumeTypes(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume-zone-1/volume_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/list_volume_types.json")
	}))

	types, err := service.ListVolumeTypes(context.Background(), &ListVolumeTypesOptions{VolumeTypeZoneID: "volume-zone-1"})
	if err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
	if len(types) != 1 || types[0].ID != "type-1" || types[0].Throughput != 200 {
		t.Fatalf("unexpected types: %+v", types)
	}
}

func TestVolumeListVolumeTypesProjectRoute(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"volumeTypes":[]}`))
	}))

	if _, err := service.ListVolumeTypes(context.Background(), nil); err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
}

func TestVolumeGetVolumeType(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_types/type-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/get_volume_type.json")
	}))

	volumeType, err := service.GetVolumeType(context.Background(), "type-1")
	if err != nil {
		t.Fatalf("GetVolumeType() error = %v", err)
	}
	if volumeType.ID != "type-1" || volumeType.ZoneID != "zone-a" {
		t.Fatalf("unexpected volume type: %+v", volumeType)
	}
}

func TestVolumeGetVolumeTypeNotFound(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"volumeTypes":[]}`))
	}))

	_, err := service.GetVolumeType(context.Background(), "missing")
	if !core.IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestVolumeGetDefaultVolumeType(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_default_id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/get_default_volume_type.json")
	}))

	volumeType, err := service.GetDefaultVolumeType(context.Background())
	if err != nil {
		t.Fatalf("GetDefaultVolumeType() error = %v", err)
	}
	if volumeType.ID != "type-1" || volumeType.VolumeTypeID != "type-1" || volumeType.ZoneID != "zone-a" || volumeType.VolumeTypeZoneID != "zone-a" {
		t.Fatalf("unexpected default volume type: %+v", volumeType)
	}
}

func TestVolumeListEncryptionTypes(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volumes/encryption_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/list_encryption_types.json")
	}))

	types, err := service.ListEncryptionTypes(context.Background())
	if err != nil {
		t.Fatalf("ListEncryptionTypes() error = %v", err)
	}
	if len(types) != 2 || types[0].Value != "aes-xts-plain64_128" {
		t.Fatalf("unexpected encryption types: %+v", types)
	}
}

func TestVolumeListEncryptionTypesArrayResponse(t *testing.T) {
	service := newTestService(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volumes/encryption_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../../testdata/volume/list_encryption_types_array.json")
	}))

	types, err := service.ListEncryptionTypes(context.Background())
	if err != nil {
		t.Fatalf("ListEncryptionTypes() error = %v", err)
	}
	if len(types) != 1 || types[0].Name != "<name>" {
		t.Fatalf("unexpected encryption types: %+v", types)
	}
}

func TestVolumeFixtureDecode(t *testing.T) {
	data, err := os.ReadFile("../../testdata/volume/list_volumes.json")
	if err != nil {
		t.Fatal(err)
	}
	var resp listVolumesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.ListData) != 1 || resp.ListData[0].Zone.UUID != "zone-a" || resp.ListData[0].ServerIDList[0] != "server-1" {
		t.Fatalf("unexpected fixture decode: %+v", resp)
	}
}

func newTestService(t *testing.T, handler http.Handler) *Service {
	t.Helper()

	client := testutil.NewCoreClient(t, handler)
	return New(client)
}
