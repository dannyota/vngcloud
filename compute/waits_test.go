package compute

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
)

func TestWaitServerActiveRejectsIdentityBeforeRetainingResponse(t *testing.T) {
	for _, status := range []string{"ACTIVE", "ERROR", "CREATING"} {
		for _, id := range []string{"server-other", "", "../bad"} {
			for _, prior := range []bool{false, true} {
				t.Run(fmt.Sprint(status, id, prior), func(t *testing.T) {
					var reads atomic.Int64
					c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						n := reads.Add(1)
						if prior && n == 1 {
							_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","name":"created-name","status":"CREATING"}}`))
							return
						}
						last := int64(1)
						if prior {
							last = 2
						}
						if n > last {
							w.WriteHeader(401)
							return
						}
						_, _ = fmt.Fprintf(w, `{"data":{"uuid":%q,"name":"foreign-name","status":%q}}`, id, status)
					})))
					out, err := c.waitServerActive(context.Background(), "compute.CreateServer", "server-1")
					var api *vngcloud.APIError
					if !errors.Is(err, ErrNotSettled) || errors.Is(err, ErrFailed) || !errors.As(err, &api) || api.Code != "InvalidResponse" {
						t.Fatalf("err=%v", err)
					}
					if prior {
						if out == nil || out.UUID != "server-1" || out.Name != "created-name" || out.Status != "CREATING" {
							t.Fatalf("lost confirmed result: %+v", out)
						}
					} else if out != nil {
						t.Fatalf("retained unconfirmed result: %+v", out)
					}
					wantReads := int64(1)
					if prior {
						wantReads = 2
					}
					if reads.Load() != wantReads {
						t.Fatalf("reads=%d, want %d", reads.Load(), wantReads)
					}
					if strings.Contains(err.Error(), "foreign-name") || strings.Contains(err.Error(), "server-other") {
						t.Fatal("foreign response escaped in error")
					}
				})
			}
		}
	}
}

func TestCreateServerConfirmationKeepsCreatedIdentity(t *testing.T) {
	for _, attach := range []bool{false, true} {
		for _, status := range []string{"ACTIVE", "ERROR", "CREATING"} {
			for _, prior := range []bool{false, true} {
				t.Run(fmt.Sprint(attach, status, prior), func(t *testing.T) {
					var reads, orders atomic.Int64
					c := withInstantSleep(newTestClient(t, floatingCreateHandler(t, func() string { return floatingSafeRules }, func(w http.ResponseWriter, _ *http.Request) {
						orders.Add(1)
						w.WriteHeader(202)
						_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
					}, func(w http.ResponseWriter, _ *http.Request) {
						n := reads.Add(1)
						if prior && n == 1 {
							_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","name":"created-name","status":"CREATING"}}`))
							return
						}
						last := int64(1)
						if prior {
							last = 2
						}
						if n > last {
							w.WriteHeader(401)
							return
						}
						_, _ = fmt.Fprintf(w, `{"data":{"uuid":"server-other","name":"foreign-name","status":%q}}`, status)
					})))
					in := validCreateServerInput()
					in.AttachFloatingIP = attach
					in.MaxPrice = 467800
					out, err := c.CreateServer(context.Background(), in)
					if !errors.Is(err, ErrNotSettled) || errors.Is(err, ErrFailed) || out == nil || out.Server.UUID != "server-1" || orders.Load() != 1 {
						t.Fatalf("out=%+v err=%v orders=%d", out, err, orders.Load())
					}
					wantName := "web-1"
					if prior {
						wantName = "created-name"
					}
					if out.Server.Name != wantName {
						t.Fatalf("name=%q want=%q", out.Server.Name, wantName)
					}
				})
			}
		}
	}
}

func TestWaitServerActiveMatchingIdentityKeepsStatusBehavior(t *testing.T) {
	for _, status := range []string{"ACTIVE", "ERROR", "CREATING", "NOT_FOUND"} {
		t.Run(status, func(t *testing.T) {
			var reads atomic.Int64
			c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := reads.Add(1)
				next := status
				if n > 1 {
					next = "ACTIVE"
				}
				if next == "NOT_FOUND" {
					w.WriteHeader(404)
					return
				}
				_, _ = fmt.Fprintf(w, `{"data":{"uuid":"server-1","status":%q}}`, next)
			})))
			out, err := c.waitServerActive(context.Background(), "compute.CreateServer", "server-1")
			if out == nil || out.UUID != "server-1" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if status == "ERROR" {
				if !errors.Is(err, ErrFailed) {
					t.Fatal(err)
				}
			} else if err != nil || out.Status != "ACTIVE" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
}
