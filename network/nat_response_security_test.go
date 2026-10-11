package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATRefusalScanDeadline(t *testing.T) {
	s := new(natScenario)
	var clockMu sync.Mutex
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
			s.orders++
			w.WriteHeader(403)
			return
		}
		if s.orders > 0 && strings.HasSuffix(r.URL.Path, "/nats") {
			clockMu.Lock()
			now = now.Add(16 * time.Minute)
			clockMu.Unlock()
		}
		s.serve(t, w, r)
	}))
	c.now = func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return now }
	out, err := c.CreateNATInstance(context.Background(), natTestInput())
	if !errors.Is(err, ErrNotSettled) || out == nil || s.orders != 1 {
		t.Fatalf("deadline not enforced: %v", err)
	}
}

func TestNATReflectedCredential(t *testing.T) {
	for _, stage := range []string{"quote", "baseline", "settlement", "ERROR partial", "read", "delete guard"} {
		for _, escaped := range []bool{false, true} {
			t.Run(stage+map[bool]string{false: " literal", true: " escaped"}[escaped], func(t *testing.T) {
				s := new(natScenario)
				if stage == "delete guard" {
					s.ordered = true
				}
				captures := 0
				c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					tokenJSON, _ := json.Marshal(token)
					if escaped {
						tokenJSON = []byte(strings.Replace(string(tokenJSON), "s", `\u0073`, 1))
					}
					inventory := (stage == "baseline" || stage == "read" || stage == "delete guard" || ((stage == "settlement" || stage == "ERROR partial") && s.ordered)) && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats")
					if inventory {
						row := strings.Replace(natTestRow, `"natName":"example"`, `"natName":"other","publicIp":`+string(tokenJSON), 1)
						if stage == "settlement" || stage == "ERROR partial" {
							row = strings.Replace(row, `"natName":"other"`, `"natName":"example"`, 1)
						}
						if stage == "ERROR partial" {
							row = strings.Replace(row, "ACTIVE", "ERROR", 1)
						}
						_, _ = w.Write([]byte(natInventory(row)))
						return
					}
					if stage == "quote" && strings.HasSuffix(r.URL.Path, "/price") {
						_, _ = w.Write([]byte(strings.Replace(natTestPrice, `"description":"Example"`, `"description":`+string(tokenJSON), 1)))
						return
					}
					s.serve(t, w, r)
				}), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures++ }))
				var out any
				var err error
				switch stage {
				case "read":
					out, err = c.ListNATInstances(context.Background(), &ListNATInstancesInput{ZoneID: natTestInput().ZoneID})
				case "delete guard":
					out, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: natTestInput().ZoneID, VPCID: "vpc-1", NATID: "nat-1"})
				default:
					out, err = c.CreateNATInstance(context.Background(), natTestInput())
				}
				after := stage == "settlement" || stage == "ERROR partial"
				if (after && !errors.Is(err, ErrNotSettled)) || (!after && vngcloud.ErrorCode(err) != "InvalidResponse") {
					t.Fatalf("unexpected result: %v", err)
				}
				encoded, _ := json.Marshal(out)
				if strings.Contains(string(encoded), "synthetic-token") || strings.Contains(err.Error(), "synthetic-token") || captures != 0 || s.puts != 0 || s.deletes != 0 || (after && s.orders != 1) || (!after && s.orders != 0) {
					t.Fatal("reflected credential was exposed or write guard failed")
				}
			})
		}
	}
}

func TestNATOversizedReplies(t *testing.T) {
	padding := strings.Repeat("x", 5<<20)
	for _, stage := range []string{"quote", "order", "inventory", "billing", "PUT", "delete"} {
		t.Run(stage, func(t *testing.T) {
			s := new(natScenario)
			if stage == "delete" {
				s.ordered = true
			}
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				target := (stage == "quote" && strings.HasSuffix(r.URL.Path, "/price")) || (stage == "order" && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats")) || (stage == "inventory" && s.ordered && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/nats")) || (stage == "billing" && strings.HasSuffix(r.URL.Path, "/resources")) || (stage == "PUT" && strings.HasSuffix(r.URL.Path, "/autoRenew")) || (stage == "delete" && r.Method == http.MethodDelete)
				if target {
					if stage == "order" {
						s.orders++
						s.ordered = true
						w.WriteHeader(201)
					}
					if stage == "PUT" {
						s.puts++
						s.manual = true
					}
					if stage == "delete" {
						s.deletes++
					}
					body := `{"code":0,"success":true}`
					switch stage {
					case "quote":
						body = natTestPrice
					case "inventory":
						body = natInventory(natTestRow)
					case "billing":
						body = natBilling(s.manual)
					case "PUT":
						body = `{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`
					}
					body = strings.TrimSuffix(body, "}") + `,"padding":"` + padding + `"}`
					_, _ = w.Write([]byte(body))
					return
				}
				s.serve(t, w, r)
			}))
			var out any
			var err error
			if stage == "delete" {
				out, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: natTestInput().ZoneID, VPCID: "vpc-1", NATID: "nat-1", NoWait: true})
			} else {
				out, err = c.CreateNATInstance(context.Background(), natTestInput())
			}
			if stage == "quote" {
				if vngcloud.ErrorCode(err) != "InvalidResponse" || s.orders != 0 {
					t.Fatalf("%v", err)
				}
			} else if !errors.Is(err, ErrNotSettled) || out == nil {
				t.Fatalf("missing partial outcome: %v", err)
			}
			if s.orders > 1 || s.puts > 1 || s.deletes > 1 {
				t.Fatal("mutation resent")
			}
		})
	}
}

func TestNATRenewalRejectsBillingAliases(t *testing.T) {
	for _, accountAlias := range []bool{false, true} {
		t.Run(map[bool]string{false: "PUT aliases", true: "account alias"}[accountAlias], func(t *testing.T) {
			s := new(natScenario)
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if accountAlias && strings.HasSuffix(r.URL.Path, "/user-info") {
					_, _ = w.Write([]byte(`{"code":200,"data":{"accountId":111,"AccountId":123456}}`))
					return
				}
				if !accountAlias && strings.HasSuffix(r.URL.Path, "/autoRenew") {
					s.puts++
					s.manual = true
					_, _ = w.Write([]byte(`{"code":200,"data":{"successAll":false,"SuccessAll":true,"errorAutoRenewResources":[{}],"ErrorAutoRenewResources":[]}}`))
					return
				}
				s.serve(t, w, r)
			}))
			out, err := c.CreateNATInstance(context.Background(), natTestInput())
			wantPuts := 1
			if accountAlias {
				wantPuts = 0
			}
			if !errors.Is(err, ErrNotSettled) || out == nil || out.NATInstance == nil || s.orders != 1 || s.puts != wantPuts {
				t.Fatalf("unsafe renewal outcome: %v", err)
			}
		})
	}
}
