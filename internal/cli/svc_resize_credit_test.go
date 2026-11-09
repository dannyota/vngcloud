package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/pricing"
)

// Synthetic credit total only; the raw server credit response is not captured.
const cliResizeCreditSynthetic = `{"optimumPrice":-180797}`

func TestResizeQuoteReadOnlyCreditSynthetic(t *testing.T) {
	for _, cmd := range []struct {
		name     string
		args     []string
		requests int
	}{
		{"compute", []string{"compute", "quote-resize-server", "--server-id", "server-1", "--flavor-id", "flavor-2"}, 1},
		{"volume", []string{"volume", "quote-resize-volume", "--volume-id", "volume-1", "--size", "20"}, 2},
		{"loadbalancer", []string{"loadbalancer", "quote-resize-load-balancer", "--load-balancer-id", "lb-1", "--package-id", "pkg-2"}, 1},
		{"pricing", []string{"pricing", "get-quote", "--resource-type", "server", "--action", "resize"}, 1},
	} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(cmd.name+"/"+format, func(t *testing.T) {
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/v1/price":                   jsonHandler(200, cliResizeCreditSynthetic),
					"/v2/proj-1/volumes/volume-1": jsonHandler(200, `{"data":{"uuid":"volume-1","status":"AVAILABLE","size":10,"volumeTypeId":"type-1"}}`),
				})
				root, stdout, stderr := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "--output", format}
				root.SetArgs(append(args, cmd.args...))
				if err := root.ExecuteContext(context.Background()); err != nil {
					t.Fatalf("quote: %v (stderr=%s)", err, stderr)
				}
				if !strings.Contains(stdout.String(), "-180797") || fixture.requestCount() != cmd.requests {
					t.Fatalf("stdout = %q, requests = %d", stdout, fixture.requestCount())
				}
				checkGolden(t, cmd.name+"-resize-credit-synthetic."+format+".golden", format, "", &pricing.GetQuoteOutput{OptimumPrice: -180797, Properties: []pricing.PriceProperty{}})
			})
		}
	}
}

func TestPricingCreateCreditRefusedSynthetic(t *testing.T) {
	for _, action := range []string{"", "create"} {
		t.Run(action, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": jsonHandler(200, cliResizeCreditSynthetic)})
			root, stdout, _ := newSvcRoot(t, fixture)
			args := []string{"--region", "hcm-3", "--read-only", "pricing", "get-quote", "--resource-type", "server"}
			if action != "" {
				args = append(args, "--action", action)
			}
			root.SetArgs(args)
			err := root.ExecuteContext(context.Background())
			if err == nil || exitCode(err) != 1 || classify(err).Code != "RequestFailed" || stdout.Len() != 0 || fixture.requestCount() != 1 {
				t.Fatalf("err = %v, stdout = %q, requests = %d", err, stdout, fixture.requestCount())
			}
		})
	}
}

func TestComputeResizeCreditConsentSynthetic(t *testing.T) {
	for _, mode := range []string{"success", "no-wait", "missing-yes", "read-only", "failed-wait"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				var puts atomic.Int64
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/v1/price": jsonHandler(200, cliResizeCreditSynthetic),
					"/v2/proj-1/servers/server-1": func(w http.ResponseWriter, _ *http.Request) {
						flavor, status := "flavor-1", "ACTIVE"
						if puts.Load() > 0 {
							flavor = "flavor-2"
							if mode == "failed-wait" {
								status = "ERROR"
							}
						}
						_, _ = w.Write([]byte(computeServerFlavorJSON(status, flavor)))
					},
					"/v2/proj-1/servers/server-1/resize": func(w http.ResponseWriter, _ *http.Request) { puts.Add(1); w.WriteHeader(202) },
				})
				root, stdout, _ := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--output", format}
				if mode != "missing-yes" {
					args = append(args, "--yes")
				}
				if mode == "read-only" {
					args = append(args, "--read-only")
				}
				args = append(args, "compute", "resize-server", "--server-id", "server-1", "--flavor-id", "flavor-2")
				if mode == "no-wait" {
					args = append(args, "--no-wait")
				}
				root.SetArgs(args)
				err := root.ExecuteContext(context.Background())
				if mode == "missing-yes" || mode == "read-only" {
					if err == nil || exitCode(err) != 2 || fixture.requestCount() != 0 {
						t.Fatalf("err = %v, requests = %d", err, fixture.requestCount())
					}
					return
				}
				if mode == "failed-wait" {
					if err == nil || exitCode(err) != 1 || classify(err).Code != "WriteFailed" || puts.Load() != 1 {
						t.Fatalf("err = %v, puts = %d", err, puts.Load())
					}
					return
				}
				if err != nil || puts.Load() != 1 || !strings.Contains(stdout.String(), "-180797") {
					t.Fatalf("err = %v, puts = %d, stdout = %q", err, puts.Load(), stdout)
				}
				if mode == "success" {
					checkGolden(t, "compute-resize-server-credit-synthetic."+format+".golden", format, "", &compute.ResizeServerOutput{Server: compute.Server{UUID: "server-1", Name: "web-1", Status: "ACTIVE"}, MonthlyPrice: -180797})
				}
			})
		}
	}
}

func TestVolumeResizeCreditRefusedSynthetic(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/v1/price":                   jsonHandler(200, cliResizeCreditSynthetic),
		"/v2/proj-1/volumes/volume-1": jsonHandler(200, `{"data":{"uuid":"volume-1","status":"AVAILABLE","size":10,"volumeTypeId":"type-1"}}`),
	})
	root, stdout, _ := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--yes", "volume", "resize-volume", "--volume-id", "volume-1", "--size", "20"})
	err := root.ExecuteContext(context.Background())
	if err == nil || exitCode(err) != 1 || classify(err).Code != "Unpriced" || !strings.Contains(fmt.Sprint(err), "-180797") || stdout.Len() != 0 || fixture.requestCount() != 2 {
		t.Fatalf("err = %v, stdout = %q, requests = %d", err, stdout, fixture.requestCount())
	}
}
