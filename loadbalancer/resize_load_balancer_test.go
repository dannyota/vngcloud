package loadbalancer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

func validResizeLoadBalancerInput() *ResizeLoadBalancerInput {
	return &ResizeLoadBalancerInput{
		LoadBalancerID: "lb-1",
		PackageID:      "pkg-2",
	}
}

func TestQuoteResizeLoadBalancerSendsQuoteBody(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("decode body: %v, raw = %s", err, data)
		}
		_, _ = w.Write([]byte(`{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
	}))

	out, err := c.QuoteResizeLoadBalancer(context.Background(), validResizeLoadBalancerInput())
	if err != nil {
		t.Fatalf("QuoteResizeLoadBalancer() error = %v", err)
	}
	if body["resourceType"] != "load-balancer" || body["action"] != "resize" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	info, ok := body["resourceInfo"].(map[string]any)
	if !ok {
		t.Fatalf("resourceInfo missing or wrong type: %+v", body)
	}
	want := map[string]any{"packageId": "pkg-2", "loadBalancerId": "lb-1"}
	for k, v := range want {
		if info[k] != v {
			t.Fatalf("resourceInfo[%q] = %v, want %v (info = %+v)", k, info[k], v, info)
		}
	}
	if len(info) != len(want) {
		t.Fatalf("resourceInfo = %+v, want exactly %+v", info, want)
	}
	if out.OptimumPrice != 800000 {
		t.Fatalf("OptimumPrice = %v, want 800000", out.OptimumPrice)
	}
}

// TestQuoteResizeLoadBalancerIgnoresMaxPriceAndNoWait checks that MaxPrice
// and NoWait, which govern only a future ResizeLoadBalancer, change nothing
// about the quote sent here.
func TestQuoteResizeLoadBalancerIgnoresMaxPriceAndNoWait(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"optimumPrice":800000,"originalPrice":800000,"discountPrice":0,"propertiesPrice":[]}`))
	}))
	in := validResizeLoadBalancerInput()
	in.MaxPrice = 1
	in.NoWait = true
	if _, err := c.QuoteResizeLoadBalancer(context.Background(), in); err != nil {
		t.Fatalf("QuoteResizeLoadBalancer() error = %v", err)
	}
}

func TestQuoteResizeLoadBalancerRejectsMissingFields(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	in := validResizeLoadBalancerInput()
	in.LoadBalancerID = ""
	if _, err := c.QuoteResizeLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("LoadBalancerID empty: err = %v, want ErrInvalidInput", err)
	}
	in2 := validResizeLoadBalancerInput()
	in2.PackageID = ""
	if _, err := c.QuoteResizeLoadBalancer(context.Background(), in2); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("PackageID empty: err = %v, want ErrInvalidInput", err)
	}
	if _, err := c.QuoteResizeLoadBalancer(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("nil input err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteResizeLoadBalancerRejectsBadBodyIDs(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not be called")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validResizeLoadBalancerInput()
		in.LoadBalancerID = bad
		if _, err := c.QuoteResizeLoadBalancer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("LoadBalancerID=%q err = %v, want ErrInvalidInput", bad, err)
		}

		in2 := validResizeLoadBalancerInput()
		in2.PackageID = bad
		if _, err := c.QuoteResizeLoadBalancer(context.Background(), in2); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("PackageID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// TestQuoteResizeLoadBalancerNotFoundPassesThrough checks that a resize
// quote for a missing load balancer, a 400 rather than a 404, reaches the
// caller unchanged and is not classified as vngcloud.IsNotFound: the server
// checks the shape before the ID, so this differs from every other
// load-balancer read's 404.
func TestQuoteResizeLoadBalancerNotFoundPassesThrough(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"The resource is not found."}`))
	}))
	_, err := c.QuoteResizeLoadBalancer(context.Background(), validResizeLoadBalancerInput())
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want *vngcloud.APIError with status 400", err)
	}
	if vngcloud.IsNotFound(err) {
		t.Fatalf("err = %v, want IsNotFound false: a 400 is not classified as NotFound", err)
	}
}
