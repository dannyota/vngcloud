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
