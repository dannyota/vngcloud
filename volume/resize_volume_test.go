package volume

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func volumeBodyWithSize(status string, size int) string {
	return `{"data":{"uuid":"volume-1","status":"` + status + `","size":` + strconv.Itoa(size) + `,"volumeTypeId":"type-1"}}`
}

func routeResizeVolumeRequest(t *testing.T, w http.ResponseWriter, r *http.Request, onGet, onQuote, onResize func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/volumes/volume-1":
		onGet(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
		if onQuote != nil {
			onQuote(w, r)
			return
		}
		_, _ = w.Write([]byte(quoteVolumeFixture))
	case r.Method == http.MethodPut && r.URL.Path == "/v2/project-1/volumes/volume-1/resize":
		onResize(w, r)
	default:
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

// --- QuoteResizeVolume ---

func TestQuoteResizeVolumeSendsResizeBody(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			},
			func(w http.ResponseWriter, r *http.Request) {
				body = decodeVolumeBody(t, r)
				_, _ = w.Write([]byte(quoteVolumeFixture))
			}, nil,
		)
	}))

	if _, err := c.QuoteResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20}); err != nil {
		t.Fatalf("QuoteResizeVolume() error = %v", err)
	}
	if body["resourceType"] != "volume" || body["action"] != "resize" {
		t.Fatalf("unexpected resourceType/action: %+v", body)
	}
	info := body["resourceInfo"].(map[string]any)
	if info["volumeId"] != "volume-1" || info["newSize"] != float64(20) || info["newVolumeTypeId"] != "type-1" {
		t.Fatalf("unexpected resourceInfo: %+v", info)
	}
}

func TestQuoteResizeVolumeShrinkRefused(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
		default:
			t.Fatal("no pricing request expected for a shrink")
		}
	}))

	if _, err := c.QuoteResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 5}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteResizeVolumeRequiredInput(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	if _, err := c.QuoteResizeVolume(context.Background(), &ResizeVolumeInput{}); !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
}

func TestQuoteResizeVolumePathIDRejection(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("no request expected")
	}))
	for _, bad := range []string{"..", ".", "/", "?"} {
		if _, err := c.QuoteResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: bad, Size: 20}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("VolumeID=%q err = %v, want ErrInvalidInput", bad, err)
		}
	}
}

// --- ResizeVolume ---

func TestResizeVolumeSendsBodyAndSucceeds(t *testing.T) {
	var resizeBody map[string]any
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				size := 10
				if getCalls > 1 {
					size = 20
				}
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", size)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				resizeBody = decodeVolumeBody(t, r)
				w.WriteHeader(http.StatusAccepted)
			},
		)
	})))

	out, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 32000})
	if err != nil {
		t.Fatalf("ResizeVolume() error = %v", err)
	}
	if resizeBody["newSize"] != float64(20) || resizeBody["newVolumeTypeId"] != "type-1" {
		t.Fatalf("unexpected resize body: %+v", resizeBody)
	}
	if out.Volume.Size != 20 {
		t.Fatalf("Size = %d, want 20", out.Volume.Size)
	}
	if out.MonthlyPrice != 32000 {
		t.Fatalf("MonthlyPrice = %v, want 32000", out.MonthlyPrice)
	}
}

func TestResizeVolumeShrinkOrEqualRefused(t *testing.T) {
	for _, size := range []int{10, 5} {
		c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			default:
				t.Fatal("no request expected for a shrink or equal size")
			}
		}))
		if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: size}); !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("Size=%d err = %v, want ErrInvalidInput", size, err)
		}
	}
}

// TestResizeVolumeRefusesUnexpectedStatus checks that ResizeVolume refuses
// with ErrUnexpectedStatus, sending nothing, when the pre-resize read shows
// a status other than AVAILABLE or IN-USE: a volume mid-create, mid-resize,
// or in ERROR is already changing, and resizing it again would be sent
// against a state the SDK never confirmed.
func TestResizeVolumeRefusesUnexpectedStatus(t *testing.T) {
	for _, status := range []string{"CREATING", "RESIZING", "ERROR", "DELETING", ""} {
		t.Run(status, func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					_, _ = w.Write([]byte(volumeBodyWithSize(status, 10)))
				default:
					t.Fatal("no request expected for an unexpected status")
				}
			}))
			_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 32000})
			if !errors.Is(err, ErrUnexpectedStatus) {
				t.Fatalf("status %q: err = %v, want ErrUnexpectedStatus", status, err)
			}
		})
	}
}

// TestResizeVolumeAllowsAvailableAndInUse checks that ResizeVolume accepts
// both statuses the design allows: AVAILABLE (unattached) and IN-USE
// (attached).
func TestResizeVolumeAllowsAvailableAndInUse(t *testing.T) {
	for _, status := range []string{"AVAILABLE", "IN-USE"} {
		t.Run(status, func(t *testing.T) {
			getCalls := 0
			c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeResizeVolumeRequest(t, w, r,
					func(w http.ResponseWriter, r *http.Request) {
						getCalls++
						size := 10
						if getCalls > 1 {
							size = 20
						}
						_, _ = w.Write([]byte(volumeBodyWithSize(status, size)))
					}, nil,
					func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusAccepted)
					},
				)
			})))
			if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 32000}); err != nil {
				t.Fatalf("status %q: ResizeVolume() error = %v", status, err)
			}
		})
	}
}

// TestResizeVolumeAmbiguousFailureNamesGetVolume checks that an ambiguous
// resize failure's error names GetVolume as the read to run, never
// suggesting the resize itself be sent again: ResizeVolume is a paid
// write, and only a read can safely confirm what happened.
func TestResizeVolumeAmbiguousFailureNamesGetVolume(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"upstream error"}`))
			},
		)
	}))

	_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 32000})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if !strings.Contains(err.Error(), "GetVolume") {
		t.Fatalf("err = %v, want it to name GetVolume as the read to run", err)
	}
	if strings.Contains(err.Error(), "run this operation again") {
		t.Fatalf("err = %v, must not suggest running ResizeVolume itself again", err)
	}
}

func TestResizeVolumeDefaultMaxPriceRefusesAndSendsNoResize(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				resizeCalls.Add(1)
				t.Fatal("no resize expected above MaxPrice")
			},
		)
	}))

	if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20}); !errors.Is(err, vngcloud.ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if resizeCalls.Load() != 0 {
		t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
	}
}

func TestResizeVolumeRejectsInvalidMaxPriceBeforeAnyRequest(t *testing.T) {
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
			if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: tt.v}); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("ResizeVolume() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestResizeVolumeQuoteFailureSendsNoResize(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
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

	if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000}); err == nil {
		t.Fatal("err = nil, want the quote's error")
	}
	if resizeCalls.Load() != 0 {
		t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
	}
}

// TestResizeVolumeInvalidQuotePriceSendsNoResize checks that a quote
// response carrying a price the guard cannot safely compare, null,
// negative, or a bare NaN literal, refuses the resize with nothing sent.
func TestResizeVolumeInvalidQuotePriceSendsNoResize(t *testing.T) {
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
				routeResizeVolumeRequest(t, w, r,
					func(w http.ResponseWriter, r *http.Request) {
						_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
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

			if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000}); err == nil {
				t.Fatal("err = nil, want an error for an invalid quote price")
			}
			if resizeCalls.Load() != 0 {
				t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
			}
		})
	}
}

func TestResizeVolumeNotRetriedAfter502(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				resizeCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"upstream error"}`))
			},
		)
	}))

	_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if resizeCalls.Load() != 1 {
		t.Fatalf("resize calls = %d, want 1: Once must never be retried", resizeCalls.Load())
	}
}

func TestResizeVolume400ReturnsAPIError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"bad request"}`))
			},
		)
	}))

	_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 400 {
		t.Fatalf("err = %v, want a 400 *core.APIError", err)
	}
	if errors.Is(err, ErrNotSettled) {
		t.Fatal("err wraps ErrNotSettled, want the raw APIError since the server never acted")
	}
}

func TestResizeVolumeNoWaitReturnsAtOnce(t *testing.T) {
	getCalls := 0
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			},
		)
	}))

	if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000, NoWait: true}); err != nil {
		t.Fatalf("ResizeVolume() error = %v", err)
	}
	if getCalls != 1 {
		t.Fatalf("GET calls = %d, want 1 (the pre-resize read only)", getCalls)
	}
}

func TestResizeVolumeWaitFailsOnError(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				if getCalls == 1 {
					_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
					return
				}
				_, _ = w.Write([]byte(volumeBodyWithSize("ERROR", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			},
		)
	})))

	_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
}

func TestResizeVolumeWaitBoundReached(t *testing.T) {
	getCalls := 0
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				getCalls++
				if getCalls == 1 {
					_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
					return
				}
				_, _ = w.Write([]byte(volumeBodyWithSize("RESIZING", 10)))
			}, nil,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			},
		)
	})))

	_, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000})
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestWaitVolumeResizedPollParameters checks the literal interval and
// bound waitVolumeResized passes to poll.
func TestWaitVolumeResizedPollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(volumeBodyWithSize("RESIZING", 10)))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitVolumeResized(context.Background(), "op", "volume-1", 20); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 150 {
		t.Fatalf("sleep calls = %d, want 150 (a 2s interval over a 5-minute bound)", len(sleeps))
	}
}

func TestResizeVolumeZeroQuoteRefusesAndSendsNoResize(t *testing.T) {
	var resizeCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeResizeVolumeRequest(t, w, r,
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(volumeBodyWithSize("AVAILABLE", 10)))
			},
			func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				resizeCalls.Add(1)
				t.Error("no resize expected for a 0 quote")
			},
		)
	}))
	if _, err := c.ResizeVolume(context.Background(), &ResizeVolumeInput{VolumeID: "volume-1", Size: 20, MaxPrice: 1000000}); !errors.Is(err, vngcloud.ErrUnpriced) {
		t.Fatalf("err = %v, want ErrUnpriced", err)
	}
	if resizeCalls.Load() != 0 {
		t.Fatalf("resize calls = %d, want 0", resizeCalls.Load())
	}
}
