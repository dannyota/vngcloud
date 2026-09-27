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

func volumeBody(status string, serverID string) string {
	extra := ""
	if serverID != "" {
		extra = `,"serverId":"` + serverID + `"`
	}
	return `{"data":{"uuid":"volume-1","status":"` + status + `"` + extra + `}}`
}

// --- AttachVolume ---

func TestAttachVolumeAlreadyAttachedSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
	}))

	out, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestAttachVolumeSendsPutAndWaits(t *testing.T) {
	var putCalls atomic.Int64
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			if r.URL.Path != "/v2/project-1/volumes/volume-1/servers/server-1/attach" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			putCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			if getCalls == 1 {
				_, _ = w.Write([]byte(volumeBody("AVAILABLE", "")))
				return
			}
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		}
	})))

	out, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if !out.Changed || out.Volume.Status != "IN-USE" {
		t.Fatalf("out = %+v, want Changed=true, Status=IN-USE", out)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestAttachVolumeKeepsRetriesOn502(t *testing.T) {
	var putCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(volumeBody("AVAILABLE", "")))
		}
	}))

	if _, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1", NoWait: true}); err == nil {
		t.Fatal("err = nil, want an error")
	}
}

func TestAttachVolumeNoWaitReturnsAtOnce(t *testing.T) {
	getCalls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			_, _ = w.Write([]byte(volumeBody("AVAILABLE", "")))
		}
	}))

	out, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1", NoWait: true})
	if err != nil {
		t.Fatalf("AttachVolume() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-attach read only)", getCalls)
	}
}

func TestAttachVolumeRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.AttachVolume(context.Background(), &AttachVolumeInput{ServerID: "server-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestAttachVolumePathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: bad, ServerID: "server-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("VolumeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
		if _, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestAttachVolumeWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			_, _ = w.Write([]byte(volumeBody("ERROR", "")))
		}
	})))

	_, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestAttachVolumeWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			_, _ = w.Write([]byte(volumeBody("ATTACHING", "")))
		}
	})))

	_, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// --- DetachVolume ---

func TestDetachVolumeNotAttachedSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(volumeBody("AVAILABLE", "")))
	}))

	out, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestDetachVolumeBootVolumeRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"IN-USE","serverId":"server-1","bootable":true}}`))
	}))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrBootVolume) {
		t.Fatalf("err = %v, want ErrBootVolume", err)
	}
}

func TestDetachVolumeRunningServerRefusedWithoutAllowRunning(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrServerRunning) {
		t.Fatalf("err = %v, want ErrServerRunning", err)
	}
}

func TestDetachVolumeAllowRunningSkipsServerRead(t *testing.T) {
	var serverReadCalled atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			serverReadCalled.Store(true)
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	if _, err := c.DetachVolume(context.Background(), &DetachVolumeInput{
		VolumeID: "volume-1", ServerID: "server-1", AllowRunning: true, NoWait: true,
	}); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if serverReadCalled.Load() {
		t.Fatal("server status was read even though AllowRunning was set")
	}
}

func TestDetachVolumeSendsPutAndWaits(t *testing.T) {
	var putCalls atomic.Int64
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			getCalls++
			if getCalls == 1 {
				_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
				return
			}
			_, _ = w.Write([]byte(volumeBody("AVAILABLE", "")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED"}}`))
		case r.Method == http.MethodPut:
			if r.URL.Path != "/v2/project-1/volumes/volume-1/servers/server-1/detach" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			putCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	out, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if !out.Changed || out.Volume.Status != "AVAILABLE" {
		t.Fatalf("out = %+v, want Changed=true, Status=AVAILABLE", out)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestDetachVolumeRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.DetachVolume(context.Background(), &DetachVolumeInput{ServerID: "server-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDetachVolumePathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: bad, ServerID: "server-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("VolumeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
		if _, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestDetachVolumeWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("ERROR", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED"}}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestDetachVolumeWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED"}}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

func TestDetachVolumeNoWaitReturnsAtOnce(t *testing.T) {
	getCalls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			getCalls++
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED"}}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		}
	}))

	out, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1", NoWait: true})
	if err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls != 1 {
		t.Fatalf("GetVolume calls = %d, want 1 (the pre-detach read only)", getCalls)
	}
}

// TestWaitVolumeAttachedPollParameters and detached check the literal
// interval and bound the waits pass to poll.
func TestWaitVolumeAttachedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(volumeBody("ATTACHING", "")))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitVolumeAttached(context.Background(), "op", "volume-1", "server-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 150 {
		t.Fatalf("sleep calls = %d, want 150 (a 2s interval over a 5-minute bound)", len(sleeps))
	}
}
