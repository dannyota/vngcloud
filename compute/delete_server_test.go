package compute

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func serverBody(status string) string {
	return `{"data":{"uuid":"server-1","status":"` + status + `"}}`
}

func TestDeleteServerSendsDeleteAllVolumeFlag(t *testing.T) {
	var deleteBody map[string]any
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete:
			deleteBody = decodeComputeBody(t, r)
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	if _, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", DeleteVolumes: true, NoWait: true}); err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if deleteBody["deleteAllVolume"] != true {
		t.Fatalf("deleteAllVolume = %v, want true", deleteBody["deleteAllVolume"])
	}
}

func TestDeleteServerKeepsVolumesByDefault(t *testing.T) {
	var deleted atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"volume-1"},{"uuid":"volume-2"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-2":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case r.Method == http.MethodDelete:
			deleted.Store(true)
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if len(out.KeptVolumeIDs) != 1 || out.KeptVolumeIDs[0] != "volume-1" {
		t.Fatalf("KeptVolumeIDs = %v, want [volume-1]", out.KeptVolumeIDs)
	}
	if len(out.DeletedVolumeIDs) != 0 {
		t.Fatalf("DeletedVolumeIDs = %v, want empty", out.DeletedVolumeIDs)
	}
}

func TestDeleteServerDeletesVolumesReportsAll(t *testing.T) {
	var deleted atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"volume-1"},{"uuid":"volume-2"}]`))
		case r.Method == http.MethodDelete:
			deleted.Store(true)
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", DeleteVolumes: true})
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if len(out.DeletedVolumeIDs) != 2 {
		t.Fatalf("DeletedVolumeIDs = %v, want 2 entries", out.DeletedVolumeIDs)
	}
}

func TestDeleteServerNoWaitNamesUnconfirmed(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"volume-1"}]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if len(out.KeptVolumeIDs) != 1 || out.KeptVolumeIDs[0] != "volume-1" {
		t.Fatalf("KeptVolumeIDs = %v, want [volume-1] (unconfirmed)", out.KeptVolumeIDs)
	}
}

func TestDeleteServerUnknownServerFailsFast(t *testing.T) {
	var deleteCalled atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		case http.MethodDelete:
			deleteCalled.Store(true)
		}
	}))

	if _, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"}); !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
	if deleteCalled.Load() {
		t.Fatal("DELETE must not be sent for an unknown server")
	}
}

func TestDeleteServerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.DeleteServer(context.Background(), &DeleteServerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteServerPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestDeleteServerStatuses(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
					_, _ = w.Write([]byte(serverBody("ACTIVE")))
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
					_, _ = w.Write([]byte(`[]`))
				case r.Method == http.MethodDelete:
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"failed"}`))
				}
			}))
			if _, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", NoWait: true}); err == nil {
				t.Fatal("err = nil, want an error")
			}
		})
	}
}

// --- DeleteServer: wait ---

func TestDeleteServerWaitSettlesTo404(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet:
			getCalls++
			if getCalls <= 1 {
				_, _ = w.Write([]byte(serverBody("ACTIVE")))
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"not found"}`))
		}
	})))

	if _, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"}); err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
}

func TestDeleteServerWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(serverBody("ERROR")))
		}
	})))

	_, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestDeleteServerWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(serverBody("DELETING")))
		}
	})))

	_, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestDeleteServerWaitFailureNamesListedVolumes checks that a wait failure
// still names the volumes the server held before the delete, rather than
// an empty Output: they still cost money either way, and the caller should
// not have to re-derive them from the pre-delete list on its own.
func TestDeleteServerWaitFailureNamesListedVolumes(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"volume-1"},{"uuid":"volume-2"}]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(serverBody("ERROR")))
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if len(out.KeptVolumeIDs) != 2 {
		t.Fatalf("KeptVolumeIDs = %v, want [volume-1 volume-2]", out.KeptVolumeIDs)
	}
}

// TestDeleteServerWaitFailureNamesDeletedVolumes is
// TestDeleteServerWaitFailureNamesListedVolumes for Input.DeleteVolumes
// true: a wait failure names the volumes as DeletedVolumeIDs instead, the
// same field a settled delete would have used.
func TestDeleteServerWaitFailureNamesDeletedVolumes(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"volume-1"}]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(serverBody("ERROR")))
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", DeleteVolumes: true})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if len(out.DeletedVolumeIDs) != 1 || out.DeletedVolumeIDs[0] != "volume-1" {
		t.Fatalf("DeletedVolumeIDs = %v, want [volume-1]", out.DeletedVolumeIDs)
	}
}

// TestDeleteServerKeptVolumeCountsUnlessNotFound checks that a volume read
// failing with anything other than NotFound, here a 500, still counts as
// kept: only a confirmed absence may drop a volume that could still be
// billing.
func TestDeleteServerKeptVolumeCountsUnlessNotFound(t *testing.T) {
	var deleted atomic.Bool
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			if deleted.Load() {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"not found"}`))
				return
			}
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"volume-1"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"internal error"}`))
		case r.Method == http.MethodDelete:
			deleted.Store(true)
			w.WriteHeader(http.StatusAccepted)
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if len(out.KeptVolumeIDs) != 1 || out.KeptVolumeIDs[0] != "volume-1" {
		t.Fatalf("KeptVolumeIDs = %v, want [volume-1]: a non-NotFound read error must not drop a volume", out.KeptVolumeIDs)
	}
}

// TestWaitServerDeletedPollParameters checks the literal interval and
// bound waitServerDeleted passes to poll.
func TestWaitServerDeletedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(serverBody("DELETING")))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if err := c.waitServerDeleted(context.Background(), "op", "server-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 120 {
		t.Fatalf("sleep calls = %d, want 120 (a 5s interval over a 10-minute bound)", len(sleeps))
	}
}

func TestDeleteServerNoWaitOmitsBootVolumeFromKept(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE","bootVolumeId":"boot-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/servers/server-1":
			_, _ = w.Write([]byte(`[{"uuid":"boot-1"},{"uuid":"data-1"}]`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))

	out, err := c.DeleteServer(context.Background(), &DeleteServerInput{ServerID: "server-1", NoWait: true})
	if err != nil {
		t.Fatalf("DeleteServer() error = %v", err)
	}
	if len(out.KeptVolumeIDs) != 1 || out.KeptVolumeIDs[0] != "data-1" {
		t.Fatalf("KeptVolumeIDs = %v, want [data-1]: the boot volume goes with the server", out.KeptVolumeIDs)
	}
}
