package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATOrderReplies(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		valid  bool
	}{
		{"accepted", 201, `{"code":0,"success":true}`, true},
		{"wrong status", 200, `{"code":0,"success":true}`, false},
		{"code nonzero", 201, `{"code":1,"success":true}`, false},
		{"false success", 201, `{"code":0,"success":false,"message":"server-secret-canary"}`, false},
		{"string code", 201, `{"code":"0","success":true}`, false},
		{"missing code", 201, `{"success":true}`, false},
		{"null code", 201, `{"code":null,"success":true}`, false},
		{"malformed", 201, `{"code":0,"success":true`, false},
		{"duplicate", 201, `{"code":0,"code":0,"success":true}`, false},
		{"case variant", 201, `{"code":0,"Success":true}`, false},
		{"unauthorized", 401, `{"code":"token-secret-canary","message":"server-secret-canary"}`, false},
		{"rate limited", 429, `{"message":"server-secret-canary"}`, false},
		{"server failure", 500, `{"message":"server-secret-canary"}`, false},
		{"redirect", 302, `{}`, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			redirects := 0
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirect-target" {
					redirects++
					return
				}
				if strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodPost {
					s.orders++
					s.ordered = true
					w.Header().Set("Location", "/redirect-target")
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
					return
				}
				s.serve(t, w, r)
			}))
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			if tt.valid {
				if err != nil || s.puts != 1 {
					t.Fatalf("%v", err)
				}
			} else {
				if !errors.Is(err, ErrNotSettled) || s.puts != 0 || out.NATInstance == nil || out.AutoRenew == nil || !*out.AutoRenew {
					t.Fatalf("%v, output %+v, puts %d", err, out, s.puts)
				}
			}
			if !tt.valid && strings.Contains(err.Error(), "write accepted") {
				t.Fatal("uncertain reply claimed acceptance")
			}
			if s.orders != 1 || redirects != 0 {
				t.Fatalf("orders %d, redirects %d", s.orders, redirects)
			}
		})
	}
}

func TestNATWriteTransportFailures(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		for _, dial := range []bool{false, true} {
			t.Run(method+map[bool]string{false: " lost reply", true: " dial failure"}[dial], func(t *testing.T) {
				s := new(natScenario)
				if method == http.MethodDelete {
					s.ordered = true
				}
				attempts := 0
				rt := natRoundTripper(func(r *http.Request) (*http.Response, error) {
					target := (method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats")) || (method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/autoRenew")) || (method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/nats/nat-1"))
					if r.Method == method && target {
						attempts++
						if method == http.MethodPost && !dial {
							s.ordered = true
						}
						if method == http.MethodDelete && !dial {
							s.ordered = false
						}
						if dial {
							return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("transport-secret-canary")}
						}
						return nil, errors.New("transport-secret-canary")
					}
					return http.DefaultTransport.RoundTrip(r)
				})
				c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(t, w, r) }), vngcloud.WithTransport(rt))
				originalSleep := c.sleep
				c.sleep = func(ctx context.Context, d time.Duration) error { return originalSleep(ctx, max(d, 16*time.Minute)) }
				var err error
				if method == http.MethodDelete {
					_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
				} else {
					_, err = c.CreateNATInstance(context.Background(), natTestInput())
				}
				if (method == http.MethodDelete && !dial && err != nil) || ((method != http.MethodDelete || dial) && !errors.Is(err, ErrNotSettled)) || attempts != 1 || (err != nil && strings.Contains(err.Error(), "secret-canary")) {
					t.Fatalf("%v, attempts %d", err, attempts)
				}
			})
		}
	}
}
func TestNATCreateReconciliation(t *testing.T) {
	for _, tt := range []struct {
		name, row string
		want      error
	}{
		{"ERROR", strings.Replace(natTestRow, `"ACTIVE"`, `"ERROR"`, 1), ErrFailed},
		{"wrong package", strings.Replace(natTestRow, "package-1", "package-2", 1), ErrNotSettled},
		{"wrong VPC", strings.Replace(natTestRow, "vpc-1", "vpc-2", 1), ErrNotSettled},
		{"wrong scope", strings.Replace(natTestRow, "66b500000000000000000001", "zone-2", 1), ErrNotSettled},
		{"unknown status", strings.Replace(natTestRow, `"ACTIVE"`, `"FUTURE"`, 1), ErrNotSettled},
		{"absent", "", ErrNotSettled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if s.ordered && strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodGet {
					_, _ = w.Write([]byte(natInventory(tt.row)))
					return
				}
				s.serve(t, w, r)
			}))
			sleep := c.sleep
			c.sleep = func(ctx context.Context, d time.Duration) error { return sleep(ctx, max(d, 16*time.Minute)) }
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			if !errors.Is(err, tt.want) || s.orders != 1 || s.puts != 0 || out.TotalPrice != 100 {
				t.Fatalf("%v, %+v", err, out)
			}
			if tt.name == "ERROR" && (out.NATInstance == nil || out.NATInstance.Status != "ERROR") {
				t.Fatal("missing partial resource")
			}
		})
	}
	t.Run("ambiguous", func(t *testing.T) {
		s := new(natScenario)
		c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.ordered && strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodGet {
				body := `{"success":true,"page":1,"size":1000,"totalPage":1,"total":2,"data":[` + natTestRow + `,` + strings.Replace(natTestRow, "nat-1", "nat-2", 1) + `]}`
				_, _ = w.Write([]byte(body))
				return
			}
			s.serve(t, w, r)
		}))
		out, err := c.CreateNATInstance(context.Background(), natTestInput())
		if !errors.Is(err, ErrNotSettled) || s.puts != 0 || out.NATInstance != nil {
			t.Fatalf("%v %+v", err, out)
		}
	})
	t.Run("read failure", func(t *testing.T) {
		s := new(natScenario)
		c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.ordered && strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodGet {
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"message":"server-secret-canary"}`))
				return
			}
			s.serve(t, w, r)
		}))
		_, err := c.CreateNATInstance(context.Background(), natTestInput())
		if !errors.Is(err, ErrNotSettled) || s.orders != 1 || s.puts != 0 {
			t.Fatal(err)
		}
	})
}
func TestNATBillingAndRenewal(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(string) string
		manual bool
		valid  bool
	}{
		{"already MANUAL", nil, true, true},
		{"AUTO-RENEW", nil, false, true},
		{"wrong SKU", func(s string) string { return strings.Replace(s, "nat.s-standard", "other", 1) }, false, false},
		{"wrong quantity", func(s string) string { return strings.Replace(s, `"quantity":1`, `"quantity":2`, 1) }, false, false},
		{"POSTPAID", func(s string) string { return strings.Replace(s, "PREPAID", "POSTPAID", 1) }, false, false},
		{"wrong status", func(s string) string { return strings.Replace(s, `"status":"active"`, `"status":"expired"`, 1) }, false, false},
		{"missing channel", func(s string) string { return strings.Replace(s, `"channel":7,`, "", 1) }, false, false},
		{"renewing", func(s string) string { return strings.Replace(s, `"channel":7`, `"channel":7,"isRenewing":true`, 1) }, false, false},
		{"absent isRenewing", nil, false, true},
		{"null isRenewing", func(s string) string { return strings.Replace(s, `"channel":7`, `"channel":7,"isRenewing":null`, 1) }, false, true},
		{"unknown renewal", func(s string) string { return strings.Replace(s, "AUTO-RENEW", "FUTURE", 1) }, false, false},
		{"wrong renewal period", func(s string) string { return strings.Replace(s, `"renewPeriod":1`, `"renewPeriod":3`, 1) }, false, false},
		{"MANUAL missing period", func(s string) string { return strings.Replace(s, `,"renewPeriod":null`, "", 1) }, true, false},
		{"MANUAL numeric period", func(s string) string { return strings.Replace(s, `"renewPeriod":null`, `"renewPeriod":0`, 1) }, true, false},
		{"case alias", func(s string) string { return strings.Replace(s, `"renewType"`, `"RenewType"`, 1) }, false, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			s := &natScenario{manual: tt.manual}
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/gateway/api/v1/resources" {
					body := natBilling(s.manual)
					if tt.mutate != nil {
						body = tt.mutate(body)
					}
					_, _ = w.Write([]byte(body))
					return
				}
				s.serve(t, w, r)
			}))
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			if tt.valid {
				wantPuts := 1
				if tt.manual {
					wantPuts = 0
				}
				if err != nil || s.puts != wantPuts || out.AutoRenew == nil || *out.AutoRenew {
					t.Fatalf("%v %+v, puts %d", err, out, s.puts)
				}
			} else if !errors.Is(err, ErrNotSettled) || s.puts != 0 {
				t.Fatalf("%v, puts %d", err, s.puts)
			}
		})
	}
	t.Run("billing appears later", func(t *testing.T) {
		s := new(natScenario)
		reads := 0
		c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/gateway/api/v1/resources" {
				reads++
				if reads == 1 {
					_, _ = w.Write([]byte(`{"code":200,"data":{"data":[]}}`))
					return
				}
			}
			s.serve(t, w, r)
		}))
		_, err := c.CreateNATInstance(context.Background(), natTestInput())
		if err != nil || reads != 3 || s.puts != 1 {
			t.Fatalf("%v reads %d", err, reads)
		}
	})
	for _, tt := range []struct {
		name   string
		status int
		body   string
		manual bool
	}{
		{"PUT failure", 500, `{"message":"server-secret-canary"}`, false},
		{"PUT 401", 401, `{}`, false},
		{"PUT 429", 429, `{}`, false},
		{"PUT redirect", 302, `{}`, false},
		{"PUT malformed", 200, `{`, false},
		{"PUT rejected", 200, `{"code":200,"data":{"successAll":false,"errorAutoRenewResources":[{"message":"server-secret-canary"}]}}`, false},
		{"PUT failure but manual", 500, `{}`, true},
		{"PUT accepted stays AUTO", 200, `{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/autoRenew") {
					s.puts++
					s.manual = tt.manual
					w.Header().Set("Location", "/gateway/api/v1/resources/autoRenew")
					w.WriteHeader(tt.status)
					_, _ = w.Write([]byte(tt.body))
					return
				}
				s.serve(t, w, r)
			}))
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			if !errors.Is(err, ErrNotSettled) || s.puts != 1 || s.orders != 1 || out.AutoRenew == nil || *out.AutoRenew == tt.manual {
				t.Fatalf("%v %+v puts %d", err, out, s.puts)
			}
		})
	}
}
func TestNATSecrets(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "success"}[success], func(t *testing.T) {
			s := new(natScenario)
			var logs bytes.Buffer
			var captures []vngcloud.ResponseCapture
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !success && strings.HasSuffix(r.URL.Path, "/nats") && r.Method == http.MethodPost {
					s.orders++
					s.ordered = true
					w.WriteHeader(201)
					_, _ = w.Write([]byte(`{"code":"synthetic-token portal-secret-canary 123456","success":false,"message":"server-secret-canary"}`))
					return
				}
				s.serve(t, w, r)
			}), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), vngcloud.WithResponseCapture(func(c vngcloud.ResponseCapture) { captures = append(captures, c) }))
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			if success && err != nil {
				t.Fatal(err)
			}
			if !success && !errors.Is(err, ErrNotSettled) {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(struct {
				Out      *CreateNATInstanceOutput
				Err      error
				Captures []vngcloud.ResponseCapture
			}{out, err, captures})
			all := string(encoded) + logs.String()
			if err != nil {
				all += err.Error()
			}
			for _, marker := range []string{"synthetic-token", "portal-secret-canary", "server-secret-canary", "123456"} {
				if strings.Contains(all, marker) {
					t.Fatalf("leaked %s", marker)
				}
			}
			if len(captures) != 0 {
				t.Fatal("NAT or billing response captured")
			}
		})
	}
}

func TestNATContextAfterSend(t *testing.T) {
	s := new(natScenario)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
			s.orders++
			cancel()
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"code":0,"success":true}`)
			return
		}
		s.serve(t, w, r)
	}))
	out, err := c.CreateNATInstance(ctx, natTestInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || s.orders != 1 || s.puts != 0 {
		t.Fatalf("%v %+v", err, out)
	}
}

func TestNATWaitBounds(t *testing.T) {
	for _, kind := range []string{"create", "renewal", "delete"} {
		t.Run(kind, func(t *testing.T) {
			s := new(natScenario)
			if kind == "delete" {
				s.ordered = true
			}
			reads := 0
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "renewal" && r.Method == http.MethodPut {
					s.puts++
					_, _ = w.Write([]byte(`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`))
					return
				}
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats") && ((kind == "create" && s.ordered) || (kind == "delete" && s.deletes > 0)) {
					reads++
					_, _ = w.Write([]byte(natInventory(strings.Replace(natTestRow, `"ACTIVE"`, `"PROVISIONING"`, 1))))
					return
				}
				s.serve(t, w, r)
			}))
			started := c.now()
			var err error
			if kind == "delete" {
				_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
			} else {
				_, err = c.CreateNATInstance(context.Background(), natTestInput())
			}
			want := 15 * time.Minute
			wantReads := 180
			if kind == "renewal" {
				want = 30 * time.Second
				wantReads = 0
			}
			if kind == "delete" {
				want = 10 * time.Minute
				wantReads = 120
			}
			if !errors.Is(err, ErrNotSettled) || c.now().Sub(started) != want || reads != wantReads {
				t.Fatalf("%v elapsed %s, reads %d", err, c.now().Sub(started), reads)
			}
		})
	}
}

func TestNATSlowReadCannotAuthorizeRenewal(t *testing.T) {
	s := new(natScenario)
	var advance func()
	billingReads := 0
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.ordered && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats") {
			advance()
		}
		if r.URL.Path == "/gateway/api/v1/resources" {
			billingReads++
		}
		s.serve(t, w, r)
	}))
	now := c.now()
	c.now = func() time.Time { return now }
	advance = func() { now = now.Add(16 * time.Minute) }
	out, err := c.CreateNATInstance(context.Background(), natTestInput())
	if !errors.Is(err, ErrNotSettled) || s.puts != 0 || billingReads != 0 || out.NATInstance == nil {
		t.Fatalf("%v, billing reads %d, puts %d", err, billingReads, s.puts)
	}
}

func TestNATAccountFailureDoesNotPut(t *testing.T) {
	s := new(natScenario)
	accountReads := 0
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/user-info") {
			accountReads++
			_, _ = w.Write([]byte(`{"code":200,"data":{"userId":123456,"accountId":654321}}`))
			return
		}
		s.serve(t, w, r)
	}))
	out, err := c.CreateNATInstance(context.Background(), natTestInput())
	if !errors.Is(err, ErrNotSettled) || accountReads != 1 || s.puts != 0 || out.AutoRenew == nil || !*out.AutoRenew {
		t.Fatalf("%v, output %+v, PUTs %d", err, out, s.puts)
	}
}
