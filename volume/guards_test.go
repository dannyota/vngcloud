package volume

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
)

func TestDeleteVolumeRejectsUnknownOrSubstitutedState(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"data":null}`, `{"data":{}}`,
		`{"data":{"uuid":"other","status":"AVAILABLE","serverId":"","serverIdList":[]}}`,
		`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`,
		`{"data":{"uuid":"volume-1","status":"AVAILABLE","serverId":null,"serverIdList":null}}`,
		`{"data":{"uuid":"volume-1","status":"UNKNOWN","serverId":"","serverIdList":[]}}`,
		`{"data":{"uuid":"volume-1","status":"IN-USE","STATUS":"AVAILABLE","serverId":"","serverIdList":[]}}`,
		`{"data":{"uuid":"other","UUID":"volume-1","status":"AVAILABLE","serverId":"","serverIdList":[]}}`,
		`{"data":{"uuid":"volume-1","status":"AVAILABLE","serverId":"server-1","SERVERID":"","serverIdList":[]}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var deletes atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes.Add(1)
					w.WriteHeader(202)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			_, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1", NoWait: true})
			if err == nil || deletes.Load() != 0 {
				t.Fatalf("error = %v, deletes = %d", err, deletes.Load())
			}
		})
	}
}

func TestVolumeReadPathValidationBeforeRequests(t *testing.T) {
	var requests atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(404) }))
	for _, id := range []string{"..", ".", "a/b", "a?b", "a%b"} {
		if _, err := c.GetUnderlyingVolume(context.Background(), &GetUnderlyingVolumeInput{VolumeID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("mapping %q: %v", id, err)
		}
		if _, err := c.GetVolumeType(context.Background(), &GetVolumeTypeInput{VolumeTypeID: id}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Errorf("type %q: %v", id, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}
