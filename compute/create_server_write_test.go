package compute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func decodeComputeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait, so a test exercising a wait's full bound runs in milliseconds
// rather than the real bound. The fake clock advances by exactly the
// duration each sleep call is asked to wait.
func withInstantSleep(c *Client) *Client {
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
	return c
}

// emptyListServersPage stands in for ListServers finding nothing, the
// pre-order duplicate-name check CreateServer runs before pricing or
// ordering anything.
const emptyListServersPage = `{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`

// routeServerWriteRequest dispatches one CreateServer-flow request to the
// matching callback: the pre-order list (listBody, static), the quote
// (the fixed server quote fixture), the order POST (onCreate), or a
// GetServer read of "server-1" by id (onGetByID, optional). Any other
// request fails the test.
func routeServerWriteRequest(t *testing.T, w http.ResponseWriter, r *http.Request, listBody string, onCreate, onGetByID func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
		_, _ = w.Write([]byte(listBody))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
		_, _ = w.Write([]byte(quoteServerFixture))
	case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
		onCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1" && onGetByID != nil:
		onGetByID(w, r)
	default:
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

func staticGetServer(body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}
}

// --- CreateServer: request body and success ---

func TestCreateServerSendsCreateBodyAndSucceeds(t *testing.T) {
	var createBody map[string]any
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				createBody = decodeComputeBody(t, r)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","name":"web-1","status":"ACTIVE"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	out, err := c.CreateServer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	want := map[string]any{
		"name": "web-1", "zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1",
		"networkId": "vpc-1", "subnetId": "subnet-1", "sshKeyId": "key-1",
		"rootDiskSize": float64(20), "rootDiskTypeId": "voltype-1",
		"encryptionVolume": false, "isEnableAutoRenew": false,
	}
	for k, v := range want {
		if createBody[k] != v {
			t.Fatalf("createBody[%q] = %v, want %v", k, createBody[k], v)
		}
	}
	if _, ok := createBody["period"]; ok {
		t.Fatal("create body must not carry period, a quote-only field")
	}
	if out.Server.UUID != "server-1" || out.Server.Status != "ACTIVE" {
		t.Fatalf("unexpected server: %+v", out.Server)
	}
	if out.MonthlyPrice != 347800 {
		t.Fatalf("MonthlyPrice = %v, want 347800", out.MonthlyPrice)
	}
}

func TestCreateServerNoWaitSkipsWait(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				getCalls.Add(1)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
			},
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	in.NoWait = true
	out, err := c.CreateServer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if out.Server.UUID != "server-1" || out.Server.Name != "web-1" {
		t.Fatalf("unexpected server: %+v", out.Server)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GetServer calls = %d, want 0: NoWait must skip the post-create wait", getCalls.Load())
	}
}

func TestCreateServerUserDataIsSensitive(t *testing.T) {
	var capturedBody []byte
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				capturedBody = data
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{}}`)) // no uuid: forces a decode failure path check
			},
			nil,
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	in.UserData = "#!/bin/sh\necho supersecret"
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error for the missing id")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("error leaked user data: %v", err)
	}
	if !strings.Contains(string(capturedBody), "dXNlckRhdGE") && !strings.Contains(string(capturedBody), "userDataBase64Encoded") {
		t.Fatalf("expected the create body to carry base64 user data, got %s", capturedBody)
	}
}

// --- CreateServer: price guard ---

func TestCreateServerDefaultMaxPriceRefusesAndSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				t.Fatal("no order expected above MaxPrice")
			}, nil)
	}))

	_, err := c.CreateServer(context.Background(), validCreateServerInput())
	if !errors.Is(err, vngcloud.ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

func TestCreateServerQuoteEqualToMaxPriceSendsOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1", orderCalls.Load())
	}
}

func TestCreateServerRejectsInvalidMaxPriceBeforeAnyRequest(t *testing.T) {
	for _, tt := range []struct {
		name string
		v    float64
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"negative", -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("no request expected for an invalid MaxPrice")
			}))
			in := validCreateServerInput()
			in.MaxPrice = tt.v
			if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("CreateServer() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateServerQuoteFailureSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(emptyListServersPage))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"pricing unavailable"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
			orderCalls.Add(1)
			t.Fatal("no order expected after a quote failure")
		}
	}))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	if _, err := c.CreateServer(context.Background(), in); err == nil {
		t.Fatal("err = nil, want the quote's error")
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

// --- CreateServer: duplicate name ---

func TestCreateServerRefusesExactDuplicateName(t *testing.T) {
	var pricingCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"server-0","name":"web-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			pricingCalls.Add(1)
			t.Fatal("no pricing or order request when the name already exists")
		}
	}))

	_, err := c.CreateServer(context.Background(), validCreateServerInput())
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if pricingCalls.Load() != 0 {
		t.Fatalf("pricing/order calls = %d, want 0", pricingCalls.Load())
	}
}

func TestCreateServerAllowsSubstringNonExactMatch(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r,
			`{"listData":[{"uuid":"server-0","name":"web-10"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v, want a substring match to still allow the create", err)
	}
}

// --- CreateServer: no resend, and error statuses ---

func TestCreateServerOrderNotRetriedAfter502(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"upstream error"}`))
			}, nil)
	}))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1: a create must never be retried after a 5xx", orderCalls.Load())
	}
	if !strings.Contains(err.Error(), "list servers") {
		t.Fatalf("err = %v, want a hint to list servers before creating again", err)
	}
}

func TestCreateServerOrderStatuses(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeServerWriteRequest(t, w, r, emptyListServersPage,
					func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"message":"failed"}`))
					}, nil)
			}))
			in := validCreateServerInput()
			in.MaxPrice = 1000000
			if _, err := c.CreateServer(context.Background(), in); err == nil {
				t.Fatal("err = nil, want an error")
			}
		})
	}
}

func TestCreateServerNoIDInResponseFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}, nil)
	}))
	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list servers") {
		t.Fatalf("err = %v, want an APIError naming list servers before creating again", err)
	}
}

// --- CreateServer: wait ---

func TestCreateServerWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"ERROR"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	out, err := c.CreateServer(context.Background(), in)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.Server.UUID != "server-1" {
		t.Fatalf("Output = %+v, want the last read server", out)
	}
}

func TestCreateServerWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				if getCalls.Add(1) == 1 {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
			},
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	out, err := c.CreateServer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if out.Server.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.Server.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateServerWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"CREATING"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestWaitServerActivePollParameters checks the literal interval and bound
// waitServerActive passes to poll.
func TestWaitServerActivePollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"CREATING"}}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitServerActive(context.Background(), "op", "server-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 180 {
		t.Fatalf("sleep calls = %d, want 180 (a 5s interval over a 15-minute bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 5*time.Second {
			t.Fatalf("sleep duration = %s, want 5s", d)
		}
	}
}
