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
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const emailQuotaPath = "/billing-api/v1/email/quota"
const emailRedirect = "https://vmonitor.console.vngcloud.vn/quota-usages/notification"

func emailSnapshot(t *testing.T, status, pkg string) string {
	t.Helper()
	return changedEmailFixture(t, map[string]any{"status": status, "packageId": pkg})
}

// All counters are shared with real HTTP handlers, including failure probes.
type emailOrderServer struct {
	mutations atomic.Int32
	reads     atomic.Int32
	quotes    atomic.Int32
	packages  string
	price     string
	quota     func(int32) string
	send      http.HandlerFunc
}

func (s *emailOrderServer) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == emailQuotaPath && r.Method == http.MethodPost:
			s.mutations.Add(1)
			s.send(w, r)
		case r.URL.Path == emailQuotaPath && r.Method == http.MethodGet:
			n := s.reads.Add(1)
			body := ""
			if s.quota != nil {
				body = s.quota(n)
			}
			if body == "" {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `{"message":"Not found email quota"}`)
			} else {
				_, _ = io.WriteString(w, body)
			}
		case r.URL.Path == "/billing-api/v1/email/packages":
			body := s.packages
			if body == "" {
				body = `[{"id":"p","name":"p","amount":20,"price":0}]`
			}
			_, _ = io.WriteString(w, body)
		case r.URL.Path == "/billing-api/v2/email/prices/created-price":
			s.quotes.Add(1)
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(body, map[string]any{"packageId": "p", "quantity": float64(1), "redirectUrl": ""}) {
				t.Errorf("quote body=%v", body)
			}
			price := s.price
			if price == "" {
				price = `{"optimumPrice":0}`
			}
			_, _ = io.WriteString(w, price)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}
}

func freeEmailSend(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(body, map[string]any{"monthPeriod": float64(1), "packageId": "p", "redirectUrl": emailRedirect, "pay": true}) {
			t.Errorf("order body=%v", body)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/CreateNotificationQuotaEmail.json")
	}
}

func TestCreateNotificationQuotaEmailFixtureAndNoWait(t *testing.T) {
	s := &emailOrderServer{send: freeEmailSend(t)}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "eMaIl", NoWait: true})
	if err != nil || out == nil || out.Quota != nil || out.OrderID != "" || out.PaymentPending || s.mutations.Load() != 1 || s.reads.Load() != 2 {
		t.Fatalf("out=%+v err=%v sends=%d reads=%d", out, err, s.mutations.Load(), s.reads.Load())
	}
	out, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
	if out == nil || !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
		t.Fatalf("repeated out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
}

func TestCreateNotificationQuotaOnceHTTPStatusesAndRedirects(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 429, 500, 502, 503, 504, 301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/foreign-private-canary")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"message":"private-canary","code":"private-canary"}`)
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			var api *core.APIError
			if !errors.As(err, &api) || api.StatusCode != status || s.mutations.Load() != 1 || strings.Contains(err.Error(), "private-canary") {
				t.Fatalf("out=%+v err=%v sends=%d", out, err, s.mutations.Load())
			}
			ambiguous := status < 400 || status >= 500
			if errors.Is(err, dns.ErrNotSettled) != ambiguous || (out != nil) != ambiguous {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if ambiguous {
				_, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
				if !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
					t.Fatalf("repeated err=%v sends=%d", err, s.mutations.Load())
				}
			}
		})
	}
}

func TestCreateNotificationQuotaOnceDisconnectAfterBodyRead(t *testing.T) {
	received := make(chan struct{})
	s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
		close(received)
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	<-received
	if out == nil || !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
		t.Fatalf("out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
	_, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
		t.Fatalf("second err=%v sends=%d", err, s.mutations.Load())
	}
}

func emailTransportClient(t *testing.T, s *emailOrderServer, hc *http.Client) *Client {
	t.Helper()
	server := httptest.NewServer(s.handler(t))
	t.Cleanup(server.Close)
	return New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: hc, RetryCount: 3, RetryInterval: time.Millisecond})))
}

func TestCreateNotificationQuotaOnceFailedDialBeforeServer(t *testing.T) {
	s := &emailOrderServer{send: freeEmailSend(t)}
	dials := atomic.Int32{}
	rt := http.DefaultTransport.(*http.Transport).Clone()
	rt.DisableKeepAlives = true
	rt.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if dials.Add(1) == 5 {
			return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("private-canary")}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	t.Cleanup(rt.CloseIdleConnections)
	c := emailTransportClient(t, s, &http.Client{Transport: rt})
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
	if out != nil || err == nil || errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 0 || dials.Load() != 5 || strings.Contains(err.Error(), "private-canary") {
		t.Fatalf("out=%+v err=%v sends=%d dials=%d", out, err, s.mutations.Load(), dials.Load())
	}
	out, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
	if err != nil || out == nil || s.mutations.Load() != 1 {
		t.Fatalf("next out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
}

func TestCreateNotificationQuotaOnceResponseTimeoutAfterBodyRead(t *testing.T) {
	received := make(chan struct{})
	release := make(chan struct{})
	s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(received)
		<-release
	}}
	rt := http.DefaultTransport.(*http.Transport).Clone()
	rt.ResponseHeaderTimeout = 50 * time.Millisecond
	t.Cleanup(rt.CloseIdleConnections)
	c := emailTransportClient(t, s, &http.Client{Transport: rt})
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	<-received
	close(release)
	if out == nil || !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
		t.Fatalf("out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
	_, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
		t.Fatalf("second err=%v sends=%d", err, s.mutations.Load())
	}
}

func TestCreateNotificationQuotaOnceCancellationAfterBodyRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		cancel()
		<-r.Context().Done()
	}}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	out, err := c.CreateNotificationQuota(ctx, &CreateNotificationQuotaInput{Kind: "Email"})
	if out == nil || !errors.Is(err, dns.ErrNotSettled) || !errors.Is(err, context.Canceled) || s.mutations.Load() != 1 {
		t.Fatalf("out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
	_, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
		t.Fatalf("second err=%v sends=%d", err, s.mutations.Load())
	}
}

func TestCreateNotificationQuotaNonzeroQuoteDefaultMaxSendsNone(t *testing.T) {
	s := &emailOrderServer{send: freeEmailSend(t), price: `{"optimumPrice":1}`}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if out != nil || !errors.Is(err, ErrPriceAboveMax) || s.mutations.Load() != 0 {
		t.Fatalf("out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
}

func TestCreateNotificationQuotaMissingQuotePriceSendsNone(t *testing.T) {
	s := &emailOrderServer{send: freeEmailSend(t), price: `{}`}
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	var api *core.APIError
	if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || s.mutations.Load() != 0 {
		t.Fatalf("out=%+v err=%v sends=%d", out, err, s.mutations.Load())
	}
}

func TestCreateNotificationQuotaPriceGuards(t *testing.T) {
	for _, tc := range []struct {
		name             string
		list, quote, max float64
		want             error
	}{
		{"list above", 110000, 100000, 100000, ErrPriceAboveMax}, {"quote above", 100000, 110000, 100000, ErrPriceAboveMax}, {"paid allowed", 110000, 100000, 110000, nil}, {"unpriced", 110000, 0, 110000, ErrUnpriced},
		{"negative", 0, 0, -1, core.ErrInvalidInput}, {"nan", 0, 0, math.NaN(), core.ErrInvalidInput}, {"infinite", 0, 0, math.Inf(1), core.ErrInvalidInput},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &emailOrderServer{send: freeEmailSend(t), packages: `[{"id":"p","name":"p","amount":20,"price":` + jsonNumber(tc.list) + `}]`, price: `{"optimumPrice":` + jsonNumber(tc.quote) + `}`}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", PackageID: "p", MaxPrice: tc.max, NoWait: true})
			if tc.want == nil {
				if err != nil || out == nil || s.mutations.Load() != 1 {
					t.Fatalf("out=%+v err=%v", out, err)
				}
			} else if out != nil || !errors.Is(err, tc.want) || s.mutations.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if errors.Is(tc.want, core.ErrInvalidInput) && s.reads.Load() != 0 {
				t.Fatal("invalid budget made requests")
			}
		})
	}
}

func TestCreateNotificationQuotaEligibilityReread(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		initial    bool
	}{
		{"existing", emailSnapshot(t, "ACTIVE", "p"), true},
		{"appeared", emailSnapshot(t, "ACTIVE", "p"), false},
		{"auto renew", changedEmailFixture(t, map[string]any{"autoRenew": 1}), false},
		{"poc", changedEmailFixture(t, map[string]any{"poc": 1}), false},
		{"invalid flag", changedEmailFixture(t, map[string]any{"poc": 2}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &emailOrderServer{send: freeEmailSend(t), quota: func(n int32) string {
				if tc.initial || n >= 2 {
					return tc.body
				}
				return ""
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
			if out != nil || err == nil || s.mutations.Load() != 0 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if tc.initial && s.quotes.Load() != 0 {
				t.Fatal("quoted existing quota")
			}
		})
	}
}

func TestCreateNotificationQuotaPaymentPending(t *testing.T) {
	for _, payment := range []string{"", emailRedirect, "https://private-canary/checkout"} {
		t.Run(payment, func(t *testing.T) {
			s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"orderId": nil, "paymentUrl": payment, "amount": nil})
			}}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			pending := payment != "" && payment != emailRedirect
			if out == nil || out.PaymentPending != pending || errors.Is(err, dns.ErrNotSettled) != pending {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "private-canary") || (err != nil && strings.Contains(err.Error(), "private-canary")) {
				t.Fatal("payment URL leaked")
			}
		})
	}
}

func TestCreateNotificationQuotaMalformedSuccessRemainsUnresolved(t *testing.T) {
	for _, body := range []string{`{`, `{}`, `{"orderId":{},"paymentUrl":"private-canary"}`, `{"orderId":null,"paymentUrl":"","extra":"` + strings.Repeat("x", quotaMaxBody) + `"}`} {
		s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }}
		c := New(testutil.NewRetryConfig(t, s.handler(t)))
		out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		var api *core.APIError
		if out == nil || !errors.Is(err, dns.ErrNotSettled) || !errors.As(err, &api) || strings.Contains(err.Error(), "private-canary") {
			t.Fatalf("out=%+v err=%v", out, err)
		}
		_, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
		if !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
			t.Fatalf("err=%v sends=%d", err, s.mutations.Load())
		}
	}
}

func changedEmailFixture(t *testing.T, changes map[string]any) string {
	t.Helper()
	return string(quotaFixture(t, "GetNotificationQuotaEmail", func(fields map[string]json.RawMessage) {
		for k, v := range changes {
			fields[k], _ = json.Marshal(v)
		}
	}))
}

func TestCreateNotificationQuotaOnce401DoesNotRefresh(t *testing.T) {
	source := &monitorRefreshTokens{first: "synthetic-token-A", second: "synthetic-token-B"}
	s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = io.WriteString(w, `{"message":"synthetic-token-A private-canary"}`)
	}}
	server := httptest.NewServer(s.handler(t))
	t.Cleanup(server.Close)
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 3, RetryInterval: time.Millisecond})))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if out != nil || !errors.Is(err, core.ErrAuth) || s.mutations.Load() != 1 || source.calls != 1 {
		t.Fatalf("out=%+v err=%v sends=%d token calls=%d", out, err, s.mutations.Load(), source.calls)
	}
	assertMonitorCredentialsAbsent(t, out, err, "synthetic-token-A", "synthetic-token-B", "private-canary")
}

func TestCreateNotificationQuotaSentCredentialsAndCaptures(t *testing.T) {
	token := "synthetic-token-A"
	s := &emailOrderServer{send: func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"orderId": token, "paymentUrl": "https://private-canary/" + token, "amount": nil})
	}}
	server := httptest.NewServer(s.handler(t))
	t.Cleanup(server.Close)
	captures := atomic.Int32{}
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &monitorRefreshTokens{first: token}, Capture: func(transport.Capture) { captures.Add(1) }})))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if out == nil || !out.PaymentPending || !errors.Is(err, dns.ErrNotSettled) || captures.Load() != 0 {
		t.Fatalf("out=%+v err=%v captures=%d", out, err, captures.Load())
	}
	assertMonitorCredentialsAbsent(t, out, err, token, "private-canary")
}

func TestCreateNotificationQuotaFreezesInputBeforePreparation(t *testing.T) {
	in := &CreateNotificationQuotaInput{Kind: "Email", PackageID: "p", NoWait: true}
	s := &emailOrderServer{send: freeEmailSend(t)}
	handler := s.handler(t)
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/billing-api/v2/email/prices/created-price" {
			in.Kind = "SMS"
			in.PackageID = "other"
			in.NoWait = false
		}
		handler(w, r)
	})))
	out, err := c.CreateNotificationQuota(context.Background(), in)
	if err != nil || out == nil || s.mutations.Load() != 1 || s.reads.Load() != 2 {
		t.Fatalf("out=%+v err=%v", out, err)
	}
}

func TestCreateNotificationQuotaRereadUnverified404Refuses(t *testing.T) {
	s := &emailOrderServer{send: freeEmailSend(t)}
	handler := s.handler(t)
	c := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == emailQuotaPath && s.reads.Load() == 1 {
			s.reads.Add(1)
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"message":"unknown route"}`)
			return
		}
		handler(w, r)
	})))
	out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email"})
	if out != nil || !errors.Is(err, core.ErrNotFound) || s.mutations.Load() != 0 || s.reads.Load() != 2 {
		t.Fatalf("out=%+v err=%v sends=%d reads=%d", out, err, s.mutations.Load(), s.reads.Load())
	}
}

func TestCreateNotificationQuotaOperationCredentialRedaction(t *testing.T) {
	const first, second = "synthetic-token-A", "synthetic-token-B"
	for _, stage := range []string{"order", "settlement", "settlement error", "settlement decode", "reconcile"} {
		t.Run(stage, func(t *testing.T) {
			s := &emailOrderServer{send: func(w http.ResponseWriter, _ *http.Request) {
				orderID := "order-1"
				if stage == "order" || stage == "reconcile" {
					orderID = first
				}
				_, _ = io.WriteString(w, `{"orderId":`+jsonString(orderID)+`,"paymentUrl":`+jsonString(emailRedirect)+`}`)
			}, quota: func(n int32) string {
				if n <= 2 {
					return ""
				}
				return changedEmailFixture(t, map[string]any{"name": first + " " + second, "packageId": "p"})
			}}
			handler := s.handler(t)
			c := monitorRefreshClient(t, first, second, "/billing-api/v1/email/packages", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == emailQuotaPath && s.mutations.Load() > 0 {
					if stage == "settlement error" {
						w.WriteHeader(403)
						_, _ = io.WriteString(w, `{"message":`+jsonString(first)+`}`)
						return
					}
					if stage == "settlement decode" {
						_, _ = io.WriteString(w, `{"name":`+jsonString(first))
						return
					}
				}
				handler(w, r)
			}))
			emailFakeClock(c)
			in := &CreateNotificationQuotaInput{Kind: "Email", NoWait: stage == "order" || stage == "reconcile"}
			out, err := c.CreateNotificationQuota(context.Background(), in)
			if stage == "reconcile" {
				assertMonitorCredentialsAbsent(t, out, err, first, second)
				in.NoWait = false
				out, err = c.CreateNotificationQuota(context.Background(), in)
			}
			if out == nil || s.mutations.Load() != 1 {
				t.Fatalf("out=%v err=%v sends=%d", out, err, s.mutations.Load())
			}
			if strings.Contains(stage, "error") || strings.Contains(stage, "decode") {
				if !errors.Is(err, dns.ErrNotSettled) {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if stage != "order" && !strings.Contains(stage, "error") && !strings.Contains(stage, "decode") && out.Quota == nil {
				t.Fatal("missing settlement")
			}
			assertMonitorCredentialsAbsent(t, out, err, first, second)
		})
	}
}

// Expired tokens force acquisition at each request, including the mutation.
type quotaPreSendTokens struct {
	calls   int
	failure error
}

func (p *quotaPreSendTokens) Token(context.Context) (transport.Token, error) {
	p.calls++
	if p.calls == 5 {
		return transport.Token{}, p.failure
	}
	return transport.Token{AccessToken: "synthetic-token", ExpiresAt: time.Unix(1, 0)}, nil
}
func (*quotaPreSendTokens) Invalidate(string) {}

func TestCreateNotificationQuotaAuthBeforeSend(t *testing.T) {
	for _, failure := range []error{
		&core.LoginError{Err: core.ErrAuth, Reason: "login failed"},
		&core.ServiceAccountLoginError{Err: core.ErrAuth, Reason: "token request failed"},
		core.ErrTokenCache, core.ErrAuth, core.ErrInvalidConfig,
		context.Canceled, context.DeadlineExceeded,
		errors.New("private-provider-canary"),
	} {
		t.Run(failure.Error(), func(t *testing.T) {
			s := &emailOrderServer{send: freeEmailSend(t)}
			server := httptest.NewServer(s.handler(t))
			defer server.Close()
			source := &quotaPreSendTokens{failure: failure}
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 3})))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if out != nil || err == nil || errors.Is(err, dns.ErrNotSettled) || len(c.quotaAttempts) != 0 || s.mutations.Load() != 0 || source.calls != 5 {
				t.Errorf("out=%+v err=%v attempts=%d sends=%d tokens=%d", out, err, len(c.quotaAttempts), s.mutations.Load(), source.calls)
			}
			if monitorReadSDKError(failure) && err != failure { //nolint:errorlint // Safe pre-send errors must pass through unchanged.
				t.Errorf("identity lost: got %v, want %v", err, failure)
			}
			if strings.Contains(err.Error(), "private-provider-canary") {
				t.Error("provider text exposed")
			}
			out, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if err != nil || out == nil || s.mutations.Load() != 1 {
				t.Fatalf("recovered out=%+v err=%v sends=%d", out, err, s.mutations.Load())
			}
		})
	}
}

func TestCreateNotificationQuotaAPIErrorBeforeSend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
	}{
		{name: "empty token"},
		{name: "provider APIError", failure: &transport.APIError{StatusCode: 503, Message: "private-provider-canary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &emailOrderServer{send: freeEmailSend(t)}
			server := httptest.NewServer(s.handler(t))
			defer server.Close()
			source := &quotaPreSendTokens{failure: tc.failure}
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source, RetryCount: 3})))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if out != nil || err == nil || errors.Is(err, dns.ErrNotSettled) || len(c.quotaAttempts) != 0 || s.mutations.Load() != 0 || source.calls != 5 {
				t.Errorf("out=%+v err=%v attempts=%d sends=%d tokens=%d", out, err, len(c.quotaAttempts), s.mutations.Load(), source.calls)
			}
			if err != nil && strings.Contains(err.Error(), "private-provider-canary") {
				t.Error("provider text exposed")
			}
			out, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if err != nil || out == nil || s.mutations.Load() != 1 {
				t.Fatalf("recovered out=%+v err=%v sends=%d", out, err, s.mutations.Load())
			}
		})
	}
}

type quotaInvalidateTokens struct{ failure error }

func (*quotaInvalidateTokens) Token(context.Context) (transport.Token, error) {
	return transport.Token{AccessToken: "synthetic-token", ExpiresAt: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
}
func (*quotaInvalidateTokens) Invalidate(string)                                 {}
func (p *quotaInvalidateTokens) InvalidateContext(context.Context, string) error { return p.failure }

func TestCreateNotificationQuotaAuthAfterSendRemainsUnresolved(t *testing.T) {
	for _, failure := range []error{context.Canceled, core.ErrTokenCache, &core.LoginError{Err: core.ErrAuth, Reason: "login failed"}} {
		t.Run(failure.Error(), func(t *testing.T) {
			s := &emailOrderServer{send: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }}
			server := httptest.NewServer(s.handler(t))
			defer server.Close()
			source := &quotaInvalidateTokens{failure: failure}
			c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{Monitor: server.URL + "/"}, transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: source})))
			out, err := c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if out == nil || !errors.Is(err, dns.ErrNotSettled) || len(c.quotaAttempts) != 1 || s.mutations.Load() != 1 {
				t.Fatalf("out=%+v err=%v attempts=%d sends=%d", out, err, len(c.quotaAttempts), s.mutations.Load())
			}
			_, err = c.CreateNotificationQuota(context.Background(), &CreateNotificationQuotaInput{Kind: "Email", NoWait: true})
			if !errors.Is(err, dns.ErrNotSettled) || s.mutations.Load() != 1 {
				t.Fatalf("next err=%v sends=%d", err, s.mutations.Load())
			}
		})
	}
}
