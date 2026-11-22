package volume

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/internal/testutil"
)

func TestDetachedVolumeSyntheticFixtureThroughSDK(t *testing.T) {
	var deletes atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		testutil.WriteFixture(t, w, "../testdata/volume/get_detached_volume_synthetic.json")
	}))
	out, err := c.GetVolume(context.Background(), &GetVolumeInput{VolumeID: "volume-1"})
	if err != nil || out == nil || out.Volume.UUID != "volume-1" || out.Volume.ServerID != "" || out.Volume.ServerIDList == nil {
		t.Fatalf("out = %v, error = %v", out, err)
	}
	if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1", NoWait: true}); err != nil || deletes.Load() != 1 {
		t.Fatalf("error = %v, deletes = %d", err, deletes.Load())
	}
}
