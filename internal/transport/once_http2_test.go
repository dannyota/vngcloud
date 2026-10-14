package transport

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOnceHTTP2NoReplay(t *testing.T) {
	for _, failure := range []string{"REFUSED_STREAM", "GOAWAY"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodGet} {
			for _, body := range []bool{true, false} {
				for _, once := range []bool{true, false} {
					name := fmt.Sprintf("%s/%s/body=%t/once=%t", failure, method, body, once)
					t.Run(name, func(t *testing.T) {
						var received atomic.Int32
						wantBody := ""
						if body {
							wantBody = `{"pay":true}`
						}
						server := httptest.NewUnstartedServer(http.NotFoundHandler())
						server.EnableHTTP2 = true
						// Control frames are not exposed by ResponseWriter. Serve the
						// negotiated HTTP/2 connection directly, using the TLS server's
						// certificate and the client's normal HTTP/2 transport.
						server.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){
							"h2": func(_ *http.Server, conn *tls.Conn, _ http.Handler) {
								if err := serveOnceHTTP2(conn, failure, wantBody, &received); err != nil &&
									!errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
									t.Errorf("HTTP/2 server: %v", err)
								}
							},
						}
						server.StartTLS()
						defer server.Close()
						hc := server.Client()
						hc.Timeout = 5 * time.Second
						defer hc.CloseIdleConnections()
						c := New(Config{HTTPClient: hc, RetryCount: 3, RetryInterval: time.Millisecond})
						if _, _, _, err := c.DoRaw(context.Background(), Request{URL: server.URL}); err != nil {
							t.Fatalf("warm connection: %v", err)
						}
						notSent := true
						req := Request{URL: server.URL, Method: method, Once: once, NotSent: &notSent}
						if body {
							req.Body = map[string]bool{"pay": true}
						}
						_, _, _, err := c.DoRaw(context.Background(), req)
						mutations := received.Load() - 1 // Exclude the successful warmup.
						t.Logf("complete requests=%d Once=%t NotSent=%t err=%v", mutations, once, notSent, err)
						want := int32(2)
						if once {
							want = 1
							assertOnceAmbiguous(t, err, notSent)
						} else if err != nil || notSent {
							t.Errorf("ordinary replay: err=%v NotSent=%t", err, notSent)
						}
						if mutations != want {
							t.Errorf("complete requests = %d, want %d", mutations, want)
						}
					})
				}
			}
		}
	}
}

// The first stream warms the connection. The second is refused only after
// END_STREAM, so the counter and body check prove the entire request arrived.
func serveOnceHTTP2(conn net.Conn, failure, wantBody string, received *atomic.Int32) error {
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	const preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"
	buf := make([]byte, len(preface))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if string(buf) != preface {
		return errors.New("missing HTTP/2 preface")
	}
	if err := writeOnceHTTP2Frame(conn, 4, 0, 0, nil); err != nil { // SETTINGS
		return err
	}
	var body []byte
	for {
		var header [9]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return err
		}
		length := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
		if length > 16384 {
			return fmt.Errorf("unexpected frame length %d", length)
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return err
		}
		kind, flags := header[3], header[4]
		stream := binary.BigEndian.Uint32(header[5:]) & 0x7fffffff
		if kind == 4 && flags&1 == 0 { // SETTINGS ACK
			if err := writeOnceHTTP2Frame(conn, 4, 1, 0, nil); err != nil {
				return err
			}
		}
		if kind == 0 { // DATA
			body = append(body, payload...)
		}
		if (kind != 0 && kind != 1) || stream == 0 || flags&1 == 0 {
			continue
		}
		n := received.Add(1)
		if n > 1 && string(body) != wantBody {
			return fmt.Errorf("request body = %q, want %q", body, wantBody)
		}
		body = nil
		if n == 2 {
			if stream != 3 {
				return fmt.Errorf("refused stream = %d, want 3 on the reused connection", stream)
			}
			if failure == "GOAWAY" {
				// Exclude the fully received stream from the processed range.
				payload := make([]byte, 8)
				binary.BigEndian.PutUint32(payload, stream-2)
				return writeOnceHTTP2Frame(conn, 7, 0, 0, payload)
			}
			if err := writeOnceHTTP2Frame(conn, 3, 0, stream, []byte{0, 0, 0, 7}); err != nil {
				return err // RST_STREAM REFUSED_STREAM
			}
			continue
		}
		// HPACK static index 8 encodes :status 200; END_HEADERS | END_STREAM.
		if err := writeOnceHTTP2Frame(conn, 1, 5, stream, []byte{0x88}); err != nil {
			return err
		}
	}
}

func writeOnceHTTP2Frame(conn net.Conn, kind, flags byte, stream uint32, payload []byte) error {
	length := len(payload)
	if length > 16384 {
		return errors.New("test frame exceeds the default HTTP/2 frame limit")
	}
	frame := make([]byte, 9+length)
	binary.BigEndian.PutUint16(frame[1:3], uint16(length))
	frame[3], frame[4] = kind, flags
	binary.BigEndian.PutUint32(frame[5:9], stream)
	copy(frame[9:], payload)
	_, err := conn.Write(frame)
	return err
}

func assertOnceAmbiguous(t *testing.T, err error, notSent bool) {
	t.Helper()
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Retryable || apiErr.StatusCode != 0 || notSent {
		t.Errorf("want ambiguous APIError with Retryable=false, StatusCode=0, NotSent=false; err=%v NotSent=%t", err, notSent)
	}
}
