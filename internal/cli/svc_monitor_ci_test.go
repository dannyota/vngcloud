package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/monitor"
)

func TestSyntheticMutationPreSDKOutcome(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, flags := range [][]string{nil, {"--max-price", "0"}, {"--output", "invalid"}, {"--query", "Sent"}, {"--cli-input-json", "{"}, {"--max-price", "oops"}, {"--read-only"}} {
			t.Run(command+strings.Join(flags, " "), func(t *testing.T) {
				withCleanEnv(t)
				withTestOptions(t, syntheticCLITestOptions(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected pre-SDK request") }))...)
				var out, stderr bytes.Buffer
				code := Main(context.Background(), append([]string{"monitor", command}, flags...), strings.NewReader(""), &out, &stderr)
				var value map[string]any
				if json.Unmarshal(out.Bytes(), &value) != nil || len(value) != 8 || value["Sent"] != "no" || value["Stage"] != "preflight" || code != 2 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
			})
		}
	}
}

func TestSyntheticMutationCLIRealTransport(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, tc := range []struct {
			name                         string
			status                       int
			body, stage, code, sent      string
			exit                         int
			noWait, confirm, stale, skip bool
			price                        string
			listPrice                    bool
			waitStatus                   int
		}{
			{name: "confirmed", status: 200, stage: "confirmed", sent: "yes", confirm: true},
			{name: "accepted", status: 200, stage: "accepted", sent: "yes", noWait: true},
			{name: "unpriced", status: 200, stage: "preflight", sent: "no", code: "Unpriced", exit: 1, listPrice: true},
			{name: "price", status: 200, stage: "preflight", sent: "no", code: "PriceAboveMax", exit: 1, price: `{"optimumPrice":1}`},
			{name: "stale", status: 200, stage: "preflight", sent: "no", code: "QuotaChanged", exit: 1, stale: true},
			{name: "bad request", status: 400, stage: "refused", sent: "yes", code: "BadRequest", exit: 1},
			{name: "unauthorized", status: 401, stage: "refused", sent: "yes", code: "Unauthorized", exit: 3},
			{name: "forbidden", status: 403, stage: "refused", sent: "yes", code: "Forbidden", exit: 1},
			{name: "not found", status: 404, stage: "refused", sent: "yes", code: "NotFound", exit: 4},
			{name: "early conflict", status: 409, stage: "refused", sent: "yes", code: "Conflict", exit: 1},
			{name: "throttled", status: 429, stage: "refused", sent: "yes", code: "Throttled", exit: 1},
			{name: "server error", status: 503, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 500", status: 500, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 502", status: 502, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 504", status: 504, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 301", status: 301, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 302", status: 302, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 303", status: 303, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 307", status: 307, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 308", status: 308, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 201", status: 201, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "status 204", status: 204, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},

			{name: "wait forbidden", status: 200, waitStatus: 403, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "wait unauthorized", status: 200, waitStatus: 401, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "malformed", status: 200, body: "{", stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "payment", status: 200, body: `{"paymentUrl":"https://peer-canary.invalid"}`, stage: "unconfirmed", sent: "yes", code: "NotSettled", exit: 1},
			{name: "disconnect", status: -1, stage: "unconfirmed", sent: "unknown", code: "NotSettled", exit: 1},
			{name: "not due", status: 200, stage: "not-due", sent: "no", skip: true},
		} {
			if command == "recover-synthetic-quota" && tc.skip {
				continue
			}
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				withCleanEnv(t)
				var sends, quotes atomic.Int32
				baseline := "2500-12-01T05:19:45Z"
				status := "ACTIVE"
				if command == "recover-synthetic-quota" {
					baseline = "2000-12-01T05:19:45Z"
					status = "IN-TRASH"
				}
				raw, err := os.ReadFile("../../testdata/monitor/GetSyntheticQuotaRecovered.json")
				if err != nil {
					t.Fatal(err)
				}
				var quota map[string]any
				if err = json.Unmarshal(raw, &quota); err != nil {
					t.Fatal(err)
				}
				quota["endTime"] = baseline
				quota["startTime"] = "1999-01-01T00:00:00Z"
				quota["status"] = status
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/billing-api/v1/synthetic/quota":
						snapshot := maps.Clone(quota)
						if sends.Load() > 0 && tc.waitStatus != 0 {
							w.WriteHeader(tc.waitStatus)
							return
						}
						if sends.Load() > 0 && tc.confirm {
							snapshot["status"] = "ACTIVE"
							snapshot["endTime"] = "2501-01-01T05:19:45Z"
						}
						_ = json.NewEncoder(w).Encode(snapshot)
					case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
						b, e := os.ReadFile("../../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
						if e != nil {
							t.Error(e)
						}
						if tc.listPrice {
							b = bytes.ReplaceAll(b, []byte(`"price": 0.0`), []byte(`"price": 10`))
						}
						_, _ = w.Write(b)
					case "/billing-api/v2/synthetic/prices/created-price":
						quotes.Add(1)
						price := tc.price
						if price == "" {
							price = `{"optimumPrice":0}`
						}
						_, _ = io.WriteString(w, price)
					case "/billing-api/v2/trash/synthetic/quota", "/billing-api/v2/synthetic/quota/extend-time":
						sends.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						if tc.status == -1 {
							conn, _, e := w.(http.Hijacker).Hijack()
							if e != nil {
								t.Error(e)
								return
							}
							_ = conn.Close()
							return
						}
						w.Header().Set("Location", "/must-not-follow")
						w.WriteHeader(tc.status)
						body := tc.body
						if body == "" && tc.status != 200 {
							body = `{"message":"peer-canary"}`
						}
						_, _ = io.WriteString(w, body)
					default:
						t.Errorf("unexpected route %s", r.URL)
						w.WriteHeader(500)
					}
				})
				server := httptest.NewServer(handler)
				t.Cleanup(server.Close)
				withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(server.Client())), vngcloud.WithStaticToken("synthetic-bearer-canary"), vngcloud.WithRetry(3, time.Millisecond))
				args := []string{"monitor", command, "--region", "hcm-3", "--max-price", "0", "--output", "json"}
				if command == "recover-synthetic-quota" {
					id := "quota-syn-1"
					if tc.stale {
						id = "other"
					}
					args = append(args, "--expected-quota-id", id)
				} else {
					end := baseline
					if tc.stale {
						end = "2500-12-02T05:19:45Z"
					}
					args = append(args, "--expected-end-time", end)
				}
				if tc.skip {
					args = append(args, "--max-days-left", "7")
				}
				if tc.noWait {
					args = append(args, "--no-wait")
				}
				var out, stderr bytes.Buffer
				code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
				var value map[string]any
				if json.Unmarshal(out.Bytes(), &value) != nil || len(value) != 8 || code != tc.exit || value["Sent"] != tc.sent || value["Stage"] != tc.stage {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
				confirmation := "Renewed"
				if command == "recover-synthetic-quota" {
					confirmation = "Recovered"
				}
				if value[confirmation] != tc.confirm || value["PaymentPending"] != (tc.name == "payment") || value["OrderID"] != "" {
					t.Fatalf("outcome=%s", &out)
				}
				if tc.confirm {
					q, ok := value["Quota"].(map[string]any)
					if !ok || q["EndTime"] != "2501-01-01T05:19:45Z" {
						t.Fatalf("quota=%v", value["Quota"])
					}
				}
				wantSends := int32(1)
				if tc.sent == "no" {
					wantSends = 0
				}
				if sends.Load() != wantSends {
					t.Fatalf("sends=%d want=%d", sends.Load(), wantSends)
				}
				wantQuotes := int32(1)
				if tc.stale || tc.skip {
					wantQuotes = 0
				}
				if quotes.Load() != wantQuotes {
					t.Fatalf("quotes=%d want=%d", quotes.Load(), wantQuotes)
				}
				if tc.code == "" {
					if value["Error"] != nil {
						t.Fatalf("output=%s", &out)
					}
				} else {
					meta := value["Error"].(map[string]any)
					expectedStatus := 0
					if tc.sent == "yes" {
						expectedStatus = tc.status
					}
					expectedOperation := "monitor.RenewSyntheticQuota"
					if command == "recover-synthetic-quota" {
						expectedOperation = "monitor.RecoverSyntheticQuota"
					}
					if meta["Status"] != float64(expectedStatus) || meta["Operation"] != expectedOperation {
						t.Fatalf("error metadata=%v", meta)
					}
					var env struct {
						Error struct {
							Code, Operation string
							Status          int
						}
					}
					if json.Unmarshal(stderr.Bytes(), &env) != nil || meta["Code"] != tc.code || env.Error.Code != tc.code || env.Error.Operation != meta["Operation"] || float64(env.Error.Status) != meta["Status"] {
						t.Fatalf("stdout=%s stderr=%s", &out, &stderr)
					}
				}
				for _, canary := range []string{"peer-canary", "synthetic-bearer-canary"} {
					if strings.Contains(out.String()+stderr.String(), canary) {
						t.Fatalf("leaked %s", canary)
					}
				}
			})
		}
	}
}

func TestSyntheticMutationGoldens(t *testing.T) {
	end := time.Date(2026, 10, 25, 21, 43, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		out  any
	}{
		{"recover-synthetic-quota", &monitor.RecoverSyntheticQuotaOutput{QuotaMutationOutcome: monitor.QuotaMutationOutcome{Sent: "yes", Stage: "accepted"}, PreviousEndTime: &end}},
		{"renew-synthetic-quota", &monitor.RenewSyntheticQuotaOutput{QuotaMutationOutcome: monitor.QuotaMutationOutcome{Sent: "yes", Stage: "unconfirmed", Error: &monitor.QuotaOutcomeError{Code: "NotSettled", Operation: "monitor.RenewSyntheticQuota", Status: 200}}, PreviousEndTime: &end}},
		{"quote-renew-synthetic-quota", &monitor.QuotaQuote{PackageID: "32b3743d-94b8-49ab-984d-bd7ec99b46f6", ResourceID: "quota-syn-1", Quantity: 10, MonthPeriod: 1, ExpectedEndTime: &end, Currency: "VND"}},
	} {
		for _, format := range []string{"json", "table", "text"} {
			var out bytes.Buffer
			if err := renderOutput(&out, format, "", tc.out, true); err != nil {
				t.Fatal(err)
			}
			dest := goldenPath("monitor-" + tc.name + "." + format + ".golden")
			if *updateGolden {
				if err := os.WriteFile(dest, out.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				want, err := os.ReadFile(dest)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(want, out.Bytes()) {
					t.Fatalf("%s got=%s want=%s", dest, &out, want)
				}
			}
		}
	}
}

func TestSyntheticMutationCLISendFailures(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, mode := range []string{"dial", "cancel before", "cancel after", "timeout"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				withCleanEnv(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var sends, dials, detailReads atomic.Int32
				expiry := "2500-12-01T05:19:45Z"
				body, err := os.ReadFile("../../testdata/monitor/GetSyntheticQuotaRecovered.json")
				if err != nil {
					t.Fatal(err)
				}
				quota := strings.ReplaceAll(string(body), "2026-12-01T05:19:45.000+00:00", expiry)
				if command == "recover-synthetic-quota" {
					quota = string(mustSyntheticCLIFixture(t, "GetSyntheticQuotaInTrash"))
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/billing-api/v1/synthetic/quota":
						_, _ = io.WriteString(w, quota)
					case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
						_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDetailNullable"))
						if detailReads.Add(1) == 2 && mode == "cancel before" {
							cancel()
						}
					case "/billing-api/v2/synthetic/prices/created-price":
						_, _ = io.WriteString(w, `{"optimumPrice":0}`)
					case "/billing-api/v2/synthetic/quota/extend-time", "/billing-api/v2/trash/synthetic/quota":
						sends.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						if mode == "cancel after" {
							cancel()
						}
						<-r.Context().Done()
					default:
						t.Errorf("unexpected %s", r.URL)
						w.WriteHeader(500)
					}
				}))
				t.Cleanup(server.Close)
				dialer := new(net.Dialer)
				rt := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					n := dials.Add(1)
					if mode == "dial" && n == 6 {
						return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
					}
					return dialer.DialContext(ctx, network, address)
				}}
				t.Cleanup(rt.CloseIdleConnections)
				hc := &http.Client{Transport: rt}
				if mode == "timeout" {
					rt.ResponseHeaderTimeout = time.Second
				}
				withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(hc)), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))
				args := []string{"monitor", command, "--region", "hcm-3", "--max-price", "0"}
				if command == "recover-synthetic-quota" {
					args = append(args, "--expected-quota-id", "quota-syn-1")
				} else {
					args = append(args, "--expected-end-time", expiry)
				}
				var out, stderr bytes.Buffer
				code := Main(ctx, args, strings.NewReader(""), &out, &stderr)
				var result struct {
					Sent, Stage string
					Error       *monitor.QuotaOutcomeError
				}
				if json.Unmarshal(out.Bytes(), &result) != nil || code != 1 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
				wantSent, wantStage, wantCode := "unknown", "unconfirmed", "NotSettled"
				wantCount := int32(1)
				if mode == "dial" || mode == "cancel before" {
					wantSent, wantStage, wantCode = "no", "preflight", "RequestFailed"
					wantCount = 0
				}
				if result.Sent != wantSent || result.Stage != wantStage || result.Error.Code != wantCode || sends.Load() != wantCount {
					t.Fatalf("result=%+v error=%+v sends=%d", result, result.Error, sends.Load())
				}
				if mode == "dial" && dials.Load() != 6 {
					t.Fatalf("dial attempts=%d", dials.Load())
				}
			})
		}
	}
}
func mustSyntheticCLIFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile("../../testdata/monitor/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestSyntheticMutationPreflightConfigOutcomes(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, mode := range []string{"credentials", "config", "flag read only", "environment read only", "profile read only", "profile invalid"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				home := withCleanEnv(t)
				opts := syntheticCLITestOptions(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request during refusal") }))
				withTestOptions(t, opts...)
				assertSyntheticCLILoopbackEndpoint(t)
				args := []string{"monitor", command, "--region", "hcm-3", "--max-price", "0"}
				if command == "recover-synthetic-quota" {
					args = append(args, "--expected-quota-id", "quota-syn-1")
				} else {
					args = append(args, "--expected-end-time", "2500-12-01T00:00:00Z")
				}
				wantCode, wantExit := "NoCredentials", 3
				switch mode {
				case "config":
					writeConfigFile(t, home, "[broken\n")
					wantCode, wantExit = "InvalidConfig", 2
				case "flag read only":
					args = append(args, "--read-only")
					wantCode, wantExit = "ReadOnly", 2
				case "environment read only":
					t.Setenv("VNGCLOUD_READ_ONLY", "true")
					wantCode, wantExit = "ReadOnly", 2
				case "profile read only", "profile invalid":
					value := "true"
					if mode == "profile invalid" {
						value = "invalid"
					}
					writeConfigFile(t, home, "[default]\nregion = hcm-3\nread_only = "+value+"\n")
					withTestOptions(t, append(slices.Clone(opts), vngcloud.WithStaticToken("synthetic-token"))...)
					wantCode, wantExit = "ReadOnly", 2
					if mode == "profile invalid" {
						wantCode = "InvalidConfig"
					}
				}
				if mode != "config" {
					assertSyntheticCLILoopbackEndpoint(t)
				}
				var out, stderr bytes.Buffer
				code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
				var result struct {
					Sent, Stage string
					Error       *monitor.QuotaOutcomeError
				}
				if json.Unmarshal(out.Bytes(), &result) != nil || code != wantExit || result.Sent != "no" || result.Stage != "preflight" || result.Error.Code != wantCode {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
			})
		}
	}
}

func TestSyntheticMutationExplicitConsentAndPrecedence(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, mode := range []string{"missing", "JSON zero", "file zero", "null", "flag wins", "negative flag", "query", "empty query", "positive warning", "guard flag wins"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				withCleanEnv(t)
				var requests atomic.Int32
				opts := syntheticCLITestOptions(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1); w.WriteHeader(404) }))
				withTestOptions(t, append(opts, vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))...)
				guard := `"ExpectedQuotaID":"quota-syn-1"`
				if command == "renew-synthetic-quota" {
					guard = `"ExpectedEndTime":"2500-12-01T00:00:00Z"`
				}
				raw := `{` + guard + `}`
				args := []string{"monitor", command, "--region", "hcm-3"}
				wantRequests := int32(0)
				wantExit := 2
				switch mode {
				case "JSON zero", "file zero", "flag wins":
					raw = `{` + guard + `,"MaxPrice":0}`
					wantRequests = 1
					wantExit = 4
				case "positive warning":
					raw = `{` + guard + `,"MaxPrice":1}`
					wantRequests = 1
					wantExit = 4
				case "guard flag wins":
					guardFlag := "--expected-quota-id"
					good := "quota-syn-1"
					badGuard := `"ExpectedQuotaID":"invalid/id"`
					if command == "renew-synthetic-quota" {
						guardFlag = "--expected-end-time"
						good = "2500-12-01T00:00:00Z"
						badGuard = `"ExpectedEndTime":"invalid"`
					}
					raw = `{` + badGuard + `,"MaxPrice":0}`
					args = append(args, guardFlag, good)
					wantRequests = 1
					wantExit = 4
				case "null":
					raw = `{` + guard + `,"MaxPrice":null}`
				case "negative flag":
					raw = `{` + guard + `,"MaxPrice":0}`
					args = append(args, "--max-price", "-1")
				case "query":
					raw = `{` + guard + `,"MaxPrice":0}`
					args = append(args, "--query", "Sent")
				case "empty query":
					raw = `{` + guard + `,"MaxPrice":0}`
					args = append(args, "--query", "")
				}
				if mode == "flag wins" {
					raw = `{` + guard + `,"MaxPrice":-1}`
					args = append(args, "--max-price", "0")
				}
				if mode == "file zero" {
					dest := filepath.Join(t.TempDir(), "input.json")
					if err := os.WriteFile(dest, []byte(raw), 0o600); err != nil {
						t.Fatal(err)
					}
					raw = "file://" + dest
				}
				args = append(args, "--cli-input-json", raw)
				var out, stderr bytes.Buffer
				code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
				if mode == "positive warning" && !strings.Contains(stderr.String(), "pay: true may debit the balance") {
					t.Fatalf("missing warning: %s", &stderr)
				}
				if code != wantExit || requests.Load() != wantRequests {
					t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), &out, &stderr)
				}
			})
		}
	}
}

type syntheticBrokenOutput struct{ calls int }

func (w *syntheticBrokenOutput) Write([]byte) (int, error) { w.calls++; return 0, io.ErrClosedPipe }
func TestSyntheticMutationRenderFailureDoesNotPrintAgain(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		t.Run(command, func(t *testing.T) {
			withCleanEnv(t)
			var sends atomic.Int32
			opts := syntheticCLITestOptions(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v1/synthetic/quota":
					name := "GetSyntheticQuotaInTrash"
					if command == "renew-synthetic-quota" {
						name = "GetSyntheticQuotaRecovered"
					}
					raw := mustSyntheticCLIFixture(t, name)
					if command == "renew-synthetic-quota" {
						raw = bytes.ReplaceAll(raw, []byte("2026-12-01"), []byte("2500-12-01"))
					}
					_, _ = w.Write(raw)
				case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
					_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDetailNullable"))
				case "/billing-api/v2/synthetic/prices/created-price":
					_, _ = io.WriteString(w, `{"optimumPrice":0}`)
				case "/billing-api/v2/trash/synthetic/quota", "/billing-api/v2/synthetic/quota/extend-time":
					sends.Add(1)
				default:
					t.Errorf("unexpected %s", r.URL)
					w.WriteHeader(500)
				}
			}))
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("synthetic-token"))...)
			args := []string{"monitor", command, "--region", "hcm-3", "--max-price", "0", "--no-wait"}
			if command == "recover-synthetic-quota" {
				args = append(args, "--expected-quota-id", "quota-syn-1")
			} else {
				args = append(args, "--expected-end-time", "2500-12-01T05:19:45Z")
			}
			out := new(syntheticBrokenOutput)
			var stderr bytes.Buffer
			code := Main(context.Background(), args, strings.NewReader(""), out, &stderr)
			if code != 1 || out.calls != 1 || sends.Load() != 1 || strings.Contains(stderr.String(), `"Sent":"no"`) {
				t.Fatalf("exit=%d writes=%d sends=%d stderr=%s", code, out.calls, sends.Load(), &stderr)
			}
		})
	}
}

func TestSyntheticMutationCLIRetainedAttemptOutcomes(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, unknown := range []bool{false, true} {
			for _, mode := range []string{"unresolved mismatch", "confirmed mismatch", "confirmed matching"} {
				t.Run(fmt.Sprintf("%s/%t/%s", command, unknown, mode), func(t *testing.T) {
					withCleanEnv(t)
					var sends, reads, quotes atomic.Int32
					ready := make(chan struct{})
					baseline := "2500-12-01T05:19:45Z"
					name := "GetSyntheticQuotaRecovered"
					if command == "recover-synthetic-quota" {
						baseline = "2000-10-25T21:43:00Z"
						name = "GetSyntheticQuotaInTrash"
					}
					var quota map[string]any
					if err := json.Unmarshal(mustSyntheticCLIFixture(t, name), &quota); err != nil {
						t.Fatal(err)
					}
					quota["endTime"] = baseline
					quota["startTime"] = "1999-01-01T00:00:00Z"
					handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch r.URL.Path {
						case "/billing-api/v1/synthetic/quota":
							n := reads.Add(1)
							snapshot := maps.Clone(quota)
							if n > 2 {
								if mode == "unresolved mismatch" {
									close(ready)
									<-r.Context().Done()
									return
								}
								snapshot["status"] = "ACTIVE"
								snapshot["endTime"] = "2600-01-01T00:00:00Z"
							}
							_ = json.NewEncoder(w).Encode(snapshot)
						case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
							_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDetailNullable"))
						case "/billing-api/v2/synthetic/prices/created-price":
							quotes.Add(1)
							_, _ = io.WriteString(w, `{"optimumPrice":0}`)
						case "/billing-api/v2/trash/synthetic/quota", "/billing-api/v2/synthetic/quota/extend-time":
							sends.Add(1)
							_, _ = io.Copy(io.Discard, r.Body)
							if unknown {
								conn, _, err := w.(http.Hijacker).Hijack()
								if err != nil {
									t.Error(err)
									return
								}
								_ = conn.Close()
							}
						default:
							t.Errorf("unexpected %s", r.URL)
							w.WriteHeader(500)
						}
					})
					server := httptest.NewServer(handler)
					t.Cleanup(server.Close)
					withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(server.Client())), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))
					cfg, err := loadConfig(context.Background(), &env{flags: &globalFlags{region: "hcm-3"}}, nil)
					if err != nil {
						t.Fatal(err)
					}
					client := monitor.New(cfg)
					args := []string{"monitor", command, "--max-price", "0", "--no-wait"}
					if command == "recover-synthetic-quota" {
						args = append(args, "--expected-quota-id", "quota-syn-1")
					} else {
						args = append(args, "--expected-end-time", baseline)
					}
					// Keep the public SDK client across service invocations to exercise its
					// ledger. The operation table and product transport remain unchanged.
					invoke := func(ctx context.Context, argv []string) (map[string]any, error) {
						var out, stderr bytes.Buffer
						e := &env{stdin: strings.NewReader(""), stdout: &out, stderr: &stderr, flags: &globalFlags{region: "hcm-3", output: "json"}}
						root := &cobra.Command{Use: "vngcloud", SilenceErrors: true, SilenceUsage: true}
						root.SetOut(&out)
						root.SetErr(&stderr)
						root.AddCommand(Service(e, "monitor", "", func(vngcloud.Config) *monitor.Client { return client }, monitorOps...))
						state := new(syntheticCLIOutcome)
						root.SetArgs(argv)
						cmd, callErr := root.ExecuteContextC(context.WithValue(ctx, syntheticOutcomeContextKey{}, state))
						if callErr != nil {
							finishSyntheticCLI(&out, &stderr, cmd, syntheticCommandOperation(cmd), state, callErr)
						}
						var result map[string]any
						if json.Unmarshal(out.Bytes(), &result) != nil || len(result) != 8 {
							t.Errorf("stdout=%s stderr=%s", &out, &stderr)
						}
						if callErr != nil {
							var envelope struct {
								Error struct {
									Code, Operation string
									Status          int
								}
							}
							if json.Unmarshal(stderr.Bytes(), &envelope) != nil {
								t.Errorf("stderr=%s", &stderr)
							}
							meta := result["Error"].(map[string]any)
							if envelope.Error.Code != meta["Code"] || envelope.Error.Operation != meta["Operation"] || float64(envelope.Error.Status) != meta["Status"] {
								t.Errorf("stdout=%s stderr=%s", &out, &stderr)
							}
						}
						return result, callErr
					}
					first, err := invoke(context.Background(), args)
					if errors.Is(err, dns.ErrNotSettled) != unknown {
						t.Fatalf("first=%v err=%v", first, err)
					}
					secondArgs := slices.Clone(args)
					if mode != "confirmed matching" {
						secondArgs[len(secondArgs)-1] = "2500-12-02T00:00:00Z"
						if command == "recover-synthetic-quota" {
							secondArgs[len(secondArgs)-1] = "other"
						}
					}
					var result map[string]any
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					if mode == "unresolved mismatch" {
						done := make(chan struct{})
						go func() { result, err = invoke(ctx, secondArgs); close(done) }()
						<-ready
						cancel()
						<-done
					} else {
						result, err = invoke(ctx, secondArgs)
					}
					retainedSent := "yes"
					if unknown {
						retainedSent = "unknown"
					}
					wantSent, wantStage, wantCode, wantExit := retainedSent, "confirmed", "", 0
					switch mode {
					case "unresolved mismatch":
						wantStage, wantCode, wantExit = "unconfirmed", "NotSettled", 1
					case "confirmed mismatch":
						wantSent, wantStage, wantCode, wantExit = "no", "preflight", "QuotaChanged", 1
					}
					if result["Sent"] != wantSent || result["Stage"] != wantStage || exitCode(err) != wantExit || sends.Load() != 1 || quotes.Load() != 1 {
						t.Fatalf("result=%v err=%v sends=%d quotes=%d", result, err, sends.Load(), quotes.Load())
					}
					if wantCode != "" && result["Error"].(map[string]any)["Code"] != wantCode {
						t.Fatalf("result=%v", result)
					}
					if mode != "confirmed matching" && !errors.Is(err, monitor.ErrQuotaChanged) {
						t.Fatalf("err=%v", err)
					}
				})
			}
		}
	}
}

func assertSyntheticCLILoopbackEndpoint(t *testing.T) {
	t.Helper()
	opts := append(slices.Clone(testOptions), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRegion("hcm-3"))
	cfg, err := vngcloud.LoadConfig(context.Background(), opts...)
	if err != nil {
		t.Fatal(err)
	}
	raw := core.ClientOf(cfg).RouteURL(routes.Route{Product: routes.ProductMonitor, Parts: []string{"billing-api", "v1", "synthetic", "quota"}})
	endpoint, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Hostname() != "127.0.0.1" && endpoint.Hostname() != "::1" && endpoint.Hostname() != "localhost" {
		t.Fatal("test has no loopback Monitor endpoint; no request was made")
	}
}

func TestSyntheticMutationMalformedTimestampOutcome(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		t.Run(command, func(t *testing.T) {
			withCleanEnv(t)
			var sends atomic.Int32
			baseline := "2500-12-01T05:19:45Z"
			name := "GetSyntheticQuotaRecovered"
			if command == "recover-synthetic-quota" {
				baseline = "2000-10-25T21:43:00Z"
				name = "GetSyntheticQuotaInTrash"
			}
			var q map[string]any
			if err := json.Unmarshal(mustSyntheticCLIFixture(t, name), &q); err != nil {
				t.Fatal(err)
			}
			q["endTime"] = baseline
			opts := syntheticCLITestOptions(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v1/synthetic/quota":
					snapshot := make(map[string]any)
					for k, v := range q {
						snapshot[k] = v
					}
					if sends.Load() > 0 {
						snapshot["status"] = "ACTIVE"
						snapshot["endTime"] = "2501-01-01T00:00:00+24:00"
					}
					_ = json.NewEncoder(w).Encode(snapshot)
				case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
					_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDetailNullable"))
				case "/billing-api/v2/synthetic/prices/created-price":
					_, _ = io.WriteString(w, `{"optimumPrice":0}`)
				case "/billing-api/v2/trash/synthetic/quota", "/billing-api/v2/synthetic/quota/extend-time":
					sends.Add(1)
				default:
					t.Errorf("unexpected %s", r.URL)
					w.WriteHeader(500)
				}
			}))
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))...)
			args := []string{"monitor", command, "--region", "hcm-3", "--max-price", "0", "--output", "json"}
			if command == "recover-synthetic-quota" {
				args = append(args, "--expected-quota-id", "quota-syn-1")
			} else {
				args = append(args, "--expected-end-time", baseline)
			}
			var out, stderr bytes.Buffer
			code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)

			var record map[string]any
			if json.Unmarshal(out.Bytes(), &record) != nil || len(record) != 8 {
				t.Fatalf("lost outcome: stdout=%q stderr=%s", &out, &stderr)
			}
			if code != 1 || sends.Load() != 1 || record["Sent"] != "yes" || record["Stage"] != "unconfirmed" || record["Quota"] != nil || record["Error"].(map[string]any)["Code"] != "NotSettled" {
				t.Fatalf("exit=%d sends=%d stdout=%s stderr=%s", code, sends.Load(), &out, &stderr)
			}
		})
	}
}

func TestSyntheticMutationEncodingFailureFallback(t *testing.T) {
	bad, err := time.Parse(time.RFC3339, "2501-01-01T00:00:00+24:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota"} {
		for _, sent := range []string{"no", "yes", "unknown"} {
			t.Run(command+"/"+sent, func(t *testing.T) {
				var output any
				outcome := monitor.QuotaMutationOutcome{Sent: sent, Stage: "confirmed"}
				q := &monitor.Quota{EndTime: bad}
				if command == "recover-synthetic-quota" {
					output = &monitor.RecoverSyntheticQuotaOutput{QuotaMutationOutcome: outcome, Quota: q, Recovered: true}
				} else {
					output = &monitor.RenewSyntheticQuotaOutput{QuotaMutationOutcome: outcome, Quota: q, Renewed: true}
				}
				state := &syntheticCLIOutcome{output: output}
				cmd := &cobra.Command{Use: command}
				cmd.Flags().String("output", "json", "")
				var stdout, stderr bytes.Buffer
				finishSyntheticCLI(&stdout, &stderr, cmd, syntheticMutationOperation("monitor", command), state, errors.New("output encoding failed"))
				var record map[string]any
				stage, code := "unconfirmed", "NotSettled"
				if sent == "no" {
					stage, code = "preflight", "RequestFailed"
				}
				if json.Unmarshal(stdout.Bytes(), &record) != nil || len(record) != 8 || record["Sent"] != sent || record["Stage"] != stage || record["Quota"] != nil || record["Error"] == nil || record["Error"].(map[string]any)["Code"] != code {
					t.Fatalf("stdout=%q stderr=%s", &stdout, &stderr)
				}
			})
		}
	}
}

func syntheticCLITestOptions(t *testing.T, handler http.Handler) []vngcloud.LoadOption {
	t.Helper()
	return newFakeServer(t, handler)
}
func syntheticCLITestHTTPClient(base *http.Client) *http.Client {
	client := *base
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	if canonical, ok := transport.(*http.Transport); ok {
		guarded := canonical.Clone()
		dial := canonical.DialContext
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		guarded.Proxy = nil
		guarded.DialTLS = nil //nolint:staticcheck // Legacy TLS hooks must not bypass loopback guards.
		guarded.DialTLSContext = nil
		guarded.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			if host != "localhost" && !net.ParseIP(host).IsLoopback() {
				return nil, errors.New("test transport refuses non-loopback host")
			}
			return dial(ctx, network, address)
		}
		client.Transport = guarded
	} else {
		client.Transport = refusingTransport{base: transport}
	}
	return &client
}

func TestSyntheticMutationOutputAttemptTracking(t *testing.T) {
	bad, err := time.Parse(time.RFC3339, "2501-01-01T00:00:00+24:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "table", "text"} {
		var stdout bytes.Buffer
		writer := &syntheticOutputWriter{Writer: &stdout}
		err = renderOutput(writer, format, "", &monitor.RenewSyntheticQuotaOutput{PreviousEndTime: &bad}, true)
		if err == nil || writer.attempted || stdout.Len() != 0 {
			t.Fatalf("format=%s attempted=%t stdout=%s err=%v", format, writer.attempted, &stdout, err)
		}
		broken := new(syntheticBrokenOutput)
		writer = &syntheticOutputWriter{Writer: broken}
		err = renderOutput(writer, format, "", &monitor.RenewSyntheticQuotaOutput{}, true)
		if err == nil || !writer.attempted || broken.calls != 1 {
			t.Fatalf("format=%s attempted=%t calls=%d err=%v", format, writer.attempted, broken.calls, err)
		}
	}
}

func TestSyntheticMutationTestClientRejectsNonLoopback(t *testing.T) {
	for _, canonical := range []bool{false, true} {
		name := "custom"
		if canonical {
			name = "canonical"
		}
		t.Run(name, func(t *testing.T) {
			calls := 0
			var transport http.RoundTripper = saCLITransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("test transport trap") })
			if canonical {
				transport = &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
					calls++
					return nil, errors.New("test dial trap")
				}}
			}
			client := syntheticCLITestHTTPClient(&http.Client{Transport: transport})
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://non-loopback.invalid/", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if response != nil {
				_ = response.Body.Close()
			}
			if err == nil || calls != 0 {
				t.Fatalf("non-loopback request reached base transport: calls=%d err=%v", calls, err)
			}
		})
	}
}
