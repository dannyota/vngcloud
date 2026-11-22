package globalloadbalancer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestReviewDeletePreflightRefusesUnsafeDetails(t *testing.T) {
	for _, detail := range []string{`{"id":"other","type":"Layer 4","status":"ACTIVE"}`, `{"id":"glb-1","type":"Layer 7","status":"ACTIVE"}`, `{"id":"glb-1","type":"Layer 4","status":"CREATING"}`, `{"id":"glb-1"}`} {
		t.Run(detail, func(t *testing.T) {
			deletes := 0
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					deletes++
					w.WriteHeader(400)
					return
				}
				_, _ = fmt.Fprint(w, detail)
			}))
			_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
			if !errors.Is(err, vngcloud.ErrInvalidInput) || deletes != 0 {
				t.Fatalf("err=%v deletes=%d", err, deletes)
			}
		})
	}
}

func TestReviewCreateUnknownStatusDoesNotWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, `{"globalLoadBalancer":{"id":"glb-1"}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"glb-1","name":"demo","package":"pkg-1","type":"Layer 4","status":"sensitive-unknown"}`)
	}))
	started := time.Now()
	out, err := c.CreateLoadBalancer(ctx, &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrNotSettled) || out == nil || out.LoadBalancer.ID != "glb-1" || strings.Contains(err.Error(), "sensitive-unknown") || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestReviewCreatePreservesBenignMessage(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(402)
		_, _ = fmt.Fprint(w, `{"message":"insufficient credits for this account token balance"}`)
	}))
	_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.Message != "insufficient credits for this account token balance" {
		t.Fatalf("err=%v", err)
	}
}

func TestReviewCreateErrorIsTerminal(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = fmt.Fprint(w, `{"globalLoadBalancer":{"id":"glb-1"}}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"id":"glb-1","name":"demo","package":"pkg-1","type":"Layer 4","status":"ERROR"}`)
	}))
	out, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	if !errors.Is(err, ErrFailed) || out == nil || out.LoadBalancer.ID != "glb-1" {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestReviewDeleteTruncatedRejectionPreservesStatus(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"id":"glb-1","type":"Layer 4","status":"ACTIVE"}`)
			return
		}
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, "short")
	}))
	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 403 {
		t.Fatalf("err=%v", err)
	}
}

func TestReviewDeleteCodeUsesHTTPStatus(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"id":"glb-1","type":"Layer 4","status":"ERROR"}`)
			return
		}
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, `{"code":"WrongProviderCode","message":"denied"}`)
	}))
	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 403 || api.Code == "WrongProviderCode" {
		t.Fatalf("err=%#v", err)
	}
}

func TestReviewPreflightCodeUsesHTTPStatus(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = fmt.Fprint(w, `{"code":"WrongProviderCode","message":"denied"}`)
	}))
	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 403 || api.Code == "WrongProviderCode" {
		t.Fatalf("err=%#v", err)
	}
}

func TestReviewPreflight503HidesProviderCode(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, `{"code":"provider-private-code","message":"unavailable"}`)
	}))
	_, err := c.DeleteLoadBalancer(context.Background(), &DeleteLoadBalancerInput{LoadBalancerID: "glb-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || api.StatusCode != 503 || api.Code == "provider-private-code" || strings.Contains(err.Error(), "provider-private-code") {
		t.Fatalf("err=%#v", err)
	}
}

func TestReviewCreateRedactsEmail(t *testing.T) {
	c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(402)
		_, _ = fmt.Fprint(w, `{"message":"contact alice@example.test"}`)
	}))
	_, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
	var api *vngcloud.APIError
	if !errors.As(err, &api) || strings.Contains(api.Message, "alice@example.test") {
		t.Fatalf("err=%v", err)
	}
}

func TestReviewCreateRejectsWrongType(t *testing.T) {
	for _, typ := range []string{"Layer 7", ""} {
		t.Run(typ, func(t *testing.T) {
			c := newWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					_, _ = fmt.Fprint(w, `{"globalLoadBalancer":{"id":"glb-1"}}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"id":"glb-1","name":"demo","package":"pkg-1","type":"Layer 4","status":"ACTIVE","type":%q}`, typ)
			}))
			out, err := c.CreateLoadBalancer(context.Background(), &CreateLoadBalancerInput{Name: "demo", PackageID: "pkg-1"})
			if !errors.Is(err, ErrNotSettled) || out == nil || out.LoadBalancer.ID != "glb-1" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
