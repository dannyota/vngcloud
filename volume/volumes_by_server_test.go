package volume

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

func TestListVolumesByServer(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/project-1/volumes/servers/server-1" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/volume/list_volumes_by_server.json")
	}))

	out, err := c.ListVolumesByServer(context.Background(), &ListVolumesByServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("ListVolumesByServer() error = %v", err)
	}
	if len(out.Items) != 1 || out.Items[0].UUID != "volume-1" {
		t.Fatalf("unexpected volumes: %+v", out.Items)
	}
}

func TestListVolumesByServerRequiresServerID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.ListVolumesByServer(context.Background(), &ListVolumesByServerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestListVolumesByServerRejectsBadServerID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.ListVolumesByServer(context.Background(), &ListVolumesByServerInput{ServerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// TestListVolumesByServerDecodesVolumesEnvelope decodes a sanitized live
// capture: the rows sit under "volumes", here the boot volume of a plain
// server, whose serverIdList is empty while serverId and serverNameList name
// the server.
func TestListVolumesByServerDecodesVolumesEnvelope(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, "../testdata/volume/list_volumes_by_server_volumes.json")
	}))

	out, err := c.ListVolumesByServer(context.Background(), &ListVolumesByServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("ListVolumesByServer() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(out.Items))
	}
	v := out.Items[0]
	if v.UUID != "<id>" || v.ServerID != "<server-id>" || v.Status != "IN-USE" || v.Size != 20 {
		t.Fatalf("unexpected row: %+v", v)
	}
	if !v.Bootable || v.BootIndex != 0 || v.MultiAttach || v.EncryptionType != nil {
		t.Fatalf("unexpected boot flags: %+v", v)
	}
	if len(v.ServerIDList) != 0 || len(v.ServerNameList) != 1 || v.Zone.UUID != "<id>" || v.VolumeTypeID != "<id>" {
		t.Fatalf("unexpected server or zone fields: %+v", v)
	}
}
