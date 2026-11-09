package compute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

// These populated servers and rules are synthetic, not live responses.
const floatingSafeRules = `{"data":[{"id":"rule-1","direction":"ingress","etherType":"IPv4","protocol":"TCP","portRangeMin":234,"portRangeMax":234,"remoteIpPrefix":"192.0.2.0/24","remoteGroupId":"sg-1","status":"ACTIVE","description":"","createdAt":""}]}`

func floatingCreateHandler(t *testing.T, rules func() string, order func(http.ResponseWriter, *http.Request), get func(http.ResponseWriter, *http.Request)) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/secGroupRules"):
			_, _ = w.Write([]byte(rules()))
		case strings.Contains(r.URL.Path, "/secgroups/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			_, _ = fmt.Fprintf(w, `{"data":{"id":%q,"status":"ACTIVE"}}`, id)
		case strings.HasSuffix(r.URL.Path, "/price"):
			var body struct {
				ResourceType string `json:"resourceType"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.ResourceType == "elastic-ip" {
				raw, err := os.ReadFile("../testdata/pricing/quote_elastic_ip_zone.json")
				if err != nil {
					t.Error(err)
				}
				_, _ = w.Write(raw)
			} else {
				_, _ = w.Write([]byte(quoteServerFixture))
			}
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/servers"):
			_, _ = w.Write([]byte(emptyListServersPage))
		case r.Method == http.MethodPost:
			order(w, r)
		case get != nil:
			get(w, r)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
}

func TestFloatingCreateExplicitBooleanAndConservativeCap(t *testing.T) {
	for _, tc := range []struct {
		attach bool
		cap    float64
		orders int64
	}{
		{false, 347800, 1}, {true, 347800, 0}, {true, 467800, 1},
	} {
		t.Run(fmt.Sprint(tc.attach, tc.cap), func(t *testing.T) {
			var orders atomic.Int64
			c := newTestClient(t, floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, r *http.Request) {
				orders.Add(1)
				body := decodeComputeBody(t, r)
				if body["attachFloating"] != tc.attach {
					t.Errorf("attachFloating = %v", body["attachFloating"])
				}
				if _, ok := body["allowOpenLogin"]; ok {
					t.Error("override on wire")
				}
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, nil))
			in := validCreateServerInput()
			in.AttachFloatingIP = tc.attach
			in.MaxPrice = tc.cap
			in.NoWait = true
			out, err := c.CreateServer(context.Background(), in)
			if tc.orders == 0 {
				if !errors.Is(err, vngcloud.ErrPriceAboveMax) || out != nil {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else if err != nil || out.MonthlyPrice != tc.cap || out.Server.PublicIP != "" {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if orders.Load() != tc.orders {
				t.Fatalf("orders=%d", orders.Load())
			}
		})
	}
}

func TestFloatingCreateExposureAndChangedSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name          string
		rules         string
		allow, change bool
		want          bool
	}{
		{"narrow", floatingSafeRules, false, false, true},
		{"open", strings.ReplaceAll(floatingSafeRules, "192.0.2.0/24", "0.0.0.0/0"), false, false, false},
		{"override", strings.ReplaceAll(floatingSafeRules, "192.0.2.0/24", "0.0.0.0/0"), true, false, true},
		{"changed", floatingSafeRules, false, true, false},
		{"changed override", floatingSafeRules, true, true, false},
		{"null data", `{"data":null}`, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads, orders atomic.Int64
			c := newTestClient(t, floatingCreateHandler(t, func() string {
				n := reads.Add(1)
				if tc.change && n > 1 {
					return strings.ReplaceAll(tc.rules, "192.0.2.0/24", "198.51.100.0/24")
				}
				return tc.rules
			}, func(w http.ResponseWriter, _ *http.Request) {
				orders.Add(1)
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, nil))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.AllowOpenLogin = tc.allow
			in.MaxPrice = 467800
			in.NoWait = true
			out, err := c.CreateServer(context.Background(), in)
			if tc.want {
				if err != nil || out == nil || orders.Load() != 1 {
					t.Fatalf("out=%v err=%v orders=%d", out, err, orders.Load())
				}
			} else if err == nil || out != nil || orders.Load() != 0 {
				t.Fatalf("unsafe write out=%v err=%v", out, err)
			}
		})
	}
}

func TestFloatingCreateInvalidOptInAndZoneBeforeRequests(t *testing.T) {
	for _, mutate := range []func(*CreateServerInput){
		func(in *CreateServerInput) { in.AllowOpenLogin = true },
		func(in *CreateServerInput) { in.AttachFloatingIP = true; in.ZoneID = "../bad" },
		func(in *CreateServerInput) {
			in.AttachFloatingIP = true
			in.SecurityGroupIDs = make([]string, 65)
			for i := range in.SecurityGroupIDs {
				in.SecurityGroupIDs[i] = fmt.Sprintf("sg-%d", i)
			}
		},
	} {
		c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid input sent a request") }))
		in := validCreateServerInput()
		mutate(in)
		if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestFloatingCreateWaitsForAddressAndSafeID(t *testing.T) {
	var reads atomic.Int64
	c := withInstantSleep(newTestClient(t, floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
	}, func(w http.ResponseWriter, _ *http.Request) {
		n := reads.Add(1)
		ip, id := "", ""
		if n == 2 {
			ip, id = "192.0.2.9", "../bad"
		}
		if n >= 3 {
			ip, id = "192.0.2.9", "ip-1"
		}
		_, _ = fmt.Fprintf(w, `{"data":{"uuid":"server-1","status":"ACTIVE","internalInterfaces":[{"uuid":"nic-1","floatingIp":%q,"floatingIpId":%q}]}}`, ip, id)
	})))
	in := validCreateServerInput()
	in.AttachFloatingIP = true
	in.MaxPrice = 467800
	out, err := c.CreateServer(context.Background(), in)
	if err != nil || out == nil || out.Server.PublicIP != "192.0.2.9" || reads.Load() != 3 {
		t.Fatalf("out=%v err=%v reads=%d", out, err, reads.Load())
	}
}

func TestFloatingCreateSendsOneOrderForEveryStatus(t *testing.T) {
	for _, status := range []int{202, 200, 204, 307, 308, 400, 401, 403, 404, 409, 429, 500, 502} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var orders atomic.Int64
			cfg := testutil.NewRetryConfig(t, floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
				orders.Add(1)
				if status == 307 || status == 308 {
					w.Header().Set("Location", "/v2/project-1/servers")
				}
				w.WriteHeader(status)
				if status == 202 {
					_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
				} else if status != 204 {
					_, _ = w.Write([]byte(`{"message":"synthetic failure"}`))
				}
			}, nil))
			c := New(cfg)
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.MaxPrice = 467800
			in.NoWait = true
			out, err := c.CreateServer(context.Background(), in)
			if orders.Load() != 1 {
				t.Fatalf("order attempts=%d", orders.Load())
			}
			if status == 202 {
				if err != nil || out == nil {
					t.Fatalf("out=%v err=%v", out, err)
				}
			} else if err == nil {
				t.Fatal("unexpected status accepted")
			}
			if status >= 400 {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status {
					t.Fatalf("lost API status: %v", err)
				}
			}
		})
	}
}

func TestFloatingCreateAddressWaitFailureKeepsPriceAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		failed bool
	}{
		{"timeout", 200, `{"data":{"uuid":"server-1","status":"ACTIVE"}}`, false},
		{"error", 200, `{"data":{"uuid":"server-1","status":"ERROR"}}`, true},
		{"read error", 401, `{"message":"synthetic failure"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int64
			c := withInstantSleep(newTestClient(t, floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, func(w http.ResponseWriter, _ *http.Request) {
				if reads.Add(1) == 1 {
					_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
					return
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.MaxPrice = 467800
			out, err := c.CreateServer(context.Background(), in)
			want := ErrNotSettled
			if tc.failed {
				want = ErrFailed
			}
			if !errors.Is(err, want) || out == nil || out.MonthlyPrice != 467800 || out.Server.UUID != "server-1" {
				t.Fatalf("out=%v err=%v", out, err)
			}
			if tc.status == 401 {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != 401 {
					t.Fatalf("lost read cause: %v", err)
				}
			}
		})
	}
}

func TestFloatingCreateRuleLimitCannotBeOverridden(t *testing.T) {
	rows := `{"data":[` + strings.Repeat(`{},`, 4096) + `{}]}`
	var orders atomic.Int64
	c := newTestClient(t, floatingCreateHandler(t, func() string { return rows }, func(http.ResponseWriter, *http.Request) { orders.Add(1) }, nil))
	in := validCreateServerInput()
	in.AttachFloatingIP = true
	in.AllowOpenLogin = true
	in.MaxPrice = 467800
	out, err := c.CreateServer(context.Background(), in)
	if err == nil || out != nil || orders.Load() != 0 {
		t.Fatalf("out=%v err=%v orders=%d", out, err, orders.Load())
	}
}

func TestFloatingCreateExposureAggregatesAcrossGroupsAndDeduplicates(t *testing.T) {
	var ruleReads, orders atomic.Int64
	base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(http.ResponseWriter, *http.Request) { orders.Add(1) }, nil)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/secGroupRules") {
			ruleReads.Add(1)
			prefix := "0.0.0.0/1"
			if strings.Contains(r.URL.Path, "sg-2") {
				prefix = "128.0.0.0/1"
			}
			_, _ = w.Write([]byte(strings.ReplaceAll(floatingSafeRules, "192.0.2.0/24", prefix)))
			return
		}
		base.ServeHTTP(w, r)
	}))
	in := validCreateServerInput()
	in.AttachFloatingIP = true
	in.SecurityGroupIDs = []string{"sg-1", "sg-1", "sg-2"}
	in.MaxPrice = 467800
	out, err := c.CreateServer(context.Background(), in)
	if !errors.Is(err, ErrOpenLogin) || out != nil || orders.Load() != 0 || ruleReads.Load() != 2 {
		t.Fatalf("out=%v err=%v reads=%d", out, err, ruleReads.Load())
	}
}

func TestFloatingCreateLastReadCannotPreventLaterRuleChange(t *testing.T) {
	open := false
	c := newTestClient(t, floatingCreateHandler(t, func() string {
		if open {
			return strings.ReplaceAll(floatingSafeRules, "192.0.2.0/24", "0.0.0.0/0")
		}
		return floatingSafeRules
	}, func(w http.ResponseWriter, _ *http.Request) {
		open = true
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
	}, nil))
	in := validCreateServerInput()
	in.AttachFloatingIP = true
	in.MaxPrice = 467800
	in.NoWait = true
	if _, err := c.CreateServer(context.Background(), in); err != nil || !open {
		t.Fatalf("err=%v", err)
	}
}

func TestFloatingCreateConcurrentCallersCanEachSendOnce(t *testing.T) {
	var reads, orders atomic.Int64
	ready := make(chan struct{})
	base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
		orders.Add(1)
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
	}, nil)
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/secGroupRules") {
			n := reads.Add(1)
			if n == 2 {
				close(ready)
			}
			if n <= 2 {
				select {
				case <-ready:
				case <-r.Context().Done():
					return
				}
			}
		}
		base.ServeHTTP(w, r)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.MaxPrice = 467800
			in.NoWait = true
			_, err := c.CreateServer(ctx, in)
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if orders.Load() != 2 {
		t.Fatalf("orders=%d, expected one per independent caller", orders.Load())
	}
}

func TestFloatingCreateAddressCannotAdoptAnotherServer(t *testing.T) {
	c := withInstantSleep(newTestClient(t, floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(202)
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
	}, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-other","status":"ACTIVE","internalInterfaces":[{"floatingIp":"192.0.2.9","floatingIpId":"ip-1"}]}}`))
	})))
	in := validCreateServerInput()
	in.AttachFloatingIP = true
	in.MaxPrice = 467800
	out, err := c.CreateServer(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) || out == nil || out.Server.UUID != "server-1" || out.Server.PublicIP != "" {
		t.Fatalf("out=%v err=%v", out, err)
	}
}

func TestFloatingCreateCredentialHistoryExhaustionPreventsOrder(t *testing.T) {
	var requests, orders atomic.Int64
	base := floatingCreateHandler(t, func() string { return floatingSafeRules }, func(http.ResponseWriter, *http.Request) { orders.Add(1) }, nil)
	c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); base.ServeHTTP(w, r) }), 0)
	in := validCreateServerInput()
	in.AttachFloatingIP = true
	in.MaxPrice = 467800
	in.SecurityGroupIDs = nil
	for i := range 64 {
		in.SecurityGroupIDs = append(in.SecurityGroupIDs, fmt.Sprintf("sg-%d", i))
	}
	out, err := c.CreateServer(context.Background(), in)
	assertNoUnsafeServerOutput(t, out, err)
	if orders.Load() != 0 || requests.Load() != 256 || captures.Load() != 0 || strings.Contains(logs.String(), "synthetic-token-") {
		t.Fatalf("orders=%d requests=%d captures=%d", orders.Load(), requests.Load(), captures.Load())
	}
}

func TestCreateServerExposureKeepsIPv6Family(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefixes []string
		open     bool
	}{
		{"narrow mapped end", []string{"::/80"}, false},
		{"narrow 79", []string{"::/79"}, false},
		{"narrow 81", []string{"::/81"}, false},
		{"narrow 96", []string{"::/96"}, false},
		{"single address", []string{"::/128"}, false},
		{"documentation prefix", []string{"2001:db8::/32"}, false},
		{"half", []string{"::/1"}, false},
		{"anywhere", []string{"::/0"}, true},
		{"two halves", []string{"::/1", "8000::/1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rows []string
			for i, prefix := range tc.prefixes {
				rows = append(rows, fmt.Sprintf(`{"id":"rule-%d","direction":"ingress","etherType":"IPv6","protocol":"tcp","portRangeMin":234,"portRangeMax":234,"remoteIpPrefix":%q,"remoteGroupId":"sg-1"}`, i, prefix))
			}
			rules := `{"data":[` + strings.Join(rows, ",") + `]}`
			var orders atomic.Int64
			c := newTestClient(t, floatingCreateHandler(t, func() string { return rules }, func(w http.ResponseWriter, _ *http.Request) {
				orders.Add(1)
				w.WriteHeader(202)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			}, nil))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.MaxPrice = 467800
			in.NoWait = true
			out, err := c.CreateServer(context.Background(), in)
			if tc.open {
				if !errors.Is(err, ErrOpenLogin) || out != nil || orders.Load() != 0 {
					t.Fatalf("out=%v err=%v orders=%d", out, err, orders.Load())
				}
			} else if err != nil || out == nil || orders.Load() != 1 {
				t.Fatalf("out=%v err=%v orders=%d", out, err, orders.Load())
			}
		})
	}
}

func TestCreateServerExposureRefusalExplainsReasonSafely(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, prefix, want string
		named                        bool
	}{
		{"icmp", "ICMP", "192.0.2.0/24", "unsupported rule encoding", true},
		{"udp", "udp", "192.0.2.0/24", "unsupported rule encoding", true},
		{"other protocol", "sctp", "192.0.2.0/24", "unsupported rule encoding", true},
		{"control text", "bad\nprotocol", "192.0.2.0/24", "unsupported rule encoding", false},
		{"resource-like text", "rule-1", "192.0.2.0/24", "unsupported rule encoding", false},
		{"CIDR-like text", "192.0.2.0/24", "192.0.2.0/24", "unsupported rule encoding", false},
		{"long text", strings.Repeat("x", 17), "192.0.2.0/24", "unsupported rule encoding", false},
		{"internet exposure", "tcp", "0.0.0.0/0", "port open to the internet", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var envelope struct {
				Data []map[string]any `json:"data"`
			}
			if err := json.Unmarshal([]byte(floatingSafeRules), &envelope); err != nil {
				t.Fatal(err)
			}
			envelope.Data[0]["protocol"] = tc.protocol
			envelope.Data[0]["remoteIpPrefix"] = tc.prefix
			envelope.Data[0]["description"] = "hidden-description"
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			var orders atomic.Int64
			c := newTestClient(t, floatingCreateHandler(t, func() string { return string(raw) }, func(http.ResponseWriter, *http.Request) { orders.Add(1) }, nil))
			in := validCreateServerInput()
			in.AttachFloatingIP = true
			in.MaxPrice = 467800
			in.NoWait = true
			out, err := c.CreateServer(context.Background(), in)
			if !errors.Is(err, ErrOpenLogin) || out != nil || orders.Load() != 0 {
				t.Fatalf("out=%v err=%v orders=%d", out, err, orders.Load())
			}
			message := err.Error()
			if !strings.Contains(message, tc.want) {
				t.Fatalf("reason=%q want=%q", message, tc.want)
			}
			if tc.named {
				if !strings.Contains(message, fmt.Sprintf("protocol %q", tc.protocol)) {
					t.Fatalf("missing safe protocol name: %q", message)
				}
			} else if strings.Contains(message, "protocol ") {
				t.Fatalf("untrusted protocol name included: %q", message)
			}
			for _, private := range []string{"sg-1", "rule-1", tc.prefix, "hidden-description"} {
				if strings.Contains(message, private) {
					t.Fatalf("private rule value escaped: %q", message)
				}
			}
			if strings.ContainsAny(message, "\r\n") {
				t.Fatal("control characters escaped")
			}
		})
	}
}
