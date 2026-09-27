package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait, so a wait test exercising a full bound runs in milliseconds rather
// than the real pollInterval and bound. The fake clock advances by exactly
// the duration each sleep call is asked to wait, so a wait's bound is still
// reached after the same number of iterations a real clock would take.
func withInstantSleep(c *Client) {
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
}

// createLoadBalancerUUID is the id every createLoadBalancerHandler test
// fixture in this file creates.
const createLoadBalancerUUID = "lb-new"

// createLoadBalancerHandler serves the three endpoints CreateLoadBalancer's
// full flow touches: the price quote (400000 VND, matching the small
// package fixture), the create POST, and GetLoadBalancer reads during the
// post-create wait. getStatuses is read in order, one value per GET; the
// last value repeats once exhausted.
func createLoadBalancerHandler(getStatuses []string, postCalls *atomic.Int32) http.HandlerFunc {
	var getCalls int
	getPath := "/v2/project-1/loadBalancers/" + createLoadBalancerUUID
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/loadBalancers":
			if postCalls != nil {
				postCalls.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, createLoadBalancerUUID)
		case r.Method == http.MethodGet && r.URL.Path == getPath:
			status := ""
			if len(getStatuses) > 0 {
				if getCalls < len(getStatuses) {
					status = getStatuses[getCalls]
				} else {
					status = getStatuses[len(getStatuses)-1]
				}
			}
			getCalls++
			if status == "404" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Cannot get load balancer with id ` + createLoadBalancerUUID + `"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, createLoadBalancerUUID, status)
		default:
			panic("unexpected request: " + r.Method + " " + r.URL.Path)
		}
	}
}

func TestCreateLoadBalancerRequestBody(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, createLoadBalancerHandlerCapture(t, &body))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	if _, err := c.CreateLoadBalancer(context.Background(), in); err != nil {
		t.Fatalf("CreateLoadBalancer() error = %v", err)
	}
	want := map[string]any{
		"name":         "lb-1",
		"packageId":    "pkg-1",
		"scheme":       "Internal",
		"subnetId":     "subnet-1",
		"type":         "Layer 4",
		"zoneId":       "zone-1",
		"autoScalable": false,
		"isPoc":        false,
	}
	for k, v := range want {
		if body[k] != v {
			t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
		}
	}
	if len(body) != len(want) {
		t.Fatalf("body = %+v, want exactly %+v (no listener or pool key)", body, want)
	}
}

// createLoadBalancerHandlerCapture is createLoadBalancerHandler but also
// decodes the create POST's body into out.
func createLoadBalancerHandlerCapture(t *testing.T, out *map[string]any) http.HandlerFunc {
	t.Helper()
	inner := createLoadBalancerHandler([]string{lbStatusCreated}, nil)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/loadBalancers" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, out); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
		}
		inner(w, r)
	}
}

func TestCreateLoadBalancerSuccess(t *testing.T) {
	c := newTestClient(t, createLoadBalancerHandler([]string{lbStatusCreated}, nil))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	out, err := c.CreateLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateLoadBalancer() error = %v", err)
	}
	if out.LoadBalancer.UUID != createLoadBalancerUUID || out.LoadBalancer.ProgressStatus != lbStatusCreated {
		t.Fatalf("LoadBalancer = %+v, want CREATED lb-new", out.LoadBalancer)
	}
	if out.QuotedPrice != 400000 {
		t.Fatalf("QuotedPrice = %v, want 400000", out.QuotedPrice)
	}
}

func TestCreateLoadBalancerNoWaitSkipsPolling(t *testing.T) {
	var getCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			_, _ = w.Write([]byte(`{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/loadBalancers":
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, createLoadBalancerUUID)
		case r.Method == http.MethodGet:
			getCalls.Add(1)
			t.Fatal("NoWait must not read the load balancer")
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	in.NoWait = true
	out, err := c.CreateLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateLoadBalancer() error = %v", err)
	}
	if out.LoadBalancer.UUID != createLoadBalancerUUID {
		t.Fatalf("LoadBalancer.UUID = %q, want lb-new", out.LoadBalancer.UUID)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GET calls = %d, want 0", getCalls.Load())
	}
}

func TestCreateLoadBalancerPriceAboveMaxSendsNothing(t *testing.T) {
	var postCalls atomic.Int32
	c := newTestClient(t, createLoadBalancerHandler(nil, &postCalls))

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 100000
	_, err := c.CreateLoadBalancer(context.Background(), in)
	if !errors.Is(err, ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if !strings.Contains(err.Error(), "400000") || !strings.Contains(err.Error(), "100000") {
		t.Fatalf("err = %v, want both amounts named", err)
	}
	if postCalls.Load() != 0 {
		t.Fatalf("create POST calls = %d, want 0", postCalls.Load())
	}
}

func TestCreateLoadBalancerQuoteFailureSendsNoOrder(t *testing.T) {
	var postCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/price":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case "/v2/project-1/loadBalancers":
			postCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, createLoadBalancerUUID)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	if _, err := c.CreateLoadBalancer(context.Background(), in); err == nil {
		t.Fatal("CreateLoadBalancer() error = nil, want an error")
	}
	if postCalls.Load() != 0 {
		t.Fatalf("create POST calls = %d, want 0", postCalls.Load())
	}
}

func TestCreateLoadBalancerRejectsBadMaxPriceSendsNothing(t *testing.T) {
	for _, bad := range []float64{
		math.NaN(), math.Inf(1), math.Inf(-1), -1,
	} {
		c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("handler should not be called")
		}))
		in := validCreateLoadBalancerInput()
		in.MaxPrice = bad
		if _, err := c.CreateLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("MaxPrice=%v err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestCreateLoadBalancerNoResendAfter502(t *testing.T) {
	var postCalls atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/price":
			_, _ = w.Write([]byte(`{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case "/v2/project-1/loadBalancers":
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream error"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))
	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	_, err := c.CreateLoadBalancer(context.Background(), in)
	if err == nil {
		t.Fatal("CreateLoadBalancer() error = nil, want an error")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("create POST calls = %d, want exactly 1 (no resend)", postCalls.Load())
	}
	if !strings.Contains(err.Error(), "list-load-balancers --name") {
		t.Fatalf("err = %v, want a hint naming list-load-balancers --name", err)
	}
}

func TestCreateLoadBalancerConflictPassesThrough(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/price":
			_, _ = w.Write([]byte(`{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case "/v2/project-1/loadBalancers":
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"duplicated name"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	_, err := c.CreateLoadBalancer(context.Background(), in)
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 409 {
		t.Fatalf("err = %v, want *vngcloud.APIError with status 409", err)
	}
	if strings.Contains(err.Error(), "list-load-balancers") {
		t.Fatalf("err = %v, a 4xx must pass through unwrapped", err)
	}
}

func TestCreateLoadBalancerNoUUIDInResponse(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/price":
			_, _ = w.Write([]byte(`{"optimumPrice":400000,"originalPrice":400000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case "/v2/project-1/loadBalancers":
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	_, err := c.CreateLoadBalancer(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "list-load-balancers --name") {
		t.Fatalf("err = %v, want a message naming list-load-balancers --name", err)
	}
}

func TestCreateLoadBalancerWaitFailedStatus(t *testing.T) {
	c := newTestClient(t, createLoadBalancerHandler([]string{lbStatusError}, nil))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	out, err := c.CreateLoadBalancer(context.Background(), in)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.LoadBalancer.UUID != createLoadBalancerUUID {
		t.Fatalf("out = %+v, want a non-nil Output carrying the load balancer id", out)
	}
}

func TestCreateLoadBalancerWait404ThenCreated(t *testing.T) {
	c := newTestClient(t, createLoadBalancerHandler([]string{"404", "404", lbStatusCreated}, nil))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	out, err := c.CreateLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateLoadBalancer() error = %v", err)
	}
	if out.LoadBalancer.ProgressStatus != lbStatusCreated {
		t.Fatalf("ProgressStatus = %q, want CREATED", out.LoadBalancer.ProgressStatus)
	}
}

func TestCreateLoadBalancerWaitBoundExceeded(t *testing.T) {
	c := newTestClient(t, createLoadBalancerHandler([]string{lbStatusCreating}, nil))
	withInstantSleep(c)

	in := validCreateLoadBalancerInput()
	in.MaxPrice = 400000
	_, err := c.CreateLoadBalancer(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !strings.Contains(err.Error(), "must not be ordered again") {
		t.Fatalf("err = %v, want a message saying not to order again", err)
	}
}

func TestCreateLoadBalancerRejectsMissingFieldsSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateLoadBalancerInput()
	in.Name = ""
	if _, err := c.CreateLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateLoadBalancerRejectsBadBodyIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validCreateLoadBalancerInput()
		in.PackageID = bad
		if _, err := c.CreateLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("PackageID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
