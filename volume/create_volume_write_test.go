package volume

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

func decodeVolumeBody(t *testing.T, r *http.Request) map[string]any {
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

func validCreateVolumeInput() *CreateVolumeInput {
	return &CreateVolumeInput{Name: "vol-1", ZoneID: "zone-1", Size: 10, VolumeTypeID: "voltype-1"}
}

// emptyListVolumesPage stands in for ListVolumes finding nothing by name,
// the pre-order duplicate-name check CreateVolume runs before pricing or
// ordering anything.
const emptyListVolumesPage = `{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`

// routeVolumeWriteRequest dispatches one CreateVolume-flow request to the
// matching callback: the pre-order list (listBody, static), the quote
// (quoteBody, static), the order POST (onCreate), or a GetVolume read of
// "volume-1" by id (onGetByID, optional). Any other request fails the test.
func routeVolumeWriteRequest(t *testing.T, w http.ResponseWriter, r *http.Request, listBody string, onCreate, onGetByID func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes":
		_, _ = w.Write([]byte(listBody))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
		_, _ = w.Write([]byte(quoteVolumeFixture))
	case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/volumes":
		onCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1" && onGetByID != nil:
		onGetByID(w, r)
	default:
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

// staticGet writes body for every GetVolume-by-id read.
func staticGet(body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}
}

// --- CreateVolume: request body and success ---

func TestCreateVolumeSendsCreateBodyAndSucceeds(t *testing.T) {
	var createBody map[string]any
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				createBody = decodeVolumeBody(t, r)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			staticGet(`{"data":{"uuid":"volume-1","name":"vol-1","status":"AVAILABLE"}}`),
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 32000
	out, err := c.CreateVolume(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if createBody["name"] != "vol-1" || createBody["size"] != float64(10) || createBody["volumeTypeId"] != "voltype-1" || createBody["zoneId"] != "zone-1" {
		t.Fatalf("unexpected create body: %+v", createBody)
	}
	if createBody["isEnableAutoRenew"] != false {
		t.Fatalf("isEnableAutoRenew = %v, want false", createBody["isEnableAutoRenew"])
	}
	if out.Volume.UUID != "volume-1" || out.Volume.Status != "AVAILABLE" {
		t.Fatalf("unexpected volume: %+v", out.Volume)
	}
	if out.MonthlyPrice != 32000 {
		t.Fatalf("MonthlyPrice = %v, want 32000", out.MonthlyPrice)
	}
}

func TestCreateVolumeNoWaitSkipsWait(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				getCalls.Add(1)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
			},
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 32000
	in.NoWait = true
	out, err := c.CreateVolume(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if out.Volume.UUID != "volume-1" || out.Volume.Name != "vol-1" {
		t.Fatalf("unexpected volume: %+v", out.Volume)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GetVolume calls = %d, want 0: NoWait must skip the post-create wait", getCalls.Load())
	}
}

// --- CreateVolume: price guard ---

func TestCreateVolumeDefaultMaxPriceRefusesAndSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				t.Fatal("no order expected above MaxPrice")
			}, nil)
	}))

	_, err := c.CreateVolume(context.Background(), validCreateVolumeInput())
	if !errors.Is(err, vngcloud.ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

func TestCreateVolumeQuoteEqualToMaxPriceSendsOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			staticGet(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`),
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 32000
	if _, err := c.CreateVolume(context.Background(), in); err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1", orderCalls.Load())
	}
}

func TestCreateVolumeRejectsInvalidMaxPriceBeforeAnyRequest(t *testing.T) {
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
			in := validCreateVolumeInput()
			in.MaxPrice = tt.v
			if _, err := c.CreateVolume(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("CreateVolume() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateVolumeQuoteFailureSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes":
			_, _ = w.Write([]byte(emptyListVolumesPage))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"pricing unavailable"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/volumes":
			orderCalls.Add(1)
			t.Fatal("no order expected after a quote failure")
		}
	}))

	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	if _, err := c.CreateVolume(context.Background(), in); err == nil {
		t.Fatal("err = nil, want the quote's error")
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

func TestCreateVolumeQuoteMissingOptimumPriceSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes":
			_, _ = w.Write([]byte(emptyListVolumesPage))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			_, _ = w.Write([]byte(`{"message":"ok"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/volumes":
			orderCalls.Add(1)
			t.Fatal("no order expected when the quote has no price")
		}
	}))

	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	if _, err := c.CreateVolume(context.Background(), in); err == nil {
		t.Fatal("err = nil, want an error for the missing price")
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

// --- CreateVolume: duplicate name ---

func TestCreateVolumeRefusesExactDuplicateName(t *testing.T) {
	var pricingCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"volume-0","name":"vol-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			pricingCalls.Add(1)
			t.Fatal("no pricing or order request when the name already exists")
		}
	}))

	_, err := c.CreateVolume(context.Background(), validCreateVolumeInput())
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if pricingCalls.Load() != 0 {
		t.Fatalf("pricing/order calls = %d, want 0", pricingCalls.Load())
	}
}

func TestCreateVolumeAllowsSubstringNonExactMatch(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r,
			`{"listData":[{"uuid":"volume-0","name":"vol-10"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			staticGet(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`),
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 32000
	if _, err := c.CreateVolume(context.Background(), in); err != nil {
		t.Fatalf("CreateVolume() error = %v, want a substring match to still allow the create", err)
	}
}

// --- CreateVolume: no resend, and error statuses ---

func TestCreateVolumeOrderNotRetriedAfter502(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"upstream error"}`))
			}, nil)
	}))

	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	_, err := c.CreateVolume(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1: a create must never be retried after a 5xx", orderCalls.Load())
	}
	if !strings.Contains(err.Error(), "list volumes") {
		t.Fatalf("err = %v, want a hint to list volumes before creating again", err)
	}
}

func TestCreateVolumeOrderStatuses(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
					func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"message":"failed"}`))
					}, nil)
			}))
			in := validCreateVolumeInput()
			in.MaxPrice = 1000000
			if _, err := c.CreateVolume(context.Background(), in); err == nil {
				t.Fatal("err = nil, want an error")
			}
		})
	}
}

func TestCreateVolumeNoIDInResponseFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}, nil)
	}))
	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	_, err := c.CreateVolume(context.Background(), in)
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list volumes") {
		t.Fatalf("err = %v, want an APIError naming list volumes before creating again", err)
	}
}

// --- CreateVolume: wait ---

func TestCreateVolumeWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			staticGet(`{"data":{"uuid":"volume-1","status":"ERROR"}}`),
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	out, err := c.CreateVolume(context.Background(), in)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.Volume.UUID != "volume-1" {
		t.Fatalf("Output = %+v, want the last read volume", out)
	}
}

func TestCreateVolumeWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				if getCalls.Add(1) == 1 {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"AVAILABLE"}}`))
			},
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	out, err := c.CreateVolume(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateVolume() error = %v", err)
	}
	if out.Volume.Status != "AVAILABLE" {
		t.Fatalf("Status = %q, want AVAILABLE", out.Volume.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateVolumeWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeVolumeWriteRequest(t, w, r, emptyListVolumesPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1"}}`))
			},
			staticGet(`{"data":{"uuid":"volume-1","status":"CREATING"}}`),
		)
	})))

	in := validCreateVolumeInput()
	in.MaxPrice = 1000000
	_, err := c.CreateVolume(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestWaitVolumeAvailablePollParameters checks the literal interval and
// bound waitVolumeAvailable passes to poll.
func TestWaitVolumeAvailablePollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"uuid":"volume-1","status":"CREATING"}}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitVolumeAvailable(context.Background(), "op", "volume-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 150 {
		t.Fatalf("sleep calls = %d, want 150 (a 2s interval over a 5-minute bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 2*time.Second {
			t.Fatalf("sleep duration = %s, want 2s", d)
		}
	}
}
