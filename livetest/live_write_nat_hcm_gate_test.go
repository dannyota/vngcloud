//go:build livewrite

package livetest_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

func TestNATHarnessNATPostObserver(t *testing.T) {
	id := ""
	tr := &natDeleteTransport{natID: &id, base: natTestTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("unavailable") })}
	for _, path := range []string{"/vnetwork/v1/zone/project/nats/price", "/vnetwork/v1/zone/project/nats"} {
		req, err := http.NewRequest(http.MethodPost, "https://example.invalid"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = tr.RoundTrip(req)
	}
	if tr.posts.Load() != 1 {
		t.Fatalf("NAT POST count %d", tr.posts.Load())
	}
	attempted := !natOrderNotSent(vngcloud.ErrInvalidConfig, tr.posts.Load())
	if !attempted || natVPCDeleteAllowed(attempted, false, false, false) {
		t.Fatal("sent order must retain cleanup safeguards")
	}
}

func TestNATHarnessCreateErrorCleanup(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		posts       int32
		wantCleanup bool
	}{
		{"price above max", vngcloud.ErrPriceAboveMax, 0, true},
		{"invalid input", vngcloud.ErrInvalidInput, 0, true},
		{"post sent", vngcloud.ErrInvalidInput, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempted := !natOrderNotSent(tc.err, tc.posts)
			if got := natVPCDeleteAllowed(attempted, false, false, false); got != tc.wantCleanup {
				t.Fatalf("cleanup allowed = %t, want %t", got, tc.wantCleanup)
			}
		})
	}
}

func TestNATHarnessHCMRefusalCleansVPC(t *testing.T) {
	vpcDeleted, natWrites := false, 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/nats/v3-whitelist"):
			_, _ = w.Write([]byte(`{"success":true,"data":{"enabledForAll":false}}`))
		case strings.Contains(r.URL.Path, "/nats") && r.Method != http.MethodGet:
			natWrites++
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/networks/vpc-1"):
			vpcDeleted = true
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/subnets"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/networks/vpc-1"):
			if vpcDeleted {
				w.WriteHeader(http.StatusNotFound)
			} else {
				_, _ = w.Write([]byte(`{"id":"vpc-1","status":"ACTIVE"}`))
			}
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer api.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"synthetic-token","expiresIn":3600}`))
	}))
	defer token.Close()
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Redirect(w, r, token.URL+"/callback?code=synthetic-code", http.StatusFound)
		} else {
			_, _ = w.Write([]byte(`<input name="_csrf" value="synthetic-csrf">`))
		}
	}))
	defer signin.Close()
	natID := ""
	observer := &natDeleteTransport{base: http.DefaultTransport, natID: &natID}
	cfg, err := vngcloud.NewConfig(vngcloud.WithTransport(observer), vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithRetry(0, 0), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: api.URL, VNetwork: api.URL}))
	if err != nil {
		t.Fatal(err)
	}
	client := network.New(cfg)
	_, err = client.CreateNATInstance(context.Background(), &network.CreateNATInstanceInput{Name: "example", ZoneID: "zone-1", AvailabilityZoneID: "HCM03-1B", PackageID: "package-1", VPCID: "vpc-1", MaxPrice: 100})
	if !errors.Is(err, vngcloud.ErrInvalidConfig) || !natOrderNotSent(err, observer.posts.Load()) {
		t.Fatalf("create must fail without an order: %v", err)
	}
	if natOrderNotSent(nil, 0) || !natOrderNotSent(errors.New("uncertain"), 0) || natOrderNotSent(err, 1) {
		t.Fatal("unproven refusal accepted")
	}
	attempted := true
	if natOrderNotSent(err, observer.posts.Load()) {
		attempted = false
	}
	if !natVPCDeleteAllowed(attempted, false, false, false) || !liveNATDeleteVPC(context.Background(), client, "vpc-1") || !vpcDeleted || natWrites != 0 {
		t.Fatalf("cleanup deleted=%v NAT writes=%d", vpcDeleted, natWrites)
	}
}
