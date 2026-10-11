package network

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func TestNATCreateCleanRefusal(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 422, 408, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s := new(natScenario)
			reads, sleeps := 0, 0
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/nats") {
					s.orders++
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"code":"REFUSED","message":"server-secret-canary"}`))
					return
				}
				if s.orders > 0 && strings.HasSuffix(r.URL.Path, "/nats") {
					reads++
				}
				s.serve(t, w, r)
			}))
			sleep := c.sleep
			c.sleep = func(ctx context.Context, d time.Duration) error { sleeps++; return sleep(ctx, max(d, 16*time.Minute)) }
			_, err := c.CreateNATInstance(context.Background(), natTestInput())
			definite := status >= 400 && status < 500 && status != 408 && status != 429
			if definite {
				var api *vngcloud.APIError
				if !errors.As(err, &api) || api.StatusCode != status || api.Operation != "network.CreateNATInstance" || api.Code != core.ResolvedCode(status, "") || errors.Is(err, ErrNotSettled) || sleeps != 0 {
					t.Fatalf("%v, sleeps %d", err, sleeps)
				}
			} else if !errors.Is(err, ErrNotSettled) || sleeps != 1 {
				t.Fatalf("%v, sleeps %d", err, sleeps)
			}
			if s.orders != 1 || reads != 1 || s.puts != 0 || strings.Contains(err.Error(), "secret-canary") {
				t.Fatalf("%v, orders %d reads %d puts %d", err, s.orders, reads, s.puts)
			}
		})
	}
}

func TestNATDeleteRefusalContradiction(t *testing.T) {
	s := &natScenario{ordered: true}
	reads := 0
	c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			s.deletes++
			s.ordered = false
			w.WriteHeader(403)
			return
		}
		if s.deletes > 0 && strings.HasSuffix(r.URL.Path, "/nats") {
			reads++
		}
		s.serve(t, w, r)
	}))
	_, err := c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: natTestInput().ZoneID, VPCID: "vpc-1", NATID: "nat-1"})
	if !errors.Is(err, ErrNotSettled) || reads != 1 || s.deletes != 1 {
		t.Fatalf("%v, reads %d deletes %d", err, reads, s.deletes)
	}
}

func TestNATInventoryScopeFields(t *testing.T) {
	for _, tt := range []struct {
		name, row string
		valid     bool
	}{
		{"verified relationships", natTestRow, true},
		{"different availability zone", strings.Replace(natTestRow, "HAN01-1B", "HAN01-2A", 1), true},
		{"wrong public project", strings.Replace(natTestRow, "pro-00000000-0000-0000-0000-000000000001", "pro-other", 1), false},
		{"wrong region", strings.Replace(natTestRow, "66b500000000000000000001", "66b500000000000000000003", 1), false},
		{"empty availability zone", strings.Replace(natTestRow, `"zoneUuid":"HAN01-1B"`, `"zoneUuid":""`, 1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(natInventory(tt.row))) }))
			scope, err := c.natScope(context.Background(), "scope-test", natTestInput().ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.natInventory(context.Background(), "scope-test", scope)
			if (err == nil) != tt.valid {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestNATPickerScopeFields(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		valid      bool
	}{
		{"verified relationships", natTestVPCs, true},
		{"null region UUID", strings.Replace(natTestVPCs, `"regionUuid":"66b500000000000000000001"`, `"regionUuid":null`, 1), true},
		{"wrong region UUID", strings.Replace(natTestVPCs, `"regionUuid":"66b500000000000000000001"`, `"regionUuid":"other"`, 1), false},
		{"wrong region ID", strings.Replace(natTestVPCs, `"regionId":"66b500000000000000000001"`, `"regionId":"other"`, 1), false},
		{"missing public project", strings.Replace(natTestVPCs, `"projectUuid":"pro-00000000-0000-0000-0000-000000000001",`, "", 1), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(tt.body)) }))
			scope, err := c.natScope(context.Background(), "picker-test", natTestInput().ZoneID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.natGuardVPC(context.Background(), "picker-test", scope, "vpc-1", "HAN01-1B")
			if (err == nil) != tt.valid {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestNATRefusalInventoryUncertain(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodDelete} {
		for _, body := range []string{`{`, `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[]}`} {
			t.Run(method+body, func(t *testing.T) {
				s := &natScenario{ordered: method == http.MethodDelete}
				reads := 0
				c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					target := strings.HasSuffix(r.URL.Path, "/nats") || strings.HasSuffix(r.URL.Path, "/nats/nat-1")
					if target && r.Method == method {
						if method == http.MethodPost {
							s.orders++
						} else {
							s.deletes++
						}
						w.WriteHeader(403)
						return
					}
					if s.orders+s.deletes > 0 && strings.HasSuffix(r.URL.Path, "/nats") {
						reads++
						_, _ = w.Write([]byte(body))
						return
					}
					s.serve(t, w, r)
				}))
				var err error
				if method == http.MethodPost {
					_, err = c.CreateNATInstance(context.Background(), natTestInput())
				} else {
					_, err = c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: natTestInput().ZoneID, VPCID: "vpc-1", NATID: "nat-1"})
				}
				if !errors.Is(err, ErrNotSettled) || reads != 1 || s.orders+s.deletes != 1 || s.puts != 0 {
					t.Fatalf("%v, reads %d orders %d deletes %d puts %d", err, reads, s.orders, s.deletes, s.puts)
				}
			})
		}
	}
}
