package compute

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

const consoleMarker = "synthetic-console-password-marker"

func consoleClient(t *testing.T, handler http.HandlerFunc, retries int, debug *bytes.Buffer) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	tc := transport.New(transport.Config{
		HTTPClient: srv.Client(), RetryCount: retries, RetryInterval: time.Nanosecond,
		Capture: func(transport.Capture) { t.Error("console response captured") },
		Logger:  slog.New(slog.NewTextHandler(debug, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	return New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VServer: srv.URL + "/"}, tc))
}

func TestConsoleLogShapes(t *testing.T) {
	fixture, err := os.ReadFile("../testdata/compute/get_server_console_log.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, body, want string
		valid            bool
	}{
		{"fixture", string(fixture), "boot\n" + consoleMarker + "\n", true},
		{"empty log", `{"data":""}`, "", true},
		{"extra fields", `{"data":"ok","status":"STOPPED"}`, "ok", true},
		{"missing", `{}`, "", false},
		{"wrong key case", `{"DATA":"ok"}`, "", false}, {"null data", `{"data":null}`, "", false},
		{"number", `{"data":123}`, "", false}, {"object data", `{"data":{}}`, "", false},
		{"array data", `{"data":[]}`, "", false}, {"boolean", `{"data":true}`, "", false},
		{"null envelope", `null`, "", false}, {"array envelope", `[]`, "", false},
		{"string envelope", `"` + consoleMarker + `"`, "", false},
		{"malformed", `{"data":"` + consoleMarker + `"`, "", false}, {"empty body", "", "", false},
		{"trailing JSON", `{"data":"ok"}{}`, "", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			var debug bytes.Buffer
			c := consoleClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v2/project-1/servers/server-1/console-log" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				_, _ = w.Write([]byte(tt.body))
			}, 0, &debug)
			out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "server-1"})
			if tt.valid {
				if err != nil || out == nil {
					t.Fatalf("success: %v", err)
				}
				if out.Log.Reveal() != tt.want {
					t.Fatal("wrong log")
				}
				encoded, marshalErr := json.Marshal(out)
				if marshalErr != nil {
					t.Fatal(marshalErr)
				}
				var logged bytes.Buffer
				slog.New(slog.NewJSONHandler(&logged, nil)).Info("output", "output", out)
				for _, text := range []string{fmt.Sprintf("%v %+v %#v %s %q %x", out, out, out, out, out, out), string(encoded), logged.String()} {
					if strings.Contains(text, consoleMarker) {
						t.Fatal("format leaked log")
					}
				}
			} else {
				assertConsoleError(t, out, err, 200, "RequestFailed", "console log response invalid; body withheld", false)
				var api *vngcloud.APIError
				if errors.As(err, &api) && api.Err != nil {
					t.Fatal("decode error retained")
				}
			}
			if strings.Contains(debug.String(), consoleMarker) {
				t.Fatal("debug leaked log")
			}
		})
	}
}

func assertConsoleError(t *testing.T, out *GetServerConsoleLogOutput, err error, status int, code, message string, retryable bool) {
	t.Helper()
	var api *vngcloud.APIError
	if out != nil || !errors.As(err, &api) {
		t.Fatalf("expected nil output and APIError: %v", err)
	}
	if api.Operation != "compute.GetServerConsoleLog" || api.StatusCode != status || api.Code != code || api.Message != message || api.Retryable != retryable {
		t.Fatalf("wrong APIError: %+v", api)
	}
	encoded, marshalErr := json.Marshal(api)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(fmt.Sprintf("%+v %#v %s", api, api, encoded), consoleMarker) {
		t.Fatal("error leaked log")
	}
}

func TestConsoleLogValidationBeforeAuth(t *testing.T) {
	c := New(vngcloud.Config{})
	for _, in := range []*GetServerConsoleLogInput{nil, {}, {ServerID: ".."}, {ServerID: "."}, {ServerID: "/"}, {ServerID: "?"}, {ServerID: "a%2fb"}, {ServerID: "a b"}} {
		out, err := c.GetServerConsoleLog(context.Background(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("validation: %v", err)
		}
	}
}

func TestConsoleLogStatuses(t *testing.T) {
	for _, status := range []int{201, 204, 400, 401, 403, 404, 409, 429, 500, 502, 503, 504} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var debug bytes.Buffer
			c := consoleClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = fmt.Fprintf(w, `{"code":%q,"message":%q,"errors":[{"code":%q,"message":%q}]}`, consoleMarker, consoleMarker, consoleMarker, consoleMarker)
			}, 0, &debug)
			out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "unknown-1"})
			code := core.ResolvedCode(status, "")
			if code == "" {
				code = "RequestFailed"
			}
			assertConsoleError(t, out, err, status, code, "console log request failed; body withheld", status == 429 || status == 502 || status == 503 || status == 504)
			sentinel := map[int]error{401: vngcloud.ErrAuth, 403: vngcloud.ErrPermission, 404: vngcloud.ErrNotFound, 429: vngcloud.ErrRateLimited}[status]
			if sentinel != nil && !errors.Is(err, sentinel) {
				t.Fatal("missing status sentinel")
			}
			if strings.Contains(debug.String(), consoleMarker) {
				t.Fatal("debug leaked error body")
			}
		})
	}
}

func TestConsoleLogBodyLimit(t *testing.T) {
	const capBytes = 8 << 20
	for _, status := range []int{200, 404, 503} {
		for _, compressed := range []bool{false, true} {
			for _, extra := range []int{0, 1} {
				t.Run(fmt.Sprintf("%d/gzip=%t/extra=%d", status, compressed, extra), func(t *testing.T) {
					body := `{"data":"` + strings.Repeat("a", capBytes-len(`{"data":""}`)+extra) + `"}`
					calls := 0
					var debug bytes.Buffer
					c := consoleClient(t, func(w http.ResponseWriter, _ *http.Request) {
						calls++
						if compressed {
							w.Header().Set("Content-Encoding", "gzip")
						}
						w.WriteHeader(status)
						if compressed {
							z := gzip.NewWriter(w)
							_, _ = z.Write([]byte(body))
							_ = z.Close()
						} else {
							_, _ = w.Write([]byte(body))
						}
					}, 1, &debug)
					out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "server-1"})
					switch {
					case extra == 1:
						assertConsoleError(t, out, err, 0, "ResponseTooLarge", "console log response exceeds 8 MiB", false)
						if !errors.Is(err, transport.ErrBodyTooLarge) || calls != 1 {
							t.Fatal("overflow must wrap limit error without retry")
						}
					case status == 200:
						if err != nil || out == nil || len(out.Log.Reveal()) != capBytes-len(`{"data":""}`) {
							t.Fatalf("cap success: %v", err)
						}
					default:
						assertConsoleError(t, out, err, status, core.ResolvedCode(status, ""), "console log request failed; body withheld", status == 503)
						if status == 503 && calls != 2 {
							t.Fatal("expected retry")
						}
					}
				})
			}
		}
	}
}

func TestConsoleLogRetriesAndCancellation(t *testing.T) {
	for _, status := range []int{401, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			var debug bytes.Buffer
			c := consoleClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(consoleMarker))
					return
				}
				_, _ = w.Write([]byte(`{"data":"ok"}`))
			}, 1, &debug)
			out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "server-1"})
			if status == 401 { // This test client has no token source to refresh.
				if out != nil || !errors.Is(err, vngcloud.ErrAuth) || calls != 1 {
					t.Fatalf("auth failure: %v", err)
				}
			} else if err != nil || out == nil || calls != 2 {
				t.Fatalf("retry: %v", err)
			}
			if strings.Contains(debug.String(), consoleMarker) {
				t.Fatal("retry debug leaked body")
			}
		})
	}
	t.Run("cancel during retry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		var debug bytes.Buffer
		c := consoleClient(t, func(w http.ResponseWriter, _ *http.Request) { calls++; cancel(); w.WriteHeader(503) }, 3, &debug)
		out, err := c.GetServerConsoleLog(ctx, &GetServerConsoleLogInput{ServerID: "server-1"})
		if out != nil || !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("cancellation: %v", err)
		}
	})
}

func TestConsoleLogRefusesRedirects(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			calls := 0
			var debug bytes.Buffer
			c := consoleClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 1 {
					t.Error("redirect followed")
				}
				w.Header().Set("Location", "/redirect-target?secret="+consoleMarker)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(consoleMarker))
			}, 2, &debug)
			out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "server-1"})
			assertConsoleError(t, out, err, status, "RequestFailed", "console log request failed; body withheld", false)
			if calls != 1 || strings.Contains(debug.String(), consoleMarker) {
				t.Fatal("redirect leaked or followed")
			}
		})
	}
}

type consoleTokens struct {
	calls         int
	invalidations int
}

func (s *consoleTokens) Token(context.Context) (transport.Token, error) {
	s.calls++
	return transport.Token{AccessToken: fmt.Sprintf("synthetic-token-%d", s.calls), ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func (s *consoleTokens) Invalidate(string) { s.invalidations++ }

func TestConsoleLogAuthRefresh(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer synthetic-token-%d", calls) {
			t.Error("wrong credential")
		}
		if calls == 1 {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(consoleMarker))
			return
		}
		_, _ = w.Write([]byte(`{"data":"ok"}`))
	}))
	defer srv.Close()
	tokens := &consoleTokens{}
	tc := transport.New(transport.Config{HTTPClient: srv.Client(), TokenSource: tokens, Capture: func(transport.Capture) { t.Error("response captured") }})
	c := New(core.NewTestConfig("hcm-3", "project-1", endpoints.Set{VServer: srv.URL + "/"}, tc))
	out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "server-1"})
	if err != nil || out == nil || calls != 2 || tokens.calls != 2 || tokens.invalidations != 1 {
		t.Fatalf("auth refresh: %v", err)
	}
}

func TestConsoleLogValidationSkipsTokenAndDiscovery(t *testing.T) {
	tokens := &consoleTokens{}
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request before validation") }))
	defer srv.Close()
	tc := transport.New(transport.Config{HTTPClient: srv.Client(), TokenSource: tokens})
	c := New(core.NewTestConfig("hcm-3", "", endpoints.Set{VServer: srv.URL + "/", Dashboard: srv.URL + "/"}, tc))
	for _, in := range []*GetServerConsoleLogInput{nil, {}, {ServerID: "a/b"}} {
		out, err := c.GetServerConsoleLog(context.Background(), in)
		if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatalf("validation: %v", err)
		}
	}
	if tokens.calls != 0 {
		t.Fatal("authentication before validation")
	}
}

func TestConsoleLogCanceledContext(t *testing.T) {
	var debug bytes.Buffer
	c := consoleClient(t, func(http.ResponseWriter, *http.Request) { t.Error("canceled request sent") }, 3, &debug)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := c.GetServerConsoleLog(ctx, &GetServerConsoleLogInput{ServerID: "server-1"})
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context: %v", err)
	}
}

func TestConsoleLogCancelDuringBody(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var debug bytes.Buffer
	c := consoleClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":"` + consoleMarker))
		w.(http.Flusher).Flush()
		cancel()
		<-r.Context().Done()
	}, 3, &debug)
	out, err := c.GetServerConsoleLog(ctx, &GetServerConsoleLogInput{ServerID: "server-1"})
	if out != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("body cancellation: %v", err)
	}
	if strings.Contains(debug.String(), consoleMarker) {
		t.Fatal("body cancellation leaked")
	}
}

func TestConsoleLogRefusesCrossHostRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("cross-host redirect followed") }))
	defer target.Close()
	var debug bytes.Buffer
	c := consoleClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL+"/target?secret="+consoleMarker)
		w.WriteHeader(302)
	}, 0, &debug)
	out, err := c.GetServerConsoleLog(context.Background(), &GetServerConsoleLogInput{ServerID: "server-1"})
	assertConsoleError(t, out, err, 302, "RequestFailed", "console log request failed; body withheld", false)
	if strings.Contains(debug.String(), consoleMarker) {
		t.Fatal("redirect debug leaked")
	}
}
