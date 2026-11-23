package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/vks"
)

func TestVKSCreateRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no-confirmation", nil},
		{"read-only", []string{"--read-only", "--yes"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, _, _ := newVKSRoot(t, fixture, false)
			args := []string{"--region", "hcm-3"}
			args = append(args, tc.args...)
			args = append(args, "vks", "create-cluster", "--cli-input-json", `{"Name":"cluster","Version":"v1","VPCID":"vpc-1","SubnetID":"subnet-1","PodCIDR":"10.20.0.0/16","NodeGroupName":"group","NumNodes":1,"FlavorID":"flavor-1","OS":"ubuntu","DiskSize":20,"DiskTypeID":"disk-1","SecurityGroupIDs":["sg-1"],"SSHKeyID":"key-1"}`)
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if err == nil || fixture.requestCount() != 0 {
				t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
			}
			want := "--yes"
			if tc.name == "read-only" {
				want = "read-only"
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error = %v, want %s", err, want)
			}
		})
	}
}

func TestVKSCreateJSON(t *testing.T) {
	h := newFakeHarness(t)
	op := Write[vks.Client, vks.CreateClusterInput, vks.CreateClusterOutput]("create-cluster", (*vks.Client).CreateCluster, Guard(requireVKSCreateConsent))
	called := false
	op.call = func(_ *cobra.Command, _ *vks.Client, _ context.Context, input any) (any, error) {
		called = true
		in := input.(*vks.CreateClusterInput)
		if in.Name != "cluster" || !in.NoWait {
			t.Fatalf("input=%+v", in)
		}
		return &vks.CreateClusterOutput{ID: "cluster-1"}, nil
	}
	root := newTestRoot(h.e)
	root.AddCommand(Service(h.e, "vks", "test", vks.New, op))
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "vks", "create-cluster", "--cli-input-json", `{"Name":"cluster","Version":"v1","VPCID":"vpc-1","SubnetID":"subnet-1","PodCIDR":"10.20.0.0/16","NodeGroupName":"group","NumNodes":1,"FlavorID":"flavor-1","OS":"ubuntu","DiskSize":20,"DiskTypeID":"disk-1","SecurityGroupIDs":["sg-1"],"SSHKeyID":"key-1","NoWait":true}`})
	if err := root.ExecuteContext(context.Background()); err != nil || !called {
		t.Fatalf("err=%v called=%v", err, called)
	}
}

func TestVKSCreatePartialOutput(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		err          error
	}{
		{"canceled", "CREATING", errors.Join(vks.ErrNotSettled, context.Canceled)},
		{"failed", "ERROR", vks.ErrFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHarness(t)
			op := Write[vks.Client, vks.CreateClusterInput, vks.CreateClusterOutput]("create-cluster", (*vks.Client).CreateCluster)
			op.call = func(_ *cobra.Command, _ *vks.Client, _ context.Context, _ any) (any, error) {
				return &vks.CreateClusterOutput{ID: "cluster-1", Status: tc.status}, tc.err
			}
			root := newTestRoot(h.e)
			root.AddCommand(Service(h.e, "vks", "test", vks.New, op))
			root.SetArgs([]string{"--region", "hcm-3", "--yes", "vks", "create-cluster", "--cli-input-json", `{"Name":"cluster","Version":"v1","VPCID":"vpc-1","SubnetID":"subnet-1","PodCIDR":"10.20.0.0/16","NodeGroupName":"group","NumNodes":1,"FlavorID":"flavor-1","OS":"ubuntu","DiskSize":20,"DiskTypeID":"disk-1","SecurityGroupIDs":["sg-1"],"SSHKeyID":"key-1"}`})
			err := root.ExecuteContext(context.Background())
			if !errors.Is(err, tc.err) || !strings.Contains(h.stdout.String(), "cluster-1") || !strings.Contains(h.stdout.String(), tc.status) {
				t.Fatalf("err=%v output=%s", err, h.stdout.String())
			}
		})
	}
}

func TestVKSDeleteRequiresYes(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, _ := newVKSRoot(t, fixture, false)
	root.SetArgs([]string{"--region", "hcm-3", "vks", "delete-cluster", "--cluster-id", "cluster-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--yes") || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestVKSWriteHelp(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, out, _ := newVKSRoot(t, fixture, false)
	root.SetArgs([]string{"vks", "create-cluster", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"no quote API", "four service endpoints", "--yes", "not workload ready", "5 to 20", "5 to 15", "network get-subnet", "volume get-default-volume-type", "same zone"} {
		if !strings.Contains(out.String(), text) {
			t.Errorf("help lacks %q", text)
		}
	}
}

func TestVKSCreateConfirmationFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		cancel bool
	}{
		{"not-found", http.StatusNotFound, false},
		{"unauthorized", http.StatusUnauthorized, false},
		{"canceled", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/vks-api/v1/clusters": func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(http.StatusAccepted)
					_, _ = fmt.Fprint(w, `{"id":"cluster-1"}`)
				},
				"/vks-api/v1/clusters/cluster-1": func(w http.ResponseWriter, r *http.Request) {
					if tc.cancel {
						cancel()
						return
					}
					w.WriteHeader(tc.status)
				},
			})
			server := httptest.NewServer(fixture.mux)
			defer server.Close()
			target, parseErr := url.Parse(server.URL)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			api := saCLITransport(func(r *http.Request) (*http.Response, error) {
				clone := r.Clone(r.Context())
				u := *r.URL
				clone.URL = &u
				clone.URL.Scheme, clone.URL.Host = target.Scheme, target.Host
				clone.Host = target.Host
				return server.Client().Transport.RoundTrip(clone)
			})
			auth := saCLITransport(func(r *http.Request) (*http.Response, error) {
				w := httptest.NewRecorder()
				switch {
				case r.URL.Host == "signin.greennode.ai" && r.Method == http.MethodGet:
					_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
				case r.URL.Host == "signin.greennode.ai":
					w.Header().Set("Location", "https://dashboard.console.greennode.ai/?code=synthetic")
					w.WriteHeader(http.StatusFound)
				default:
					_, _ = w.Write([]byte(`{"accessToken":"test-token","expiresIn":3600}`))
				}
				return w.Result(), nil
			})
			cfg, cfgErr := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root", Username: "user", Password: "synthetic", HTTPClient: &http.Client{Transport: auth}}), vngcloud.WithTransport(api), vngcloud.WithRetry(0, time.Millisecond))
			if cfgErr != nil {
				t.Fatal(cfgErr)
			}
			h := newFakeHarness(t)
			root := newTestRoot(h.e)
			op := Write[vks.Client, vks.CreateClusterInput, vks.CreateClusterOutput]("create-cluster", (*vks.Client).CreateCluster)
			root.AddCommand(Service(h.e, "vks", "test", func(vngcloud.Config) *vks.Client { return vks.New(cfg) }, op))
			stdout := h.stdout
			root.SetArgs([]string{"--region", "hcm-3", "--yes", "vks", "create-cluster", "--cli-input-json", `{"Name":"cluster","Version":"v1","VPCID":"vpc-1","SubnetID":"subnet-1","PodCIDR":"10.20.0.0/16","NodeGroupName":"group","NumNodes":1,"FlavorID":"flavor-1","OS":"ubuntu","DiskSize":20,"DiskTypeID":"disk-1","SecurityGroupIDs":["sg-1"],"SSHKeyID":"key-1"}`})
			err := root.ExecuteContext(ctx)
			if !errors.Is(err, vks.ErrNotSettled) || classify(err).Code != "NotSettled" || exitCode(err) != 1 || !strings.Contains(stdout.String(), "cluster-1") {
				t.Fatalf("err=%v exit=%d stdout=%s", err, exitCode(err), stdout)
			}
			if tc.cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("missing cancellation: %v", err)
			}
			if !strings.Contains(err.Error(), "vks.CreateCluster") || !strings.Contains(err.Error(), "inspect inventory and billing before another mutation") {
				t.Fatalf("missing reconciliation guidance: %v", err)
			}
		})
	}
}
