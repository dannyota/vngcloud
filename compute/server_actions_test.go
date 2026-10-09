package compute

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

// --- StartServer ---

func TestStartServerAlreadyActiveSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBody("ACTIVE")))
	}))

	out, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestStartServerWrongStatusRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBody("CREATING")))
	}))

	_, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus", err)
	}
}

func TestStartServerSendsPutOnceAndWaits(t *testing.T) {
	var putCalls atomic.Int64
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			if r.URL.Path != "/v2/project-1/servers/server-1/start" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			putCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			status := "STOPPED"
			if getCalls > 1 {
				status = "ACTIVE"
			}
			_, _ = w.Write([]byte(serverBody(status)))
		}
	})))

	out, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	if !out.Changed || out.Server.Status != "ACTIVE" {
		t.Fatalf("out = %+v, want Changed=true, Status=ACTIVE", out)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestStartServerPut502NotRetriedAndNotSettled(t *testing.T) {
	var putCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			putCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(serverBody("STOPPED")))
		}
	})))

	_, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1: Once must never be retried", putCalls.Load())
	}
}

func TestStartServerPut400ReturnsAPIError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"bad request"}`))
		case http.MethodGet:
			_, _ = w.Write([]byte(serverBody("STOPPED")))
		}
	})))

	_, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
	if errors.Is(err, ErrNotSettled) {
		t.Fatal("err wraps ErrNotSettled, want the raw APIError since the server never acted")
	}
}

func TestStartServerNoWaitReturnsAtOnce(t *testing.T) {
	getCalls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			_, _ = w.Write([]byte(serverBody("STOPPED")))
		}
	}))

	out, err := c.StartServer(context.Background(), &StartServerInput{ServerID: "server-1", NoWait: true})
	if err != nil {
		t.Fatalf("StartServer() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-toggle read only)", getCalls)
	}
}

func TestStartServerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.StartServer(context.Background(), &StartServerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestStartServerPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.StartServer(context.Background(), &StartServerInput{ServerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// --- StopServer ---

func TestStopServerAlreadyStoppedSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBody("STOPPED")))
	}))

	out, err := c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("StopServer() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false")
	}
}

func TestStopServerWrongStatusRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBody("CREATING")))
	}))

	_, err := c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus", err)
	}
}

func TestStopServerSendsPutOnceAndWaits(t *testing.T) {
	var putCalls atomic.Int64
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			if r.URL.Path != "/v2/project-1/servers/server-1/stop" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			putCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			status := "ACTIVE"
			if getCalls > 1 {
				status = "STOPPED"
			}
			_, _ = w.Write([]byte(serverBody(status)))
		}
	})))

	out, err := c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("StopServer() error = %v", err)
	}
	if !out.Changed || out.Server.Status != "STOPPED" {
		t.Fatalf("out = %+v, want Changed=true, Status=STOPPED", out)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestStopServerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.StopServer(context.Background(), &StopServerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// --- RebootServer ---

func TestRebootServerWrongStatusRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBody("STOPPED")))
	}))

	_, err := c.RebootServer(context.Background(), &RebootServerInput{ServerID: "server-1"})
	if !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus", err)
	}
}

func TestRebootServerSendsPutOnceAndWaitsAtLeast10s(t *testing.T) {
	var putCalls atomic.Int64
	getCalls := 0
	clock := time.Now()
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			if r.URL.Path != "/v2/project-1/servers/server-1/reboot" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			putCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			// Every read shows ACTIVE, including the first one right after
			// the PUT; the wait must still not settle until the fake clock
			// has advanced 10s worth of sleeps.
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		}
	}))
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}

	out, err := c.RebootServer(context.Background(), &RebootServerInput{ServerID: "server-1"})
	if err != nil {
		t.Fatalf("RebootServer() error = %v", err)
	}
	if out.Server.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.Server.Status)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
	// serverRebootMinWait is 10s and serverPollInterval is 5s: the wait
	// needs at least 3 reads (t=0, t=5s, t=10s) before it can settle.
	if getCalls < 3 {
		t.Fatalf("GET calls = %d, want at least 3: a read before 10s must not settle the wait", getCalls)
	}
}

func TestRebootServerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.RebootServer(context.Background(), &RebootServerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRebootServerNoWaitReturnsAtOnce(t *testing.T) {
	getCalls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			getCalls++
			_, _ = w.Write([]byte(serverBody("ACTIVE")))
		}
	}))

	if _, err := c.RebootServer(context.Background(), &RebootServerInput{ServerID: "server-1", NoWait: true}); err != nil {
		t.Fatalf("RebootServer() error = %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-reboot read only)", getCalls)
	}
}

// --- RenameServer ---

func TestRenameServerSendsNewNameAndReturnsServer(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/v2/project-1/servers/server-1/rename" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		body := decodeComputeBody(t, r)
		if body["newName"] != "web-2" {
			t.Fatalf("body = %+v, want newName=web-2", body)
		}
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","name":"web-2","status":"ACTIVE"}}`))
	}))

	out, err := c.RenameServer(context.Background(), &RenameServerInput{ServerID: "server-1", Name: "web-2"})
	if err != nil {
		t.Fatalf("RenameServer() error = %v", err)
	}
	if out.Server.Name != "web-2" {
		t.Fatalf("Name = %q, want web-2", out.Server.Name)
	}
}

func TestRenameServerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.RenameServer(context.Background(), &RenameServerInput{ServerID: "server-1"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.RenameServer(context.Background(), &RenameServerInput{Name: "web-2"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestRenameServerPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.RenameServer(context.Background(), &RenameServerInput{ServerID: bad, Name: "web-2"}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// checkWriteStatusError asserts err is the mapping of a write that got
// status. A 4xx is the raw APIError. A 5xx on a write sent once means the
// outcome is unknown, so it wraps ErrNotSettled and still carries the status.
func checkWriteStatusError(t *testing.T, err error, status int, sentOnce bool) {
	t.Helper()
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
		t.Fatalf("err = %v, want an *core.APIError with status %d", err, status)
	}
	if status == 404 && !core.IsNotFound(err) {
		t.Fatalf("err = %v, want not found", err)
	}
	if want := sentOnce && status >= 500; errors.Is(err, ErrNotSettled) != want {
		t.Fatalf("errors.Is(err, ErrNotSettled) = %v, want %v", !want, want)
	}
}

func TestServerWriteErrorStatuses(t *testing.T) {
	statuses := []int{400, 404, 409, 500, 502, 503}
	cases := []struct {
		name     string
		sentOnce bool
		call     func(c *Client) error
		get      string
	}{
		{"StopServer", true, func(c *Client) error {
			_, err := c.StopServer(context.Background(), &StopServerInput{ServerID: "server-1"})
			return err
		}, serverBody("ACTIVE")},
		{"RebootServer", true, func(c *Client) error {
			_, err := c.RebootServer(context.Background(), &RebootServerInput{ServerID: "server-1"})
			return err
		}, serverBody("ACTIVE")},
		{"RenameServer", false, func(c *Client) error {
			_, err := c.RenameServer(context.Background(), &RenameServerInput{ServerID: "server-1", Name: "web-2"})
			return err
		}, serverBody("ACTIVE")},
		{"ResizeServer", true, func(c *Client) error {
			in := validResizeServerInput()
			in.MaxPrice = 1000000
			_, err := c.ResizeServer(context.Background(), in)
			return err
		}, serverBodyWithFlavor("ACTIVE", "flavor-1")},
	}
	for _, tc := range cases {
		for _, status := range statuses {
			t.Run(fmt.Sprintf("%s/%d", tc.name, status), func(t *testing.T) {
				var writes atomic.Int64
				c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodGet:
						_, _ = w.Write([]byte(tc.get))
					case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
						_, _ = w.Write([]byte(quoteServerFixture))
					default:
						writes.Add(1)
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"message":"failed"}`))
					}
				})))
				err := tc.call(c)
				checkWriteStatusError(t, err, status, tc.sentOnce)
				if tc.sentOnce && writes.Load() != 1 {
					t.Fatalf("writes = %d, want 1", writes.Load())
				}
			})
		}
	}
}

func TestResizeServer429SentOnce(t *testing.T) {
	var writes atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				writes.Add(1)
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"message":"slow down"}`))
			},
		)
	}))
	in := validResizeServerInput()
	in.MaxPrice = 1000000
	_, err := c.ResizeServer(context.Background(), in)
	if !core.IsRateLimited(err) {
		t.Fatalf("err = %v, want rate limited", err)
	}
	if writes.Load() != 1 {
		t.Fatalf("writes = %d, want 1", writes.Load())
	}
}
