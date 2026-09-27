package compute

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func serverBodyWithFlavor(status, flavorID string) string {
	return `{"data":{"uuid":"server-1","status":"` + status + `","flavor":{"flavorId":"` + flavorID + `"}}}`
}

func validResizeServerInput() *ResizeServerInput {
	return &ResizeServerInput{ServerID: "server-1", FlavorID: "flavor-2"}
}

// --- QuoteResizeServer ---

func TestQuoteResizeServerSendsResizeBody(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = decodeComputeBody(t, r)
		_, _ = w.Write([]byte(quoteServerFixture))
	}))

	if _, err := c.QuoteResizeServer(context.Background(), validResizeServerInput()); err != nil {
		t.Fatalf("QuoteResizeServer() error = %v", err)
	}
	if body["resourceType"] != "server" || body["action"] != "resize" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	info := body["resourceInfo"].(map[string]any)
	if info["serverId"] != "server-1" || info["flavorId"] != "flavor-2" {
		t.Fatalf("unexpected resourceInfo: %+v", info)
	}
}

func TestQuoteResizeServerRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.QuoteResizeServer(context.Background(), &ResizeServerInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteResizeServerPathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		in := validResizeServerInput()
		in.ServerID = bad
		if _, err := c.QuoteResizeServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("ServerID=%q err = %v, want ErrInvalidInput", bad, err)
		}
		in2 := validResizeServerInput()
		in2.FlavorID = bad
		if _, err := c.QuoteResizeServer(context.Background(), in2); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("FlavorID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// --- ResizeServer ---

func routeResizeServerRequest(t *testing.T, w http.ResponseWriter, r *http.Request, onGet, onQuote, onResize func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
		onGet(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
		if onQuote != nil {
			onQuote(w, r)
			return
		}
		_, _ = w.Write([]byte(quoteServerFixture))
	case r.Method == http.MethodPut && r.URL.Path == "/v2/project-1/servers/server-1/resize":
		onResize(w, r)
	default:
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

func TestResizeServerSendsBodyAndSucceeds(t *testing.T) {
	var resizeBody map[string]any
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				flavor := "flavor-1"
				if getCalls > 1 {
					flavor = "flavor-2"
				}
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", flavor)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				resizeBody = decodeComputeBody(t, r)
				w.WriteHeader(http.StatusAccepted)
			},
		)
	})))

	in := validResizeServerInput()
	in.MaxPrice = 347800
	out, err := c.ResizeServer(context.Background(), in)
	if err != nil {
		t.Fatalf("ResizeServer() error = %v", err)
	}
	if resizeBody["flavorId"] != "flavor-2" || resizeBody["serverId"] != "server-1" {
		t.Fatalf("unexpected resize body: %+v", resizeBody)
	}
	if out.Server.Flavor.FlavorID != "flavor-2" {
		t.Fatalf("Flavor = %+v, want flavor-2", out.Server.Flavor)
	}
	if out.MonthlyPrice != 347800 {
		t.Fatalf("MonthlyPrice = %v, want 347800", out.MonthlyPrice)
	}
}

func TestResizeServerSameFlavorRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-2")))
	}))

	in := validResizeServerInput()
	if _, err := c.ResizeServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestResizeServerWrongStatusRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal("no write expected")
		}
		_, _ = w.Write([]byte(serverBodyWithFlavor("CREATING", "flavor-1")))
	}))

	if _, err := c.ResizeServer(context.Background(), validResizeServerInput()); !errors.Is(err, ErrUnexpectedStatus) {
		t.Fatalf("err = %v, want ErrUnexpectedStatus", err)
	}
}

func TestResizeServerDefaultMaxPriceRefusesAndSendsNoResize(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				resizeCalls.Add(1)
				t.Fatal("no resize expected above MaxPrice")
			},
		)
	}))

	if _, err := c.ResizeServer(context.Background(), validResizeServerInput()); !errors.Is(err, vngcloud.ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if resizeCalls.Load() != 0 {
		t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
	}
}

func TestResizeServerRejectsInvalidMaxPriceBeforeAnyRequest(t *testing.T) {
	for _, tt := range []struct {
		name string
		v    float64
	}{
		{"NaN", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
		{"negative", -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("no request expected for an invalid MaxPrice")
			}))
			in := validResizeServerInput()
			in.MaxPrice = tt.v
			if _, err := c.ResizeServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("ResizeServer() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestResizeServerQuoteFailureSendsNoResize(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			},
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"pricing unavailable"}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				resizeCalls.Add(1)
				t.Fatal("no resize expected after a quote failure")
			},
		)
	}))

	in := validResizeServerInput()
	in.MaxPrice = 1000000
	if _, err := c.ResizeServer(context.Background(), in); err == nil {
		t.Fatal("err = nil, want the quote's error")
	}
	if resizeCalls.Load() != 0 {
		t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
	}
}

// TestResizeServerInvalidQuotePriceSendsNoResize checks that a quote
// response carrying a price the guard cannot safely compare, null,
// negative, or a bare NaN literal, refuses the resize with nothing sent.
func TestResizeServerInvalidQuotePriceSendsNoResize(t *testing.T) {
	cases := []struct {
		name      string
		quoteBody string
	}{
		{"null price", `{"optimumPrice":null}`},
		{"negative price", `{"optimumPrice":-100}`},
		{"NaN literal", `{"optimumPrice":NaN}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resizeCalls atomic.Int64
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeServerRequest(t, w, r,
					func(w http.ResponseWriter, r *http.Request) {
						_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
					},
					func(w http.ResponseWriter, r *http.Request) {
						_, _ = w.Write([]byte(tc.quoteBody))
					},
					func(w http.ResponseWriter, r *http.Request) {
						resizeCalls.Add(1)
						t.Fatal("no resize expected for an invalid quote price")
					},
				)
			}))

			in := validResizeServerInput()
			in.MaxPrice = 1000000
			if _, err := c.ResizeServer(context.Background(), in); err == nil {
				t.Fatal("err = nil, want an error for an invalid quote price")
			}
			if resizeCalls.Load() != 0 {
				t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
			}
		})
	}
}

func TestResizeServerNotRetriedAfter502(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				resizeCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"upstream error"}`))
			},
		)
	}))

	in := validResizeServerInput()
	in.MaxPrice = 1000000
	_, err := c.ResizeServer(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if resizeCalls.Load() != 1 {
		t.Fatalf("resize calls = %d, want 1: Once must never be retried", resizeCalls.Load())
	}
	if !strings.Contains(err.Error(), "GetServer") {
		t.Fatalf("err = %v, want it to name GetServer as the read to run", err)
	}
	if strings.Contains(err.Error(), "run this operation again") {
		t.Fatalf("err = %v, must not suggest running ResizeServer itself again", err)
	}
}

func TestResizeServer400ReturnsAPIError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"bad request"}`))
			},
		)
	}))

	in := validResizeServerInput()
	in.MaxPrice = 1000000
	_, err := c.ResizeServer(context.Background(), in)
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
	if errors.Is(err, ErrNotSettled) {
		t.Fatal("err wraps ErrNotSettled, want the raw APIError since the server never acted")
	}
}

func TestResizeServerNoWaitReturnsAtOnce(t *testing.T) {
	getCalls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			},
		)
	}))

	in := validResizeServerInput()
	in.MaxPrice = 1000000
	in.NoWait = true
	if _, err := c.ResizeServer(context.Background(), in); err != nil {
		t.Fatalf("ResizeServer() error = %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-resize read only)", getCalls)
	}
}

func TestResizeServerWaitFailsOnError(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				if getCalls == 1 {
					_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
					return
				}
				_, _ = w.Write([]byte(serverBodyWithFlavor("ERROR", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			},
		)
	})))

	in := validResizeServerInput()
	in.MaxPrice = 1000000
	_, err := c.ResizeServer(context.Background(), in)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestResizeServerWaitBoundReached(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeServerRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				if getCalls == 1 {
					_, _ = w.Write([]byte(serverBodyWithFlavor("ACTIVE", "flavor-1")))
					return
				}
				_, _ = w.Write([]byte(serverBodyWithFlavor("CHANGING-FLAVOR", "flavor-1")))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			},
		)
	})))

	in := validResizeServerInput()
	in.MaxPrice = 1000000
	_, err := c.ResizeServer(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !strings.Contains(err.Error(), "GetServer") {
		t.Fatalf("err = %v, want it to name GetServer as the read to run", err)
	}
	if strings.Contains(err.Error(), "run this operation again") {
		t.Fatalf("err = %v, must not suggest running ResizeServer itself again", err)
	}
}

// TestWaitServerResizedPollParameters checks the literal interval and
// bound waitServerResized passes to poll.
func TestWaitServerResizedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(serverBodyWithFlavor("VERIFYING-FLAVOR", "flavor-1")))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitServerResized(context.Background(), "op", "server-1", "flavor-2"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 180 {
		t.Fatalf("sleep calls = %d, want 180 (a 5s interval over a 15-minute bound)", len(sleeps))
	}
}
