package network

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestNATDeleteReplies(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		valid  bool
	}{
		{"accepted", 200, `{"code":0,"success":true}`, true},
		{"wrong status", 201, `{"code":0,"success":true}`, false},
		{"nonzero code", 200, `{"code":1,"success":true}`, false},
		{"false success", 200, `{"code":0,"success":false}`, false},
		{"string code", 200, `{"code":"0","success":true}`, false},
		{"malformed", 200, `{`, false},
		{"duplicate keys", 200, `{"code":0,"code":0,"success":true}`, false},
		{"case alias", 200, `{"code":0,"Success":true}`, false},
		{"400", 400, `{}`, false},
		{"403", 403, `{}`, false},
		{"404", 404, `{}`, false},
		{"409", 409, `{}`, false},
		{"408", 408, `{}`, false},
		{"401", 401, `{"message":"server-secret-canary"}`, false},
		{"429", 429, `{}`, false},
		{"500", 500, `{}`, false},
		{"redirect", 302, `{}`, false},
	}
	for _, tt := range cases {
		for _, noWait := range []bool{false, true} {
			t.Run(tt.name+map[bool]string{false: " wait", true: " no wait"}[noWait], func(t *testing.T) {
				s := &natScenario{ordered: true}
				postReads := 0
				redirects := 0
				c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/redirect-target" {
						redirects++
						return
					}
					if r.Method == http.MethodDelete {
						s.deletes++
						raw, _ := io.ReadAll(r.Body)
						if string(raw) != "{}" {
							t.Errorf("delete body %s", raw)
						}
						w.Header().Set("Location", "/redirect-target")
						w.WriteHeader(tt.status)
						_, _ = w.Write([]byte(tt.body))
						return
					}
					if s.deletes > 0 && strings.HasSuffix(r.URL.Path, "/nats") {
						postReads++
						row := natTestRow
						if postReads >= 2 {
							row = ""
						}
						_, _ = w.Write([]byte(natInventory(row)))
						return
					}
					s.serve(t, w, r)
				}))
				_, err := c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1", NoWait: noWait})
				refused := tt.status >= 400 && tt.status < 500 && tt.status != 408 && tt.status != 429
				wantReads := 2
				if noWait {
					wantReads = 0
				}
				switch {
				case noWait && !tt.valid:
					if !errors.Is(err, ErrNotSettled) {
						t.Fatalf("%v", err)
					}
				case refused:
					wantReads = 1
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.StatusCode != tt.status || errors.Is(err, ErrNotSettled) {
						t.Fatalf("%v", err)
					}
				case err != nil:
					t.Fatal(err)
				}
				if s.deletes != 1 || redirects != 0 || postReads != wantReads {
					t.Fatalf("deletes %d, reads %d, redirects %d", s.deletes, postReads, redirects)
				}
			})
		}
	}
}
func TestNATDeleteGuardsAndWait(t *testing.T) {
	for _, tt := range []struct {
		name, row string
		want      error
	}{
		{"absent", "", vngcloud.ErrNotFound},
		{"VPC mismatch", strings.Replace(natTestRow, "vpc-1", "vpc-2", 1), vngcloud.ErrInvalidInput},
		{"region mismatch", strings.Replace(natTestRow, "66b500000000000000000001", "region-2", 1), nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := new(natScenario)
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/nats") {
					_, _ = w.Write([]byte(natInventory(tt.row)))
					return
				}
				s.serve(t, w, r)
			}))
			_, err := c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
			if (tt.want != nil && !errors.Is(err, tt.want)) || (tt.want == nil && vngcloud.ErrorCode(err) != "InvalidResponse") || s.deletes != 0 {
				t.Fatalf("%v", err)
			}
		})
	}
	for _, readFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "read failure"}[readFailure], func(t *testing.T) {
			s := &natScenario{ordered: true}
			c := natWriteTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if s.deletes > 0 && strings.HasSuffix(r.URL.Path, "/nats") {
					if readFailure {
						w.WriteHeader(403)
					} else {
						_, _ = w.Write([]byte(natInventory(natTestRow)))
					}
					return
				}
				s.serve(t, w, r)
			}))
			sleep := c.sleep
			c.sleep = func(ctx context.Context, d time.Duration) error { return sleep(ctx, max(d, 11*time.Minute)) }
			_, err := c.DeleteNATInstance(context.Background(), &DeleteNATInstanceInput{ZoneID: "66b500000000000000000001", VPCID: "vpc-1", NATID: "nat-1"})
			if !errors.Is(err, ErrNotSettled) || s.deletes != 1 {
				t.Fatalf("%v", err)
			}
		})
	}
}
