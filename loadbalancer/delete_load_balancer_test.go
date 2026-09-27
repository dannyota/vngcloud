package loadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// deleteLoadBalancerID is the id every deleteLoadBalancerHandler test
// fixture in this file deletes.
const deleteLoadBalancerID = "lb-1"

// deleteLoadBalancerHandler serves GetLoadBalancer and the DELETE call.
// getStatuses is read in order, one value per GET; the last repeats once
// exhausted. "404" means GetLoadBalancer returns not found.
func deleteLoadBalancerHandler(getStatuses []string, deleteCalls *atomic.Int32) http.HandlerFunc {
	var getCalls int
	path := "/v2/project-1/loadBalancers/" + deleteLoadBalancerID
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == path:
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
				_, _ = w.Write([]byte(`{"message":"Cannot get load balancer with id ` + deleteLoadBalancerID + `"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":%q}}`, deleteLoadBalancerID, status)
		case r.Method == http.MethodDelete && r.URL.Path == path:
			if deleteCalls != nil {
				deleteCalls.Add(1)
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			panic("unexpected request: " + r.Method + " " + r.URL.Path)
		}
	}
}

func TestDeleteLoadBalancerSuccess(t *testing.T) {
	var deleteCalls atomic.Int32
	c := newTestClient(t, deleteLoadBalancerHandler([]string{lbStatusCreated, "404"}, &deleteCalls))
	withInstantSleep(c)

	if _, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID}); err != nil {
		t.Fatalf("DeleteLoadBalancer() error = %v", err)
	}
	if deleteCalls.Load() != 1 {
		t.Fatalf("DELETE calls = %d, want 1", deleteCalls.Load())
	}
}

func TestDeleteLoadBalancerNotFoundSendsNoDelete(t *testing.T) {
	var deleteCalls atomic.Int32
	c := newTestClient(t, deleteLoadBalancerHandler([]string{"404"}, &deleteCalls))

	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID})
	if !vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want NotFound", err)
	}
	if deleteCalls.Load() != 0 {
		t.Fatalf("DELETE calls = %d, want 0", deleteCalls.Load())
	}
}

func TestDeleteLoadBalancerAlreadyDeletingSkipsDelete(t *testing.T) {
	var deleteCalls atomic.Int32
	c := newTestClient(t, deleteLoadBalancerHandler([]string{lbStatusDeleting, lbStatusDeleting, "404"}, &deleteCalls))
	withInstantSleep(c)

	if _, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID}); err != nil {
		t.Fatalf("DeleteLoadBalancer() error = %v", err)
	}
	if deleteCalls.Load() != 0 {
		t.Fatalf("DELETE calls = %d, want 0 (already DELETING)", deleteCalls.Load())
	}
}

func TestDeleteLoadBalancerNoWaitSkipsPolling(t *testing.T) {
	var deleteCalls atomic.Int32
	var getAfterDelete atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/project-1/loadBalancers/" + deleteLoadBalancerID
		switch {
		case r.Method == http.MethodGet && r.URL.Path == path:
			if deleteCalls.Load() > 0 {
				getAfterDelete.Add(1)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":"CREATED"}}`, deleteLoadBalancerID)
		case r.Method == http.MethodDelete && r.URL.Path == path:
			deleteCalls.Add(1)
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	if _, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID, NoWait: true}); err != nil {
		t.Fatalf("DeleteLoadBalancer() error = %v", err)
	}
	if getAfterDelete.Load() != 0 {
		t.Fatalf("GET calls after DELETE = %d, want 0 (NoWait)", getAfterDelete.Load())
	}
}

func TestDeleteLoadBalancerWaitFailedStatus(t *testing.T) {
	c := newTestClient(t, deleteLoadBalancerHandler([]string{lbStatusCreated, lbStatusError}, nil))
	withInstantSleep(c)

	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestDeleteLoadBalancerWaitBoundExceeded(t *testing.T) {
	c := newTestClient(t, deleteLoadBalancerHandler([]string{lbStatusCreated, lbStatusDeleting}, nil))
	withInstantSleep(c)

	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !strings.Contains(err.Error(), "rerun is safe") {
		t.Fatalf("err = %v, want a message saying a rerun is safe", err)
	}
}

func TestDeleteLoadBalancerDeleteIsRetried(t *testing.T) {
	var deleteCalls atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := "/v2/project-1/loadBalancers/" + deleteLoadBalancerID
		switch {
		case r.Method == http.MethodGet && r.URL.Path == path:
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"progressStatus":"CREATED"}}`, deleteLoadBalancerID)
		case r.Method == http.MethodDelete && r.URL.Path == path:
			n := deleteCalls.Add(1)
			if n == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})))
	withInstantSleep(c)
	if _, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: deleteLoadBalancerID, NoWait: true}); err != nil {
		t.Fatalf("DeleteLoadBalancer() error = %v", err)
	}
	if deleteCalls.Load() != 2 {
		t.Fatalf("DELETE calls = %d, want 2 (DELETE keeps the transport's retries)", deleteCalls.Load())
	}
}

func TestDeleteLoadBalancerRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	if _, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestDeleteLoadBalancerRejectsBadPathID(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: bad}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}
