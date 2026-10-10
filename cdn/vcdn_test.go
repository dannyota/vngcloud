package cdn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

const testKey = "<secret>-vcdn-api-key"

// vcdnHarness is a client wired to an httptest server with an API key, a
// token source that fails the test if used, a capture hook, and a debug log.
type vcdnHarness struct {
	*Client
	requests atomic.Int64
	captured atomic.Int64
	logs     bytes.Buffer
	writes   bytes.Buffer
}

func newVCDN(t *testing.T, key string, handler func(w http.ResponseWriter, r *http.Request)) *vcdnHarness {
	t.Helper()
	h := &vcdnHarness{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.requests.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	tc := transport.New(transport.Config{
		HTTPClient:    server.Client(),
		TokenSource:   failTokenSource{t},
		RetryCount:    2,
		RetryInterval: time.Millisecond,
		Capture: func(c transport.Capture) {
			h.captured.Add(1)
			if c.Operation == "cdn.UpdateWebAccelerator" {
				h.writes.Write(c.Body)
			}
		},
		Logger: slog.New(slog.NewTextHandler(&h.logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	set := endpoints.Set{CDN: server.URL + "/vcdn-api/"}
	h.Client = New(core.NewTestConfigWithCDNAPIKey("hcm-3", "", set, tc, key))
	return h
}

// reply returns a handler that answers status with body.
func reply(status int, contentType, body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../testdata/cdn/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func apiError(t *testing.T, err error) *core.APIError {
	t.Helper()
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *APIError", err, err)
	}
	return apiErr
}

func TestVCDNCallsWithoutKeyFailBeforeAnyRequest(t *testing.T) {
	h := newVCDN(t, "", reply(200, "", `{}`))
	calls := map[string]func() error{
		"ListCertificates": func() error { _, err := h.ListCertificates(context.Background(), nil); return err },
		"GetCertificate": func() error {
			_, err := h.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "c-1"})
			return err
		},
		"ListAPIKeys": func() error { _, err := h.ListAPIKeys(context.Background(), nil); return err },
	}
	for name, run := range calls {
		err := run()
		if !errors.Is(err, ErrNoAPIKey) || !errors.Is(err, vngcloud.ErrNoCredentials) {
			t.Errorf("%s err = %v, want ErrNoAPIKey wrapping ErrNoCredentials", name, err)
		}
	}
	if h.requests.Load() != 0 {
		t.Fatal("a request was sent")
	}
}

func TestVCDNZeroConfig(t *testing.T) {
	if _, err := New(vngcloud.Config{}).ListAPIKeys(context.Background(), nil); !errors.Is(err, vngcloud.ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestVCDNRequestShape(t *testing.T) {
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/vcdn-api/v1/certificate/list" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testKey {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("Origin"); got != "" {
			t.Errorf("Origin = %q, want none", got)
		}
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":[]}`))
	})
	out, err := h.ListCertificates(context.Background(), nil)
	if err != nil || len(out.Items) != 0 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if strings.Contains(h.logs.String(), testKey) || !strings.Contains(h.logs.String(), "/vcdn-api/v1/certificate/list") {
		t.Fatalf("debug log = %q, want the path and no key", h.logs.String())
	}
}

func TestVCDNErrorTable(t *testing.T) {
	const noReason = "vCDN ListCertificates failed; the server gave no reason"
	const withheld = "vCDN ListCertificates refused: the server message named an account user and was withheld; check that every domain is a CDN of this account"
	cases := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantStatus  int
		wantCode    string
		wantMessage string
		wantIs      error
		wantRetries bool
	}{
		{"401 empty body", 401, "", ``, 401, "Unauthorized", rejectedKeyMessage, vngcloud.ErrAuth, false},
		{"401 with a body", 401, "application/json", `{"message":"token abc"}`, 401, "Unauthorized", rejectedKeyMessage, vngcloud.ErrAuth, false},
		{"403 names the user", 403, "application/json", `{"message":"user someone@example.test may not"}`, 403, "Forbidden", forbiddenKeyMessage, vngcloud.ErrPermission, false},
		{"403 plain text", 403, "text/plain", `Invalid CORS request`, 403, "Forbidden", forbiddenKeyMessage, vngcloud.ErrPermission, false},
		{"400 problem+json", 400, "application/problem+json", `{"type":"about:blank","title":"Bad Request","status":400,"detail":"JSON parse error","instance":"/vcdn-api/v1/x"}`, 400, "BadRequest", "JSON parse error", vngcloud.ErrInvalidInput, false},
		{"404 problem+json", 404, "application/problem+json", `{"title":"Not Found","status":404,"detail":"No static resource webacc/list."}`, 404, "NotFound", "No static resource webacc/list.", vngcloud.ErrNotFound, false},
		{"405 title only", 405, "application/problem+json", `{"title":"Method Not Allowed","status":405}`, 405, "ClientError", "Method Not Allowed", nil, false},
		{"503 is retried", 503, "", ``, 503, "ServerError", "Service Unavailable", nil, true},
		{"envelope 500 null message", 200, "application/json", `{"success":false,"code":500,"message":null,"data":""}`, 200, "500", noReason, nil, false},
		{"envelope 500 empty message", 200, "application/json", `{"success":false,"code":500,"message":"","data":""}`, 200, "500", noReason, nil, false},
		{"envelope 500 text", 200, "application/json", `{"success":false,"code":500,"message":"Domain already exists","data":null}`, 200, "500", "Domain already exists", nil, false},
		{"envelope message with fullwidth at", 200, "application/json", `{"success":false,"code":500,"message":"User a＠b.test is not the owner","data":""}`, 200, "500", withheld, nil, false},
		{"envelope message with small at", 200, "application/json", `{"success":false,"code":500,"message":"User a﹫b.test is not the owner","data":""}`, 200, "500", withheld, nil, false},
		{"400 message with fullwidth at", 400, "application/json", `{"message":"user a＠b.test"}`, 400, "BadRequest", withheld, vngcloud.ErrInvalidInput, false},
		{"envelope message with @", 200, "application/json", `{"success":false,"code":500,"message":"User a@b.test is not the owner","data":""}`, 200, "500", withheld, nil, false},
		{"envelope code 400", 200, "application/json", `{"success":false,"code":400,"message":"bad domain","data":""}`, 200, "400", "bad domain", vngcloud.ErrInvalidInput, false},
		{"envelope code 401", 200, "application/json", `{"success":false,"code":401,"message":"x","data":""}`, 200, "401", rejectedKeyMessage, vngcloud.ErrAuth, false},
		{"envelope code 403", 200, "application/json", `{"success":false,"code":"403","message":"user a@b.test","data":""}`, 200, "403", forbiddenKeyMessage, vngcloud.ErrPermission, false},
		{"envelope code 404", 200, "application/json", `{"success":false,"code":404,"message":"gone","data":""}`, 200, "404", "gone", vngcloud.ErrNotFound, false},
		{"non-JSON 200", 200, "text/html", `<html>hi</html>`, 200, codeEmptyResponse, "vCDN ListCertificates returned an unexpected response", nil, false},
		{"empty 200", 200, "", ``, 200, codeEmptyResponse, "vCDN ListCertificates returned an unexpected response", nil, false},
		{"JSON without envelope", 200, "application/json", `{"items":[]}`, 200, codeEmptyResponse, "vCDN ListCertificates returned an unexpected response", nil, false},
		{"bare list", 200, "application/json", `[]`, 200, codeEmptyResponse, "vCDN ListCertificates returned an unexpected response", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newVCDN(t, testKey, reply(tc.status, tc.contentType, tc.body))
			_, err := h.ListCertificates(context.Background(), nil)
			apiErr := apiError(t, err)
			if apiErr.StatusCode != tc.wantStatus || apiErr.Code != tc.wantCode || apiErr.Message != tc.wantMessage {
				t.Errorf("got status %d code %q message %q, want %d %q %q",
					apiErr.StatusCode, apiErr.Code, apiErr.Message, tc.wantStatus, tc.wantCode, tc.wantMessage)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err = %v, want it to match %v", err, tc.wantIs)
			}
			if tc.wantIs == nil {
				for _, s := range []error{vngcloud.ErrAuth, vngcloud.ErrPermission, vngcloud.ErrNotFound, vngcloud.ErrInvalidInput} {
					if errors.Is(err, s) {
						t.Errorf("err matches %v, want no sentinel", s)
					}
				}
			}
			if tc.wantRetries != (h.requests.Load() > 1) {
				t.Errorf("requests = %d, retries wanted = %v", h.requests.Load(), tc.wantRetries)
			}
			if strings.ContainsAny(err.Error(), "@\uff20\ufe6b") {
				t.Errorf("error holds an account name: %q", err)
			}
		})
	}
}

func TestVCDN401IsNeverRetried(t *testing.T) {
	h := newVCDN(t, testKey, reply(401, "", ``))
	if _, err := h.ListAPIKeys(context.Background(), nil); !errors.Is(err, vngcloud.ErrAuth) {
		t.Fatalf("err = %v", err)
	}
	if h.requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", h.requests.Load())
	}
}

func TestVCDNEnvelopeFailureIsNeverRetried(t *testing.T) {
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":false,"code":500,"message":null,"data":""}`))
	_, _ = h.ListCertificates(context.Background(), nil)
	if h.requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", h.requests.Load())
	}
}

func TestVCDN503ThenSuccess(t *testing.T) {
	var n atomic.Int64
	h := newVCDN(t, testKey, func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"code":200,"data":[]}`))
	})
	if _, err := h.ListCertificates(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestVCDNDetailEmptyDataIsNotFound(t *testing.T) {
	for _, data := range []string{`""`, `null`} {
		h := newVCDN(t, testKey, reply(200, "application/json", `{"success":false,"code":500,"message":null,"data":`+data+`}`))
		_, err := h.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "c-1"})
		apiErr := apiError(t, err)
		if !errors.Is(err, vngcloud.ErrNotFound) || !vngcloud.IsNotFound(err) || apiErr.Code != "NotFound" {
			t.Errorf("data %s: err = %v code %q, want NotFound", data, err, apiErr.Code)
		}
		if h.requests.Load() != 1 {
			t.Errorf("requests = %d, want 1", h.requests.Load())
		}
	}
	// A list read has no detail rule: the same envelope is a plain failure.
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":false,"code":500,"message":null,"data":""}`))
	if _, err := h.ListCertificates(context.Background(), nil); errors.Is(err, vngcloud.ErrNotFound) {
		t.Fatalf("list err = %v, want no ErrNotFound", err)
	}
	// A detail failure that carries data is a plain failure too.
	h = newVCDN(t, testKey, reply(200, "application/json", `{"success":false,"code":500,"message":"busy","data":{"x":1}}`))
	_, err := h.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "c-1"})
	if errors.Is(err, vngcloud.ErrNotFound) || apiError(t, err).Message != "busy" {
		t.Fatalf("detail err = %v", err)
	}
}

// A server that echoes the key reaches no error, capture, or log.
func TestVCDNKeyEchoedByServerNeverSurfaces(t *testing.T) {
	body := `{"title":"Bad Request","detail":"cannot parse ` + testKey + ` here","code":"` + testKey + `"}`
	h := newVCDN(t, testKey, reply(400, "application/problem+json", body))
	_, err := h.ListAPIKeys(context.Background(), nil)
	apiErr := apiError(t, err)
	for name, text := range map[string]string{"error": err.Error(), "message": apiErr.Message, "code": apiErr.Code, "log": h.logs.String(), "format": fmt.Sprintf("%v %+v %#v", err, apiErr, apiErr)} {
		if strings.Contains(text, testKey) {
			t.Errorf("%s holds the key: %q", name, text)
		}
	}
	if !strings.Contains(apiErr.Message, "cannot parse") {
		t.Errorf("message = %q, want the rest kept", apiErr.Message)
	}
}

func TestVCDNKeyEchoedInEnvelopeNeverSurfaces(t *testing.T) {
	body := `{"success":false,"code":"` + testKey + `","message":"invalid token ` + testKey + `"}`
	h := newVCDN(t, testKey, reply(200, "application/json", body))
	_, err := h.ListAPIKeys(context.Background(), nil)
	apiErr := apiError(t, err)
	for name, text := range map[string]string{"error": err.Error(), "message": apiErr.Message, "code": apiErr.Code, "log": h.logs.String(), "format": fmt.Sprintf("%v %+v %#v", err, apiErr, apiErr)} {
		if strings.Contains(text, testKey) {
			t.Errorf("%s holds the key: %q", name, text)
		}
	}
	if apiErr.Code != "[redacted]" || !strings.Contains(apiErr.Message, "[redacted]") {
		t.Errorf("code = %q, message = %q, want [redacted] in both", apiErr.Code, apiErr.Message)
	}
}

func TestVCDNMessagesAreCleanedAndCut(t *testing.T) {
	long := strings.Repeat("é", 200) + "\x07tail"
	quoted, err := json.Marshal("line\none\u0007 " + long)
	if err != nil {
		t.Fatal(err)
	}
	h := newVCDN(t, testKey, reply(200, "application/json", `{"success":false,"code":500,"message":`+string(quoted)+`,"data":""}`))
	_, err = h.ListCertificates(context.Background(), nil)
	msg := apiError(t, err).Message
	if len(msg) > maxMessageBytes || strings.ContainsAny(msg, "\n\a") || !strings.HasPrefix(msg, "lineone ") {
		t.Fatalf("message = %q (%d bytes)", msg, len(msg))
	}
	if !strings.HasSuffix(msg, "é") {
		t.Fatalf("message was cut inside a rune: %q", msg)
	}
}

func TestVCDNSensitiveReadsReachNoCapture(t *testing.T) {
	fixtures := map[string]func(*vcdnHarness) error{
		"certificate-list.json": func(h *vcdnHarness) error { _, err := h.ListCertificates(context.Background(), nil); return err },
		"certificate-detail.json": func(h *vcdnHarness) error {
			_, err := h.GetCertificate(context.Background(), &GetCertificateInput{CertificateID: "cert-1"})
			return err
		},
		"apikey-list.json": func(h *vcdnHarness) error { _, err := h.ListAPIKeys(context.Background(), nil); return err },
	}
	for name, run := range fixtures {
		h := newVCDN(t, "<secret>", reply(200, "application/json", readFixture(t, name)))
		if err := run(h); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if h.captured.Load() != 0 {
			t.Errorf("%s: capture hook ran", name)
		}
		if strings.Contains(h.logs.String(), "private-key") || strings.Contains(h.logs.String(), "<secret") {
			t.Errorf("%s: log holds body content: %q", name, h.logs.String())
		}
	}
}
