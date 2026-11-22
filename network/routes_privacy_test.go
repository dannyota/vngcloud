package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
)

type routeRotatingCredentials struct{ calls atomic.Int32 }

func (p *routeRotatingCredentials) Token(context.Context) (vngcloud.Token, error) {
	return vngcloud.Token{AccessToken: fmt.Sprintf("route-token-%d", p.calls.Add(1))}, nil
}

func (*routeRotatingCredentials) Invalidate(string) {}

func TestRouteOperationCredentialHistory(t *testing.T) {
	for _, action := range []string{"add", "remove"} {
		for _, stage := range []string{"confirmation", "write error", "retained output"} {
			t.Run(action+"/"+stage, func(t *testing.T) {
				var logs bytes.Buffer
				var captures atomic.Int32
				reads, writes := 0, 0
				entry := routeEntry{DestinationCIDRBlock: "203.0.113.0/24", Target: "10.0.0.1"}
				first := ""
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
					if first == "" {
						first = token
					}
					if r.Method == http.MethodPut {
						writes++
						if token == first {
							t.Error("credentials did not rotate")
						}
						if stage == "retained output" {
							w.WriteHeader(200)
							return
						}
						w.WriteHeader(503)
						if stage == "write error" {
							_ = json.NewEncoder(w).Encode(map[string]string{"code": first, "message": first})
						}
						return
					}
					reads++
					name := "safe"
					if stage == "confirmation" && writes > 0 {
						name = first
					}
					if stage == "retained output" {
						name = "route-token-3"
					}
					routes := []routeEntry{}
					if (action == "remove" && writes == 0) || (action == "add" && writes > 0) {
						routes = append(routes, entry)
					}
					_, _ = w.Write([]byte(routeTableJSON("rt-2", name, "ACTIVE", routes)))
				}))
				t.Cleanup(srv.Close)
				cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(&routeRotatingCredentials{}), vngcloud.WithHTTPClient(srv.Client()), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VServer: srv.URL}), vngcloud.WithRetry(0, 0), vngcloud.WithLogger(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))), vngcloud.WithResponseCapture(func(vngcloud.ResponseCapture) { captures.Add(1) }))
				if err != nil {
					t.Fatal(err)
				}
				c := withInstantSleep(New(cfg))
				var out any
				if action == "add" {
					out, err = c.AddRoute(context.Background(), &AddRouteInput{RouteTableID: "rt-2", DestinationCIDR: entry.DestinationCIDRBlock, Target: entry.Target, NoWait: stage == "retained output"})
				} else {
					out, err = c.RemoveRoute(context.Background(), &RemoveRouteInput{RouteTableID: "rt-2", DestinationCIDR: entry.DestinationCIDRBlock, NoWait: stage == "retained output"})
				}
				encoded, marshalErr := json.Marshal(out)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				if strings.Contains(string(encoded), "route-token-") || strings.Contains(fmt.Sprintf("%+v %#v", out, err), "route-token-") || routeErrorReflectsCredential(err) || strings.Contains(logs.String(), "route-token-") || captures.Load() != 0 {
					t.Fatal("route credential escaped output, errors, logs, or captures")
				}
				if !errors.Is(err, ErrNotSettled) || writes != 1 || reads < 2 {
					t.Fatalf("settlement=%v writes=%d reads=%d", err, writes, reads)
				}
			})
		}
	}
}

//nolint:errorlint // Inspect every error node and joined cause for credential text.
func routeErrorReflectsCredential(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "route-token-") {
		return true
	}
	var api *vngcloud.APIError
	if errors.As(err, &api) && strings.Contains(fmt.Sprintf("%+v %#v", api, api), "route-token-") {
		return true
	}
	switch cause := err.(type) {
	case interface{ Unwrap() []error }:
		for _, child := range cause.Unwrap() {
			if routeErrorReflectsCredential(child) {
				return true
			}
		}
	case interface{ Unwrap() error }:
		return routeErrorReflectsCredential(cause.Unwrap())
	}
	return false
}
