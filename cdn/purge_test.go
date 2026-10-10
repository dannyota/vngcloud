package cdn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/transport"
)

func TestPurgePathsRequest(t *testing.T) {
	const wantBody = `{"cdnDomain":"cdn.example.test","type":"URI","patterns":["/","..","?"]}`
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/vcdn-api/v1/cdn/flush-cache" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) != wantBody {
			t.Errorf("body = %s, want %s", body, wantBody)
		}
		jsonReply(w, `{"success":true,"code":200,"message":"ok","data":""}`)
	})
	out, err := h.PurgePaths(context.Background(), &PurgePathsInput{
		CDNDomain: "cdn.example.test",
		Paths:     []string{"/", "..", "?"},
	})
	if err != nil || out == nil {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestPurgePathsRejectsBadInputWithoutARequest(t *testing.T) {
	cases := map[string]*PurgePathsInput{
		"nil input":       nil,
		"empty domain":    {Paths: []string{"/asset.js"}},
		"nil paths":       {CDNDomain: "cdn.example.test"},
		"empty paths":     {CDNDomain: "cdn.example.test", Paths: []string{}},
		"empty path":      {CDNDomain: "cdn.example.test", Paths: []string{"/asset.js", ""}},
		"wildcard path":   {CDNDomain: "cdn.example.test", Paths: []string{"/asset-*.js"}},
		"only wildcard":   {CDNDomain: "cdn.example.test", Paths: []string{"*"}},
		"wildcard prefix": {CDNDomain: "cdn.example.test", Paths: []string{"*/asset.js"}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			h := newVCDN(t, testKey, reply(200, "application/json", `{}`))
			out, err := h.PurgePaths(context.Background(), in)
			if out != nil || !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("out = %+v, err = %v, want ErrInvalidInput", out, err)
			}
			if h.requests.Load() != 0 {
				t.Fatalf("requests = %d, want 0", h.requests.Load())
			}
		})
	}
}

func TestPurgePathsHTTPErrorTable(t *testing.T) {
	cases := []struct {
		name        string
		status      int
		body        string
		wantCode    string
		wantIs      error
		wantMessage string
	}{
		{"400", 400, `{"message":"bad purge"}`, "BadRequest", vngcloud.ErrInvalidInput, "bad purge"},
		{"401", 401, `{"message":"echo ` + testKey + `"}`, "Unauthorized", vngcloud.ErrAuth, rejectedKeyMessage},
		{"403", 403, `{"message":"user a@example.test"}`, "Forbidden", vngcloud.ErrPermission, forbiddenKeyMessage},
		{"404", 404, `{"message":"missing"}`, "NotFound", vngcloud.ErrNotFound, "missing"},
		{"500", 500, `{"message":"failed"}`, "ServerError", nil, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newVCDN(t, testKey, reply(tc.status, "application/json", tc.body))
			_, err := h.PurgePaths(context.Background(), &PurgePathsInput{CDNDomain: "cdn.example.test", Paths: []string{"/asset.js"}})
			apiErr := apiError(t, err)
			if apiErr.Code != tc.wantCode || apiErr.Message != tc.wantMessage {
				t.Errorf("code = %q, message = %q", apiErr.Code, apiErr.Message)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Errorf("err = %v, want %v", err, tc.wantIs)
			}
			if h.requests.Load() != 1 {
				t.Errorf("requests = %d, want 1", h.requests.Load())
			}
		})
	}
}

func TestPurgePathsEnvelopeErrorTable(t *testing.T) {
	const withheld = "vCDN PurgePaths refused: the server message named an account user and was withheld; check that every domain is a CDN of this account"
	cases := []struct {
		name        string
		body        string
		wantCode    string
		wantMessage string
		wantIs      error
		wantOnly    bool
	}{
		{"cooldown", `{"success":false,"code":202,"message":"Last CDN flush cache time is 10/10/2026 09:00:00","data":""}`, "202", "Last CDN flush cache time is 10/10/2026 09:00:00", ErrPurgeCooldown, true},
		{"other 202", `{"success":false,"code":202,"message":"URI invalid","data":""}`, "202", "URI invalid", vngcloud.ErrInvalidInput, true},
		{"busy 500", `{"success":false,"code":500,"message":"Current cdn status is not allow to update or delete","data":""}`, "500", "Current cdn status is not allow to update or delete", ErrBusy, true},
		{"busy 400", `{"success":false,"code":400,"message":"Current cdn status is not allow to update or delete","data":""}`, "400", "Current cdn status is not allow to update or delete", ErrBusy, true},
		{"not found null code", `{"success":false,"code":null,"message":"Not found cdn example","data":""}`, "NotFound", "Not found cdn example", vngcloud.ErrNotFound, true},
		{"code 400", `{"success":false,"code":400,"message":"bad","data":""}`, "400", "bad", vngcloud.ErrInvalidInput, false},
		{"code 401", `{"success":false,"code":401,"message":"ignored","data":""}`, "401", rejectedKeyMessage, vngcloud.ErrAuth, false},
		{"code 403", `{"success":false,"code":403,"message":"ignored","data":""}`, "403", forbiddenKeyMessage, vngcloud.ErrPermission, false},
		{"code 404", `{"success":false,"code":404,"message":"gone","data":""}`, "404", "gone", vngcloud.ErrNotFound, false},
		{"null code", `{"success":false,"code":null,"message":"failed","data":""}`, "EnvelopeError", "failed", nil, false},
		{"code 500", `{"success":false,"code":500,"message":"failed","data":""}`, "500", "failed", nil, false},
		{"account name", `{"success":false,"code":500,"message":"User a@example.test is not the owner","data":""}`, "500", withheld, nil, false},
		{"missing message", `{"success":false,"code":500,"message":null,"data":""}`, "500", "vCDN PurgePaths failed; the server gave no reason", nil, false},
	}
	allSentinels := []error{ErrPurgeCooldown, ErrBusy, vngcloud.ErrInvalidInput, vngcloud.ErrAuth, vngcloud.ErrPermission, vngcloud.ErrNotFound}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newVCDN(t, testKey, reply(200, "application/json", tc.body))
			_, err := h.PurgePaths(context.Background(), &PurgePathsInput{CDNDomain: "cdn.example.test", Paths: []string{"/asset.js"}})
			apiErr := apiError(t, err)
			if apiErr.Code != tc.wantCode || apiErr.Message != tc.wantMessage {
				t.Errorf("code = %q, message = %q, want %q, %q", apiErr.Code, apiErr.Message, tc.wantCode, tc.wantMessage)
			}
			for _, sentinel := range allSentinels {
				matched := errors.Is(err, sentinel)
				if errors.Is(tc.wantIs, sentinel) {
					if !matched {
						t.Errorf("err = %v, want %v", err, sentinel)
					}
				} else if tc.wantOnly && matched {
					t.Errorf("err also matches %v", sentinel)
				}
			}
			if h.requests.Load() != 1 {
				t.Errorf("requests = %d, want 1", h.requests.Load())
			}
		})
	}
}

func TestPurgePathsRetryBoundary(t *testing.T) {
	t.Run("429 is retried", func(t *testing.T) {
		var calls atomic.Int64
		h := newVCDN(t, testKey, func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) == 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			jsonReply(w, `{"success":true,"code":200,"data":""}`)
		})
		if _, err := h.PurgePaths(context.Background(), &PurgePathsInput{CDNDomain: "cdn.example.test", Paths: []string{"/asset.js"}}); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 {
			t.Fatalf("calls = %d, want 2", calls.Load())
		}
	})

	t.Run("failed dial is retried", func(t *testing.T) {
		var calls atomic.Int64
		client := newPurgeRoundTripClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
			}
			return purgeHTTPResponse(200, `{"success":true,"code":200,"data":""}`), nil
		}))
		if _, err := client.PurgePaths(context.Background(), &PurgePathsInput{CDNDomain: "cdn.example.test", Paths: []string{"/asset.js"}}); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 {
			t.Fatalf("calls = %d, want 2", calls.Load())
		}
	})

	t.Run("ambiguous network failure is not retried", func(t *testing.T) {
		var calls atomic.Int64
		client := newPurgeRoundTripClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("connection reset after request")
		}))
		if _, err := client.PurgePaths(context.Background(), &PurgePathsInput{CDNDomain: "cdn.example.test", Paths: []string{"/asset.js"}}); err == nil {
			t.Fatal("err = nil")
		}
		if calls.Load() != 1 {
			t.Fatalf("calls = %d, want 1", calls.Load())
		}
	})
}

func TestPurgePathsNeverCarriesOrEchoesTheKey(t *testing.T) {
	var requestBody string
	body := `{"success":false,"code":"` + testKey + `","message":"bad ` + testKey + `","data":""}`
	h := newVCDN(t, testKey, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		requestBody = string(b)
		jsonReply(w, body)
	})
	out, err := h.PurgePaths(context.Background(), &PurgePathsInput{CDNDomain: "cdn.example.test", Paths: []string{"/asset.js"}})
	apiErr := apiError(t, err)
	for name, text := range map[string]string{
		"body": requestBody, "error": err.Error(), "message": apiErr.Message,
		"code": apiErr.Code, "capture": h.captures.String(), "log": h.logs.String(),
		"format": fmt.Sprintf("%v %+v %#v", out, err, apiErr),
	} {
		if strings.Contains(text, testKey) {
			t.Errorf("%s holds the key: %q", name, text)
		}
	}
	if apiErr.Code != "[redacted]" || !strings.Contains(apiErr.Message, "[redacted]") {
		t.Errorf("code = %q, message = %q, want redaction", apiErr.Code, apiErr.Message)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newPurgeRoundTripClient(t *testing.T, rt http.RoundTripper) *Client {
	t.Helper()
	tc := transport.New(transport.Config{
		HTTPClient:    &http.Client{Transport: rt},
		TokenSource:   failTokenSource{t},
		RetryCount:    2,
		RetryInterval: time.Millisecond,
	})
	set := endpoints.Set{CDN: "https://vcdn.test/vcdn-api/"}
	return New(core.NewTestConfigWithCDNAPIKey("hcm-3", "", set, tc, testKey))
}

func purgeHTTPResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}
