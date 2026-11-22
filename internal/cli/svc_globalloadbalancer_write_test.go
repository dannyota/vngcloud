package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/globalloadbalancer"
)

func TestGLBCreateRejectionMessageOutput(t *testing.T) {
	var output bytes.Buffer
	printError(&output, &vngcloud.APIError{Operation: "globalloadbalancer.CreateLoadBalancer", StatusCode: 402, Code: "RequestFailed", Message: "Insufficient credits for this order"})
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Status  int    `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Message != "Insufficient credits for this order" || envelope.Error.Status != 402 {
		t.Fatalf("output=%s", output.String())
	}
}

func TestGLBUnsettledClassification(t *testing.T) {
	if got := classify(globalloadbalancer.ErrNotSettled).Code; got != "NotSettled" {
		t.Fatalf("code=%s", got)
	}
}

func TestGLBCreateReadOnly(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--read-only", "globalloadbalancer", "create-load-balancer", "--name", "demo", "--package-id", "pkg-1", "--yes"})
	err := root.ExecuteContext(context.Background())
	if err == nil || classify(err).Code != "ReadOnly" || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestGLBDeleteRequiresConsent(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"globalloadbalancer", "delete-load-balancer", "--load-balancer-id", "glb-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--yes") || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestGLBCreateNestedFlagsOverrideJSON(t *testing.T) {
	var input globalloadbalancer.CreateLoadBalancerInput
	if err := json.Unmarshal([]byte(`{"Name":"demo","PackageID":"pkg-1","Listener":{"Name":"old","Port":80,"AllowedCIDRs":["10.0.0.0/8"]},"Pool":{"Name":"pool"}}`), &input); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{}
	cmd.Flags().Bool("yes", false, "")
	createGlobalLoadBalancerOp().extraFlags(cmd)
	if err := cmd.Flags().Parse([]string{"--listener-name", "new", "--yes"}); err != nil {
		t.Fatal(err)
	}
	if err := globalLoadBalancerCreateGuard(cmd, &input); err != nil {
		t.Fatal(err)
	}
	if input.Listener.Name != "new" || input.Listener.Port != 80 {
		t.Fatalf("listener=%+v", input.Listener)
	}
}

func TestGLBCreateRequiresConsent(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, _, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"globalloadbalancer", "create-load-balancer", "--name", "demo", "--package-id", "pkg-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--yes") || fixture.requestCount() != 0 {
		t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
	}
}

func TestGLBCreateHelpExplainsCharges(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"globalloadbalancer", "create-load-balancer", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"credits", "three-day", "traffic", "CIDR"} {
		if !strings.Contains(stdout.String(), word) {
			t.Errorf("missing %s", word)
		}
	}
}
