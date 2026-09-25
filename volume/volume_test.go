package volume

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

func TestVolumeListVolumes(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("name") != "data" || r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "10" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_volumes.json")
	}))

	out, err := c.ListVolumes(context.Background(), &ListVolumesInput{Name: "data", Page: 2, Size: 10})
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "volume-1" {
		t.Fatalf("unexpected volumes: %+v", out)
	}
	if !out.Items[0].IsInUse() || !out.Items[0].AttachedToServer("server-1") || out.Items[0].CanDelete() {
		t.Fatalf("unexpected volume helpers: %+v", out.Items[0])
	}
}

func TestVolumeListVolumesDefaultPageSize(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") != "1" || r.URL.Query().Get("size") != "10000" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	if _, err := c.ListVolumes(context.Background(), nil); err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
}

func TestVolumeGetVolume(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/volume-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/get_volume.json")
	}))

	out, err := c.GetVolume(context.Background(), &GetVolumeInput{VolumeID: "volume-1"})
	if err != nil {
		t.Fatalf("GetVolume() error = %v", err)
	}
	if out.Volume.UUID != "volume-1" || !out.Volume.IsAvailable() || !out.Volume.CanDelete() {
		t.Fatalf("unexpected volume: %+v", out.Volume)
	}
}

func TestVolumeGetUnderlyingVolume(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/volume-1/mapping" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/get_underlying_volume.json")
	}))

	out, err := c.GetUnderlyingVolume(context.Background(), &GetUnderlyingVolumeInput{VolumeID: "volume-1"})
	if err != nil {
		t.Fatalf("GetUnderlyingVolume() error = %v", err)
	}
	if out.Volume.UUID != "<volume-id>" || out.Volume.ProjectID != "<project-id>" || !out.Volume.AttachedToServer("<server-id>") {
		t.Fatalf("unexpected underlying volume: %+v", out.Volume)
	}
}

func TestVolumeListSnapshots(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/volume-1/snapshots" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("page") != "2" || r.URL.Query().Get("size") != "25" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_snapshots.json")
	}))

	out, err := c.ListSnapshots(context.Background(), &ListSnapshotsInput{VolumeID: "volume-1", Page: 2, Size: 25})
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "snapshot-1" || out.PageSize != 25 {
		t.Fatalf("unexpected snapshots: %+v", out)
	}
}

func TestVolumeListVolumeTypeZones(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_type_zones" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("zoneId") != "zone-a" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_volume_type_zones.json")
	}))

	out, err := c.ListVolumeTypeZones(context.Background(), &ListVolumeTypeZonesInput{ZoneID: "zone-a"})
	if err != nil {
		t.Fatalf("ListVolumeTypeZones() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "volume-zone-1" || out.Items[0].PoolName[0] != "<name>" {
		t.Fatalf("unexpected zones: %+v", out)
	}
}

func TestVolumeListVolumeTypes(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume-zone-1/volume_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_volume_types.json")
	}))

	out, err := c.ListVolumeTypes(context.Background(), &ListVolumeTypesInput{VolumeTypeZoneID: "volume-zone-1"})
	if err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].ID != "type-1" || out.Items[0].Throughput != 200 {
		t.Fatalf("unexpected types: %+v", out)
	}
}

func TestVolumeListVolumeTypesProjectRoute(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"volumeTypes":[]}`))
	}))

	if _, err := c.ListVolumeTypes(context.Background(), nil); err != nil {
		t.Fatalf("ListVolumeTypes() error = %v", err)
	}
}

func TestVolumeGetVolumeType(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_types/type-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/get_volume_type.json")
	}))

	out, err := c.GetVolumeType(context.Background(), &GetVolumeTypeInput{VolumeTypeID: "type-1"})
	if err != nil {
		t.Fatalf("GetVolumeType() error = %v", err)
	}
	if out.VolumeType.ID != "type-1" || out.VolumeType.ZoneID != "zone-a" {
		t.Fatalf("unexpected volume type: %+v", out.VolumeType)
	}
}

func TestVolumeGetVolumeTypeNotFound(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"volumeTypes":[]}`))
	}))

	_, err := c.GetVolumeType(context.Background(), &GetVolumeTypeInput{VolumeTypeID: "missing"})
	if !core.IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestVolumeGetDefaultVolumeType(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volume_default_id" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/get_default_volume_type.json")
	}))

	out, err := c.GetDefaultVolumeType(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetDefaultVolumeType() error = %v", err)
	}
	if out.VolumeType.ID != "type-1" || out.VolumeType.VolumeTypeID != "type-1" || out.VolumeType.ZoneID != "zone-a" || out.VolumeType.VolumeTypeZoneID != "zone-a" {
		t.Fatalf("unexpected default volume type: %+v", out.VolumeType)
	}
}

func TestVolumeListEncryptionTypes(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volumes/encryption_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_encryption_types.json")
	}))

	out, err := c.ListEncryptionTypes(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListEncryptionTypes() error = %v", err)
	}
	if len(out.Items) != 2 || out.Items[0].Value != "aes-xts-plain64_128" {
		t.Fatalf("unexpected encryption types: %+v", out)
	}
}

func TestVolumeListEncryptionTypesArrayResponse(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/project-1/volumes/encryption_types" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_encryption_types_array.json")
	}))

	out, err := c.ListEncryptionTypes(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListEncryptionTypes() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "<name>" {
		t.Fatalf("unexpected encryption types: %+v", out)
	}
}

func TestVolumeFixtureDecode(t *testing.T) {
	data, err := os.ReadFile("../testdata/volume/list_volumes.json")
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

func TestVolumeRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	_, err := c.GetVolume(context.Background(), &GetVolumeInput{})
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v", err)
	}
	if _, err := c.GetVolume(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v", err)
	}
}

func newTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()

	return New(testutil.NewConfig(t, handler))
}
