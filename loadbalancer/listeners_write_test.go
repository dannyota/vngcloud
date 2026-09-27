package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const (
	listenerTestLBID = "lb-1"
	listenerTestID   = "listener-1"
)

var (
	listenerLBPath = "/v2/project-1/loadBalancers/" + listenerTestLBID
	listenerPath   = listenerLBPath + "/listeners/" + listenerTestID
)

func listenerLBHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestLBID, lbStatusCreated)
}

func validCreateListenerInput() *CreateListenerInput {
	return &CreateListenerInput{
		LoadBalancerID: listenerTestLBID,
		Name:           "listener-1",
		Protocol:       ProtocolHTTP,
		Port:           80,
		AllowedCIDRs:   []string{"10.0.0.0/24"},
	}
}

func TestCreateListenerRequestBodyDefaults(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodPost && r.URL.Path == listenerLBPath+"/listeners":
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, listenerTestID)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.CreateListener(context.Background(), validCreateListenerInput()); err != nil {
		t.Fatalf("CreateListener() error = %v", err)
	}
	want := map[string]any{
		"listenerName":         "listener-1",
		"listenerProtocol":     "HTTP",
		"listenerProtocolPort": float64(80),
		"timeoutClient":        float64(50),
		"timeoutMember":        float64(50),
		"timeoutConnection":    float64(5),
		"allowedCidrs":         "10.0.0.0/24",
	}
	for k, v := range want {
		if body[k] != v {
			t.Fatalf("body[%q] = %v, want %v (body = %+v)", k, body[k], v, body)
		}
	}
	for _, absent := range []string{"certificateAuthorities", "defaultCertificateAuthority", "clientCertificate", "insertHeaders", "defaultPoolId"} {
		if _, ok := body[absent]; ok {
			t.Fatalf("body = %+v, want no %q key", body, absent)
		}
	}
}

func TestCreateListenerJoinsMultipleCIDRs(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, listenerTestID)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := validCreateListenerInput()
	in.AllowedCIDRs = []string{"10.0.0.0/24", "172.16.0.0/16"}
	if _, err := c.CreateListener(context.Background(), in); err != nil {
		t.Fatalf("CreateListener() error = %v", err)
	}
	if body["allowedCidrs"] != "10.0.0.0/24,172.16.0.0/16" {
		t.Fatalf("allowedCidrs = %v, want the joined CIDRs", body["allowedCidrs"])
	}
}

func TestCreateListenerHTTPSSendsCertificateFields(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodPost:
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, listenerTestID)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestID, lbStatusCreated)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := validCreateListenerInput()
	in.Protocol = ProtocolHTTPS
	in.DefaultCertificateID = "cert-1"
	if _, err := c.CreateListener(context.Background(), in); err != nil {
		t.Fatalf("CreateListener() error = %v", err)
	}
	if body["defaultCertificateAuthority"] != "cert-1" {
		t.Fatalf("defaultCertificateAuthority = %v, want cert-1", body["defaultCertificateAuthority"])
	}
}

func TestCreateListenerHTTPSRequiresDefaultCertificate(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateListenerInput()
	in.Protocol = ProtocolHTTPS
	if _, err := c.CreateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateListenerRejectsCertificateFieldOnTCP(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateListenerInput()
	in.Protocol = ProtocolTCP
	in.DefaultCertificateID = "cert-1"
	if _, err := c.CreateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateListenerRejectsBadCIDRs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range [][]string{
		{"10.0.0.1/24"},   // host bits set
		{"2001:db8::/32"}, // IPv6
		{"not-a-cidr"},    // no prefix
		{},                // empty
	} {
		in := validCreateListenerInput()
		in.AllowedCIDRs = bad
		if _, err := c.CreateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("AllowedCIDRs=%v err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

func TestCreateListenerNoResendAfter502(t *testing.T) {
	var postCalls atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listenerLBHandler(w, r)
		case http.MethodPost:
			postCalls.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"upstream"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))
	if _, err := c.CreateListener(context.Background(), validCreateListenerInput()); err == nil {
		t.Fatal("CreateListener() error = nil, want an error")
	}
	if postCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1 (no resend)", postCalls.Load())
	}
}

func TestCreateListenerDuplicatePortPassesThrough(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			listenerLBHandler(w, r)
		case http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"duplicated listener protocol port"}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	_, err := c.CreateListener(context.Background(), validCreateListenerInput())
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want *vngcloud.APIError with status 400", err)
	}
}

func TestCreateListenerBusyPreWriteBoundExceeded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == listenerLBPath {
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestLBID, lbStatusUpdating)
			return
		}
		t.Fatal("handler should only be read from")
	}))
	withInstantSleep(c)

	_, err := c.CreateListener(context.Background(), validCreateListenerInput())
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

func TestCreateListenerWaitFailedStatus(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodPost:
			_, _ = fmt.Fprintf(w, `{"uuid":%q}`, listenerTestID)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestID, lbStatusError)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	_, err := c.CreateListener(context.Background(), validCreateListenerInput())
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestCreateListenerRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validCreateListenerInput()
	in.Name = ""
	if _, err := c.CreateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestCreateListenerRejectsBadPathID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validCreateListenerInput()
		in.LoadBalancerID = bad
		if _, err := c.CreateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// --- UpdateListener ---

// listenerGetHandler serves GetListener for listenerTestID with a fixed set
// of current values, so an update test can check which fields a merge
// resent unchanged.
func listenerGetHandler(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte(`{"data":{"uuid":"listener-1","protocol":"HTTP","protocolPort":80,` +
		`"defaultPoolId":"pool-1","timeoutClient":60,"timeoutMember":60,"timeoutConnection":10,` +
		`"allowedCidrs":"10.0.0.0/24,172.16.0.0/16","insertHeaders":[{"headerName":"X-Old","headerValue":"1"}],` +
		`"progressStatus":"CREATED"}}`))
}

func TestUpdateListenerReadMergeResendsUnsetFields(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			listenerGetHandler(w, r)
		case r.Method == http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if err := json.Unmarshal(data, &body); err != nil {
				t.Fatalf("decode body: %v, raw = %s", err, data)
			}
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &UpdateListenerInput{LoadBalancerID: listenerTestLBID, ListenerID: listenerTestID, TimeoutClient: vngcloud.Ptr(30)}
	if _, err := c.UpdateListener(context.Background(), in); err != nil {
		t.Fatalf("UpdateListener() error = %v", err)
	}
	if body["timeoutClient"] != float64(30) {
		t.Fatalf("timeoutClient = %v, want 30 (the set field)", body["timeoutClient"])
	}
	if body["timeoutMember"] != float64(60) || body["timeoutConnection"] != float64(10) {
		t.Fatalf("body = %+v, want the read timeouts resent", body)
	}
	if body["allowedCidrs"] != "10.0.0.0/24,172.16.0.0/16" {
		t.Fatalf("allowedCidrs = %v, want the read value resent", body["allowedCidrs"])
	}
	if body["defaultPoolId"] != "pool-1" {
		t.Fatalf("defaultPoolId = %v, want the read value resent", body["defaultPoolId"])
	}
	headers, ok := body["insertHeaders"].([]any)
	if !ok || len(headers) != 1 {
		t.Fatalf("insertHeaders = %+v, want the read header resent", body["insertHeaders"])
	}
}

func TestUpdateListenerRejectsNoFieldsSet(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := &UpdateListenerInput{LoadBalancerID: listenerTestLBID, ListenerID: listenerTestID}
	if _, err := c.UpdateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateListenerRejectsCertificateFieldOnHTTP(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			listenerGetHandler(w, r)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	in := &UpdateListenerInput{LoadBalancerID: listenerTestLBID, ListenerID: listenerTestID, DefaultCertificateID: vngcloud.Ptr("cert-1")}
	if _, err := c.UpdateListener(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateListenerBusyPreWriteBoundExceeded(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == listenerLBPath {
			listenerLBHandler(w, r)
			return
		}
		if r.URL.Path == listenerPath {
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestID, lbStatusUpdating)
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
	}))
	withInstantSleep(c)

	in := &UpdateListenerInput{LoadBalancerID: listenerTestLBID, ListenerID: listenerTestID, TimeoutClient: vngcloud.Ptr(30)}
	_, err := c.UpdateListener(context.Background(), in)
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v, want ErrBusy", err)
	}
}

// --- DeleteListener ---

func TestDeleteListenerSuccess(t *testing.T) {
	var getCalls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == listenerLBPath:
			listenerLBHandler(w, r)
		case r.Method == http.MethodGet && r.URL.Path == listenerPath:
			n := getCalls.Add(1)
			if n == 1 {
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, listenerTestID, lbStatusCreated)
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"cannot get listener with id ` + listenerTestID + `"}`))
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	withInstantSleep(c)

	if _, err := c.DeleteListener(context.Background(), &DeleteListenerInput{LoadBalancerID: listenerTestLBID, ListenerID: listenerTestID}); err != nil {
		t.Fatalf("DeleteListener() error = %v", err)
	}
}

func TestDeleteListenerRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.DeleteListener(context.Background(), &DeleteListenerInput{LoadBalancerID: listenerTestLBID}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteListenerRejectsBadPathIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.DeleteListener(context.Background(), &DeleteListenerInput{LoadBalancerID: listenerTestLBID, ListenerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ListenerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
