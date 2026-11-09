package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Synthetic credit total for cases without a matching captured response.
const cliResizeCreditSynthetic = `{"optimumPrice":-180797}`

func resizeCreditQuoteFixture(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("../../testdata/pricing/GetQuoteServerResizeCredit.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Compare command stdout directly: rendering a separate model would not
// exercise the command's fixture decoding, query, or output wiring.
func checkResizeCreditCommandGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := goldenPath(name)
	if *updateGolden {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s: got:\n%s\nwant:\n%s", name, got, want)
	}
}

func TestResizeQuoteReadOnlyCredit(t *testing.T) {
	captured := resizeCreditQuoteFixture(t)
	for _, cmd := range []struct {
		name      string
		args      []string
		requests  int
		quoteBody string
	}{
		{"compute", []string{"compute", "quote-resize-server", "--server-id", "server-1", "--flavor-id", "flavor-2"}, 1, captured},
		{"volume", []string{"volume", "quote-resize-volume", "--volume-id", "volume-1", "--size", "20"}, 2, cliResizeCreditSynthetic},
		{"loadbalancer", []string{"loadbalancer", "quote-resize-load-balancer", "--load-balancer-id", "lb-1", "--package-id", "pkg-2"}, 1, cliResizeCreditSynthetic},
		{"pricing", []string{"pricing", "get-quote", "--resource-type", "server", "--action", "resize"}, 1, captured},
	} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(cmd.name+"/"+format, func(t *testing.T) {
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/v1/price":                   jsonHandler(200, cmd.quoteBody),
					"/v2/proj-1/volumes/volume-1": jsonHandler(200, `{"data":{"uuid":"volume-1","status":"AVAILABLE","size":10,"volumeTypeId":"type-1"}}`),
				})
				root, stdout, stderr := newSvcRoot(t, fixture)
				args := []string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "--output", format}
				root.SetArgs(append(args, cmd.args...))
				if err := root.ExecuteContext(context.Background()); err != nil {
					t.Fatalf("quote: %v (stderr=%s)", err, stderr)
				}
				if fixture.requestCount() != cmd.requests {
					t.Fatalf("stdout = %q, requests = %d", stdout, fixture.requestCount())
				}
				prefix := cmd.name + "-resize-credit"
				if cmd.name == "volume" || cmd.name == "loadbalancer" {
					prefix += "-synthetic"
				}
				checkResizeCreditCommandGolden(t, prefix+"."+format+".golden", stdout.Bytes())
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

func TestComputeResizeCreditConsent(t *testing.T) {
	captured := resizeCreditQuoteFixture(t)
	for _, mode := range []string{"success", "no-wait", "missing-yes", "read-only", "failed-wait"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				var puts atomic.Int64
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
					"/v1/price": jsonHandler(200, captured),
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
				if err != nil || puts.Load() != 1 || !strings.Contains(stdout.String(), "-372552") {
					t.Fatalf("err = %v, puts = %d, stdout = %q", err, puts.Load(), stdout)
				}
				if mode == "success" {
					checkResizeCreditCommandGolden(t, "compute-resize-server-credit."+format+".golden", stdout.Bytes())
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
