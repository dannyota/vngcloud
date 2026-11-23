package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

const (
	epCLIVPC     = "net-00000000-0000-0000-0000-000000000002"
	epCLISubnet  = "sub-00000000-0000-0000-0000-000000000003"
	epCLIID      = "enp-00000000-0000-0000-0000-0000000000c1"
	epCLIName    = "demo"
	epCLIRefusal = "Refusing"
)

func epCreateArgs(name string) []string {
	return []string{"network", name, "--name", epCLIName, "--vpc-id", epCLIVPC, "--subnet-id", epCLISubnet}
}

func TestNetworkEndpointWriteMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"quote-create-endpoint", epCreateArgs("quote-create-endpoint")[2:], map[string]any{"Name": epCLIName, "VPCID": epCLIVPC, "SubnetID": epCLISubnet, "MaxPrice": float64(0)}},
		{"create-endpoint", append(epCreateArgs("create-endpoint")[2:], "--yes", "--max-price", "712400"), map[string]any{"Name": epCLIName, "VPCID": epCLIVPC, "SubnetID": epCLISubnet, "MaxPrice": float64(712400)}},
		{"create-endpoint", append(epCreateArgs("create-endpoint")[2:], "--yes", "--max-price", "0"), map[string]any{"Name": epCLIName, "VPCID": epCLIVPC, "SubnetID": epCLISubnet, "MaxPrice": float64(0)}},
		{"delete-endpoint", []string{"--endpoint-id", epCLIID, "--vpc-id", epCLIVPC, "--yes"}, map[string]any{"EndpointID": epCLIID, "VPCID": epCLIVPC}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFakeHarness(t)
			op := natWriteOp(t, tc.name)
			calls := 0
			op.call = func(_ *cobra.Command, _ *network.Client, _ context.Context, in any) (any, error) {
				calls++
				raw, _ := json.Marshal(in)
				var got map[string]any
				_ = json.Unmarshal(raw, &got)
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("input=%s want=%v", raw, tc.want)
				}
				return op.newOutput(), nil
			}
			root := newTestRoot(h.e)
			root.AddCommand(Service(h.e, "network", "test", network.New, op))
			if err := execCmd(t, root, append([]string{"network", tc.name}, tc.args...)); err != nil || calls != 1 {
				t.Fatalf("error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestNetworkEndpointKinds(t *testing.T) {
	if natWriteOp(t, "quote-create-endpoint").kind != kindRead {
		t.Error("quote is not a read")
	}
	for _, name := range []string{"create-endpoint", "delete-endpoint"} {
		if natWriteOp(t, name).kind != kindWrite {
			t.Errorf("%s is not a write", name)
		}
	}
}

func TestNetworkEndpointConsentBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"create missing yes", append(epCreateArgs("create-endpoint"), "--max-price", "100")},
		{"create missing cap", append(epCreateArgs("create-endpoint"), "--yes")},
		{"create null cap", []string{"network", "create-endpoint", "--yes", "--cli-input-json", `{"Name":"demo","VPCID":"` + epCLIVPC + `","SubnetID":"` + epCLISubnet + `","MaxPrice":null}`}},
		{"create negative cap", append(epCreateArgs("create-endpoint"), "--yes", "--max-price", "-1")},
		{"create read-only", append(epCreateArgs("create-endpoint"), "--yes", "--max-price", "100", "--profile", "agent")},
		{"delete missing yes", []string{"network", "delete-endpoint", "--endpoint-id", epCLIID, "--vpc-id", epCLIVPC}},
		{"delete read-only", []string{"network", "delete-endpoint", "--endpoint-id", epCLIID, "--vpc-id", epCLIVPC, "--yes", "--profile", "agent"}},
		{"delete missing vpc", []string{"network", "delete-endpoint", "--endpoint-id", epCLIID, "--yes"}},
		{"create bad vpc", []string{"network", "create-endpoint", "--name", "demo", "--vpc-id", "x", "--subnet-id", epCLISubnet, "--yes", "--max-price", "1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			code, out, stderr := run(tc.args...)
			if code != 2 || calls != 0 || out != "" {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, out, stderr)
			}
		})
	}
}

func TestNetworkEndpointQuoteRunsOnReadOnlyProfile(t *testing.T) {
	run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	_, _, stderr := run(append(epCreateArgs("quote-create-endpoint"), "--profile", "agent")...)
	if strings.Contains(strings.ToLower(stderr), "read-only") {
		t.Fatalf("quote refused as write: %s", stderr)
	}
}

func TestNetworkEndpointForbiddenFlags(t *testing.T) {
	for name, flags := range map[string][]string{
		"quote-create-endpoint": {"no-wait", "force", "category-id", "service-id", "package-id", "dns", "auto-renew", "max-price"},
		"create-endpoint":       {"no-wait", "force", "category-id", "service-id", "package-id", "dns", "auto-renew", "zone-id"},
		"delete-endpoint":       {"no-wait", "force", "cascade", "zone-id"},
	} {
		for _, flag := range flags {
			t.Run(name+"/"+flag, func(t *testing.T) {
				run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request sent") }))
				code, _, stderr := run("network", name, "--"+flag+"=x")
				if code != 2 || !strings.Contains(stderr, "unknown flag") {
					t.Fatalf("exit=%d stderr=%s", code, stderr)
				}
			})
		}
	}
}

func TestNetworkEndpointWriteHelp(t *testing.T) {
	run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("help sent request") }))
	for name, want := range map[string][]string{
		"quote-create-endpoint": {"VND per month", "vStorage", "HCM03-1C", "Read-only profiles can run this command"},
		"create-endpoint":       {"VND per month", "request auto-renew off", "--yes", "--max-price", "not a server price lock", "Do not repeat", "network list-endpoints", "no --no-wait", "Quote and debit can differ", "does not grant bucket access"},
		"delete-endpoint":       {"--yes", "breaks", "cluster", "promises no refund", "refund can stay pending", "billing list-transactions", "network list-endpoints", "attached resource"},
	} {
		code, out, stderr := run("network", name, "--help")
		if code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", name, code, stderr)
		}
		for _, value := range want {
			if !strings.Contains(out, value) {
				t.Errorf("%s help lacks %q", name, value)
			}
		}
		if strings.Contains(out, "—") {
			t.Errorf("%s help has an em dash", name)
		}
	}
}

func TestNetworkEndpointErrorsAndPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    string
		exit    int
		partial bool
	}{
		{vngcloud.ErrInvalidConfig, "InvalidConfig", 2, false},
		{network.ErrNotSettled, "NotSettled", 1, true},
		{vngcloud.ErrPriceAboveMax, "PriceAboveMax", 1, false},
		{vngcloud.ErrUnpriced, "Unpriced", 1, false},
		{vngcloud.ErrNotFound, "NotFound", 4, false},
		{vngcloud.ErrAuth, "AuthError", 3, false},
	} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(tc.code+"/"+format, func(t *testing.T) {
				h := newFakeHarness(t)
				op := natWriteOp(t, "create-endpoint")
				op.call = func(_ *cobra.Command, _ *network.Client, _ context.Context, _ any) (any, error) {
					if !tc.partial {
						return (*network.CreateEndpointOutput)(nil), tc.err
					}
					return &network.CreateEndpointOutput{EndpointID: epCLIID, Status: "PROVISIONING", MonthlyPrice: 712400, Currency: "VND"}, errors.Join(tc.err, context.Canceled)
				}
				root := newTestRoot(h.e)
				root.AddCommand(Service(h.e, "network", "test", network.New, op))
				err := execCmd(t, root, append(epCreateArgs("create-endpoint"), "--yes", "--max-price", "712400", "--output", format, "--query", "Nothing"))
				if exitCode(err) != tc.exit || (tc.code != "AuthError" && classify(err).Code != tc.code) {
					t.Fatalf("error=%v code=%s exit=%d", err, classify(err).Code, exitCode(err))
				}
				if tc.partial {
					for _, value := range []string{epCLIID, "712400"} {
						if !strings.Contains(h.stdout.String(), value) {
							t.Errorf("partial output lacks %s: %s", value, h.stdout)
						}
					}
				} else if h.stdout.Len() != 0 {
					t.Errorf("unexpected stdout %s", h.stdout)
				}
				var stderr bytes.Buffer
				printError(&stderr, err)
			})
		}
	}
}
