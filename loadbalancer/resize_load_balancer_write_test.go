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

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// resizeLoadBalancerID and resizeLoadBalancerNewPackage are the id and
// target package every resizeLoadBalancerHandler test fixture in this file
// uses.
const (
	resizeLoadBalancerID         = "lb-1"
	resizeLoadBalancerOldPackage = "pkg-1"
	resizeLoadBalancerNewPackage = "pkg-2"
)

// resizeLoadBalancerHandler serves the price quote, GetLoadBalancer reads
// (before the PUT and during the post-resize wait), and the resize PUT
// itself. getStatuses and getPackages are read together in order, one pair
// per GET; the last pair repeats once exhausted. putStatus, when non-zero,
// is the PUT's response status; a zero putStatus sends 202 with an empty
// body.
func resizeLoadBalancerHandler(getStatuses, getPackages []string, putCalls *atomic.Int32, putStatus int, putBody string) http.HandlerFunc {
	var getCalls int
	lbPath := "/v2/project-1/loadBalancers/" + resizeLoadBalancerID
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == lbPath:
			i := getCalls
			if i >= len(getStatuses) {
				i = len(getStatuses) - 1
			}
			status, pkg := getStatuses[i], getPackages[i]
			getCalls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"packageId":%q,"progressStatus":%q}}`, resizeLoadBalancerID, pkg, status)
		case r.Method == http.MethodPut && r.URL.Path == lbPath+"/resize":
			if putCalls != nil {
				putCalls.Add(1)
			}
			status := putStatus
			if status == 0 {
				status = http.StatusAccepted
			}
			w.WriteHeader(status)
			if putBody != "" {
				_, _ = w.Write([]byte(putBody))
			}
		default:
			panic("unexpected request: " + r.Method + " " + r.URL.Path)
		}
	}
}

func validResizeInput() *ResizeLoadBalancerInput {
	return &ResizeLoadBalancerInput{LoadBalancerID: resizeLoadBalancerID, PackageID: resizeLoadBalancerNewPackage, MaxPrice: 800000}
}

func TestResizeLoadBalancerRequestBody(t *testing.T) {
	var body map[string]any
	inner := resizeLoadBalancerHandler(
		[]string{lbStatusCreated, lbStatusCreated},
		[]string{resizeLoadBalancerOldPackage, resizeLoadBalancerNewPackage},
		nil, 0, "",
	)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
		}
		inner(w, r)
	}))
	withInstantSleep(c)

	if _, err := c.ResizeLoadBalancer(context.Background(), validResizeInput()); err != nil {
		t.Fatalf("ResizeLoadBalancer() error = %v", err)
	}
	if len(body) != 1 || body["packageId"] != resizeLoadBalancerNewPackage {
		t.Fatalf("body = %+v, want exactly {packageId: %q}", body, resizeLoadBalancerNewPackage)
	}
}

// fixedStatusPackage returns a single-element (status, resizeLoadBalancerOldPackage) pair for
// resizeLoadBalancerHandler's getStatuses and getPackages, repeated for every
// GET the handler serves.
func fixedStatusPackage(status string) ([]string, []string) {
	return []string{status}, []string{resizeLoadBalancerOldPackage}
}

// TestResizeLoadBalancerToleratesFixtureResponseBody checks that the
// resize response fixture, the same flat {"uuid": "..."} shape a create
// returns per VNG Cloud's SDK, does not break ResizeLoadBalancer when the
// server sends it on the PUT: the SDK does not decode this body today, so
// this documents that a body of this shape is harmless either way.
func TestResizeLoadBalancerToleratesFixtureResponseBody(t *testing.T) {
	statuses := []string{lbStatusCreated, lbStatusCreated, lbStatusCreated}
	packages := []string{resizeLoadBalancerOldPackage, resizeLoadBalancerOldPackage, resizeLoadBalancerNewPackage}
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, nil, http.StatusAccepted, testutil.FixtureBody(t, "../testdata/loadbalancer/resize_load_balancer.json")))
	withInstantSleep(c)

	if _, err := c.ResizeLoadBalancer(context.Background(), validResizeInput()); err != nil {
		t.Fatalf("ResizeLoadBalancer() error = %v", err)
	}
}

func TestResizeLoadBalancerSuccess(t *testing.T) {
	// The first read (before the PUT) shows the old package; the second
	// (mid-wait) still UPDATING; the third settles on the new package.
	statuses := []string{lbStatusCreated, lbStatusUpdating, lbStatusCreated}
	packages := []string{resizeLoadBalancerOldPackage, resizeLoadBalancerNewPackage, resizeLoadBalancerNewPackage}
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, nil, 0, ""))
	withInstantSleep(c)

	out, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if err != nil {
		t.Fatalf("ResizeLoadBalancer() error = %v", err)
	}
	if !out.Changed {
		t.Fatal("Changed = false, want true")
	}
	if out.LoadBalancer.PackageID != resizeLoadBalancerNewPackage {
		t.Fatalf("PackageID = %q, want %q", out.LoadBalancer.PackageID, resizeLoadBalancerNewPackage)
	}
	if out.QuotedPrice != 800000 {
		t.Fatalf("QuotedPrice = %v, want 800000", out.QuotedPrice)
	}
}

func TestResizeLoadBalancerSamePackageSendsNothing(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/price" {
			t.Fatal("same-package resize must not quote")
		}
		resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, "")(w, r)
	}))

	in := validResizeInput()
	in.PackageID = resizeLoadBalancerOldPackage
	out, err := c.ResizeLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("ResizeLoadBalancer() error = %v", err)
	}
	if out.Changed {
		t.Fatal("Changed = true, want false for the same package")
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
}

func TestResizeLoadBalancerPriceAboveMaxSendsNothing(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, ""))

	in := validResizeInput()
	in.MaxPrice = 100000
	_, err := c.ResizeLoadBalancer(context.Background(), in)
	if !errors.Is(err, ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
}

// TestResizeLoadBalancerQuoteNullPriceRefusesOrder checks that a resize
// quote whose optimumPrice is a literal JSON null refuses the PUT, the same
// way a create's null quote is refused (TestCreateLoadBalancerQuoteNullPriceRefusesOrder):
// decoded through pricing.GetQuoteOutput's plain float64, a null price would
// otherwise silently become 0 and pass the default MaxPrice of 0.
func TestResizeLoadBalancerQuoteNullPriceRefusesOrder(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/price" {
			_, _ = w.Write([]byte(`{"optimumPrice":null}`))
			return
		}
		resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, "")(w, r)
	}))
	if _, err := c.ResizeLoadBalancer(context.Background(), validResizeInput()); err == nil {
		t.Fatal("ResizeLoadBalancer() error = nil, want an error for a null quote price")
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
}

// TestResizeLoadBalancerUnpricedQuoteRefusesPUT checks that a quote of 0
// refuses the resize with ErrUnpriced and sends no PUT, even when MaxPrice
// would allow it.
func TestResizeLoadBalancerUnpricedQuoteRefusesPUT(t *testing.T) {
	for _, quote := range []string{"0"} {
		var putCalls atomic.Int32
		statuses, packages := fixedStatusPackage(lbStatusCreated)
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/price" {
				_, _ = fmt.Fprintf(w, `{"optimumPrice":%s}`, quote)
				return
			}
			resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, "")(w, r)
		}))
		for _, maxPrice := range []float64{0, 800000} {
			in := validResizeInput()
			in.MaxPrice = maxPrice
			_, err := c.ResizeLoadBalancer(context.Background(), in)
			if !errors.Is(err, vngcloud.ErrUnpriced) {
				t.Fatalf("quote %s MaxPrice %v: err = %v, want ErrUnpriced", quote, maxPrice, err)
			}
			if !strings.Contains(err.Error(), "loadbalancer.ResizeLoadBalancer") {
				t.Fatalf("err = %v, want the operation name", err)
			}
		}
		if putCalls.Load() != 0 {
			t.Fatalf("quote %s: PUT calls = %d, want 0", quote, putCalls.Load())
		}
	}
}

// TestResizeLoadBalancerQuoteNegativePriceAllowed checks that a resize's
// negative quote, unlike a create's, is allowed through: a downsize can
// legitimately refund, so it must never be refused as though it were an
// unpriced quote, and is always at or under a non-negative MaxPrice.
func TestResizeLoadBalancerQuoteNegativePriceAllowed(t *testing.T) {
	var putCalls atomic.Int32
	statuses := []string{lbStatusCreated, lbStatusCreated, lbStatusCreated}
	packages := []string{resizeLoadBalancerOldPackage, resizeLoadBalancerOldPackage, resizeLoadBalancerNewPackage}
	inner := resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, "")
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/price" {
			_, _ = w.Write([]byte(`{"optimumPrice":-50000}`))
			return
		}
		inner(w, r)
	}))
	withInstantSleep(c)

	in := validResizeInput()
	in.MaxPrice = 0
	out, err := c.ResizeLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("ResizeLoadBalancer() error = %v, want a negative resize quote to be allowed", err)
	}
	if !out.Changed || out.QuotedPrice != -50000 {
		t.Fatalf("out = %+v, want Changed true and QuotedPrice -50000", out)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want 1", putCalls.Load())
	}
}

func TestResizeLoadBalancerRejectsBadMaxPriceSendsNothing(t *testing.T) {
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("handler should not be called")
		}))
		in := validResizeInput()
		in.MaxPrice = bad
		if _, err := c.ResizeLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("MaxPrice=%v err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// TestResizeLoadBalancerNoResendAfter5xx also checks that the returned error
// advises reading the load balancer and comparing its package before any
// rerun: the resize PUT is sent with Once and never resent, so after a 5xx
// the caller does not know whether it reached the server, and a blind rerun
// risks a second paid resize.
func TestResizeLoadBalancerNoResendAfter5xx(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	c := New(testutil.NewRetryConfig(t, resizeLoadBalancerHandler(statuses, packages, &putCalls, http.StatusBadGateway, `{"message":"upstream"}`)))

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if err == nil {
		t.Fatal("ResizeLoadBalancer() error = nil, want an error")
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want exactly 1 (Once)", putCalls.Load())
	}
	if !strings.Contains(err.Error(), "GetLoadBalancer") || !strings.Contains(err.Error(), "before any rerun") {
		t.Fatalf("err = %v, want advice to read the load balancer and compare its package before any rerun", err)
	}
}

// TestResizeLoadBalancerNoResendAfterDroppedConnection checks the same
// advice as TestResizeLoadBalancerNoResendAfter5xx, but for a network error
// (a connection dropped mid-request) rather than a 5xx status: both leave
// the caller unsure whether the resize PUT reached the server.
func TestResizeLoadBalancerNoResendAfterDroppedConnection(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	inner := resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, "")
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCalls.Add(1)
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("ResponseWriter does not support hijacking")
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		inner(w, r)
	}))

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if err == nil {
		t.Fatal("ResizeLoadBalancer() error = nil, want an error")
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want exactly 1 (no resend)", putCalls.Load())
	}
	if !strings.Contains(err.Error(), "GetLoadBalancer") || !strings.Contains(err.Error(), "before any rerun") {
		t.Fatalf("err = %v, want advice to read the load balancer and compare its package before any rerun", err)
	}
}

func TestResizeLoadBalancerBusyRefusalReturnsErrBusy(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, &putCalls, http.StatusBadRequest, `{"message":"load balancer id lb-1 is not ready"}`))

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want exactly 1", putCalls.Load())
	}
}

func TestResizeLoadBalancerPreWriteBusyBoundExceeded(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusUpdating)
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, &putCalls, 0, ""))
	withInstantSleep(c)

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
	if putCalls.Load() != 0 {
		t.Fatalf("PUT calls = %d, want 0", putCalls.Load())
	}
}

func TestResizeLoadBalancerNoWaitSkipsPolling(t *testing.T) {
	// Two GETs happen before the PUT regardless of NoWait: the same-package
	// check and the pre-write busy check. NoWait's own job is to skip the
	// post-write wait, so no GET may follow the PUT.
	var putDone atomic.Bool
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/price":
			_, _ = w.Write([]byte(`{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		case r.Method == http.MethodGet:
			if putDone.Load() {
				t.Fatal("NoWait must not poll after the PUT")
			}
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"packageId":%q,"progressStatus":%q}}`, resizeLoadBalancerID, resizeLoadBalancerOldPackage, lbStatusCreated)
		case r.Method == http.MethodPut:
			putDone.Store(true)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	in := validResizeInput()
	in.NoWait = true
	out, err := c.ResizeLoadBalancer(context.Background(), in)
	if err != nil {
		t.Fatalf("ResizeLoadBalancer() error = %v", err)
	}
	if out.LoadBalancer.PackageID != resizeLoadBalancerNewPackage {
		t.Fatalf("PackageID = %q, want %q", out.LoadBalancer.PackageID, resizeLoadBalancerNewPackage)
	}
}

func TestResizeLoadBalancerWaitFailedStatus(t *testing.T) {
	statuses := []string{lbStatusCreated, lbStatusError}
	packages := []string{resizeLoadBalancerOldPackage, resizeLoadBalancerOldPackage}
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, nil, 0, ""))
	withInstantSleep(c)

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestResizeLoadBalancerWaitBoundExceeded(t *testing.T) {
	// index 0: the same-package check read; index 1: the pre-write busy
	// check read (not busy, so the PUT is sent); index 2+: the post-write
	// wait, stuck UPDATING forever.
	statuses := []string{lbStatusCreated, lbStatusCreated, lbStatusUpdating}
	packages := []string{resizeLoadBalancerOldPackage, resizeLoadBalancerOldPackage, resizeLoadBalancerOldPackage}
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, nil, 0, ""))
	withInstantSleep(c)

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !strings.Contains(err.Error(), "must not be repeated") {
		t.Fatalf("err = %v, want a message saying not to repeat the resize", err)
	}
}

func TestResizeLoadBalancerRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validResizeInput()
	in.LoadBalancerID = ""
	if _, err := c.ResizeLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestResizeLoadBalancerRejectsBadPathIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validResizeInput()
		in.LoadBalancerID = bad
		if _, err := c.ResizeLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// TestResizeLoadBalancer5xxWithBusyTextIsNotErrBusy checks that busy text in
// a 5xx body is not a busy refusal: a 5xx may have reached the server, so
// the error keeps its resize advice and is not ErrBusy.
func TestResizeLoadBalancer5xxWithBusyTextIsNotErrBusy(t *testing.T) {
	var putCalls atomic.Int32
	statuses, packages := fixedStatusPackage(lbStatusCreated)
	c := newTestClient(t, resizeLoadBalancerHandler(statuses, packages, &putCalls, http.StatusBadGateway, `{"message":"load balancer id lb-1 is not ready"}`))

	_, err := c.ResizeLoadBalancer(context.Background(), validResizeInput())
	if err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want a non-nil error that is not ErrBusy", err)
	}
	if putCalls.Load() != 1 {
		t.Fatalf("PUT calls = %d, want exactly 1", putCalls.Load())
	}
}
