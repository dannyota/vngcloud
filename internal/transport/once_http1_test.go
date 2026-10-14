package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnceHTTP1NoReplay(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, body := range []bool{true, false} {
			for _, key := range []string{"", "Idempotency-Key", "X-Idempotency-Key"} {
				if method == http.MethodPost && key == "" {
					continue // HTTP/1 does not replay a POST without an idempotency key here.
				}
				for _, once := range []bool{true, false} {
					name := fmt.Sprintf("%s/body=%t/key=%s/once=%t", method, body, key, once)
					t.Run(name, func(t *testing.T) {
						var received atomic.Int32
						server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							data, err := io.ReadAll(r.Body)
							if err != nil {
								t.Error(err)
								return
							}
							if r.URL.Path == "/warmup" {
								w.WriteHeader(http.StatusNoContent)
								return
							}
							wantBody := ""
							if body {
								wantBody = `{"pay":true}`
							}
							if string(data) != wantBody {
								t.Errorf("body = %q, want %q", data, wantBody)
							}
							if received.Add(1) == 1 {
								// Close the reused connection after receiving the full
								// request, with no response, to trigger the idle retry rule.
								conn, _, err := w.(http.Hijacker).Hijack()
								if err != nil {
									t.Error(err)
									return
								}
								_ = conn.Close()
								return
							}
							w.WriteHeader(http.StatusNoContent)
						}))
						defer server.Close()
						hc := server.Client()
						hc.Timeout = 5 * time.Second
						defer hc.CloseIdleConnections()
						c := New(Config{HTTPClient: hc})
						if _, _, _, err := c.DoRaw(context.Background(), Request{URL: server.URL + "/warmup"}); err != nil {
							t.Fatal(err)
						}
						var connections atomic.Int32
						var reused atomic.Bool
						ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
							GotConn: func(info httptrace.GotConnInfo) {
								if connections.Add(1) == 1 {
									reused.Store(info.Reused)
								}
							},
						})
						notSent := true
						req := Request{URL: server.URL, Method: method, Once: once, NotSent: &notSent}
						if body {
							req.Body = map[string]bool{"pay": true}
						}
						if key != "" {
							req.Headers = map[string]string{key: "synthetic-key"}
						}
						_, _, _, err := c.DoRaw(ctx, req)
						t.Logf("complete requests=%d connections=%d reused=%t Once=%t NotSent=%t err=%v", received.Load(), connections.Load(), reused.Load(), once, notSent, err)
						want := int32(2)
						if once {
							want = 1
							assertOnceAmbiguous(t, err, notSent)
						} else if err != nil || notSent {
							t.Errorf("ordinary replay: err=%v NotSent=%t", err, notSent)
						}
						if received.Load() != want || connections.Load() != want || !reused.Load() {
							t.Errorf("received=%d connections=%d reused=%t, want %d requests/connections and reuse", received.Load(), connections.Load(), reused.Load(), want)
						}
					})
				}
			}
		}
	}
}

type onceIdleWriteConn struct {
	net.Conn
	fail *atomic.Bool
}

func (c onceIdleWriteConn) Write(data []byte) (int, error) {
	if c.fail.CompareAndSwap(true, false) {
		return 0, errors.New("synthetic dead idle connection")
	}
	return c.Conn.Write(data)
}

func TestOnceHTTP1NoReplayAfterZeroByteWrite(t *testing.T) {
	for _, body := range []bool{true, false} {
		for _, once := range []bool{true, false} {
			t.Run(fmt.Sprintf("body=%t/once=%t", body, once), func(t *testing.T) {
				var received, dials atomic.Int32
				var fail atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if _, err := io.Copy(io.Discard, r.Body); err != nil {
						t.Error(err)
					}
					if r.URL.Path != "/warmup" {
						received.Add(1)
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				defer server.Close()
				tr := server.Client().Transport.(*http.Transport).Clone()
				tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					dials.Add(1)
					conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
					if err != nil {
						return nil, err
					}
					return onceIdleWriteConn{Conn: conn, fail: &fail}, nil
				}
				defer tr.CloseIdleConnections()
				c := New(Config{HTTPClient: &http.Client{Transport: tr, Timeout: 5 * time.Second}})
				if _, _, _, err := c.DoRaw(context.Background(), Request{URL: server.URL + "/warmup"}); err != nil {
					t.Fatal(err)
				}
				fail.Store(true)
				notSent := true
				req := Request{URL: server.URL, Method: http.MethodPost, Once: once, NotSent: &notSent}
				if body {
					req.Body = map[string]bool{"pay": true}
				}
				_, _, _, err := c.DoRaw(context.Background(), req)
				t.Logf("received=%d dials=%d Once=%t NotSent=%t err=%v", received.Load(), dials.Load(), once, notSent, err)
				want := int32(1)
				if once {
					want = 0
					assertOnceAmbiguous(t, err, notSent)
				} else if err != nil || notSent {
					t.Errorf("ordinary replay: err=%v NotSent=%t", err, notSent)
				}
				if received.Load() != want || dials.Load() != want+1 || fail.Load() {
					t.Errorf("received=%d dials=%d failure armed=%t, want %d received and %d dials", received.Load(), dials.Load(), fail.Load(), want, want+1)
				}
			})
		}
	}
}
