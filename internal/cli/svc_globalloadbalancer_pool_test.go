package cli

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud/globalloadbalancer"
)

var childWriteArgs = map[string][]string{
	"create-pool":        {"create-pool", "--load-balancer-id", "glb-1", "--name", "p", "--member-name", "m", "--backend-server-id", "srv-1", "--backend-vpc-id", "vpc-1", "--backend-subnet-id", "sub-1", "--backend-port", "8080"},
	"create-listener":    {"create-listener", "--load-balancer-id", "glb-1", "--name", "l", "--pool-id", "pool-1", "--port", "443", "--allowed-cidrs", "192.0.2.5/32", "--allowed-cidrs", "198.51.100.0/24", "--backend-server-id", "srv-1"},
	"delete-listener":    {"delete-listener", "--load-balancer-id", "glb-1", "--listener-id", "lis-1"},
	"delete-pool":        {"delete-pool", "--load-balancer-id", "glb-1", "--pool-id", "pool-1"},
}

func childArgs(command string, extra ...string) []string {
	out := []string{"--region", "hcm-3", "--project-id", "proj-1"}
	out = append(out, extra...)
	out = append(out, "globalloadbalancer")
	return append(out, childWriteArgs[command]...)
}

func TestGLBChildWritesRequireYes(t *testing.T) {
	for command := range childWriteArgs {
		t.Run(command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, _, _ := newSvcRoot(t, fixture)
			root.SetArgs(childArgs(command))
			err := root.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "--yes") || exitCode(err) != 2 || fixture.requestCount() != 0 {
				t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
			}
		})
	}
}

func TestGLBChildWritesReadOnly(t *testing.T) {
	for command := range childWriteArgs {
		t.Run(command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, _, _ := newSvcRoot(t, fixture)
			root.SetArgs(childArgs(command, "--read-only", "--yes"))
			err := root.ExecuteContext(context.Background())
			if err == nil || classify(err).Code != "ReadOnly" || fixture.requestCount() != 0 {
				t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
			}
		})
	}
}

func TestGLBChildWritesRefuseUntrustedAuthBeforeRequests(t *testing.T) {
	for command := range childWriteArgs {
		t.Run(command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, _, _ := newSvcRoot(t, fixture)
			root.SetArgs(childArgs(command, "--yes"))
			err := root.ExecuteContext(context.Background())
			if err == nil || classify(err).Code != "InvalidConfig" || fixture.requestCount() != 0 {
				t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
			}
		})
	}
}

func TestGLBChildCreatesNeedProjectID(t *testing.T) {
	for _, command := range []string{"create-pool", "create-listener"} {
		t.Run(command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, _, _ := newSvcRoot(t, fixture)
			root.SetArgs(append([]string{"--region", "hcm-3", "--yes", "globalloadbalancer"}, childWriteArgs[command]...))
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
				t.Fatalf("err=%v requests=%d", err, fixture.requestCount())
			}
		})
	}
}

func TestGLBChildFlagsBindProjectAndRepeatCIDRs(t *testing.T) {
	var got *globalloadbalancer.CreateListenerInput
	op := globalLoadBalancerChildOps()[1]
	op.call = func(_ *cobra.Command, _ *globalloadbalancer.Client, _ context.Context, in any) (any, error) {
		got = in.(*globalloadbalancer.CreateListenerInput)
		return &globalloadbalancer.CreateListenerOutput{}, nil
	}
	h := newFakeHarness(t)
	root := newTestRoot(h.e)
	root.AddCommand(Service(h.e, "globalloadbalancer", "test", globalloadbalancer.New, op))
	args := append([]string{"--project-id", "proj-1", "--yes", "globalloadbalancer"}, childWriteArgs["create-listener"]...)
	if err := execCmd(t, root, args); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.BackendProjectID != "proj-1" || len(got.AllowedCIDRs) != 2 || got.AllowedCIDRs[0] != "192.0.2.5/32" || got.Port != 443 {
		t.Fatalf("input=%+v", got)
	}
}

func TestGLBChildHelpStatesBillingAndOrder(t *testing.T) {
	want := map[string][]string{
		"create-pool":        {"No quote is available", "bills child usage under the account's terms", "neither selects nor verifies the payment source", "does not promise a refund", "create-listener"},
		"create-listener":    {"No quote is available", "bills child usage under the account's terms", "neither selects nor verifies the payment source", "does not promise a refund", "/32", "create-pool"},
		"delete-listener":    {"does not promise a refund", "delete-pool", "pool and its member group stay"},
		"delete-pool":        {"does not promise a refund", "member group and backend row", "never the vServer", "delete-load-balancer"},
	}
	for command, words := range want {
		t.Run(command, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){})
			root, stdout, _ := newSvcRoot(t, fixture)
			root.SetArgs([]string{"globalloadbalancer", command, "--help"})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, word := range words {
				if !strings.Contains(stdout.String(), word) {
					t.Errorf("help for %s misses %q", command, word)
				}
			}
			if strings.Contains(strings.ToLower(stdout.String()), "free") {
				t.Errorf("help for %s says free", command)
			}
			for _, banned := range []string{"--no-wait", "--force"} {
				if strings.Contains(stdout.String(), banned) {
					t.Errorf("help for %s offers %s", command, banned)
				}
			}
		})
	}
}

func TestGLBChildPartialOutputAndExitCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		code string
	}{
		"not settled": {globalloadbalancer.ErrNotSettled, "NotSettled"},
		"failed":      {globalloadbalancer.ErrFailed, "WriteFailed"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newFakeHarness(t)
			op := globalLoadBalancerChildOps()[0]
			op.call = func(_ *cobra.Command, _ *globalloadbalancer.Client, _ context.Context, _ any) (any, error) {
				return &globalloadbalancer.CreatePoolOutput{Pool: globalloadbalancer.Pool{ID: "pool-1"}}, tc.err
			}
			root := newTestRoot(h.e)
			root.AddCommand(Service(h.e, "globalloadbalancer", "test", globalloadbalancer.New, op))
			args := append([]string{"--output", "json", "--project-id", "proj-1", "--yes", "globalloadbalancer"}, childWriteArgs["create-pool"]...)
			err := execCmd(t, root, args)
			if classify(err).Code != tc.code || !strings.Contains(h.stdout.String(), `"pool-1"`) {
				t.Fatalf("err=%v stdout=%s", err, h.stdout)
			}
			if exitCode(err) == 0 {
				t.Fatalf("exit code 0 for %v", err)
			}
		})
	}
}
