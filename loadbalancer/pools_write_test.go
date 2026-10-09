package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const (
	poolTestLBID   = "lb-1"
	poolTestPoolID = "pool-1"
)

func validCreatePoolInput() *CreatePoolInput {
	return &CreatePoolInput{
		LoadBalancerID:      poolTestLBID,
		Name:                "pool-1",
		Protocol:            PoolProtocolHTTP,
		HealthCheckProtocol: HealthCheckProtocolHTTP,
	}
}

// poolLBPath and poolPath are the URL paths every pool write test uses.
var (
	poolLBPath = "/v2/project-1/loadBalancers/" + poolTestLBID
	poolPath   = poolLBPath + "/pools/" + poolTestPoolID
)

// poolLBHandler serves a not-busy GetLoadBalancer read at poolLBPath.
func poolLBHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestLBID, lbStatusCreated)
}

func TestCreatePoolRequestBodyDefaults(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == poolLBPath+"/pools":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, poolTestPoolID)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.CreatePool(context.Background(), validCreatePoolInput()); err != nil {
		t.Fatalf("CreatePool() error = %v", err)
	}
	if body["poolName"] != "pool-1" || body["poolProtocol"] != "HTTP" || body["algorithm"] != "ROUND_ROBIN" {
		t.Fatalf("body = %+v, want defaults", body)
	}
	if body["stickiness"] != false || body["tlsEncryption"] != false {
		t.Fatalf("body = %+v, want stickiness and tlsEncryption false for an unset HTTP pool", body)
	}
	hm, ok := body["healthMonitor"].(map[string]any)
	if !ok {
		t.Fatalf("body[healthMonitor] missing or wrong type: %+v", body)
	}
	want := map[string]any{"healthCheckProtocol": "HTTP", "healthyThreshold": float64(3), "unhealthyThreshold": float64(3), "interval": float64(30), "timeout": float64(5)}
	for k, v := range want {
		if hm[k] != v {
			t.Fatalf("healthMonitor[%q] = %v, want %v (hm = %+v)", k, hm[k], v, hm)
		}
	}
}

func TestCreatePoolHTTPFieldsOnlyForHTTPChecks(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, poolTestPoolID)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := validCreatePoolInput()
	in.HealthCheckProtocol = HealthCheckProtocolHTTPS
	in.HealthCheckPath = "/healthz"
	in.HealthCheckMethod = "GET"
	if _, err := c.CreatePool(context.Background(), in); err != nil {
		t.Fatalf("CreatePool() error = %v", err)
	}
	hm := body["healthMonitor"].(map[string]any)
	if hm["healthCheckPath"] != "/healthz" || hm["healthCheckMethod"] != "GET" {
		t.Fatalf("healthMonitor = %+v, want the HTTP fields set", hm)
	}
}

func TestCreatePoolRejectsHTTPFieldOnTCPCheck(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreatePoolInput()
	in.HealthCheckProtocol = HealthCheckProtocolTCP
	in.HealthCheckPath = "/healthz"
	if _, err := c.CreatePool(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePoolSendsStickinessOnlyWhenSet(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, poolTestPoolID)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := validCreatePoolInput()
	in.Stickiness = vngcloud.Ptr(true)
	if _, err := c.CreatePool(context.Background(), in); err != nil {
		t.Fatalf("CreatePool() error = %v", err)
	}
	if body["stickiness"] != true {
		t.Fatalf("body[stickiness] = %v, want true", body["stickiness"])
	}
}

func createPoolBodyFor(t *testing.T, in *CreatePoolInput) map[string]any {
	t.Helper()
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, poolTestPoolID)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)
	if _, err := c.CreatePool(context.Background(), in); err != nil {
		t.Fatalf("CreatePool() error = %v", err)
	}
	return body
}

func TestCreatePoolHTTPAlwaysSendsStickinessAndTLS(t *testing.T) {
	body := createPoolBodyFor(t, validCreatePoolInput())
	if v, ok := body["stickiness"]; !ok || v != false {
		t.Fatalf("stickiness = %v (present %v), want false", v, ok)
	}
	if v, ok := body["tlsEncryption"]; !ok || v != false {
		t.Fatalf("tlsEncryption = %v (present %v), want false", v, ok)
	}
	in := validCreatePoolInput()
	in.Stickiness = vngcloud.Ptr(true)
	in.TLSEncryption = vngcloud.Ptr(true)
	body = createPoolBodyFor(t, in)
	if body["stickiness"] != true || body["tlsEncryption"] != true {
		t.Fatalf("body = %+v, want explicit true sent", body)
	}
}

func TestCreatePoolNonHTTPOmitsStickinessAndTLS(t *testing.T) {
	in := validCreatePoolInput()
	in.Protocol = PoolProtocolTCP
	in.HealthCheckProtocol = HealthCheckProtocolTCP
	body := createPoolBodyFor(t, in)
	for _, k := range []string{"stickiness", "tlsEncryption"} {
		if _, ok := body[k]; ok {
			t.Fatalf("body = %+v, want no %s for a TCP pool", body, k)
		}
	}
}

func TestUpdatePoolHTTPPoolSendsFalseWhenReadHasNoValue(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"protocol":"HTTP","loadBalanceMethod":"ROUND_ROBIN","progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		case r.Method == http.MethodGet && r.URL.Path == poolPath+"/healthMonitor":
			_, _ = w.Write([]byte(`{"data":{"healthCheckProtocol":"TCP","healthyThreshold":3,"unhealthyThreshold":3,"interval":30,"timeout":5}}`))
		case r.Method == http.MethodPut:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &UpdatePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID, HealthyThreshold: vngcloud.Ptr(5)}
	if _, err := c.UpdatePool(context.Background(), in); err != nil {
		t.Fatalf("UpdatePool() error = %v", err)
	}
	if body["stickiness"] != false || body["tlsEncryption"] != false {
		t.Fatalf("body = %+v, want stickiness and tlsEncryption false", body)
	}
}

func TestCreatePoolNoResendAfter502(t *testing.T) {
	var postCalls atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			poolLBHandler(w, r)
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))
	if _, err := c.CreatePool(context.Background(), validCreatePoolInput()); err == nil {
		t.Fatal("CreatePool() error = nil, want an error")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1 (no resend)", postCalls.Load())
	}
}

func TestCreatePoolDuplicateNamePassesThrough(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			poolLBHandler(w, r)
		case http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"duplicated pool name"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	_, err := c.CreatePool(context.Background(), validCreatePoolInput())
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want *vngcloud.APIError with status 400", err)
	}
}

// TestCreatePoolBusyRefusalResendsOnce checks that a busy refusal on the
// create POST itself, a race that can still happen right after the
// pre-write wait passes, is followed by one more wait and exactly one more
// send: the server did not act on the first attempt, so this is safe.
func TestCreatePoolBusyRefusalResendsOnce(t *testing.T) {
	var postCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == poolLBPath+"/pools":
			if postCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"load balancer id lb-1 is updating"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, poolTestPoolID)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	out, err := c.CreatePool(context.Background(), validCreatePoolInput())
	if err != nil {
		t.Fatalf("CreatePool() error = %v", err)
	}
	if out.Pool.UUID != poolTestPoolID {
		t.Fatalf("Pool = %+v, want uuid %s", out.Pool, poolTestPoolID)
	}
	if got := postCalls.Load(); got != 2 {
		t.Fatalf("POST calls = %d, want 2 (busy refusal, then one resend)", got)
	}
}

// TestCreatePoolNonBusyRefusalNoResend checks that a refusal not matching a
// busy message, such as a duplicate name, is never resent.
func TestCreatePoolNonBusyRefusalNoResend(t *testing.T) {
	var postCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == poolLBPath+"/pools":
			postCalls.Add(1)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"duplicated pool name"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	if _, err := c.CreatePool(context.Background(), validCreatePoolInput()); err == nil {
		t.Fatal("CreatePool() error = nil, want an error")
	}
	if got := postCalls.Load(); got != 1 {
		t.Fatalf("POST calls = %d, want 1 (a non-busy refusal is never resent)", got)
	}
}

// TestCreatePoolConcurrentWritesToSameLoadBalancerDoNotOverlap checks that
// the per-load-balancer lock serializes two CreatePool calls targeting the
// same LoadBalancerID within one process: the busy resend above assumes the
// server, not a concurrent goroutine in this process, is the only source of
// a busy refusal.
func TestCreatePoolConcurrentWritesToSameLoadBalancerDoNotOverlap(t *testing.T) {
	var inFlight, maxInFlight, postCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == poolLBPath+"/pools":
			n := inFlight.Add(1)
			for {
				m := maxInFlight.Load()
				if n <= m || maxInFlight.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			inFlight.Add(-1)
			id := postCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{"uuid":"pool-%d"}`, id)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))

	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := validCreatePoolInput()
			in.Name = fmt.Sprintf("pool-%d", i)
			in.NoWait = true
			if _, err := c.CreatePool(context.Background(), in); err != nil {
				t.Errorf("CreatePool() error = %v", err)
			}
		}(i)
	}
	wg.Wait()

	if postCalls.Load() != 2 {
		t.Fatalf("POST calls = %d, want 2", postCalls.Load())
	}
	if got := maxInFlight.Load(); got > 1 {
		t.Fatalf("max concurrent create POSTs = %d, want 1 (per-load-balancer lock)", got)
	}
}

func TestCreatePoolBusyPreWriteBoundExceeded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == poolLBPath {
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestLBID, lbStatusUpdating)
			return
		}
		t.Fatal("handler should only be read from")
	}))
	withInstantSleep(c)

	_, err := c.CreatePool(context.Background(), validCreatePoolInput())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestCreatePoolWaitFailedStatus(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost:
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, poolTestPoolID)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	_, err := c.CreatePool(context.Background(), validCreatePoolInput())
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestCreatePoolRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreatePoolInput()
	in.Name = ""
	if _, err := c.CreatePool(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreatePoolRejectsBadPathID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validCreatePoolInput()
		in.LoadBalancerID = bad
		if _, err := c.CreatePool(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// --- UpdatePool ---

// poolGetHandler serves GetPool and GetPoolHealthMonitor for poolTestPoolID
// with a fixed set of current values, so an update test can check which
// fields a merge resent unchanged.
func poolGetHandler(t *testing.T, poolStatus string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case poolLBPath:
			poolLBHandler(w, r)
		case poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"loadBalanceMethod":"LEAST_CONNECTIONS","stickiness":true,"progressStatus":%q}}`, poolTestPoolID, poolStatus)
		case poolPath + "/healthMonitor":
			_, _ = w.Write([]byte(`{"data":{"healthCheckProtocol":"HTTP","healthyThreshold":4,"unhealthyThreshold":4,"interval":15,"timeout":8,"healthCheckPath":"/old","healthCheckMethod":"GET","httpVersion":"1.1","domainName":"old.example","successCode":"200"}}`))
		default:
			t.Fatalf("unexpected GET: %s", r.URL.Path)
		}
	}
}

func TestUpdatePoolReadMergeResendsUnsetFields(t *testing.T) {
	var body map[string]any
	get := poolGetHandler(t, lbStatusCreated)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			get(w, r)
		case http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	withInstantSleep(c)

	in := &UpdatePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID, HealthyThreshold: vngcloud.Ptr(5)}
	if _, err := c.UpdatePool(context.Background(), in); err != nil {
		t.Fatalf("UpdatePool() error = %v", err)
	}
	if body["algorithm"] != "LEAST_CONNECTIONS" || body["stickiness"] != true {
		t.Fatalf("body = %+v, want the read algorithm and stickiness resent", body)
	}
	hm := body["healthMonitor"].(map[string]any)
	if hm["healthyThreshold"] != float64(5) {
		t.Fatalf("healthyThreshold = %v, want 5 (the set field)", hm["healthyThreshold"])
	}
	for k, v := range map[string]any{"unhealthyThreshold": float64(4), "interval": float64(15), "timeout": float64(8), "healthCheckPath": "/old", "healthCheckMethod": "GET", "httpVersion": "1.1", "domainName": "old.example", "successCode": "200"} {
		if hm[k] != v {
			t.Fatalf("healthMonitor[%q] = %v, want %v (read value resent)", k, hm[k], v)
		}
	}
	if _, ok := hm["healthCheckProtocol"]; ok {
		t.Fatalf("healthMonitor = %+v, want no healthCheckProtocol key in an update", hm)
	}
}

// TestUpdatePoolWriteStatusesPassThrough checks that a 404, 409, or 500 on
// the PUT itself reaches the caller as an unwrapped *vngcloud.APIError
// naming that status.
func TestUpdatePoolWriteStatusesPassThrough(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError} {
		get := poolGetHandler(t, lbStatusCreated)
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				get(w, r)
			case http.MethodPut:
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"boom"}`))
			default:
				t.Fatalf("unexpected method: %s", r.Method)
			}
		}))
		in := &UpdatePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID, HealthyThreshold: vngcloud.Ptr(5)}
		_, err := c.UpdatePool(context.Background(), in)
		var apiErr *vngcloud.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Fatalf("status %d: err = %v, want *vngcloud.APIError with that status", status, err)
		}
	}
}

func TestUpdatePoolRejectsNoFieldsSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := &UpdatePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID}
	if _, err := c.UpdatePool(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdatePoolRejectsHTTPFieldOnTCPPool(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case poolLBPath:
			poolLBHandler(w, r)
		case poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"loadBalanceMethod":"ROUND_ROBIN","progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		case poolPath + "/healthMonitor":
			_, _ = w.Write([]byte(`{"data":{"healthCheckProtocol":"TCP","healthyThreshold":3,"unhealthyThreshold":3,"interval":30,"timeout":5}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &UpdatePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID, HealthCheckPath: vngcloud.Ptr("/x")}
	if _, err := c.UpdatePool(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdatePoolBusyPreWriteBoundExceeded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == poolLBPath {
			poolLBHandler(w, r)
			return
		}
		if r.URL.Path == poolPath {
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusUpdating)
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	withInstantSleep(c)

	in := &UpdatePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID, Algorithm: vngcloud.Ptr(AlgorithmSourceIP)}
	_, err := c.UpdatePool(context.Background(), in)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// --- DeletePool ---

func TestDeletePoolInUseByListenerSendsNothing(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == poolLBPath+"/listeners" {
			_, _ = w.Write([]byte(`{"data":[{"uuid":"listener-1","defaultPoolId":"pool-1"}]}`))
			return
		}
		t.Fatal("handler should only list listeners")
	}))
	_, err := c.DeletePool(context.Background(), &DeletePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeletePoolServerInUseRefusalWraps(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == poolLBPath+"/listeners":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"message":"pool is used in listener x"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	_, err := c.DeletePool(context.Background(), &DeletePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID})
	if !errors.Is(err, ErrInUse) {
		t.Fatalf("err = %v, want ErrInUse", err)
	}
}

func TestDeletePoolSuccess(t *testing.T) {
	var getCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == poolLBPath+"/listeners":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			n := getCalls.Add(1)
			if n == 1 {
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"cannot get pool with id ` + poolTestPoolID + `"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.DeletePool(context.Background(), &DeletePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID}); err != nil {
		t.Fatalf("DeletePool() error = %v", err)
	}
}

// TestDeletePoolBusyRefusalResendsOnce checks that a busy refusal on the
// DELETE itself, a race after the pre-write wait passed, is followed by one
// more wait and exactly one more send.
func TestDeletePoolBusyRefusalResendsOnce(t *testing.T) {
	var deleteCalls, getCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == poolLBPath+"/listeners":
			_, _ = w.Write([]byte(`{"data":[]}`))
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == poolPath:
			n := getCalls.Add(1)
			if n >= 3 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"cannot get pool with id ` + poolTestPoolID + `"}`))
				return
			}
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, poolTestPoolID, lbStatusCreated)
		case r.Method == http.MethodDelete:
			if deleteCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"pool id pool-1 is updating"}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.DeletePool(context.Background(), &DeletePoolInput{LoadBalancerID: poolTestLBID, PoolID: poolTestPoolID}); err != nil {
		t.Fatalf("DeletePool() error = %v", err)
	}
	if got := deleteCalls.Load(); got != 2 {
		t.Fatalf("DELETE calls = %d, want 2 (busy refusal, then one resend)", got)
	}
}

func TestDeletePoolRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.DeletePool(context.Background(), &DeletePoolInput{LoadBalancerID: poolTestLBID}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

// TestCreatePool5xxWithBusyTextNoResend checks that busy text in a 5xx body
// is not a busy refusal: a 5xx may have reached the server, so the create is
// never resent.
func TestCreatePool5xxWithBusyTextNoResend(t *testing.T) {
	var postCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
			poolLBHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == poolLBPath+"/pools":
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"load balancer id lb-1 is updating"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.CreatePool(context.Background(), validCreatePoolInput()); err == nil {
		t.Fatal("CreatePool() error = nil, want an error")
	}
	if got := postCalls.Load(); got != 1 {
		t.Fatalf("POST calls = %d, want 1", got)
	}
}

func TestCreatePoolRefusesNonHTTPProtocolOnLayer7(t *testing.T) {
	for _, protocol := range []string{PoolProtocolTCP, PoolProtocolUDP, PoolProtocolPROXY} {
		t.Run(protocol, func(t *testing.T) {
			var posts atomic.Int32
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == poolLBPath:
					_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"type":%q,"progressStatus":%q}}`, poolTestLBID, TypeLayer7, lbStatusCreated)
				default:
					posts.Add(1)
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
			}))
			withInstantSleep(c)

			in := validCreatePoolInput()
			in.Protocol = protocol
			in.HealthCheckProtocol = HealthCheckProtocolTCP
			if _, err := c.CreatePool(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if posts.Load() != 0 {
				t.Fatalf("sent %d writes, want none", posts.Load())
			}
		})
	}
}
