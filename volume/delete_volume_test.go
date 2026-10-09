package volume

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestDeleteVolumeGuardInUseStatus(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"IN-USE"}}`))
	}))

	_, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"})
	if !errors.Is(err, ErrVolumeInUse) {
		t.Fatalf("err = %v, want ErrVolumeInUse", err)
	}
}

func TestDeleteVolumeGuardListsServer(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE","serverId":"server-1"}}`))
	}))

	_, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"})
	if !errors.Is(err, ErrVolumeInUse) {
		t.Fatalf("err = %v, want ErrVolumeInUse", err)
	}
}

func TestDeleteVolumeGuardServerIDList(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE","serverIdList":["server-1"]}}`))
	}))

	_, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"})
	if !errors.Is(err, ErrVolumeInUse) {
		t.Fatalf("err = %v, want ErrVolumeInUse", err)
	}
}

func TestDeleteVolumeSendsDeleteWhenClear(t *testing.T) {
	var deleteCalled atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			deleteCalled.Store(true)
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
		}
	}))

	if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if !deleteCalled.Load() {
		t.Fatal("DELETE was never sent")
	}
}

func TestDeleteVolumeRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteVolumePathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("VolumeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestDeleteVolumeStatuses(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
				case http.MethodDelete:
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"failed"}`))
				}
			}))
			if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1", NoWait: true}); err == nil {
				t.Fatal("err = nil, want an error")
			}
		})
	}
}

// --- DeleteVolume: wait ---

func TestDeleteVolumeWaitSettlesTo404(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			if getCalls <= 1 {
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	})))

	if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
}

func TestDeleteVolumeWaitSettlesOnDeletedStatus(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			status := "AVAILABLE"
			if getCalls > 1 {
				status = "DELETED"
			}
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"` + status + `"}}`))
		}
	})))

	if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
}

func TestDeleteVolumeWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"ERROR"}}`))
		}
	})))

	_, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestDeleteVolumeWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"DELETING"}}`))
		}
	})))

	_, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestDeleteVolumeNoWaitSkipsWait(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
		}
	})))

	if _, err := c.DeleteVolume(context.Background(), &DeleteVolumeInput{VolumeID: "volume-1", NoWait: true}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-delete guard read only): NoWait must skip the post-delete wait", getCalls)
	}
}

// TestWaitVolumeDeletedPollParameters checks the literal interval and bound
// waitVolumeDeleted passes to poll.
func TestWaitVolumeDeletedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"DELETING"}}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if err := c.waitVolumeDeleted(context.Background(), "op", "volume-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 150 {
		t.Fatalf("sleep calls = %d, want 150 (a 2s interval over a 5-minute bound)", len(sleeps))
	}
}
