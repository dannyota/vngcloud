package globalloadbalancer

import (
	"errors"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

func TestChildWritesRejectUntrustedConfigWithZeroRequests(t *testing.T) {
	provider := &testProvider{}
	invalid := "https://invalid.example"
	for name, opt := range map[string]vngcloud.LoadOption{
		"glb":       vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{GLB: invalid}),
		"vserver":   vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: invalid}),
		"signin":    vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Signin: invalid}),
		"token":     vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Token: invalid}),
		"dashboard": vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Dashboard: invalid}),
		"static":    vngcloud.WithStaticToken("synthetic"),
		"provider":  vngcloud.WithCredentialsProvider(provider),
	} {
		t.Run(name, func(t *testing.T) {
			requests := 0
			rt := glbRoundTrip(func(*http.Request) (*http.Response, error) { requests++; return nil, errors.New("unexpected request") })
			cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID(fxProject), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: rt}}), vngcloud.WithTransport(rt), opt)
			if err != nil {
				t.Fatal(err)
			}
			c := New(cfg)
			_, e1 := c.CreatePool(bg(), poolInput())
			_, e2 := c.CreateListener(bg(), listenerInput())
			_, e3 := c.DeleteListener(bg(), &DeleteListenerInput{LoadBalancerID: fxGLB, ListenerID: fxListener})
			_, e4 := c.DeletePool(bg(), &DeletePoolInput{LoadBalancerID: fxGLB, PoolID: fxPool})
			for i, err := range []error{e1, e2, e3, e4} {
				if !errors.Is(err, vngcloud.ErrInvalidConfig) {
					t.Errorf("call %d err=%v", i, err)
				}
			}
			if requests != 0 || provider.calls != 0 {
				t.Fatalf("requests=%d provider=%d", requests, provider.calls)
			}
		})
	}
}

func TestChildWritesRejectNonIAMClientsWithZeroRequests(t *testing.T) {
	requests := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	_, err := c.CreatePool(bg(), poolInput())
	_, err2 := c.DeleteLoadBalancer(bg(), &DeleteLoadBalancerInput{LoadBalancerID: fxGLB})
	if !errors.Is(err, vngcloud.ErrInvalidConfig) || !errors.Is(err2, vngcloud.ErrInvalidConfig) || requests != 0 {
		t.Fatalf("err=%v err2=%v requests=%d", err, err2, requests)
	}
}
