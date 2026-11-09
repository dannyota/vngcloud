package compute

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

const serverResizeCreditSynthetic = `{"optimumPrice":-180797}`

func TestQuoteResizeServerCreditSynthetic(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeComputeBody(t, r)
		want := map[string]any{"resourceType": "server", "action": "resize", "resourceInfo": map[string]any{
			"serverId": "server-1", "flavorId": "flavor-2", "period": float64(1), "isPoc": false,
		}}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("quote body = %+v, want %+v", body, want)
		}
		_, _ = w.Write([]byte(serverResizeCreditSynthetic))
	}))
	out, err := c.QuoteResizeServer(context.Background(), validResizeServerInput())
	if err != nil || out == nil || out.OptimumPrice != -180797 {
		t.Fatalf("out = %+v, err = %v, want signed credit", out, err)
	}
}

// No synthetic fallback: this stays skipped until the manager adds the raw
// capture and records its sanitized provenance beside the pricing decode test.
func TestQuoteResizeServerCreditFixture(t *testing.T) {
	body, err := os.ReadFile("../testdata/pricing/GetQuoteServerResizeCredit.json")
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("raw server credit fixture awaits manager capture and provenance")
	}
	if err != nil {
		t.Fatal(err)
	}
	cfg := testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	quote, err := pricing.New(cfg).GetQuote(context.Background(), &pricing.GetQuoteInput{ResourceType: pricing.ResourceServer, Action: pricing.ActionResize})
	if err != nil || quote == nil || quote.OptimumPrice >= 0 {
		t.Fatalf("raw quote = %+v, err = %v, want credit", quote, err)
	}
	out, err := New(cfg).QuoteResizeServer(context.Background(), validResizeServerInput())
	if err != nil || !reflect.DeepEqual(out, quote) {
		t.Fatalf("out = %+v, err = %v, want raw quote %+v", out, err, quote)
	}
}

func TestResizeServerCreditSuccessSynthetic(t *testing.T) {
	for _, maxPrice := range []float64{0, 1000000} {
		for _, noWait := range []bool{false, true} {
			t.Run(fmt.Sprintf("%v/%v", maxPrice, noWait), func(t *testing.T) {
				var gets, puts atomic.Int64
				c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					routeResizeServerRequest(t, w, r,
						func(w http.ResponseWriter, _ *http.Request) {
							flavor := "flavor-1"
							if gets.Add(1) > 1 {
								flavor = "flavor-2"
							}
							_, _ = w.Write([]byte(serverBodyWithFlavor("STOPPED", flavor)))
						},
						func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(serverResizeCreditSynthetic)) },
						func(w http.ResponseWriter, r *http.Request) {
							puts.Add(1)
							body := decodeComputeBody(t, r)
							if !reflect.DeepEqual(body, map[string]any{"flavorId": "flavor-2", "serverId": "server-1"}) {
								t.Errorf("resize body = %+v", body)
							}
							w.WriteHeader(http.StatusAccepted)
							_, _ = w.Write([]byte(`not a server response`))
						},
					)
				}))
				in := validResizeServerInput()
				in.MaxPrice, in.NoWait = maxPrice, noWait
				out, err := c.ResizeServer(context.Background(), in)
				wantFlavor, wantGets := "flavor-2", int64(2)
				if noWait {
					wantFlavor, wantGets = "flavor-1", 1
				}
				if err != nil || out == nil || out.MonthlyPrice != -180797 || out.Server.Flavor.FlavorID != wantFlavor || gets.Load() != wantGets || puts.Load() != 1 {
					t.Fatalf("out = %+v, err = %v, gets = %d, puts = %d", out, err, gets.Load(), puts.Load())
				}
			})
		}
	}
}

func TestResizeServerCreditPublicIPAndResponseSafetySynthetic(t *testing.T) {
	for _, tc := range []struct {
		name       string
		noWait     bool
		status     int
		reflection bool
	}{
		{"settled", false, http.StatusAccepted, false},
		{"no wait", true, http.StatusAccepted, false},
		{"uncertain", false, http.StatusBadGateway, false},
		{"uncertain no wait", true, http.StatusBadGateway, false},
		{"unsafe retained output", true, http.StatusBadGateway, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gets, puts atomic.Int64
			c, logs, captures := newServerSecurityClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeServerRequest(t, w, r,
					func(w http.ResponseWriter, _ *http.Request) {
						flavor, name := "flavor-1", "web"
						if gets.Add(1) > 1 {
							flavor = "flavor-2"
						}
						if tc.reflection {
							name = "synthetic-token-003"
						}
						_, _ = fmt.Fprintf(w, `{"data":{"uuid":"server-1","name":%q,"status":"ACTIVE","flavor":{"flavorId":%q},"internalInterfaces":[{"floatingIp":"::ffff:192.0.2.1"}]}}`, name, flavor)
					},
					func(w http.ResponseWriter, _ *http.Request) {
						_, _ = w.Write([]byte(serverResizeCreditSynthetic))
					},
					func(w http.ResponseWriter, _ *http.Request) {
						puts.Add(1)
						w.WriteHeader(tc.status)
						_, _ = w.Write([]byte(`{"message":"synthetic-token-001 synthetic-token-003"}`))
					})
			}), 3)
			in := validResizeServerInput()
			in.NoWait = tc.noWait
			out, err := c.ResizeServer(context.Background(), in)
			wantFlavor, wantGets := "flavor-1", int64(1)
			if !tc.noWait && tc.status == http.StatusAccepted {
				wantFlavor, wantGets = "flavor-2", 2
			}
			if puts.Load() != 1 || gets.Load() != wantGets {
				t.Fatalf("puts = %d, gets = %d, want 1, %d", puts.Load(), gets.Load(), wantGets)
			}
			if tc.reflection {
				assertNoUnsafeServerOutput(t, out, err)
			} else if out == nil || out.MonthlyPrice != -180797 || out.Server.PublicIP != "192.0.2.1" || out.Server.Flavor.FlavorID != wantFlavor {
				t.Fatalf("out = %+v, err = %v, want signed credit and public address", out, err)
			}
			if tc.status == http.StatusAccepted {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var apiErr *vngcloud.APIError
				if !errors.Is(err, ErrNotSettled) || !errors.As(err, &apiErr) || apiErr.StatusCode != tc.status {
					t.Fatalf("err = %v, want unsettled resize and original API status", err)
				}
			}
			assertSafeServerCauseTree(t, err, "synthetic-token-001")
			assertSafeServerCauseTree(t, err, "synthetic-token-003")
			if strings.Contains(logs.String(), "synthetic-token-") || captures.Load() != 0 {
				t.Fatal("operation leaked a credential to logs or captures")
			}
		})
	}
}

func TestResizeServerCreditFailuresSynthetic(t *testing.T) {
	for _, status := range []int{400, 401, 404, 409, 429, 500, 502, 307, 308, 0} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var puts atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeServerRequest(t, w, r,
					func(w http.ResponseWriter, _ *http.Request) {
						_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
					},
					func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(serverResizeCreditSynthetic)) },
					func(w http.ResponseWriter, _ *http.Request) {
						puts.Add(1)
						if status == 0 {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
						w.Header().Set("Location", "/must-not-follow")
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"message":"resize refused"}`))
					},
				)
			}))
			defer srv.Close()
			c := newTestClientForOnce(srv)
			out, err := c.ResizeServer(context.Background(), validResizeServerInput())
			if puts.Load() != 1 {
				t.Fatalf("puts = %d, want one", puts.Load())
			}
			if status >= 400 && status < 500 {
				var apiErr *vngcloud.APIError
				if out != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != status || errors.Is(err, ErrNotSettled) {
					t.Fatalf("out = %+v, err = %v", out, err)
				}
				return
			}
			if out == nil || out.MonthlyPrice != -180797 || out.Server.Flavor.FlavorID != "flavor-1" || !errors.Is(err, ErrNotSettled) || !strings.Contains(err.Error(), "GetServer") {
				t.Fatalf("out = %+v, err = %v, want signed partial output and recovery read", out, err)
			}
			if status >= 500 {
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
					t.Fatalf("err = %v, want underlying APIError", err)
				}
			}
		})
	}
}

func TestResizeServerCreditWaitSynthetic(t *testing.T) {
	for _, outcome := range []string{"failed", "bound", "cancel", "read-error"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var gets, puts atomic.Int64
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeServerRequest(t, w, r,
					func(w http.ResponseWriter, _ *http.Request) {
						status := "ACTIVE"
						if gets.Add(1) > 1 {
							status = "CHANGING-FLAVOR"
							if outcome == "failed" {
								status = "ERROR"
							}
							if outcome == "read-error" {
								w.WriteHeader(400)
								return
							}
						}
						_, _ = w.Write([]byte(serverBodyWithFlavor(status, "flavor-1")))
					},
					func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(serverResizeCreditSynthetic)) },
					func(w http.ResponseWriter, _ *http.Request) { puts.Add(1); w.WriteHeader(http.StatusAccepted) },
				)
			}))
			clock, sleeps := time.Unix(1000, 0), 0
			c.now = func() time.Time { return clock }
			c.sleep = func(ctx context.Context, d time.Duration) error {
				if d != 5*time.Second {
					t.Errorf("poll = %v, want 5s", d)
				}
				sleeps++
				clock = clock.Add(d)
				if outcome == "cancel" {
					cancel()
				}
				return ctx.Err()
			}
			out, err := c.ResizeServer(ctx, validResizeServerInput())
			wantErr := ErrNotSettled
			if outcome == "failed" {
				wantErr = ErrFailed
			}
			if out == nil || out.MonthlyPrice != -180797 || out.Server.UUID != "server-1" || puts.Load() != 1 || !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "GetServer") {
				t.Fatalf("out = %+v, err = %v, puts = %d", out, err, puts.Load())
			}
			if outcome == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if outcome == "bound" && (sleeps != 180 || !strings.Contains(err.Error(), "GetServer")) {
				t.Fatalf("sleeps = %d, err = %v", sleeps, err)
			}
			if outcome == "read-error" {
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("lost APIError: %v", err)
				}
			}
		})
	}
}

func TestResizeServerCreditQuoteToSendRaceSynthetic(t *testing.T) {
	var puts atomic.Int64
	currentFlavor := "expensive-flavor"
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", currentFlavor)))
			},
			func(w http.ResponseWriter, _ *http.Request) {
				// Another actor makes our target an upgrade after the quote.
				currentFlavor = "cheaper-than-target"
				_, _ = w.Write([]byte(serverResizeCreditSynthetic))
			},
			func(w http.ResponseWriter, r *http.Request) {
				puts.Add(1)
				if currentFlavor != "cheaper-than-target" {
					t.Error("peer change missing")
				}
				if !reflect.DeepEqual(decodeComputeBody(t, r), map[string]any{"serverId": "server-1", "flavorId": "flavor-2"}) {
					t.Error("target body changed")
				}
				currentFlavor = "flavor-2"
				w.WriteHeader(502)
			},
		)
	}))
	out, err := c.ResizeServer(context.Background(), validResizeServerInput())
	if out == nil || out.MonthlyPrice != -180797 || !errors.Is(err, ErrNotSettled) || puts.Load() != 1 {
		t.Fatalf("out = %+v, err = %v, puts = %d", out, err, puts.Load())
	}
	recovered, err := c.GetServer(context.Background(), &GetServerInput{ServerID: "server-1"})
	if err != nil || recovered.Server.Flavor.FlavorID != "flavor-2" || puts.Load() != 1 {
		t.Fatalf("recovery = %+v, err = %v, puts = %d", recovered, err, puts.Load())
	}
	// No charge assertion: the API has no conditional price or old-flavor field.
}

type resizeCreditDialTransport struct {
	base  http.RoundTripper
	dials atomic.Int64
}

func (r *resizeCreditDialTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPut {
		r.dials.Add(1)
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic dial failure")}
	}
	return r.base.RoundTrip(req)
}

func TestResizeServerCreditFailedDialSynthetic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
		case http.MethodPost:
			_, _ = w.Write([]byte(serverResizeCreditSynthetic))
		default:
			t.Error("unexpected write reached server")
		}
	}))
	defer srv.Close()
	rt := &resizeCreditDialTransport{base: srv.Client().Transport}
	tc := transport.New(transport.Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 3, RetryInterval: time.Millisecond})
	c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VServer: srv.URL + "/", Portal: srv.URL + "/"}, tc))
	out, err := c.ResizeServer(context.Background(), validResizeServerInput())
	var apiErr *vngcloud.APIError
	if out == nil || out.MonthlyPrice != -180797 || !errors.Is(err, ErrNotSettled) || !errors.As(err, &apiErr) || rt.dials.Load() != 1 {
		t.Fatalf("out = %+v, err = %v, dials = %d", out, err, rt.dials.Load())
	}
}

func TestResizeServerSignedZeroRefusedSynthetic(t *testing.T) {
	for _, zero := range []string{"0", "-0"} {
		t.Run(zero, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeServerRequest(t, w, r,
					func(w http.ResponseWriter, _ *http.Request) {
						_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
					},
					func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"optimumPrice":` + zero + `}`)) },
					func(http.ResponseWriter, *http.Request) { t.Error("zero quote sent a resize") })
			}))
			out, err := c.ResizeServer(context.Background(), validResizeServerInput())
			if out != nil || !errors.Is(err, vngcloud.ErrUnpriced) {
				t.Fatalf("out = %+v, err = %v", out, err)
			}
		})
	}
}

// A zero token expiry forces acquisition before each request, so the third
// acquisition belongs to the PUT after the pre-read and quote have completed.
type resizeCreditPreSendTokens struct {
	calls   atomic.Int64
	failure error
	cancel  context.CancelFunc
}

func (s *resizeCreditPreSendTokens) Token(context.Context) (transport.Token, error) {
	if s.calls.Add(1) == 3 {
		if s.cancel != nil {
			s.cancel()
		}
		return transport.Token{}, s.failure
	}
	return transport.Token{AccessToken: "synthetic-resize-token"}, nil
}

func (*resizeCreditPreSendTokens) Invalidate(string) {}

func TestResizeServerCreditTokenFailureBeforeSendSynthetic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		failure error
		cancel  bool
	}{
		{"acquisition", errors.New("synthetic token acquisition failure"), false},
		{"provider APIError", &vngcloud.APIError{Operation: "synthetic.Token", StatusCode: 503}, false},
		{"cancellation", context.Canceled, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var reads, quotes, puts atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeServerRequest(t, w, r,
					func(w http.ResponseWriter, _ *http.Request) {
						reads.Add(1)
						_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
					},
					func(w http.ResponseWriter, _ *http.Request) {
						quotes.Add(1)
						_, _ = w.Write([]byte(serverResizeCreditSynthetic))
					},
					func(w http.ResponseWriter, _ *http.Request) {
						puts.Add(1)
						w.WriteHeader(http.StatusAccepted)
					})
			}))
			defer srv.Close()
			source := &resizeCreditPreSendTokens{failure: tc.failure}
			if tc.cancel {
				source.cancel = cancel
			}
			clientTransport := transport.New(transport.Config{HTTPClient: srv.Client(), TokenSource: source})
			c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VServer: srv.URL + "/", Portal: srv.URL + "/"}, clientTransport))
			out, err := c.ResizeServer(ctx, validResizeServerInput())
			if reads.Load() != 1 || quotes.Load() != 1 || puts.Load() != 0 || source.calls.Load() != 3 {
				t.Fatalf("reads = %d, quotes = %d, puts = %d, token calls = %d", reads.Load(), quotes.Load(), puts.Load(), source.calls.Load())
			}
			if out != nil || errors.Is(err, ErrNotSettled) || !errors.Is(err, source.failure) {
				t.Fatalf("out = %+v, err = %v, want nil Output and original acquisition error", out, err)
			}
			var providerErr *vngcloud.APIError
			if errors.As(source.failure, &providerErr) {
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr != providerErr {
					t.Fatalf("err = %v, want original provider APIError", err)
				}
			}
		})
	}
}

func TestResizeServerCreditProvenFailedDialSynthetic(t *testing.T) {
	var refuseDial atomic.Bool
	var failedDials, puts atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			},
			func(w http.ResponseWriter, _ *http.Request) {
				refuseDial.Store(true)
				_, _ = w.Write([]byte(serverResizeCreditSynthetic))
			},
			func(w http.ResponseWriter, _ *http.Request) {
				puts.Add(1)
				w.WriteHeader(http.StatusAccepted)
			})
	}))
	defer srv.Close()
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if refuseDial.Load() {
			failedDials.Add(1)
			return nil, &net.OpError{Op: "dial", Net: network, Err: errors.New("synthetic dial refusal")}
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	defer tr.CloseIdleConnections()
	tc := transport.New(transport.Config{HTTPClient: &http.Client{Transport: tr}, RetryCount: 3, RetryInterval: time.Millisecond})
	c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VServer: srv.URL + "/", Portal: srv.URL + "/"}, tc))
	out, err := c.ResizeServer(context.Background(), validResizeServerInput())
	var apiErr *vngcloud.APIError
	if out != nil || errors.Is(err, ErrNotSettled) || !errors.As(err, &apiErr) || puts.Load() != 0 || failedDials.Load() != 1 {
		t.Fatalf("out = %+v, err = %v, puts = %d, failed dials = %d", out, err, puts.Load(), failedDials.Load())
	}
}
