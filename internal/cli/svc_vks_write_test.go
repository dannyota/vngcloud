package cli

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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
