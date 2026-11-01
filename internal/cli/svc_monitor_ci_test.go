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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota"} {
		for _, flags := range [][]string{nil, {"--max-price", "0", "--query", "Sent"}, {"--output", "invalid"}, {"--query", "Sent"}, {"--cli-input-json", "{"}, {"--max-price", "oops"}, {"--read-only"}} {
			t.Run(command+strings.Join(flags, " "), func(t *testing.T) {
				withCleanEnv(t)
				withTestOptions(t, syntheticCLITestOptions(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected pre-SDK request") }))...)
				var out, stderr bytes.Buffer
				code := Main(context.Background(), append([]string{"monitor", command}, flags...), strings.NewReader(""), &out, &stderr)
				var value map[string]any
				fields := 8
				if command == "ensure-synthetic-quota" {
					fields = 10
				}
				if json.Unmarshal(out.Bytes(), &value) != nil || len(value) != fields || value["Sent"] != "no" || value["Stage"] != "preflight" || code != 2 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
			})
		}
	}
}

func TestSyntheticMutationCLIRealTransport(t *testing.T) {
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota/renew", "ensure-synthetic-quota/recover"} {
		command, action, ensure := strings.Cut(command, "/")
		recovery := command == "recover-synthetic-quota" || action == "recover"
		for _, tc := range []struct {
			name                         string
			status                       int
			body, stage, code, sent      string
			exit                         int
			noWait, confirm, stale, skip bool
			price                        string
			listPrice                    bool
			waitStatus                   int
			quoteStatus                  int
			final                        string
		}{
			{name: "confirmed", status: 200, stage: "confirmed", sent: "yes", confirm: true},
			{name: "accepted", status: 200, stage: "accepted", sent: "yes", noWait: true},
			{name: "unpriced", status: 200, stage: "preflight", sent: "no", code: "Unpriced", exit: 1, listPrice: true},
			{name: "price", status: 200, stage: "preflight", sent: "no", code: "PriceAboveMax", exit: 1, price: `{"optimumPrice":1}`},
			{name: "stale", status: 200, stage: "preflight", sent: "no", code: "QuotaChanged", exit: 1, stale: true},
			{name: "bad request", status: 400, stage: "refused", sent: "yes", code: "BadRequest", exit: 1},
			{name: "unauthorized", status: 401, stage: "refused", sent: "yes", code: "Unauthorized", exit: 3},
			{name: "quote forbidden", quoteStatus: 403, stage: "preflight", sent: "no", code: "Forbidden", exit: 1},
			{name: "final state", final: "state", stage: "preflight", sent: "no", code: "QuotaChanged", exit: 1},
			{name: "replacement same expiry", final: "replacement", stage: "preflight", sent: "no", code: "QuotaChanged", exit: 1},
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
			if recovery && tc.skip {
				continue
			}
			t.Run(command+"/"+tc.name, func(t *testing.T) {
				withCleanEnv(t)
				var sends, quotes, reads atomic.Int32
				baseline := "2500-12-01T05:19:45Z"
				status := "ACTIVE"
				if recovery {
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
						if reads.Add(1) == 2 {
							if tc.final == "replacement" {
								snapshot["id"] = "quota-syn-2"
							}
							if tc.final == "state" {
								snapshot["status"] = map[bool]string{true: "ACTIVE", false: "IN-TRASH"}[recovery]
							}
						}
						if sends.Load() > 0 && tc.waitStatus != 0 {
							w.WriteHeader(tc.waitStatus)
							return
						}
						if sends.Load() > 0 && tc.confirm {
							snapshot["status"] = "ACTIVE"
							snapshot["endTime"] = "2501-01-01T05:19:45Z"
						}
						_ = json.NewEncoder(w).Encode(snapshot)
					case "/billing-api/v2/synthetic/quota-syn-1/quota-detail", "/billing-api/v2/synthetic/quota-syn-2/quota-detail":
						b, e := os.ReadFile("../../testdata/monitor/GetSyntheticQuotaDetailNullable.json")
						if e != nil {
							t.Error(e)
						}
						if tc.listPrice {
							b = bytes.ReplaceAll(b, []byte(`"price": 0.0`), []byte(`"price": 10`))
						}
						if strings.Contains(r.URL.Path, "quota-syn-2") {
							b = bytes.ReplaceAll(b, []byte("quota-syn-1"), []byte("quota-syn-2"))
						}
						_, _ = w.Write(b)
					case "/billing-api/v2/synthetic/prices/created-price":
						quotes.Add(1)
						if tc.quoteStatus != 0 {
							w.WriteHeader(tc.quoteStatus)
							return
						}
						price := tc.price
						if price == "" {
							price = `{"optimumPrice":0}`
						}
						_, _ = io.WriteString(w, price)
					case "/billing-api/v2/trash/synthetic/quota", "/billing-api/v2/synthetic/quota/extend-time":
						if strings.Contains(r.URL.Path, "trash") != recovery {
							t.Errorf("wrong action route %s", r.URL.Path)
						}
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
				if recovery && !ensure {
					id := "quota-syn-1"
					if tc.stale {
						id = "other"
					}
					args = append(args, "--expected-quota-id", id)
				} else if !ensure {
					end := baseline
					if tc.stale {
						end = "2500-12-02T05:19:45Z"
					}
					args = append(args, "--expected-end-time", end)
				}
				if ensure {
					if recovery {
						id := "quota-syn-1"
						if tc.stale {
							id = "other"
						}
						args = append(args, "--expected-quota-id", id)
					} else {
						args = append(args, "--max-days-left", "0")
						if tc.stale {
							args = append(args, "--expected-quota-id", "other")
						}
					}
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
				fields := 8
				if ensure {
					fields = 10
				}
				if json.Unmarshal(out.Bytes(), &value) != nil || len(value) != fields || code != tc.exit || value["Sent"] != tc.sent || value["Stage"] != tc.stage {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
				}
				confirmation := "Renewed"
				if recovery {
					confirmation = "Recovered"
				}
				if ensure {
					wantAction := action
					if tc.stale || tc.skip {
						wantAction = "none"
					}
					if value["Action"] != wantAction || value[map[bool]string{true: "Renewed", false: "Recovered"}[recovery]] != false {
						t.Fatalf("outcome=%s", &out)
					}
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
					expectedStatus := tc.quoteStatus
					if tc.sent == "yes" {
						expectedStatus = tc.status
					}
					expectedOperation := "monitor.RenewSyntheticQuota"
					if recovery {
						expectedOperation = "monitor.RecoverSyntheticQuota"
					}
					if ensure {
						expectedOperation = "monitor.EnsureSyntheticQuota"
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
		{"ensure-synthetic-quota", &monitor.EnsureSyntheticQuotaOutput{QuotaMutationOutcome: monitor.QuotaMutationOutcome{Sent: "yes", Stage: "refused", Error: &monitor.QuotaOutcomeError{Code: "Forbidden", Operation: "monitor.EnsureSyntheticQuota", Status: 403}}, Action: "recover", PreviousEndTime: &end}},
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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota/renew", "ensure-synthetic-quota/recover"} {
		command, action, ensure := strings.Cut(command, "/")
		recovery := command == "recover-synthetic-quota" || action == "recover"
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
				if recovery {
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
				if recovery {
					args = append(args, "--expected-quota-id", "quota-syn-1")
				} else if command == "renew-synthetic-quota" {
					args = append(args, "--expected-end-time", expiry)
				}
				if ensure {
					args = append(args, "--max-days-left", "0")
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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "create-synthetic-quota", "ensure-synthetic-quota"} {
		for _, mode := range []string{"credentials", "config", "flag read only", "environment read only", "profile read only", "profile invalid"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				home := withCleanEnv(t)
				opts := syntheticCLITestOptions(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected request during refusal") }))
				withTestOptions(t, opts...)
				assertSyntheticCLILoopbackEndpoint(t)
				args := []string{"monitor", command, "--region", "hcm-3", "--max-price", "0"}
				switch command {
				case "create-synthetic-quota":
					args = syntheticLifecycleCLIArgs(command)
				case "recover-synthetic-quota":
					args = append(args, "--expected-quota-id", "quota-syn-1")
				case "renew-synthetic-quota":
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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota"} {
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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota"} {
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
			switch command {
			case "recover-synthetic-quota":
				args = append(args, "--expected-quota-id", "quota-syn-1")
			case "renew-synthetic-quota":
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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota/renew", "ensure-synthetic-quota/recover"} {
		command, action, ensure := strings.Cut(command, "/")
		recovery := command == "recover-synthetic-quota" || action == "recover"
		for _, unknown := range []bool{false, true} {
			for _, mode := range []string{"unresolved mismatch", "confirmed mismatch", "confirmed matching"} {
				t.Run(fmt.Sprintf("%s/%t/%s", command, unknown, mode), func(t *testing.T) {
					withCleanEnv(t)
					var sends, reads, quotes atomic.Int32
					ready := make(chan struct{})
					baseline := "2500-12-01T05:19:45Z"
					name := "GetSyntheticQuotaRecovered"
					if recovery {
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
					switch {
					case ensure:
						args = append(args, "--max-days-left", "0", "--expected-quota-id", "quota-syn-1")
					case recovery:
						args = append(args, "--expected-quota-id", "quota-syn-1")
					default:
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
						fields := 8
						if ensure {
							fields = 10
						}
						if json.Unmarshal(out.Bytes(), &result) != nil || len(result) != fields {
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
						if recovery || ensure {
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
					if ensure {
						wantAction := action
						if mode == "confirmed mismatch" {
							wantAction = "none"
						}
						if result["Action"] != wantAction {
							t.Fatalf("result=%v", result)
						}
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

func TestSyntheticCreateDeletePreSDK(t *testing.T) {
	for _, command := range []string{"create-synthetic-quota", "delete-synthetic-quota"} {
		for _, format := range []string{"json", "table", "text"} {
			for _, flags := range [][]string{nil, {"--read-only"}, {"--query", "Stage"}, {"--cli-input-json", "{"}} {
				t.Run(command+format+strings.Join(flags, ""), func(t *testing.T) {
					withCleanEnv(t)
					withTestOptions(t, syntheticCLITestOptions(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected pre-SDK request") }))...)
					var out, stderr bytes.Buffer
					args := append([]string{"monitor", command, "--output", format}, flags...)
					code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
					if code != 2 || out.Len() == 0 {
						t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &stderr)
					}
					if command == "delete-synthetic-quota" {
						if !strings.Contains(out.String(), syntheticDeleteEffect) {
							t.Fatalf("warning missing: %s", &out)
						}
					} else if format == "json" {
						var v map[string]any
						if json.Unmarshal(out.Bytes(), &v) != nil || len(v) != 8 || v["Sent"] != "no" || v["Created"] != false || v["SentAt"] != nil {
							t.Fatalf("out=%s", &out)
						}
					}
				})
			}
		}
	}
}

func TestSyntheticCreateDeleteCLITransport(t *testing.T) {
	for _, command := range []string{"create-synthetic-quota", "quote-create-synthetic-quota", "delete-synthetic-quota"} {
		for _, status := range []int{200, 201, 204, 301, 302, 303, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504, -1} {
			if command == "quote-create-synthetic-quota" && status != 200 {
				continue
			}
			t.Run(fmt.Sprint(command, status), func(t *testing.T) {
				withCleanEnv(t)
				var sends, quotes atomic.Int32
				fixture := func(name string) string {
					b, e := os.ReadFile("../../testdata/monitor/" + name + ".json")
					if e != nil {
						t.Fatal(e)
					}
					return string(b)
				}
				quota := strings.ReplaceAll(fixture("GetSyntheticQuotaCreated"), "2026-12-01", "2501-12-01")
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodDelete && r.URL.Path == "/billing-api/v1/synthetic/quota", r.Method == http.MethodPost && r.URL.Path == "/billing-api/v2/synthetic/quota":
						sends.Add(1)
						body, _ := io.ReadAll(r.Body)
						if command == "create-synthetic-quota" {
							var v map[string]any
							if json.Unmarshal(body, &v) != nil || len(v) != 6 || v["quantity"] != float64(10) || v["pay"] != true || v["monthPeriod"] != float64(1) {
								t.Errorf("body=%s", body)
							}
							if buy, ok := v["buyWith"].(map[string]any); !ok || len(buy) != 0 {
								t.Errorf("body=%s", body)
							}
						} else if len(body) != 0 {
							t.Errorf("delete body=%s", body)
						}
						if status < 0 {
							conn, _, e := w.(http.Hijacker).Hijack()
							if e != nil {
								t.Error(e)
								return
							}
							_ = conn.Close()
							return
						}
						w.Header().Set("Location", "/must-not-follow")
						w.WriteHeader(status)
						// Success bodies are constructed from metadata, not raw fixtures.
						if status != 200 && status != 204 {
							_, _ = io.WriteString(w, `{"message":"peer-canary"}`)
						}
					case r.URL.Path == "/billing-api/v1/synthetic/quota":
						if command == "delete-synthetic-quota" && sends.Load() == 0 {
							_, _ = io.WriteString(w, quota)
							return
						}
						w.WriteHeader(404)
						_, _ = io.WriteString(w, fixture("GetSyntheticQuotaDeleted"))
					case r.URL.Path == "/billing-api/v2/synthetic/quota-syn-2/quota-detail":
						_, _ = io.WriteString(w, fixture("GetSyntheticQuotaCreatedDetail"))
					case r.URL.Path == "/billing-api/v2/synthetic/quota-class":
						_, _ = io.WriteString(w, fixture("ListSyntheticQuotaClasses"))
					case strings.HasSuffix(r.URL.Path, "/packages") && strings.Contains(r.URL.Path, "/quota-class/"):
						_, _ = io.WriteString(w, fixture("ListSyntheticClassPackages"))
					case r.URL.Path == "/billing-api/v1/settings":
						_, _ = io.WriteString(w, fixture("GetQuotaSettings"))
					case r.URL.Path == "/billing-api/v2/synthetic/prices/created-price":
						quotes.Add(1)
						var v map[string]any
						_ = json.NewDecoder(r.Body).Decode(&v)
						if len(v) != 4 || v["quantity"] != float64(10) || v["resourceId"] != nil || v["monthPeriod"] != nil {
							t.Errorf("quote=%v", v)
						}
						_, _ = io.WriteString(w, fixture("QuoteCreateSyntheticQuota"))
					default:
						t.Errorf("unexpected %s %s", r.Method, r.URL)
						w.WriteHeader(400)
					}
				})
				server := httptest.NewServer(handler)
				t.Cleanup(server.Close)
				withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(server.Client())), vngcloud.WithStaticToken("synthetic-bearer-canary"), vngcloud.WithRetry(3, time.Millisecond))
				args := []string{"monitor", command, "--region", "hcm-3", "--output", "json"}
				if command == "delete-synthetic-quota" {
					args = append(args, "--yes", "--expected-quota-id", "quota-syn-2")
				} else {
					args = append(args, "--class-id", "e574cf4a-460b-48d8-a794-80367289a6c8", "--package-id", "32b3743d-94b8-49ab-984d-bd7ec99b46f6", "--quantity", "10")
					if command == "create-synthetic-quota" {
						args = append(args, "--max-price", "0", "--no-wait")
					}
				}
				var out, stderr bytes.Buffer
				exit := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
				var value map[string]any
				if json.Unmarshal(out.Bytes(), &value) != nil {
					t.Fatalf("exit=%d out=%s err=%s", exit, &out, &stderr)
				}
				if strings.Contains(out.String()+stderr.String(), "peer-canary") || strings.Contains(out.String()+stderr.String(), "synthetic-bearer-canary") {
					t.Fatal("unsafe output")
				}
				if command == "quote-create-synthetic-quota" {
					if exit != 0 || sends.Load() != 0 || quotes.Load() != 1 {
						t.Fatalf("exit=%d sends=%d quotes=%d", exit, sends.Load(), quotes.Load())
					}
					return
				}
				if sends.Load() != 1 {
					t.Fatalf("sends=%d", sends.Load())
				}
				success := (command == "create-synthetic-quota" && status == 200) || (command == "delete-synthetic-quota" && status == 204)
				wantExit := 1
				switch {
				case success:
					wantExit = 0
				case status == 401:
					wantExit = 3
				case status == 404:
					wantExit = 4
				}
				if exit != wantExit {
					t.Fatalf("exit=%d want=%d out=%s err=%s", exit, wantExit, &out, &stderr)
				}
				if command == "delete-synthetic-quota" {
					stage := "sent"
					if success {
						stage = "settled"
					} else if status >= 400 && status < 500 {
						stage = "prepared"
					}
					if len(value) != 4 || value["Effect"] != syntheticDeleteEffect || value["Stage"] != stage || value["Purged"] != false {
						t.Fatalf("out=%s", &out)
					}
					if quotes.Load() != 0 {
						t.Fatal("delete quoted")
					}
				} else {
					sent, stage := "yes", "unconfirmed"
					if status < 0 {
						sent = "unknown"
					}
					if success {
						stage = "accepted"
					} else if status >= 400 && status < 500 {
						stage = "refused"
					}
					if len(value) != 8 || value["Sent"] != sent || value["Stage"] != stage || value["Created"] != false || value["SentAt"] == nil {
						t.Fatalf("out=%s", &out)
					}
					if !success {
						meta := value["Error"].(map[string]any)
						var env struct {
							Error struct {
								Code      string
								Status    int
								Operation string
							}
						}
						_ = json.Unmarshal(stderr.Bytes(), &env)
						if meta["Code"] != env.Error.Code || meta["Status"] != float64(env.Error.Status) || env.Error.Operation != "monitor.CreateSyntheticQuota" {
							t.Fatalf("out=%s stderr=%s", &out, &stderr)
						}
					}
				}
			})
		}
	}
}

func TestSyntheticLifecycleGoldens(t *testing.T) {
	sentAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		out  any
	}{
		{"create-synthetic-quota", &monitor.CreateSyntheticQuotaOutput{QuotaMutationOutcome: monitor.QuotaMutationOutcome{Sent: "yes", Stage: "accepted"}, SentAt: &sentAt}},
		{"create-synthetic-quota-preflight", &monitor.CreateSyntheticQuotaOutput{QuotaMutationOutcome: monitor.QuotaMutationOutcome{Sent: "no", Stage: "preflight", Error: &monitor.QuotaOutcomeError{Code: "InvalidUsage", Operation: "monitor.CreateSyntheticQuota"}}}},
		{"quote-create-synthetic-quota", &monitor.QuotaQuote{PackageID: "unit-package-1", Quantity: 10, MonthPeriod: 1, Currency: "VND"}},
		{"delete-synthetic-quota", &monitor.DeleteSyntheticQuotaOutput{Stage: "settled"}},
		{"delete-synthetic-quota-preflight", &monitor.DeleteSyntheticQuotaOutput{}},
	}
	for _, tc := range cases {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(tc.name+format, func(t *testing.T) {
				var out bytes.Buffer
				if err := renderSyntheticCommand(&out, format, tc.out); err != nil {
					t.Fatal(err)
				}
				path := goldenPath("monitor-" + tc.name + "." + format + ".golden")
				if *updateGolden {
					if err := os.WriteFile(path, out.Bytes(), 0644); err != nil {
						t.Fatal(err)
					}
				} else {
					want, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(want, out.Bytes()) {
						t.Fatalf("got=%s want=%s", &out, want)
					}
				}
			})
		}
	}
}
func TestSyntheticDeleteWarningAndQuoteFlags(t *testing.T) {
	withCleanEnv(t)
	root := newRootCmd(strings.NewReader(""), io.Discard, io.Discard)
	cmd, _, err := root.Find([]string{"monitor", "delete-synthetic-quota"})
	if err != nil || !strings.Contains(cmd.Short, syntheticDeleteEffect) {
		t.Fatalf("cmd=%v err=%v", cmd, err)
	}
	for _, command := range []string{"quote-create-synthetic-quota"} {
		cmd, _, err = root.Find([]string{"monitor", command})
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"max-price", "no-wait"} {
			if flag := cmd.Flags().Lookup(name); flag != nil && !flag.Hidden {
				t.Fatalf("quote exposes %s", name)
			}
		}
	}
	body, err := os.ReadFile("../../docs/wiki/Monitor-Synthetic-Quota.md")
	if err != nil || !bytes.Contains(body, []byte("permanently deletes every synthetic check")) {
		t.Fatalf("wiki warning missing: %v", err)
	}
	if !strings.Contains(docOpNotes["monitor delete-synthetic-quota"], syntheticDeleteEffect) {
		t.Fatal("generated source warning missing")
	}
}

func syntheticLifecycleCLIArgs(command string) []string {
	args := []string{"monitor", command, "--region", "hcm-3", "--output", "json"}
	if command == "delete-synthetic-quota" {
		return append(args, "--yes", "--expected-quota-id", "quota-syn-2")
	}
	return append(args, "--class-id", "e574cf4a-460b-48d8-a794-80367289a6c8", "--package-id", "32b3743d-94b8-49ab-984d-bd7ec99b46f6", "--quantity", "10", "--max-price", "0")
}
func TestSyntheticLifecycleCLISendFailures(t *testing.T) {
	for _, command := range []string{"create-synthetic-quota", "delete-synthetic-quota"} {
		for _, mode := range []string{"dial", "cancel before", "cancel after", "timeout"} {
			t.Run(command+mode, func(t *testing.T) {
				withCleanEnv(t)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var sends, dials, finalReads atomic.Int32
				lastDial := int32(10)
				if command == "delete-synthetic-quota" {
					lastDial = 5
				}
				handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodDelete || r.Method == http.MethodPost && r.URL.Path == "/billing-api/v2/synthetic/quota":
						sends.Add(1)
						_, _ = io.Copy(io.Discard, r.Body)
						if mode == "cancel after" {
							cancel()
						}
						<-r.Context().Done()
					case r.URL.Path == "/billing-api/v1/synthetic/quota":
						if command == "create-synthetic-quota" {
							w.WriteHeader(404)
							_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDeleted"))
						} else {
							_, _ = io.WriteString(w, strings.ReplaceAll(string(mustSyntheticCLIFixture(t, "GetSyntheticQuotaCreated")), "2026-12-01", "2500-12-01"))
						}
					case strings.HasSuffix(r.URL.Path, "/quota-detail"):
						_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaCreatedDetail"))
						if finalReads.Add(1) == 2 && mode == "cancel before" {
							cancel()
						}
					case r.URL.Path == "/billing-api/v2/synthetic/quota-class":
						_, _ = w.Write(mustSyntheticCLIFixture(t, "ListSyntheticQuotaClasses"))
					case strings.HasSuffix(r.URL.Path, "/packages"):
						_, _ = w.Write(mustSyntheticCLIFixture(t, "ListSyntheticClassPackages"))
					case r.URL.Path == "/billing-api/v1/settings":
						_, _ = w.Write(mustSyntheticCLIFixture(t, "GetQuotaSettings"))
						if finalReads.Add(1) == 2 && mode == "cancel before" {
							cancel()
						}
					case r.URL.Path == "/billing-api/v2/synthetic/prices/created-price":
						_, _ = w.Write(mustSyntheticCLIFixture(t, "QuoteCreateSyntheticQuota"))
					default:
						t.Errorf("unexpected %s", r.URL)
						w.WriteHeader(400)
					}
				})
				server := httptest.NewServer(handler)
				t.Cleanup(server.Close)
				dialer := new(net.Dialer)
				rt := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
					if n := dials.Add(1); mode == "dial" && n == lastDial {
						return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
					}
					return dialer.DialContext(ctx, network, address)
				}}
				if mode == "timeout" {
					rt.ResponseHeaderTimeout = time.Second
				}
				t.Cleanup(rt.CloseIdleConnections)
				withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(&http.Client{Transport: rt})), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))
				var out, stderr bytes.Buffer
				exit := Main(ctx, syntheticLifecycleCLIArgs(command), strings.NewReader(""), &out, &stderr)
				var v map[string]any
				if json.Unmarshal(out.Bytes(), &v) != nil || exit != 1 {
					t.Fatalf("exit=%d out=%s stderr=%s", exit, &out, &stderr)
				}
				before := mode == "dial" || mode == "cancel before"
				count := int32(1)
				if before {
					count = 0
				}
				if sends.Load() != count {
					t.Fatalf("sends=%d want=%d", sends.Load(), count)
				}
				if mode == "dial" && dials.Load() != lastDial {
					t.Fatalf("dials=%d want=%d", dials.Load(), lastDial)
				}
				if command == "create-synthetic-quota" {
					sent, stage, code := "unknown", "unconfirmed", "NotSettled"
					if before {
						sent, stage, code = "no", "preflight", "RequestFailed"
					}
					meta := v["Error"].(map[string]any)
					if v["Sent"] != sent || v["Stage"] != stage || meta["Code"] != code {
						t.Fatalf("out=%s", &out)
					}
				} else {
					stage := "sent"
					if before {
						stage = "prepared"
					}
					if v["Stage"] != stage || v["Effect"] != syntheticDeleteEffect {
						t.Fatalf("out=%s", &out)
					}
				}
			})
		}
	}
}

func TestSyntheticCreateCLIReadbackOutcomes(t *testing.T) {
	for _, mode := range []string{"confirmed", "malformed", "payment", "read forbidden", "price", "unpriced", "changed", "nonencodable"} {
		t.Run(mode, func(t *testing.T) {
			withCleanEnv(t)
			var sends, quotes, reads atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v1/synthetic/quota":
					n := reads.Add(1)
					if sends.Load() > 0 && mode == "read forbidden" {
						w.WriteHeader(403)
						return
					}
					if sends.Load() > 0 && (mode == "confirmed" || mode == "nonencodable") || mode == "changed" && n > 1 {
						body := string(mustSyntheticCLIFixture(t, "GetSyntheticQuotaCreated"))
						body = strings.ReplaceAll(body, "2026-11-01", "2500-11-01")
						body = strings.ReplaceAll(body, "2026-12-01", "2500-12-01")
						if mode == "nonencodable" {
							var q map[string]any
							if e := json.Unmarshal([]byte(body), &q); e != nil {
								t.Error(e)
							}
							q["createdAt"] = "2501-01-01T00:00:00+24:00"
							_ = json.NewEncoder(w).Encode(q)
						} else {
							_, _ = io.WriteString(w, body)
						}
						return
					}
					w.WriteHeader(404)
					_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDeleted"))
				case "/billing-api/v2/synthetic/quota-syn-2/quota-detail":
					_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaCreatedDetail"))
				case "/billing-api/v2/synthetic/quota-class":
					_, _ = w.Write(mustSyntheticCLIFixture(t, "ListSyntheticQuotaClasses"))
				case "/billing-api/v2/synthetic/quota-class/e574cf4a-460b-48d8-a794-80367289a6c8/packages":
					body := string(mustSyntheticCLIFixture(t, "ListSyntheticClassPackages"))
					if mode == "unpriced" {
						body = strings.ReplaceAll(body, `"price": 0.0`, `"price": 1`)
					}
					_, _ = io.WriteString(w, body)
				case "/billing-api/v1/settings":
					_, _ = w.Write(mustSyntheticCLIFixture(t, "GetQuotaSettings"))
				case "/billing-api/v2/synthetic/prices/created-price":
					quotes.Add(1)
					if mode == "price" {
						_, _ = io.WriteString(w, `{"optimumPrice":1}`)
					} else {
						_, _ = w.Write(mustSyntheticCLIFixture(t, "QuoteCreateSyntheticQuota"))
					}
				case "/billing-api/v2/synthetic/quota":
					sends.Add(1)
					switch mode {
					case "malformed":
						_, _ = io.WriteString(w, "{")
					case "payment":
						_, _ = io.WriteString(w, `{"paymentUrl":"https://peer-canary.invalid/pay"}`)
					}
				default:
					t.Errorf("unexpected route %s", r.URL)
					w.WriteHeader(400)
				}
			})
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(server.Client())), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))
			var out, stderr bytes.Buffer
			exit := Main(context.Background(), syntheticLifecycleCLIArgs("create-synthetic-quota"), strings.NewReader(""), &out, &stderr)
			var v struct {
				Sent, Stage             string
				Error                   *monitor.QuotaOutcomeError
				Quota                   *monitor.Quota
				Created, PaymentPending bool
			}
			if json.Unmarshal(out.Bytes(), &v) != nil {
				t.Fatalf("out=%s", &out)
			}
			if mode == "confirmed" {
				if exit != 0 || !v.Created || v.Stage != "confirmed" || v.Quota == nil || sends.Load() != 1 {
					t.Fatalf("exit=%d out=%s stderr=%s", exit, &out, &stderr)
				}
				return
			}
			code, sent, stage, count := "NotSettled", "yes", "unconfirmed", int32(1)
			switch mode {
			case "price":
				code, sent, stage, count = "PriceAboveMax", "no", "preflight", 0
			case "unpriced":
				code, sent, stage, count = "Unpriced", "no", "preflight", 0
			case "changed":
				code, sent, stage, count = "QuotaChanged", "no", "preflight", 0
			}
			if exit != 1 || v.Error == nil || v.Error.Code != code || v.Sent != sent || v.Stage != stage || v.Created || sends.Load() != count || quotes.Load() != 1 {
				t.Fatalf("exit=%d out=%s stderr=%s sends=%d", exit, &out, &stderr, sends.Load())
			}
			if strings.Contains(out.String()+stderr.String(), "peer-canary") {
				t.Fatal("URL leaked")
			}
		})
	}
}

func invokeSyntheticLifecycleCLI(t *testing.T, ctx context.Context, client *monitor.Client, args []string) (map[string]any, error) {
	t.Helper()
	var out, stderr bytes.Buffer
	e := &env{stdin: strings.NewReader(""), stdout: &out, stderr: &stderr, flags: &globalFlags{region: "hcm-3", output: "json"}}
	root := &cobra.Command{Use: "vngcloud", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().StringVar(&e.flags.region, "region", "hcm-3", "")
	root.PersistentFlags().StringVar(&e.flags.output, "output", "json", "")
	root.PersistentFlags().BoolVar(&e.flags.yes, "yes", false, "")
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.AddCommand(Service(e, "monitor", "", func(vngcloud.Config) *monitor.Client { return client }, monitorOps...))
	state := new(syntheticCLIOutcome)
	root.SetArgs(args)
	cmd, err := root.ExecuteContextC(context.WithValue(ctx, syntheticOutcomeContextKey{}, state))
	if err != nil {
		finishSyntheticCLI(&out, &stderr, cmd, syntheticCommandOperation(cmd), state, err)
	}
	var result map[string]any
	if json.Unmarshal(out.Bytes(), &result) != nil {
		t.Errorf("stdout=%s stderr=%s", &out, &stderr)
	}
	return result, err
}
func TestSyntheticLifecycleCLIRetainedAttempts(t *testing.T) {
	for _, command := range []string{"create-synthetic-quota", "delete-synthetic-quota"} {
		for _, unknown := range []bool{false, true} {
			for _, mode := range []string{"matching", "changed", "unresolved"} {
				t.Run(fmt.Sprintf("%s/%t/%s", command, unknown, mode), func(t *testing.T) {
					withCleanEnv(t)
					var sends, quotes atomic.Int32
					ready := make(chan struct{})
					handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch {
						case r.Method == http.MethodDelete || r.Method == http.MethodPost && r.URL.Path == "/billing-api/v2/synthetic/quota":
							sends.Add(1)
							_, _ = io.Copy(io.Discard, r.Body)
							if unknown {
								conn, _, err := w.(http.Hijacker).Hijack()
								if err != nil {
									t.Error(err)
									return
								}
								_ = conn.Close()
								return
							}
							if command == "delete-synthetic-quota" {
								w.WriteHeader(502)
							}
						case r.URL.Path == "/billing-api/v1/synthetic/quota":
							if sends.Load() > 0 && mode == "unresolved" {
								close(ready)
								<-r.Context().Done()
								return
							}
							if command == "create-synthetic-quota" && sends.Load() == 0 || command == "delete-synthetic-quota" && sends.Load() > 0 {
								w.WriteHeader(404)
								_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDeleted"))
								return
							}
							body := string(mustSyntheticCLIFixture(t, "GetSyntheticQuotaCreated"))
							body = strings.ReplaceAll(body, "2026-11-01", "2500-11-01")
							body = strings.ReplaceAll(body, "2026-12-01", "2500-12-01")
							_, _ = io.WriteString(w, body)
						case strings.HasSuffix(r.URL.Path, "/quota-detail"):
							_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaCreatedDetail"))
						case r.URL.Path == "/billing-api/v2/synthetic/quota-class":
							_, _ = w.Write(mustSyntheticCLIFixture(t, "ListSyntheticQuotaClasses"))
						case strings.HasSuffix(r.URL.Path, "/packages"):
							_, _ = w.Write(mustSyntheticCLIFixture(t, "ListSyntheticClassPackages"))
						case r.URL.Path == "/billing-api/v1/settings":
							_, _ = w.Write(mustSyntheticCLIFixture(t, "GetQuotaSettings"))
						case r.URL.Path == "/billing-api/v2/synthetic/prices/created-price":
							quotes.Add(1)
							_, _ = w.Write(mustSyntheticCLIFixture(t, "QuoteCreateSyntheticQuota"))
						default:
							t.Errorf("unexpected %s", r.URL)
							w.WriteHeader(400)
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
					args := syntheticLifecycleCLIArgs(command)
					if command == "create-synthetic-quota" {
						args = append(args, "--no-wait")
					}
					// Keep the SDK client so the real command can reconcile its ledger.
					first, err := invokeSyntheticLifecycleCLI(t, context.Background(), client, args)
					if command == "create-synthetic-quota" && !unknown {
						if err != nil || first["Stage"] != "accepted" {
							t.Fatalf("first=%v err=%v", first, err)
						}
					} else if !errors.Is(err, dns.ErrNotSettled) {
						t.Fatalf("first=%v err=%v", first, err)
					}
					secondArgs := slices.Clone(args)
					if mode != "matching" {
						if command == "create-synthetic-quota" {
							secondArgs = append(secondArgs, "--quantity", "11")
						} else {
							secondArgs = append(secondArgs, "--expected-quota-id", "other")
						}
					}
					var result map[string]any
					if mode == "unresolved" {
						ctx, cancel := context.WithCancel(context.Background())
						defer cancel()
						done := make(chan struct{})
						go func() { result, err = invokeSyntheticLifecycleCLI(t, ctx, client, secondArgs); close(done) }()
						<-ready
						cancel()
						<-done
					} else {
						result, err = invokeSyntheticLifecycleCLI(t, context.Background(), client, secondArgs)
					}
					wantQuotes := int32(0)
					if command == "create-synthetic-quota" {
						wantQuotes = 1
					}
					if sends.Load() != 1 || quotes.Load() != wantQuotes {
						t.Fatalf("sends=%d quotes=%d", sends.Load(), quotes.Load())
					}
					switch mode {
					case "matching":
						stage := "settled"
						if command == "create-synthetic-quota" {
							stage = "confirmed"
							if result["Created"] != true {
								t.Fatalf("result=%v", result)
							}
						}
						if err != nil || result["Stage"] != stage {
							t.Fatalf("result=%v err=%v", result, err)
						}
						retiredArgs := slices.Clone(args)
						if command == "create-synthetic-quota" {
							retiredArgs = append(retiredArgs, "--class-id", "invalid/id")
						} else {
							retiredArgs = append(retiredArgs, "--expected-quota-id", "invalid/id")
						}
						retired, e := invokeSyntheticLifecycleCLI(t, context.Background(), client, retiredArgs)
						if !errors.Is(e, vngcloud.ErrInvalidInput) || errors.Is(e, dns.ErrNotSettled) || exitCode(e) != 2 {
							t.Fatalf("retired=%v err=%v", retired, e)
						}
						if command == "create-synthetic-quota" && retired["Sent"] != "no" || command == "delete-synthetic-quota" && retired["Stage"] != "" {
							t.Fatalf("stale retired evidence: %v", retired)
						}
						if sends.Load() != 1 {
							t.Fatal("retirement test sent another mutation")
						}
					case "changed":
						if !errors.Is(err, vngcloud.ErrInvalidInput) || exitCode(err) != 2 {
							t.Fatalf("result=%v err=%v", result, err)
						}
					case "unresolved":
						if !errors.Is(err, dns.ErrNotSettled) || exitCode(err) != 1 {
							t.Fatalf("result=%v err=%v", result, err)
						}
					}
					if command == "create-synthetic-quota" && mode != "changed" {
						sent := "yes"
						if unknown {
							sent = "unknown"
						}
						if result["Sent"] != sent {
							t.Fatalf("result=%v", result)
						}
					}
					if command == "delete-synthetic-quota" && result["Effect"] != syntheticDeleteEffect {
						t.Fatalf("result=%v", result)
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
	for _, command := range []string{"recover-synthetic-quota", "renew-synthetic-quota", "ensure-synthetic-quota"} {
		for _, sent := range []string{"no", "yes", "unknown"} {
			t.Run(command+"/"+sent, func(t *testing.T) {
				var output any
				outcome := monitor.QuotaMutationOutcome{Sent: sent, Stage: "confirmed"}
				q := &monitor.Quota{EndTime: bad}
				switch command {
				case "ensure-synthetic-quota":
					output = &monitor.EnsureSyntheticQuotaOutput{QuotaMutationOutcome: outcome, Action: "recover", Quota: q, Recovered: true}
				case "recover-synthetic-quota":
					output = &monitor.RecoverSyntheticQuotaOutput{QuotaMutationOutcome: outcome, Quota: q, Recovered: true}
				default:
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
				fields := 8
				if command == "ensure-synthetic-quota" {
					fields = 10
				}
				if json.Unmarshal(stdout.Bytes(), &record) != nil || len(record) != fields || record["Sent"] != sent || record["Stage"] != stage || record["Quota"] != nil || record["Error"] == nil || record["Error"].(map[string]any)["Code"] != code {
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

func TestSyntheticCreateEncodingFailureFallback(t *testing.T) {
	bad, err := time.Parse(time.RFC3339, "2501-01-01T00:00:00+24:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, sent := range []string{"no", "yes", "unknown"} {
		t.Run(sent, func(t *testing.T) {
			out := &monitor.CreateSyntheticQuotaOutput{QuotaMutationOutcome: monitor.QuotaMutationOutcome{Sent: sent, Stage: "confirmed"}, Created: true, Quota: &monitor.Quota{EndTime: bad}, SentAt: &bad}
			state := &syntheticCLIOutcome{output: out}
			cmd := &cobra.Command{Use: "create-synthetic-quota"}
			cmd.Flags().String("output", "json", "")
			var stdout, stderr bytes.Buffer
			finishSyntheticCLI(&stdout, &stderr, cmd, "monitor.CreateSyntheticQuota", state, errors.New("encoding failed"))
			var record map[string]any
			if json.Unmarshal(stdout.Bytes(), &record) != nil || len(record) != 8 || record["Sent"] != sent || record["Quota"] != nil || record["Created"] != false || record["SentAt"] != nil {
				t.Fatalf("stdout=%s stderr=%s", &stdout, &stderr)
			}
		})
	}
}

func TestEnsureSyntheticQuotaCLIDefaults(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		send  bool
		stale bool
	}{
		{name: "default"},
		{name: "JSON default", flags: []string{"--cli-input-json", `{"MaxPrice":0}`}},
		{name: "flag zero", flags: []string{"--max-days-left", "0"}, send: true},
		{name: "JSON zero", flags: []string{"--cli-input-json", `{"MaxDaysLeft":0,"MaxPrice":0}`}, send: true},
		{name: "flag overrides JSON", flags: []string{"--cli-input-json", `{"MaxDaysLeft":7}`, "--max-days-left", "0"}, send: true},
		{name: "JSON guard before skip", flags: []string{"--cli-input-json", `{"ExpectedQuotaID":"other"}`}, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCleanEnv(t)
			var sends, quotes atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/billing-api/v1/synthetic/quota":
					body := strings.ReplaceAll(string(mustSyntheticCLIFixture(t, "GetSyntheticQuotaRecovered")), "2026-12-01T05:19:45.000+00:00", "2500-12-01T05:19:45Z")
					_, _ = io.WriteString(w, body)
				case "/billing-api/v2/synthetic/quota-syn-1/quota-detail":
					_, _ = w.Write(mustSyntheticCLIFixture(t, "GetSyntheticQuotaDetailNullable"))
				case "/billing-api/v2/synthetic/prices/created-price":
					quotes.Add(1)
					_, _ = io.WriteString(w, `{"optimumPrice":0}`)
				case "/billing-api/v2/synthetic/quota/extend-time":
					sends.Add(1)
				default:
					t.Errorf("unexpected route %s", r.URL.Path)
					w.WriteHeader(400)
				}
			})
			server := httptest.NewServer(handler)
			t.Cleanup(server.Close)
			withTestOptions(t, vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL + "/"}), vngcloud.WithHTTPClient(syntheticCLITestHTTPClient(server.Client())), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithRetry(3, time.Millisecond))
			args := append([]string{"monitor", "ensure-synthetic-quota", "--region", "hcm-3", "--max-price", "0", "--no-wait"}, tc.flags...)
			var out, stderr bytes.Buffer
			code := Main(context.Background(), args, strings.NewReader(""), &out, &stderr)
			var value monitor.EnsureSyntheticQuotaOutput
			if err := json.Unmarshal(out.Bytes(), &value); err != nil {
				t.Fatal(err)
			}
			wantStage, wantAction, wantCount, wantExit := "not-due", "none", int32(0), 0
			if tc.send {
				wantStage, wantAction, wantCount = "accepted", "renew", 1
			}
			if tc.stale {
				wantStage, wantExit = "preflight", 1
			}
			if value.Stage != wantStage || value.Action != wantAction || sends.Load() != wantCount || quotes.Load() != wantCount || code != wantExit {
				t.Fatalf("exit=%d out=%s stderr=%s sends=%d quotes=%d", code, &out, &stderr, sends.Load(), quotes.Load())
			}
		})
	}
}

func TestSyntheticDeleteEncodingFailureFallback(t *testing.T) {
	bad, err := time.Parse(time.RFC3339, "2501-01-01T00:00:00+24:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"prepared", "sent", "settled"} {
		for _, format := range []string{"json", "table", "text"} {
			t.Run(stage+format, func(t *testing.T) {
				out := &monitor.DeleteSyntheticQuotaOutput{Quota: &monitor.Quota{EndTime: bad}, Stage: stage}
				var stdout, stderr bytes.Buffer
				writer := &syntheticOutputWriter{Writer: &stdout}
				renderErr := renderSyntheticCommand(writer, format, out)
				if renderErr == nil || writer.attempted {
					t.Fatalf("attempted=%t err=%v", writer.attempted, renderErr)
				}
				state := &syntheticCLIOutcome{output: out, format: format}
				cmd := &cobra.Command{Use: "delete-synthetic-quota"}
				cmd.Flags().String("output", "json", "")
				finishSyntheticCLI(&stdout, &stderr, cmd, "monitor.DeleteSyntheticQuota", state, renderErr)
				if !strings.Contains(stdout.String(), syntheticDeleteEffect) {
					t.Fatalf("stdout=%s stderr=%s", &stdout, &stderr)
				}
				if format == "json" {
					var record map[string]any
					wantStage := stage
					if stage == "settled" {
						wantStage = "sent"
					}
					if json.Unmarshal(stdout.Bytes(), &record) != nil || len(record) != 4 || record["Stage"] != wantStage || record["Quota"] != nil || record["Purged"] != false {
						t.Fatalf("stdout=%s", &stdout)
					}
				}
				if stage != "prepared" && !strings.Contains(stderr.String(), `"NotSettled"`) {
					t.Fatalf("stderr=%s", &stderr)
				}
			})
		}
	}
}

func TestSyntheticCreateDeleteOutputAttemptTracking(t *testing.T) {
	bad, err := time.Parse(time.RFC3339, "2501-01-01T00:00:00+24:00")
	if err != nil {
		t.Fatal(err)
	}
	for _, out := range []any{&monitor.CreateSyntheticQuotaOutput{SentAt: &bad}, &monitor.DeleteSyntheticQuotaOutput{Quota: &monitor.Quota{EndTime: bad}}} {
		for _, format := range []string{"json", "table", "text"} {
			var stdout bytes.Buffer
			writer := &syntheticOutputWriter{Writer: &stdout}
			if e := renderSyntheticCommand(writer, format, out); e == nil || writer.attempted || stdout.Len() != 0 {
				t.Fatalf("format=%s attempted=%t stdout=%s err=%v", format, writer.attempted, &stdout, e)
			}
		}
	}
	for _, command := range []string{"create-synthetic-quota", "delete-synthetic-quota"} {
		var out any = &monitor.CreateSyntheticQuotaOutput{}
		if command == "delete-synthetic-quota" {
			out = &monitor.DeleteSyntheticQuotaOutput{}
		}
		for _, format := range []string{"json", "table", "text"} {
			broken := new(syntheticBrokenOutput)
			writer := &syntheticOutputWriter{Writer: broken}
			renderErr := renderSyntheticCommand(writer, format, out)
			if renderErr == nil || !writer.attempted || broken.calls != 1 {
				t.Fatalf("calls=%d err=%v", broken.calls, renderErr)
			}
			state := &syntheticCLIOutcome{output: out, format: format, rendered: writer.attempted}
			cmd := &cobra.Command{Use: command}
			finishSyntheticCLI(broken, io.Discard, cmd, syntheticMutationOperation("monitor", command), state, renderErr)
			if broken.calls != 1 {
				t.Fatal("second stdout write after failed write")
			}
		}
	}
}
