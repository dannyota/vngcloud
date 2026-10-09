package volume

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
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
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE","bootVolumeId":"boot-volume-1"}}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrServerRunning) {
		t.Fatalf("err = %v, want ErrServerRunning", err)
	}
}

// TestDetachVolumeRefusesEveryNonStoppedStatusWithoutAllowRunning checks
// that a status other than STOPPED refuses with ErrServerRunning, not only
// ACTIVE: a transitional status such as REBOOTING or STARTING, or an empty
// one, may still have the volume mounted, so only STOPPED is accepted.
func TestDetachVolumeRefusesEveryNonStoppedStatusWithoutAllowRunning(t *testing.T) {
	for _, status := range []string{"REBOOTING", "STARTING", ""} {
		t.Run(status, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
					_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
					_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"` + status + `","bootVolumeId":"boot-volume-1"}}`))
				default:
					t.Fatal("no write expected")
				}
			}))

			_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
			if !errors.Is(err, ErrServerRunning) {
				t.Fatalf("status %q: err = %v, want ErrServerRunning", status, err)
			}
		})
	}
}

// TestDetachVolumeAllowRunningStillReadsServerForBootCheck checks that
// AllowRunning skips only the running-server status refusal, not the
// server read itself: DetachVolume always reads the server to cross-check
// the boot volume id, even when the caller has consented to detaching from
// a running server.
func TestDetachVolumeAllowRunningStillReadsServerForBootCheck(t *testing.T) {
	var serverReadCalled atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			serverReadCalled.Store(true)
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE","bootVolumeId":"boot-volume-1"}}`))
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	if _, err := c.DetachVolume(context.Background(), &DetachVolumeInput{
		VolumeID: "volume-1", ServerID: "server-1", AllowRunning: true, NoWait: true,
	}); err != nil {
		t.Fatalf("DetachVolume() error = %v", err)
	}
	if !serverReadCalled.Load() {
		t.Fatal("server was never read: the boot volume cross-check needs it even with AllowRunning")
	}
}

// TestDetachVolumeMissingBootVolumeIDFailsClosed checks that a server read
// carrying no BootVolumeID at all refuses with ErrBootVolume, even though
// the volume itself is not marked Bootable: a missing id cannot rule out
// this being the boot volume.
func TestDetachVolumeMissingBootVolumeIDFailsClosed(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED"}}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1"})
	if !errors.Is(err, ErrBootVolume) {
		t.Fatalf("err = %v, want ErrBootVolume", err)
	}
}

// TestDetachVolumeIDMatchesServerBootVolumeIDRefused checks that a volume
// id equal to the server's own BootVolumeID refuses with ErrBootVolume even
// when the volume's own Bootable field says false, and even with
// AllowRunning set: the server's own record is a second, independent
// signal that neither AllowRunning nor a wrong Bootable value overrides.
func TestDetachVolumeIDMatchesServerBootVolumeIDRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(volumeBody("IN-USE", "server-1")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE","bootVolumeId":"volume-1"}}`))
		default:
			t.Fatal("no write expected")
		}
	}))

	_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1", AllowRunning: true})
	if !errors.Is(err, ErrBootVolume) {
		t.Fatalf("err = %v, want ErrBootVolume", err)
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
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED","bootVolumeId":"boot-volume-1"}}`))
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
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED","bootVolumeId":"boot-volume-1"}}`))
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
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED","bootVolumeId":"boot-volume-1"}}`))
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
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED","bootVolumeId":"boot-volume-1"}}`))
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

func TestVolumeWriteErrorStatuses(t *testing.T) {
	statuses := []int{400, 404, 409, 500, 502, 503}
	cases := []struct {
		name     string
		sentOnce bool
		get      string
		call     func(c *Client) error
	}{
		{"AttachVolume", false, volumeBody("AVAILABLE", ""), func(c *Client) error {
			_, err := c.AttachVolume(context.Background(), &AttachVolumeInput{VolumeID: "volume-1", ServerID: "server-1", NoWait: true})
			return err
		}},
		{"DetachVolume", false, volumeBody("IN-USE", "server-1"), func(c *Client) error {
			_, err := c.DetachVolume(context.Background(), &DetachVolumeInput{VolumeID: "volume-1", ServerID: "server-1", NoWait: true})
			return err
		}},
		{"ResizeVolume", true, volumeBodyWithSize("AVAILABLE", 10), func(c *Client) error {
			_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000, NoWait: true})
			return err
		}},
	}
	for _, tc := range cases {
		for _, status := range statuses {
			t.Run(fmt.Sprintf("%s/%d", tc.name, status), func(t *testing.T) {
				var writes atomic.Int64
				c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
						_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"STOPPED","bootVolumeId":"boot-volume-1"}}`))
					case r.Method == http.MethodGet:
						_, _ = w.Write([]byte(tc.get))
					case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
						_, _ = w.Write([]byte(quoteVolumeFixture))
					default:
						writes.Add(1)
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"message":"failed"}`))
					}
				})))
				err := tc.call(c)
				var apiErr *core.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
					t.Fatalf("err = %v, want an *core.APIError with status %d", err, status)
				}
				if status == 404 && !core.IsNotFound(err) {
					t.Fatalf("err = %v, want not found", err)
				}
				if tc.sentOnce && writes.Load() != 1 {
					t.Fatalf("writes = %d, want 1", writes.Load())
				}
			})
		}
	}
}

func TestResizeVolume429SentOnce(t *testing.T) {
	var writes atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				writes.Add(1)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"message":"slow down"}`))
			},
		)
	}))
	_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000})
	if !core.IsRateLimited(err) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	if writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", writes.Load())
	}
}
