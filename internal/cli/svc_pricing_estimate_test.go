package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/pricing"
)

func TestEstimatePublicSDKQuoteAndUnknown(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		info, ok := body["resourceInfo"].(map[string]any)
		if !ok || body["resourceType"] != "public-vip" || body["action"] != "create" || len(body) != 3 || len(info) != 3 || info["type"] != "public-vm" || info["isBuyMorePo"] != false || info["isPoc"] != false {
			t.Errorf("unexpected quote body: %v", body)
		}
		testutil.WriteFixture(t, w, "../../testdata/network/discovery_quote_public_vm.json")
	}})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--read-only", "pricing", "estimate", "--cli-input-json",
		`{"PublicIPs":[{"Key":"quoted","Kind":"public-vip","Type":"public-vm","Quantity":3},{"Key":"unknown","Kind":"floating-ip","Quantity":2}]}`})
	if err := root.ExecuteContext(computeDiscoveryCLIContext()); err != nil {
		t.Fatal(err)
	}
	var out pricing.EstimateOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if fixture.requestCount() != 1 || len(out.Items) != 2 || out.Items[0].MonthlyAmount == nil || *out.Items[0].MonthlyAmount != *out.Items[0].Price.MonthlyPrice*3 || out.Items[1].MonthlyAmount != nil || !out.Metadata.Complete || out.Total.RequestedComplete || out.Total.RequestedMonthlyTotal != nil || out.Total.CostComplete || stderr.Len() != 0 {
		t.Fatalf("output=%s stderr=%s requests=%d", stdout, stderr, fixture.requestCount())
	}
}

func TestEstimateReadOnlyAllowsQuoteAndRefusesWrite(t *testing.T) {
	for _, mode := range []string{"flag", "profile"} {
		t.Run(mode, func(t *testing.T) {
			home := withCleanEnv(t)
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": func(w http.ResponseWriter, _ *http.Request) {
				testutil.WriteFixture(t, w, "../../testdata/network/discovery_quote_public_vm.json")
			}})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)
			globals := []string{"--read-only", "--region", "hcm-3", "--project-id", "proj-1"}
			if mode == "profile" {
				writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nproject_id = proj-1\nread_only = true\n")
				writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")
				globals = []string{"--profile", "agent"}
			}
			var stdout, stderr bytes.Buffer
			root := newRootCmd(strings.NewReader(""), &stdout, &stderr)
			root.SetArgs(append(globals, "pricing", "estimate", "--cli-input-json",
				`{"PublicIPs":[{"Key":"quoted","Kind":"public-vip","Type":"public-vm","Quantity":1}]}`))
			if err := root.ExecuteContext(computeDiscoveryCLIContext()); err != nil {
				t.Fatalf("estimate refused under %s read-only: %v", mode, err)
			}
			var out pricing.EstimateOutput
			if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Items) != 1 || out.Items[0].Price.Status != "quoted" || out.Items[0].MonthlyAmount == nil || fixture.requestCount() != 1 || stderr.Len() != 0 {
				t.Fatalf("estimate: stdout=%s stderr=%s requests=%d", &stdout, &stderr, fixture.requestCount())
			}
			stdout.Reset()
			stderr.Reset()
			root = newRootCmd(strings.NewReader(""), &stdout, &stderr)
			root.SetArgs(append(globals, "billing", "delete-budget", "--budget-uuid", "budget-1", "--yes"))
			err := root.ExecuteContext(computeDiscoveryCLIContext())
			var refusal readOnlyError
			if !errors.As(err, &refusal) || exitCode(err) != 2 || stdout.Len() != 0 || fixture.requestCount() != 1 {
				t.Fatalf("write under %s read-only: err=%v stdout=%s requests=%d", mode, err, &stdout, fixture.requestCount())
			}
		})
	}
}

func TestEstimateLimitReportsRequiredRequests(t *testing.T) {
	fixture := newSvcFixture(nil)
	newSvcRoot(t, fixture)
	var stdout, stderr bytes.Buffer
	code := Main(computeDiscoveryCLIContext(), []string{"--region", "hcm-3", "--project-id", "proj-1", "pricing", "estimate", "--max-requests", "1", "--cli-input-json",
		`{"PublicIPs":[{"Key":"one","Kind":"public-vip","Type":"public","Quantity":1},{"Key":"two","Kind":"public-vip","Type":"public-vm","Quantity":1}]}`}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || fixture.requestCount() != 0 || !strings.Contains(stderr.String(), "requires 2 requests") || !strings.Contains(stderr.String(), `"code":"EstimateLimit"`) {
		t.Fatalf("exit=%d stdout=%s stderr=%s requests=%d", code, &stdout, &stderr, fixture.requestCount())
	}
}

func TestEstimateArithmeticCLI(t *testing.T) {
	for _, stage := range []string{"multiplication", "subtotal"} {
		for _, partial := range []bool{false, true} {
			for _, query := range []string{"", "Total"} {
				t.Run(stage+"/"+strconv.FormatBool(partial)+"/"+query, func(t *testing.T) {
					fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": jsonHandler(200, `{"optimumPrice":1e308,"propertiesPrice":[]}`)})
					root, stdout, stderr := newSvcRoot(t, fixture)
					quantity := 1
					if stage == "multiplication" {
						quantity = 2
					}
					input := fmt.Sprintf(`{"AllowPartial":%t,"PublicIPs":[{"Key":"one","Kind":"public-vip","Type":"public","Quantity":%d},{"Key":"two","Kind":"public-vip","Type":"public-vm","Quantity":1},{"Key":"unknown","Kind":"reservation","Quantity":1}]}`, partial, quantity)
					root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--query", query, "pricing", "estimate", "--cli-input-json", input})
					err := root.ExecuteContext(computeDiscoveryCLIContext())
					var arithmetic *pricing.EstimateArithmeticError
					if exitCode(err) != 1 || !errors.Is(err, pricing.ErrEstimateOverflow) || !errors.As(err, &arithmetic) || arithmetic.Stage != stage {
						t.Fatalf("err=%v", err)
					}
					if partial {
						if !json.Valid(stdout.Bytes()) || !errors.Is(err, pricing.ErrDiscoveryIncomplete) || strings.Contains(stdout.String(), "Infinity") || strings.Contains(stdout.String(), "NaN") {
							t.Fatalf("partial stdout=%s error=%v", stdout, err)
						}
						if query == "" {
							var out pricing.EstimateOutput
							if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
								t.Fatal(err)
							}
							if out.Metadata.Complete || out.Total.RequestedComplete || out.Total.CostComplete || out.Total.RequestedMonthlyTotal != nil {
								t.Fatal("overflow claimed completeness")
							}
							if stage == "subtotal" && (out.Total.KnownMonthlySubtotal != nil || out.Items[0].MonthlyAmount == nil || out.Items[1].MonthlyAmount == nil) {
								t.Fatal("subtotal overflow discarded rows or returned a prefix sum")
							}
							if stage == "multiplication" && (out.Items[0].MonthlyAmount != nil || out.Items[0].Price.MonthlyPrice != nil || len(out.Items[0].Breakdown) != 0 || out.Total.KnownMonthlySubtotal == nil) {
								t.Fatal("multiplication overflow retained invalid amounts or lost survivor")
							}
						}
					} else if stdout.Len() != 0 {
						t.Fatalf("fail-fast output=%s stderr=%s", stdout, stderr)
					}
					want := 2
					if stage == "multiplication" && !partial {
						want = 1
					}
					if fixture.requestCount() != want {
						t.Fatalf("requests=%d want=%d", fixture.requestCount(), want)
					}
					if partial && query != "" && (!strings.Contains(stderr.String(), "arithmetic-overflow") || !strings.Contains(stderr.String(), "not a commitment")) {
						t.Fatalf("query hid overflow coverage: %s", stderr)
					}
				})
			}
		}
	}
}

func TestEstimateReflectionsAcrossFormats(t *testing.T) {
	for _, format := range []string{"json", "table", "text"} {
		for _, query := range []string{"", "Items[].Price"} {
			t.Run(format+"/"+query, func(t *testing.T) {
				fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": jsonHandler(200, `{"optimumPrice":1,"propertiesPrice":[{"name":"test\u002dtoken"}]}`)})
				root, stdout, stderr := newSvcRoot(t, fixture)
				root.SetArgs([]string{"--region", "hcm-3", "--project-id", "proj-1", "--debug", "--output", format, "--query", query, "pricing", "estimate", "--allow-partial", "--cli-input-json",
					`{"PublicIPs":[{"Key":"safe","Kind":"reservation","Quantity":1},{"Key":"bad","Kind":"public-vip","Type":"public","Quantity":1}]}`})
				err := root.ExecuteContext(computeDiscoveryCLIContext())
				printError(stderr, err)
				var api *vngcloud.APIError
				if exitCode(err) != 1 || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || fixture.requestCount() != 1 {
					t.Fatalf("error=%v requests=%d", err, fixture.requestCount())
				}
				for _, text := range []string{stdout.String(), stderr.String(), err.Error()} {
					if strings.Contains(text, "test-token") || strings.Contains(text, `test\u002dtoken`) {
						t.Fatal("credential reflection escaped the SDK guard")
					}
				}
			})
		}
	}
}

func TestEstimateMainPartialErrorAndControlOverride(t *testing.T) {
	for _, partial := range []bool{false, true} {
		fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){"/v1/price": jsonHandler(500, `{}`)})
		newSvcRoot(t, fixture)
		var stdout, stderr bytes.Buffer
		code := Main(computeDiscoveryCLIContext(), []string{"--region", "hcm-3", "--project-id", "proj-1", "--query", "Items[].Key", "pricing", "estimate", "--allow-partial=" + strconv.FormatBool(partial), "--cli-input-json",
			`{"AllowPartial":true,"PublicIPs":[{"Key":"known-unknown","Kind":"reservation","Quantity":1},{"Key":"failed","Kind":"public-vip","Type":"public","Quantity":1}]}`}, strings.NewReader(""), &stdout, &stderr)
		if code != 1 || fixture.requestCount() != 1 {
			t.Fatalf("exit=%d requests=%d stderr=%s", code, fixture.requestCount(), &stderr)
		}
		if partial {
			if !strings.Contains(stdout.String(), "known-unknown") || !strings.Contains(stderr.String(), `"code":"DiscoveryIncomplete"`) || !strings.Contains(stderr.String(), "Coverage") {
				t.Fatalf("partial stdout=%s stderr=%s", &stdout, &stderr)
			}
		} else if stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code":"ServerError"`) {
			t.Fatalf("override did not disable partial mode: stdout=%s stderr=%s", &stdout, &stderr)
		}
	}
}

func TestEstimateHelpAndLocalFlags(t *testing.T) {
	root, stdout, _ := newSvcRoot(t, newSvcFixture(nil))
	root.SetArgs([]string{"pricing", "estimate", "--help"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Quantity", "bundled", "Volumes", "Coverage", "file://setup.json", "--allow-partial", "--max-requests"} {
		if !strings.Contains(stdout.String(), text) {
			t.Errorf("help lacks %q", text)
		}
	}
	cmd, _, err := root.Find([]string{"pricing", "estimate"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"yes", "max-price", "servers", "volumes", "public-ips", "storage", "sort-by", "descending"} {
		if cmd.LocalFlags().Lookup(name) != nil {
			t.Errorf("unexpected local flag --%s", name)
		}
	}
}
