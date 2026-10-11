package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/network"
)

const natRouteWarning = "Creates a 0.0.0.0/0 route in the named VPC. Every VM in that VPC uses this NAT for egress. Use a disposable VPC for testing; never test in a production VPC."
const natRenewalWarning = "The purchase starts with auto-renew enabled. After the NAT is ACTIVE, this command disables renewal through billing and confirms it. The command waits for both steps. If it fails after purchase, renewal may remain enabled; inspect the NAT and billing before taking another action."

func natWriteOp(t *testing.T, name string) Op[network.Client] {
	t.Helper()
	for _, op := range networkOps {
		if op.name == name {
			return op
		}
	}
	t.Fatalf("%s is not registered", name)
	return Op[network.Client]{}
}

func natCreateArgs(name string) []string {
	return []string{"network", name, "--name", "edge", "--zone-id", "zone-1", "--availability-zone-id", "HAN01-1B", "--package-id", "package-1", "--vpc-id", "vpc-1"}
}

func TestNetworkNATWriteMapping(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want map[string]any
	}{
		{"list-nat-zones", []string{"--zone-id", "zone-1"}, map[string]any{"ZoneID": "zone-1"}},
		{"list-nat-packages", []string{"--zone-id", "zone-1", "--availability-zone-id", "HAN01-1B"}, map[string]any{"ZoneID": "zone-1", "AvailabilityZoneID": "HAN01-1B"}},
		{"quote-create-nat-instance", natCreateArgs("quote-create-nat-instance")[2:], map[string]any{"Name": "edge", "ZoneID": "zone-1", "AvailabilityZoneID": "HAN01-1B", "PackageID": "package-1", "VPCID": "vpc-1", "MaxPrice": float64(0)}},
		{"create-nat-instance", append(natCreateArgs("create-nat-instance")[2:], "--yes", "--max-price", "100"), map[string]any{"Name": "edge", "ZoneID": "zone-1", "AvailabilityZoneID": "HAN01-1B", "PackageID": "package-1", "VPCID": "vpc-1", "MaxPrice": float64(100)}},
		{"delete-nat-instance", []string{"--zone-id", "zone-1", "--vpc-id", "vpc-1", "--nat-id", "nat-1", "--yes", "--no-wait"}, map[string]any{"ZoneID": "zone-1", "VPCID": "vpc-1", "NATID": "nat-1", "NoWait": true}},
	} {
		for _, inputJSON := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/json=%t", tc.name, inputJSON), func(t *testing.T) {
				h := newFakeHarness(t)
				op := natWriteOp(t, tc.name)
				calls := 0
				op.call = func(_ *cobra.Command, _ *network.Client, _ context.Context, in any) (any, error) {
					calls++
					raw, err := json.Marshal(in)
					if err != nil {
						t.Fatal(err)
					}
					var got map[string]any
					if err = json.Unmarshal(raw, &got); err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Errorf("input=%s want=%v", raw, tc.want)
					}
					return op.newOutput(), nil
				}
				root := newTestRoot(h.e)
				root.AddCommand(Service(h.e, "network", "test", network.New, op))
				args := append([]string{"network", tc.name}, tc.args...)
				if inputJSON {
					values := make(map[string]any)
					for k, v := range tc.want {
						values[k] = v
					}
					values["ZoneID"] = "zone-json"
					raw, err := json.Marshal(values)
					if err != nil {
						t.Fatal(err)
					}
					args = []string{"network", tc.name, "--cli-input-json", string(raw), "--zone-id", "zone-1"}
					if op.kind == kindWrite {
						args = append(args, "--yes")
					}
				}
				if err := execCmd(t, root, args); err != nil || calls != 1 {
					t.Fatalf("error=%v calls=%d", err, calls)
				}
			})
		}
	}
}

func TestNetworkNATConsentBeforeRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"create missing yes", append(natCreateArgs("create-nat-instance"), "--max-price", "100")},
		{"create missing cap", append(natCreateArgs("create-nat-instance"), "--yes")},
		{"create null cap", []string{"network", "create-nat-instance", "--yes", "--cli-input-json", `{"Name":"edge","ZoneID":"zone-1","AvailabilityZoneID":"HAN01-1B","PackageID":"package-1","VPCID":"vpc-1","MaxPrice":null}`}},
		{"create read-only", append(natCreateArgs("create-nat-instance"), "--yes", "--max-price", "100", "--profile", "agent")},
		{"delete missing yes", []string{"network", "delete-nat-instance", "--zone-id", "zone-1", "--vpc-id", "vpc-1", "--nat-id", "nat-1"}},
		{"delete read-only", []string{"network", "delete-nat-instance", "--zone-id", "zone-1", "--vpc-id", "vpc-1", "--nat-id", "nat-1", "--yes", "--profile", "agent"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			code, out, stderr := run(tc.args...)
			if code != 2 || calls != 0 || out != "" || !strings.Contains(stderr, "InvalidUsage") {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, calls, out, stderr)
			}
		})
	}
}

func TestNetworkNATRequiredAndForbiddenFlags(t *testing.T) {
	for _, name := range []string{"quote-create-nat-instance", "create-nat-instance", "delete-nat-instance"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
			args := natCreateArgs(name)
			if name == "delete-nat-instance" {
				args = []string{"network", name, "--vpc-id", "vpc-1", "--nat-id", "nat-1", "--yes"}
			} else {
				args = append(args[:4], args[6:]...)
				if name == "create-nat-instance" {
					args = append(args, "--yes", "--max-price", "100")
				}
			}
			code, _, stderr := run(args...)
			if code != 2 || calls != 0 || !strings.Contains(stderr, "--zone-id is required") {
				t.Fatalf("exit=%d calls=%d stderr=%s", code, calls, stderr)
			}
		})
	}
	for _, name := range []string{"quote-create-nat-instance", "create-nat-instance"} {
		for _, flag := range []string{"no-wait", "period", "auto-renew", "body", "subnet-id"} {
			t.Run(name+"/"+flag, func(t *testing.T) {
				run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request") }))
				code, _, stderr := run("network", name, "--"+flag+"=ignored")
				if code != 2 || !strings.Contains(stderr, "unknown flag") {
					t.Fatalf("exit=%d stderr=%s", code, stderr)
				}
			})
		}
	}
}

func TestNetworkNATWriteHelp(t *testing.T) {
	run := natCLI(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("help sent request") }))
	for _, name := range []string{"quote-create-nat-instance", "create-nat-instance", "delete-nat-instance", "list-nat-zones", "list-nat-packages"} {
		code, out, stderr := run("network", name, "--help")
		if code != 0 {
			t.Fatalf("%s exit=%d stderr=%s", name, code, stderr)
		}
		want := []string{"region-level", "--zone-id"}
		if name == "create-nat-instance" {
			want = append(want, natRouteWarning, natRenewalWarning, "HAN", "IAM-user login", "package UUID")
		}
		if name == "delete-nat-instance" {
			want = append(want, "egress can stop", "prior routes are not restored", "--no-wait", "--yes")
		}
		if name == "quote-create-nat-instance" || name == "create-nat-instance" || name == "delete-nat-instance" {
			want = append(want, "vngcloud network list-v-network-regions", "uuid")
		}
		if name != "delete-nat-instance" && name != "list-nat-zones" {
			want = append(want, "HAN01-1B", "vngcloud network list-nat-zones")
		}
		for _, value := range want {
			if !strings.Contains(out, value) {
				t.Errorf("%s help lacks %q", name, value)
			}
		}
	}
}

func TestNetworkNATWriteErrorsAndPartialOutput(t *testing.T) {
	for _, tc := range []struct {
		err     error
		code    string
		exit    int
		partial bool
	}{
		{vngcloud.ErrInvalidConfig, "InvalidConfig", 2, false},
		{network.ErrNotSettled, "NotSettled", 1, true},
		{network.ErrFailed, "WriteFailed", 1, true},
		{vngcloud.ErrPriceAboveMax, "PriceAboveMax", 1, false},
		{vngcloud.ErrUnpriced, "Unpriced", 1, false},
		{vngcloud.ErrNotFound, "NotFound", 4, false},
	} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(tc.code+"/"+format, func(t *testing.T) {
				h := newFakeHarness(t)
				op := natWriteOp(t, "create-nat-instance")
				op.call = func(_ *cobra.Command, _ *network.Client, _ context.Context, _ any) (any, error) {
					if !tc.partial {
						return (*network.CreateNATInstanceOutput)(nil), tc.err
					}
					renewal := true
					return &network.CreateNATInstanceOutput{NATInstance: &network.NATInstance{UUID: "nat-1", NATName: "edge", Status: "ACTIVE"}, MonthlyPrice: 100, TotalPrice: 100, Currency: "VND", AutoRenew: &renewal}, errors.Join(tc.err, context.Canceled)
				}
				root := newTestRoot(h.e)
				root.AddCommand(Service(h.e, "network", "test", network.New, op))
				args := append(natCreateArgs("create-nat-instance"), "--yes", "--max-price", "100", "--output", format, "--query", "OrderID")
				err := execCmd(t, root, args)
				if classify(err).Code != tc.code || exitCode(err) != tc.exit {
					t.Fatalf("error=%v code=%s exit=%d", err, classify(err).Code, exitCode(err))
				}
				if tc.partial {
					for _, value := range []string{"nat-1", "100", "VND"} {
						if !strings.Contains(h.stdout.String(), value) {
							t.Errorf("partial output lacks %s: %s", value, h.stdout)
						}
					}
				} else if h.stdout.Len() != 0 {
					t.Errorf("unexpected stdout %s", h.stdout)
				}
				var stderr bytes.Buffer
				printError(&stderr, err)
				if !strings.Contains(stderr.String(), `"code":"`+tc.code+`"`) {
					t.Errorf("stderr=%s", stderr.String())
				}
			})
		}
	}
}
