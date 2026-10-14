package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

type lifecycleServer struct {
	kind                 string
	delete               bool
	reads, sends, quotes atomic.Int32
	quota                func(int32) string
	send                 http.HandlerFunc
	price                string
	detail               string
}

func (s *lifecycleServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		path := "/billing-api/v1/" + s.kind + "/quota"
		switch {
		case r.Method == http.MethodGet && r.URL.Path == path:
			n := s.reads.Add(1)
			body := s.quota(n)
			if body == "" {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `{"message":"Not found `+s.kind+` quota"}`)
				return
			}
			_, _ = io.WriteString(w, body)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/billing-api/v2/synthetic/") && strings.HasSuffix(r.URL.Path, "/quota-detail"):
			body := s.detail
			if body == "" {
				body = testutil.FixtureBody(t, "../testdata/monitor/GetSyntheticQuotaDetail.json")
			}
			body = strings.ReplaceAll(body, "quota-syn-1", strings.Split(r.URL.Path, "/")[4])
			_, _ = io.WriteString(w, body)
		case r.URL.Path == "/billing-api/v2/synthetic/prices/created-price" && r.Method == http.MethodPost:
			s.quotes.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !reflect.DeepEqual(body, map[string]any{"redirectUrl": "", "packageId": "32b3743d-94b8-49ab-984d-bd7ec99b46f6", "quantity": float64(10), "resourceId": "quota-syn-1"}) {
				t.Errorf("quote body=%v", body)
			}
			price := s.price
			if price == "" {
				price = `{"optimumPrice":0}`
			}
			_, _ = io.WriteString(w, price)
		case s.delete && r.Method == http.MethodDelete && r.URL.Path == path:
			s.sends.Add(1)
			b, _ := io.ReadAll(r.Body)
			if len(b) != 0 {
				t.Errorf("delete body=%s", b)
			}
			s.send(w, r)
		case !s.delete && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/update-auto-renew"):
			s.sends.Add(1)
			id := "quota-sms-1"
			if s.kind == "synthetic" {
				id = "quota-syn-1"
			}
			if r.URL.Path != path+"/"+id+"/update-auto-renew" {
				t.Errorf("path=%s", r.URL.Path)
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if !reflect.DeepEqual(body, map[string]any{"resourceId": id, "enabled": s.kind == "synthetic", "period": float64(1)}) {
				t.Errorf("body=%v", body)
			}
			s.send(w, r)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}
}
func lifecycleFixture(t *testing.T, kind string, changes map[string]any) string {
	t.Helper()
	fixture := "GetNotificationQuotaSMSAutoRenewOn"
	if kind == "synthetic" {
		fixture = "GetSyntheticQuota"
	}
	return string(quotaFixture(t, fixture, func(m map[string]json.RawMessage) {
		for k, v := range changes {
			m[k], _ = json.Marshal(v)
		}
	}))
}
func lifecycleCall(c *Client, kind string, del bool) (any, error) {
	if del {
		return c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: kind})
	}
	if kind == "synthetic" {
		return c.SetSyntheticQuotaAutoRenew(context.Background(), &SetSyntheticQuotaAutoRenewInput{Enabled: boolPointer(true)})
	}
	return c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: kind, Enabled: boolPointer(false)})
}
func boolPointer(v bool) *bool { return &v }

func TestLifecycleSingleSendHTTPAndAmbiguousReconciliation(t *testing.T) {
	for _, kind := range []string{"email", "sms", "synthetic"} {
		for _, del := range []bool{false, true} {
			if kind == "synthetic" && del {
				continue
			}
			for _, status := range []int{200, 204, 400, 401, 403, 404, 409, 429, 502, 307, 0} {
				if (del && status == 200) || (!del && status == 204) {
					continue
				}
				t.Run(kind+map[bool]string{false: " auto-renew", true: " delete"}[del]+" "+http.StatusText(status), func(t *testing.T) {
					s := &lifecycleServer{kind: kind, delete: del, quota: func(n int32) string {
						if status == 200 || status == 204 {
							if n > 2 {
								if del {
									return ""
								}
								target, period := 0, 0
								if kind == "synthetic" {
									target, period = 1, 1
								}
								return lifecycleFixture(t, kind, map[string]any{"autoRenew": target, "autoRenewPeriod": period})
							}
						}
						changes := map[string]any{}
						if del {
							changes["autoRenew"] = 0
							changes["autoRenewPeriod"] = 0
						}
						return lifecycleFixture(t, kind, changes)
					}, send: func(w http.ResponseWriter, r *http.Request) {
						if status == 0 {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
						w.Header().Set("Location", "/foreign")
						w.WriteHeader(status)
						if status >= 400 {
							_, _ = io.WriteString(w, `{"code":"private","message":"unrelated private response"}`)
						}
					}}
					c := New(testutil.NewRetryConfig(t, s.handler(t)))
					emailFakeClock(c)
					out, err := lifecycleCall(c, kind, del)
					ambiguous := status == 502 || status == 307 || status == 0
					if s.sends.Load() != 1 {
						t.Fatalf("sends=%d err=%v", s.sends.Load(), err)
					}
					switch {
					case status == 200 || status == 204:
						if err != nil {
							t.Fatal(err)
						}
						switch v := out.(type) {
						case *DeleteNotificationQuotaOutput:
							if v.Stage != "settled" || v.Quota == nil {
								t.Fatalf("out=%+v", v)
							}
						case *SetNotificationQuotaAutoRenewOutput:
							if v.Stage != "settled" || !v.Changed {
								t.Fatalf("out=%+v", v)
							}
						}
					case ambiguous:
						if !errors.Is(err, dns.ErrNotSettled) || len(c.quotaAttempts) != 1 {
							t.Fatalf("out=%v err=%v", out, err)
						}
						_, err = lifecycleCall(c, kind, del)
						if !errors.Is(err, dns.ErrNotSettled) || s.sends.Load() != 1 {
							t.Fatalf("repeat err=%v sends=%d", err, s.sends.Load())
						}
					default:
						var api *core.APIError
						if !errors.As(err, &api) || api.StatusCode != status || api.Code != core.ResolvedCode(status, "") || len(c.quotaAttempts) != 0 || s.reads.Load() != 2 || strings.Contains(err.Error(), "private") {
							t.Fatalf("out=%v err=%v reads=%d", out, err, s.reads.Load())
						}
					}
				})
			}
		}
	}
}

func TestLifecycleUnsupportedZeroRequests(t *testing.T) {
	var requests atomic.Int32
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) })))
	calls := []func() error{
		func() error {
			_, e := c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: "Email", Enabled: boolPointer(true)})
			return e
		},
		func() error {
			_, e := c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: "SMS", Enabled: boolPointer(true)})
			return e
		},
		func() error {
			_, e := c.SetSyntheticQuotaAutoRenew(context.Background(), &SetSyntheticQuotaAutoRenewInput{Enabled: boolPointer(false)})
			return e
		},
		func() error {
			_, e := c.DeleteSyntheticQuota(context.Background(), &DeleteSyntheticQuotaInput{})
			return e
		},
		func() error {
			_, e := c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: "SMS", Purge: true})
			return e
		},
		func() error {
			_, e := c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: "Email", Purge: true})
			return e
		},
	}
	for _, call := range calls {
		if e := call(); !errors.Is(e, core.ErrInvalidInput) {
			t.Fatal(e)
		}
	}
	for _, kind := range []string{"", "unknown", "../email"} {
		_, e := c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: kind})
		if !errors.Is(e, core.ErrInvalidInput) {
			t.Fatal(e)
		}
	}
	for _, max := range []float64{-1, math.NaN(), math.Inf(1)} {
		_, e := c.SetSyntheticQuotaAutoRenew(context.Background(), &SetSyntheticQuotaAutoRenewInput{Enabled: boolPointer(true), MaxPrice: max})
		if !errors.Is(e, core.ErrInvalidInput) {
			t.Fatal(e)
		}
	}
	_, e := c.SetSyntheticQuotaAutoRenew(context.Background(), nil)
	if !errors.Is(e, core.ErrInvalidInput) {
		t.Fatal(e)
	}
	_, e = c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: "SMS"})
	if !errors.Is(e, core.ErrInvalidInput) {
		t.Fatal(e)
	}
	_, e = c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: "SMS", ExpectedQuotaID: "../quota"})
	if !errors.Is(e, core.ErrInvalidInput) {
		t.Fatal(e)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestLifecycleDeleteEligibilityPreparationAndFinalRead(t *testing.T) {
	for _, kind := range []string{"email", "sms"} {
		for _, final := range []bool{false, true} {
			for _, change := range []map[string]any{{"status": "CREATING", "updatedAt": nil}, {"status": "IN-TRASH"}, {"status": "ERROR"}, {"status": "NEW"}, {"autoRenew": 1}, {"poc": 1}, {"packageId": "replacement"}, {"id": "replacement"}, {"endTime": "2027-01-01T00:00:00Z"}} {
				if !final && (change["packageId"] != nil || change["id"] != nil || change["endTime"] != nil) {
					continue
				}
				t.Run(kind+lifecycleJSON(change)+map[bool]string{false: " initial", true: " final"}[final], func(t *testing.T) {
					s := &lifecycleServer{kind: kind, delete: true, quota: func(n int32) string {
						changes := map[string]any{"autoRenew": 0, "autoRenewPeriod": 0}
						if !final || n >= 2 {
							for k, v := range change {
								changes[k] = v
							}
						}
						return lifecycleFixture(t, kind, changes)
					}}
					c := New(testutil.NewRetryConfig(t, s.handler(t)))
					out, err := c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: kind})
					want := core.ErrInvalidInput
					if change["status"] == "NEW" {
						want = ErrUnexpectedStatus
					}
					if out != nil || !errors.Is(err, want) || s.sends.Load() != 0 {
						t.Fatalf("out=%v err=%v sends=%d", out, err, s.sends.Load())
					}
				})
			}
		}
	}
}

func TestLifecycleSyntheticFreeRefusalAndGuard(t *testing.T) {
	for _, price := range []string{`{"optimumPrice":0}`, `{"optimumPrice":1}`, `{}`} {
		s := &lifecycleServer{kind: "synthetic", price: price, quota: func(int32) string { return lifecycleFixture(t, "synthetic", nil) }, send: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(400)
			testutil.WriteFixture(t, w, "../testdata/monitor/SetQuotaAutoRenewSyntheticRefused.json")
		}}
		c := New(testutil.NewRetryConfig(t, s.handler(t)))
		out, err := c.SetSyntheticQuotaAutoRenew(context.Background(), &SetSyntheticQuotaAutoRenewInput{Enabled: boolPointer(true)})
		if out != nil || err == nil || len(c.quotaAttempts) != 0 || s.quotes.Load() != 1 {
			t.Fatalf("out=%v err=%v", out, err)
		}
		if price == `{"optimumPrice":0}` {
			var api *core.APIError
			if s.sends.Load() != 1 || s.reads.Load() != 2 || !errors.As(err, &api) || api.Code != "BadRequest" || api.StatusCode != 400 || api.Operation != "monitor.SetSyntheticQuotaAutoRenew" {
				t.Fatalf("err=%v sends=%d", err, s.sends.Load())
			}
		} else if s.sends.Load() != 0 {
			t.Fatal("guard sent write")
		}
	}
}

func lifecycleJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestLifecycleExpectedIDBeforeNoOpAndOpenRecord(t *testing.T) {
	s := &lifecycleServer{kind: "sms", quota: func(int32) string {
		return lifecycleFixture(t, "sms", map[string]any{"autoRenew": 0, "autoRenewPeriod": 0})
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	out, e := c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: "SMS", Enabled: boolPointer(false), ExpectedQuotaID: "replacement"})
	if out != nil || !errors.Is(e, core.ErrInvalidInput) || s.sends.Load() != 0 || s.reads.Load() != 1 {
		t.Fatalf("out=%v err=%v", out, e)
	}
	out, e = c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: "SMS", Enabled: boolPointer(false), ExpectedQuotaID: "quota-sms-1"})
	if e != nil || out == nil || out.Changed || out.Stage != "prepared" || s.sends.Load() != 0 || s.reads.Load() != 2 {
		t.Fatalf("out=%v err=%v", out, e)
	}
	s.delete = true
	s.send = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }
	emailFakeClock(c)
	_, e = c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: "SMS"})
	if !errors.Is(e, dns.ErrNotSettled) {
		t.Fatal(e)
	}
	reads := s.reads.Load()
	_, e = c.DeleteNotificationQuota(context.Background(), &DeleteNotificationQuotaInput{Kind: "SMS", ExpectedQuotaID: "replacement"})
	if !errors.Is(e, core.ErrInvalidInput) || s.reads.Load() != reads || s.sends.Load() != 1 {
		t.Fatalf("err=%v reads=%d sends=%d", e, s.reads.Load(), s.sends.Load())
	}
}

func TestLifecycleAutoRenewFrozenTermsAndNoCorrection(t *testing.T) {
	for _, kind := range []string{"sms", "synthetic"} {
		for _, change := range []map[string]any{{"packageId": "other"}, {"id": "other"}, {"status": "CREATING"}, {"poc": 1}, {"autoRenew": 0}, {"autoRenewPeriod": 3}, {"endTime": "2027-01-01T00:00:00Z"}} {
			if kind == "synthetic" && change["autoRenew"] != nil {
				change = map[string]any{"autoRenew": 1}
			}
			s := &lifecycleServer{kind: kind, quota: func(n int32) string {
				if n == 1 {
					return lifecycleFixture(t, kind, nil)
				}
				return lifecycleFixture(t, kind, change)
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			_, e := lifecycleCall(c, kind, false)
			if e == nil || s.sends.Load() != 0 {
				t.Fatalf("kind=%s change=%v err=%v sends=%d", kind, change, e, s.sends.Load())
			}
		}
	}
	s := &lifecycleServer{kind: "synthetic", quota: func(int32) string {
		return lifecycleFixture(t, "synthetic", map[string]any{"autoRenew": 1, "autoRenewPeriod": 3})
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	_, e := lifecycleCall(c, "synthetic", false)
	if !errors.Is(e, core.ErrInvalidInput) || s.quotes.Load() != 0 || s.sends.Load() != 0 {
		t.Fatal(e)
	}
}

func TestLifecycleSettlementBoundsAndReadOnlyReconciliation(t *testing.T) {
	for _, del := range []bool{false, true} {
		for _, changes := range []map[string]any{{"id": "replacement", "autoRenew": 0, "autoRenewPeriod": 0}, {"packageId": "replacement", "autoRenew": 0, "autoRenewPeriod": 0}, {"status": "CREATING"}, {"status": "ERROR"}, {}} {
			s := &lifecycleServer{kind: "sms", delete: del, quota: func(n int32) string {
				baseline := map[string]any{}
				if del {
					baseline["autoRenew"] = 0
					baseline["autoRenewPeriod"] = 0
				}
				if n > 2 {
					for k, v := range changes {
						baseline[k] = v
					}
				}
				return lifecycleFixture(t, "sms", baseline)
			}, send: func(w http.ResponseWriter, _ *http.Request) {
				if del {
					w.WriteHeader(204)
				} else {
					w.WriteHeader(200)
				}
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			now := emailFakeClock(c)
			out, e := lifecycleCall(c, "sms", del)
			bound := 30 * time.Second
			if del {
				bound = 60 * time.Second
			}
			if !errors.Is(e, dns.ErrNotSettled) || now.Sub(time.Unix(0, 0)) != bound || s.sends.Load() != 1 || len(c.quotaAttempts) != 1 {
				t.Fatalf("out=%v err=%v elapsed=%v", out, e, now.Sub(time.Unix(0, 0)))
			}
			// An unchanged baseline never proves a rejection.
			s.quota = func(int32) string {
				baseline := map[string]any{}
				if del {
					baseline["autoRenew"] = 0
				}
				return lifecycleFixture(t, "sms", baseline)
			}
			_, e = lifecycleCall(c, "sms", del)
			if !errors.Is(e, dns.ErrNotSettled) || s.sends.Load() != 1 || len(c.quotaAttempts) != 1 {
				t.Fatal(e)
			}
			s.quota = func(int32) string {
				if del {
					return ""
				}
				return lifecycleFixture(t, "sms", map[string]any{"autoRenew": 0, "autoRenewPeriod": 0})
			}
			out, e = lifecycleCall(c, "sms", del)
			if e != nil || s.sends.Load() != 1 || len(c.quotaAttempts) != 0 {
				t.Fatalf("out=%v err=%v", out, e)
			}
		}
	}
}

func TestLifecycleCrossOperationNeverSendsAfterUncertainty(t *testing.T) {
	var absent atomic.Bool
	s := &lifecycleServer{kind: "sms", delete: true, quota: func(int32) string {
		if absent.Load() {
			return ""
		}
		return lifecycleFixture(t, "sms", map[string]any{"autoRenew": 0})
	}, send: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	emailFakeClock(c)
	_, e := lifecycleCall(c, "sms", true)
	if !errors.Is(e, dns.ErrNotSettled) {
		t.Fatal(e)
	}
	_, e = lifecycleCall(c, "sms", false)
	if !errors.Is(e, dns.ErrNotSettled) || s.sends.Load() != 1 {
		t.Fatal(e)
	}
	absent.Store(true)
	_, e = lifecycleCall(c, "sms", false)
	if !errors.Is(e, core.ErrInvalidInput) || s.sends.Load() != 1 || len(c.quotaAttempts) != 0 {
		t.Fatal(e)
	}
}

func TestLifecycleMalformedSuccessRemainsUnresolved(t *testing.T) {
	s := &lifecycleServer{kind: "sms", quota: func(int32) string { return lifecycleFixture(t, "sms", nil) }, send: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) }}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	emailFakeClock(c)
	out, e := c.SetNotificationQuotaAutoRenew(context.Background(), &SetNotificationQuotaAutoRenewInput{Kind: "SMS", Enabled: boolPointer(false)})
	var api *core.APIError
	if out == nil || out.Stage != "sent" || !errors.Is(e, dns.ErrNotSettled) || !errors.As(e, &api) || api.Code != "InvalidResponse" || len(c.quotaAttempts) != 1 || s.sends.Load() != 1 || s.reads.Load() != 2 {
		t.Fatalf("out=%v err=%v reads=%d", out, e, s.reads.Load())
	}
}

func TestLifecycleCredentialEchoAcrossOperation(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	for _, del := range []bool{false, true} {
		s := &lifecycleServer{kind: "sms", delete: del, quota: func(n int32) string {
			if n > 2 {
				if del {
					return ""
				}
				return lifecycleFixture(t, "sms", map[string]any{"autoRenew": 0, "autoRenewPeriod": 0, "name": first + second})
			}
			changes := map[string]any{"name": first + second}
			if del {
				changes["autoRenew"] = 0
			}
			return lifecycleFixture(t, "sms", changes)
		}, send: func(w http.ResponseWriter, _ *http.Request) {
			if del {
				w.WriteHeader(204)
			} else {
				w.WriteHeader(200)
			}
		}}
		var refresh atomic.Bool
		handler := s.handler(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.sends.Load() > 0 && r.Method == http.MethodGet && refresh.CompareAndSwap(false, true) {
				w.WriteHeader(401)
				return
			}
			handler(w, r)
		}))
		source := &monitorRefreshTokens{first: first, second: second}
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 3})))
		emailFakeClock(c)
		out, e := lifecycleCall(c, "sms", del)
		if e != nil || s.sends.Load() != 1 || source.calls != 2 {
			t.Fatalf("out=%v err=%v calls=%d", out, e, source.calls)
		}
		assertMonitorCredentialsAbsent(t, out, e, first, second)
		server.Close()
	}
}

func TestLifecycleReadOnlyProfileZeroRequests(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("[default]\nregion = hcm-3\nread_only = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	credentialsPath := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(credentialsPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, e := vngcloud.LoadConfig(context.Background(), vngcloud.WithConfigFile(path), vngcloud.WithSharedCredentialsFile(credentialsPath), vngcloud.WithStaticToken("synthetic-token"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Monitor: server.URL}))
	if e != nil {
		t.Fatal(e)
	}
	for _, del := range []bool{false, true} {
		_, e = lifecycleCall(New(cfg), "sms", del)
		var api *core.APIError
		if !errors.As(e, &api) || api.Code != "ReadOnly" {
			t.Fatal(e)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("requests=%d", requests.Load())
	}
}

func TestLifecycleNotSentDialFailureZeroMutations(t *testing.T) {
	for _, del := range []bool{false, true} {
		s := &lifecycleServer{kind: "sms", delete: del, quota: func(int32) string {
			changes := map[string]any{}
			if del {
				changes["autoRenew"] = 0
			}
			return lifecycleFixture(t, "sms", changes)
		}}
		server := httptest.NewServer(s.handler(t))
		var dials atomic.Int32
		dialer := &net.Dialer{}
		httpTransport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if dials.Add(1) > 2 {
				return nil, &net.OpError{Op: "dial", Net: network, Err: syscall.ECONNREFUSED}
			}
			return dialer.DialContext(ctx, network, address)
		}}
		c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: &http.Client{Transport: httpTransport}, RetryCount: 3})))
		_, e := lifecycleCall(c, "sms", del)
		if e == nil || errors.Is(e, dns.ErrNotSettled) || s.sends.Load() != 0 || dials.Load() != 3 || len(c.quotaAttempts) != 0 {
			t.Fatalf("err=%v sends=%d dials=%d", e, s.sends.Load(), dials.Load())
		}
		httpTransport.CloseIdleConnections()
		server.Close()
	}
}

func TestLifecycleSyntheticQuantityChangeRefusesWrite(t *testing.T) {
	s := &lifecycleServer{kind: "synthetic", quota: func(int32) string { return lifecycleFixture(t, "synthetic", nil) }}
	handler := s.handler(t)
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/quota-detail") && s.reads.Load() >= 2 {
			_, _ = w.Write(quotaFixture(t, "GetSyntheticQuotaDetail", func(m map[string]json.RawMessage) { m["api"] = json.RawMessage(`11`) }))
			return
		}
		handler(w, r)
	})))
	_, e := lifecycleCall(c, "synthetic", false)
	if !errors.Is(e, core.ErrInvalidInput) || s.sends.Load() != 0 || s.quotes.Load() != 1 {
		t.Fatalf("err=%v sends=%d quotes=%d", e, s.sends.Load(), s.quotes.Load())
	}
}

func TestLifecycleConcurrentDisableSendsOnce(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := &lifecycleServer{kind: "sms", quota: func(n int32) string {
		if n <= 2 {
			return lifecycleFixture(t, "sms", nil)
		}
		if n == 3 {
			close(entered)
			<-release
		}
		return lifecycleFixture(t, "sms", map[string]any{"autoRenew": 0, "autoRenewPeriod": 0})
	}, send: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	done := make(chan error, 2)
	go func() { _, e := lifecycleCall(c, "sms", false); done <- e }()
	<-entered
	go func() { _, e := lifecycleCall(c, "sms", false); done <- e }()
	close(release)
	for range 2 {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if s.sends.Load() != 1 {
		t.Fatalf("sends=%d", s.sends.Load())
	}
}
