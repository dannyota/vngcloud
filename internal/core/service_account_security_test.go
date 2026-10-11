package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceAccountPrivateClient(t *testing.T) {
	for _, timeout := range []time.Duration{-time.Second, 0, time.Second} {
		client := newServiceAccountHTTPClient(timeout, nil)
		want := timeout
		if want <= 0 {
			want = 120 * time.Second
		}
		if client.Timeout != want || client.Jar != nil {
			t.Fatal("timeout or cookie jar")
		}
		rt, ok := client.Transport.(*http.Transport)
		if !ok || rt == http.DefaultTransport || rt.TLSClientConfig != nil && rt.TLSClientConfig.InsecureSkipVerify {
			t.Fatal("unsafe transport")
		}
		other := newServiceAccountHTTPClient(timeout, nil)
		if client.Transport == other.Transport {
			t.Fatal("shared transport")
		}
		rt.CloseIdleConnections()
		other.Transport.(*http.Transport).CloseIdleConnections()
	}
}

func TestServiceAccountTLSRefusal(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("untrusted TLS reached handler") }))
	defer server.Close()
	rt := newServiceAccountHTTPClient(time.Second, nil).Transport.(*http.Transport)
	rt.Proxy = nil
	rt.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	defer rt.CloseIdleConnections()
	source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Second, nil, rt, time.Now)
	_, err := source.Token(context.Background())
	var login *ServiceAccountLoginError
	if !errors.As(err, &login) || login.Reason != "token request failed" || !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
}

func TestServiceAccountHookIsolationAndSafeOutput(t *testing.T) {
	const marker = "synthetic-secret-canary"
	var log bytes.Buffer
	captures, hooks := 0, 0
	public := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) { hooks++; return nil, errors.New("public hook") })
	private := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != serviceAccountTokenURL {
			t.Fatal("endpoint override reached exchange")
		}
		return serviceAccountResponse(401, `{"errors":[{"code":"`+marker+`","message":"`+marker+`"}],"nested":"{\"secret\":\"`+marker+`\"}"}`), nil
	})
	opts := []Option{WithRegion("hcm-3"), WithServiceAccountCredentials("client", marker, true), WithHTTPClient(&http.Client{Transport: public}), WithTransport(public), WithEndpointOverrides(EndpointOverrides{Token: "https://override.example.test", IAM: "https://override.example.test", Dashboard: "https://override.example.test"}), WithResponseCapture(func(ResponseCapture) { captures++ }), WithLogger(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))), serviceAccountTestOption(private, time.Now)}
	cfg, err := NewConfig(opts...)
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Authenticate(context.Background())
	if !errors.Is(err, ErrAuth) {
		t.Fatal(err)
	}
	if hooks != 0 || captures != 0 {
		t.Fatal("public hook or capture received exchange")
	}
	values := []any{cfg, ClientOf(cfg), opts[1], err}
	settings := defaultClientConfig()
	opts[1].apply(&settings)
	values = append(values, settings, settings.serviceAccount, settings.serviceAccount.secret)
	for _, value := range values {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x"} {
			if strings.Contains(fmt.Sprintf(verb, value), marker) {
				t.Fatal("format leaked")
			}
		}
		b, jsonErr := json.Marshal(value)
		if jsonErr != nil {
			t.Fatal(jsonErr)
		}
		if strings.Contains(string(b), marker) {
			t.Fatal("JSON leaked")
		}
		if text, ok := value.(interface{ MarshalText() ([]byte, error) }); ok {
			b, textErr := text.MarshalText()
			if textErr != nil || strings.Contains(string(b), marker) {
				t.Fatal("text leaked")
			}
		}
		logger := slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug}))
		logger.DebugContext(context.Background(), "format", "value", value)
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if strings.Contains(fmt.Sprintf("%#v", cause), marker) {
			t.Fatal("chain leaked")
		}
	}
	if strings.Contains(log.String(), marker) || !strings.Contains(log.String(), "exchange started") || !strings.Contains(log.String(), "ok=false") {
		t.Fatal("unsafe exchange log")
	}
}

func TestServiceAccountFailureCanaries(t *testing.T) {
	const marker = "synthetic-canary"
	for _, mode := range []string{"network", "decode", "redirect", "reject"} {
		rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
			echo := marker + " " + r.Header.Get("Authorization") + " https://" + marker + " Cookie=" + marker
			if mode == "network" {
				return nil, errors.New(echo)
			}
			status := 200
			if mode == "redirect" {
				status = 302
			}
			if mode == "reject" {
				status = 500
			}
			response := serviceAccountResponse(status, echo)
			response.Header.Set("Location", "https://"+marker)
			response.Header.Set("Set-Cookie", marker)
			return response, nil
		})
		source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret(marker)}, time.Second, nil, rt, time.Now)
		_, err := source.Token(context.Background())
		if !errors.Is(err, ErrAuth) {
			t.Fatal(err)
		}
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			for _, verb := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(verb, cause), marker) || strings.Contains(fmt.Sprintf(verb, cause), "Basic ") {
					t.Fatal("failure leaked")
				}
			}
		}
	}
}

func TestServiceAccountHTTP2RefusalSendsOnce(t *testing.T) {
	for _, goAway := range []bool{false, true} {
		name := "REFUSED_STREAM"
		if goAway {
			name = "GOAWAY"
		}
		t.Run(name, func(t *testing.T) {
			// Borrow only the loopback certificate; the peer below speaks real HTTP/2.
			certificateServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			cert := certificateServer.Certificate()
			tlsConfig := &tls.Config{Certificates: certificateServer.TLS.Certificates, NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS12}
			certificateServer.Close()
			tcp, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			listener := tls.NewListener(tcp, tlsConfig)
			var sends atomic.Int32
			var mu sync.Mutex
			var active net.Conn
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					mu.Lock()
					active = conn
					mu.Unlock()
					serveServiceAccountHTTP2Refusal(t, conn, goAway, &sends)
					_ = conn.Close()
					mu.Lock()
					active = nil
					mu.Unlock()
				}
			}()
			t.Cleanup(func() {
				_ = listener.Close()
				mu.Lock()
				if active != nil {
					_ = active.Close()
				}
				mu.Unlock()
				<-done
			})
			client := newServiceAccountHTTPClient(3*time.Second, nil)
			rt := client.Transport.(*http.Transport)
			rt.Proxy = nil
			roots := x509.NewCertPool()
			roots.AddCert(cert)
			serverName := cert.IPAddresses[0].String()
			if len(cert.DNSNames) > 0 {
				serverName = cert.DNSNames[0]
			}
			rt.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12}
			rt.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, tcp.Addr().String())
			}
			defer rt.CloseIdleConnections()
			cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("synthetic-client", "synthetic-secret", true), serviceAccountTestOption(rt, func() time.Time { return time.Unix(1700000000, 0) }))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err = cfg.Authenticate(ctx)
			var login *ServiceAccountLoginError
			if !errors.As(err, &login) || login.Reason != "token request failed" {
				t.Fatalf("unexpected exchange error: %v", err)
			}
			if got := sends.Load(); got != 1 {
				t.Fatalf("physical token POSTs = %d, want 1", got)
			}
			if !errors.Is(err, ErrAuth) {
				t.Fatal("refusal did not fail promptly as authentication error")
			}
		})
	}
}

// serveServiceAccountHTTP2Refusal rejects only completed request streams, so
// the counter proves that the peer received the full token POST each time.
func serveServiceAccountHTTP2Refusal(t *testing.T, conn net.Conn, goAway bool, sends *atomic.Int32) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Error(err)
		return
	}
	tlsConn := conn.(*tls.Conn)
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		return
	}
	if tlsConn.ConnectionState().NegotiatedProtocol != "h2" {
		t.Error("peer did not negotiate HTTP/2")
		return
	}
	preface := make([]byte, len("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return
	}
	if string(preface) != "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n" {
		t.Error("invalid HTTP/2 preface")
		return
	}
	if err := writeServiceAccountHTTP2Frame(conn, 4, 0, 0, nil); err != nil {
		return
	}
	bodies := make(map[uint32][]byte)
	for {
		var header [9]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		length := int(header[0])<<16 | int(header[1])<<8 | int(header[2])
		if length > 1<<16 {
			t.Error("unexpected oversized HTTP/2 frame")
			return
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		stream := binary.BigEndian.Uint32(header[5:]) & 0x7fffffff
		switch header[3] {
		case 4:
			if header[4]&1 == 0 {
				if err := writeServiceAccountHTTP2Frame(conn, 4, 1, 0, nil); err != nil {
					return
				}
			}
		case 0:
			bodies[stream] = append(bodies[stream], payload...)
			if header[4]&1 == 0 {
				continue
			}
			if string(bodies[stream]) != `{"grant_type":"client_credentials"}` {
				t.Error("unexpected physical token request body")
				return
			}
			delete(bodies, stream)
			sends.Add(1)
			if goAway {
				// LastStreamID zero declares the completed request unprocessed.
				_ = writeServiceAccountHTTP2Frame(conn, 7, 0, 0, make([]byte, 8))
				return
			}
			reset := make([]byte, 4)
			binary.BigEndian.PutUint32(reset, 7)
			if err := writeServiceAccountHTTP2Frame(conn, 3, 0, stream, reset); err != nil {
				return
			}
		}
	}
}

func writeServiceAccountHTTP2Frame(w io.Writer, kind, flags byte, stream uint32, payload []byte) error {
	header := make([]byte, 9)
	if len(payload) > 0xffffff {
		return errors.New("HTTP/2 test frame exceeds 24-bit length")
	}
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload))) //nolint:gosec // len is nonnegative and bounded above to 24 bits before conversion.
	copy(header[:3], length[1:])
	header[3], header[4] = kind, flags
	binary.BigEndian.PutUint32(header[5:], stream)
	_, err := w.Write(append(header, payload...))
	return err
}
