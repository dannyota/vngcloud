package compute

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func decodeComputeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	data, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(data) == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode body: %v, raw = %s", err, data)
	}
	return body
}

// withInstantSleep replaces c's sleep and now with fakes that never really
// wait, so a test exercising a wait's full bound runs in milliseconds
// rather than the real bound. The fake clock advances by exactly the
// duration each sleep call is asked to wait.
func withInstantSleep(c *Client) *Client {
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		clock = clock.Add(d)
		return ctx.Err()
	}
	return c
}

// emptyListServersPage stands in for ListServers finding nothing, the
// pre-order duplicate-name check CreateServer runs before pricing or
// ordering anything.
const emptyListServersPage = `{"listData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`

// routeServerWriteRequest dispatches one CreateServer-flow request to the
// matching callback: the pre-order list (listBody, static), the quote
// (the fixed server quote fixture), the order POST (onCreate), or a
// GetServer read of "server-1" by id (onGetByID, optional). Any other
// request fails the test.
func routeServerWriteRequest(t *testing.T, w http.ResponseWriter, r *http.Request, listBody string, onCreate, onGetByID func(w http.ResponseWriter, r *http.Request)) {
	t.Helper()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
		_, _ = w.Write([]byte(listBody))
	case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
		_, _ = w.Write([]byte(quoteServerFixture))
	case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
		onCreate(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1" && onGetByID != nil:
		onGetByID(w, r)
	default:
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

func staticGetServer(body string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}
}

// --- CreateServer: request body and success ---

func TestCreateServerSendsCreateBodyAndSucceeds(t *testing.T) {
	var createBody map[string]any
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				createBody = decodeComputeBody(t, r)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","name":"web-1","status":"ACTIVE"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	out, err := c.CreateServer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	want := map[string]any{
		"name": "web-1", "zoneId": "zone-1", "flavorId": "flavor-1", "imageId": "image-1",
		"networkId": "vpc-1", "subnetId": "subnet-1", "sshKeyId": "key-1",
		"rootDiskSize": float64(20), "rootDiskTypeId": "voltype-1",
		"encryptionVolume": false, "isEnableAutoRenew": false,
	}
	for k, v := range want {
		if createBody[k] != v {
			t.Fatalf("createBody[%q] = %v, want %v", k, createBody[k], v)
		}
	}
	if _, ok := createBody["period"]; ok {
		t.Fatal("create body must not carry period, a quote-only field")
	}
	if out.Server.UUID != "server-1" || out.Server.Status != "ACTIVE" {
		t.Fatalf("unexpected server: %+v", out.Server)
	}
	if out.MonthlyPrice != 347800 {
		t.Fatalf("MonthlyPrice = %v, want 347800", out.MonthlyPrice)
	}
}

func TestCreateServerNoWaitSkipsWait(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				getCalls.Add(1)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
			},
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	in.NoWait = true
	out, err := c.CreateServer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if out.Server.UUID != "server-1" || out.Server.Name != "web-1" {
		t.Fatalf("unexpected server: %+v", out.Server)
	}
	if getCalls.Load() != 0 {
		t.Fatalf("GetServer calls = %d, want 0: NoWait must skip the post-create wait", getCalls.Load())
	}
}

func TestCreateServerUserDataIsSensitive(t *testing.T) {
	var capturedBody []byte
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				capturedBody = data
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{}}`)) // no uuid: forces a decode failure path check
			},
			nil,
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	in.UserData = "#!/bin/sh\necho supersecret"
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error for the missing id")
	}
	if strings.Contains(err.Error(), "supersecret") {
		t.Fatalf("error leaked user data: %v", err)
	}
	if !strings.Contains(string(capturedBody), "dXNlckRhdGE") && !strings.Contains(string(capturedBody), "userDataBase64Encoded") {
		t.Fatalf("expected the create body to carry base64 user data, got %s", capturedBody)
	}
}

// TestCreateServerUserDataNeverCaptured checks that CreateServer's
// Sensitive request never reaches the configured response-capture hook,
// while the pre-order list, the quote, and the post-create read on the
// same call still do.
func TestCreateServerUserDataNeverCaptured(t *testing.T) {
	const userData = "#!/bin/sh\necho supersecret"
	var captured []transport.Capture
	cfg := testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(emptyListServersPage))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			_, _ = w.Write([]byte(quoteServerFixture))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers/server-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
		}
	}), func(c transport.Capture) {
		captured = append(captured, c)
	})
	c := withInstantSleep(New(cfg))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	in.UserData = userData
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if len(captured) != 3 {
		t.Fatalf("captured = %d, want 3 (list, quote, post-create read; the create POST itself must never be captured)", len(captured))
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(userData))
	for _, cap := range captured {
		if strings.Contains(string(cap.Body), userData) || strings.Contains(string(cap.Body), encoded) {
			t.Fatalf("a captured response held user data: %+v", cap)
		}
	}
}

// TestCreateServerUserDataRedaction checks that a rejection whose message
// quotes the plain user data, its base64 form, or an escaped rendering of
// either never leaks any of them into the returned error, --debug output,
// or a fmt/slog/json rendering of the Input: the message is withheld
// outright (createServerWithheldMessage), so nothing the body said
// survives.
func TestCreateServerUserDataRedaction(t *testing.T) {
	const userData = "#!/bin/sh\necho supersecret"
	encoded := base64.StdEncoding.EncodeToString([]byte(userData))
	escapedRaw, err := json.Marshal(userData)
	if err != nil {
		t.Fatal(err)
	}
	escaped := string(escapedRaw[1 : len(escapedRaw)-1])

	rejections := map[string]string{
		"plain userData":   "invalid userData: " + userData,
		"base64 userData":  "invalid userData: " + encoded,
		"escaped userData": "invalid userData: " + escaped,
	}

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	for name, message := range rejections {
		t.Run(name, func(t *testing.T) {
			logBuf.Reset()
			body, marshalErr := json.Marshal(map[string]string{"message": message})
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			cfg := testutil.NewConfigWithLogger(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
					_, _ = w.Write([]byte(emptyListServersPage))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
					_, _ = w.Write([]byte(quoteServerFixture))
				case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write(body)
				}
			}), logger)
			c := New(cfg)

			in := validCreateServerInput()
			in.MaxPrice = 347800
			in.UserData = vngcloud.Secret(userData)
			_, callErr := c.CreateServer(context.Background(), in)
			if callErr == nil {
				t.Fatal("CreateServer() error = nil, want the server's rejection")
			}
			var apiErr *vngcloud.APIError
			if !errors.As(callErr, &apiErr) {
				t.Fatalf("callErr = %v, want *vngcloud.APIError", callErr)
			}
			if apiErr.Message != createServerWithheldMessage {
				t.Fatalf("Message = %q, want the withheld message %q", apiErr.Message, createServerWithheldMessage)
			}
			for _, secret := range []string{userData, encoded, escaped} {
				if strings.Contains(callErr.Error(), secret) {
					t.Fatalf("error leaks user data: %v", callErr)
				}
			}
			if strings.Contains(logBuf.String(), userData) || strings.Contains(logBuf.String(), encoded) {
				t.Fatalf("debug log leaks user data: %s", logBuf.String())
			}

			forms := []string{
				fmt.Sprintf("%v", in),
				fmt.Sprintf("%+v", in),
				fmt.Sprintf("%#v", in),
			}
			data, jsonErr := json.Marshal(in) //nolint:gosec // G117: UserData is vngcloud.Secret; MarshalJSON redacts it, verified below
			if jsonErr != nil {
				t.Fatalf("json.Marshal(in) error = %v", jsonErr)
			}
			forms = append(forms, string(data))
			for _, form := range forms {
				if strings.Contains(form, userData) {
					t.Fatalf("Input rendering leaks user data: %s", form)
				}
			}
		})
	}
}

// TestCreateServerInputUserDataRedactionIndependentOfServer checks that
// CreateServerInput.UserData never leaks through fmt verbs, slog, or
// json.Marshal of the whole Input, independent of any server response.
func TestCreateServerInputUserDataRedactionIndependentOfServer(t *testing.T) {
	const userData = "#!/bin/sh\necho supersecret"
	in := validCreateServerInput()
	in.UserData = userData

	forms := map[string]string{
		"%v":  fmt.Sprintf("%v", in),
		"%+v": fmt.Sprintf("%+v", in),
		"%#v": fmt.Sprintf("%#v", in),
	}
	for name, got := range forms {
		if strings.Contains(got, userData) {
			t.Fatalf("%s = %q, holds user data", name, got)
		}
		if !strings.Contains(got, "[redacted]") {
			t.Fatalf("%s = %q, want it to contain [redacted]", name, got)
		}
	}

	data, err := json.Marshal(in) //nolint:gosec // G117: UserData is vngcloud.Secret; MarshalJSON redacts it, which this test itself verifies below
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), userData) {
		t.Fatalf("json.Marshal() = %s, holds user data", data)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	logger.Info("creating server", "user_data", in.UserData)
	if strings.Contains(buf.String(), userData) {
		t.Fatalf("slog output = %q, holds user data", buf.String())
	}
}

// --- CreateServer: price guard ---

func TestCreateServerDefaultMaxPriceRefusesAndSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				t.Fatal("no order expected above MaxPrice")
			}, nil)
	}))

	_, err := c.CreateServer(context.Background(), validCreateServerInput())
	if !errors.Is(err, vngcloud.ErrPriceAboveMax) {
		t.Fatalf("err = %v, want ErrPriceAboveMax", err)
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

func TestCreateServerQuoteEqualToMaxPriceSendsOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1", orderCalls.Load())
	}
}

func TestCreateServerRejectsInvalidMaxPriceBeforeAnyRequest(t *testing.T) {
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
			in := validCreateServerInput()
			in.MaxPrice = tt.v
			if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("CreateServer() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestCreateServerQuoteFailureSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(emptyListServersPage))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"pricing unavailable"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
			orderCalls.Add(1)
			t.Fatal("no order expected after a quote failure")
		}
	}))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	if _, err := c.CreateServer(context.Background(), in); err == nil {
		t.Fatal("err = nil, want the quote's error")
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}

// TestCreateServerInvalidQuotePriceSendsNoOrder checks that a quote response
// carrying a price the guard cannot safely compare, null, negative, or a
// bare NaN literal (invalid JSON syntax a hostile or broken gateway could
// still send), refuses the create with nothing ordered, matching pricing's
// own validQuotePrice checks.
func TestCreateServerInvalidQuotePriceSendsNoOrder(t *testing.T) {
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
			var orderCalls atomic.Int64
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
					_, _ = w.Write([]byte(emptyListServersPage))
				case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
					_, _ = w.Write([]byte(tc.quoteBody))
				case r.Method == http.MethodPost && r.URL.Path == "/v2/project-1/servers":
					orderCalls.Add(1)
					t.Fatal("no order expected for an invalid quote price")
				}
			}))

			in := validCreateServerInput()
			in.MaxPrice = 1000000
			if _, err := c.CreateServer(context.Background(), in); err == nil {
				t.Fatal("err = nil, want an error for an invalid quote price")
			}
			if orderCalls.Load() != 0 {
				t.Fatalf("order calls = %d, want 0", orderCalls.Load())
			}
		})
	}
}

// --- CreateServer: duplicate name ---

func TestCreateServerRefusesExactDuplicateName(t *testing.T) {
	var pricingCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"server-0","name":"web-1"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		default:
			pricingCalls.Add(1)
			t.Fatal("no pricing or order request when the name already exists")
		}
	}))

	_, err := c.CreateServer(context.Background(), validCreateServerInput())
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if pricingCalls.Load() != 0 {
		t.Fatalf("pricing/order calls = %d, want 0", pricingCalls.Load())
	}
}

// TestCreateServerRefusesDuplicateNameOnLaterPage checks that the
// duplicate-name check walks past the first page: a match only ListServers'
// second page holds must still refuse the create, and a check that stopped
// at page 1 would miss it.
func TestCreateServerRefusesDuplicateNameOnLaterPage(t *testing.T) {
	var pricingCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			switch r.URL.Query().Get("page") {
			case "1":
				_, _ = w.Write([]byte(`{"listData":[{"uuid":"server-0","name":"other-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`))
			case "2":
				_, _ = w.Write([]byte(`{"listData":[{"uuid":"server-1","name":"web-1"}],"page":2,"pageSize":1,"totalPage":2,"totalItem":2}`))
			default:
				t.Fatalf("unexpected page: %s", r.URL.Query().Get("page"))
			}
		default:
			pricingCalls.Add(1)
			t.Fatal("no pricing or order request when the name already exists on a later page")
		}
	}))

	_, err := c.CreateServer(context.Background(), validCreateServerInput())
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if pricingCalls.Load() != 0 {
		t.Fatalf("pricing/order calls = %d, want 0", pricingCalls.Load())
	}
}

// TestCreateServerFailsClosedOnUnderreportedServerTotal checks that the
// duplicate-name check refuses the create, rather than proceeding, when
// ListServers' own totals cannot prove the walk saw every server: trusting
// a short collection as "no duplicate" would be the same hole as skipping
// the check entirely.
func TestCreateServerFailsClosedOnUnderreportedServerTotal(t *testing.T) {
	var pricingCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"server-0","name":"other-1"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":5}`))
		default:
			pricingCalls.Add(1)
			t.Fatal("no pricing or order request when the server list's own totals cannot be trusted")
		}
	}))

	_, err := c.CreateServer(context.Background(), validCreateServerInput())
	if !errors.Is(err, vngcloud.ErrInvalidInput) {
		t.Fatalf("err = %v, want ErrInvalidInput", err)
	}
	if pricingCalls.Load() != 0 {
		t.Fatalf("pricing/order calls = %d, want 0", pricingCalls.Load())
	}
}

func TestCreateServerAllowsSubstringNonExactMatch(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r,
			`{"listData":[{"uuid":"server-0","name":"web-10"}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 347800
	if _, err := c.CreateServer(context.Background(), in); err != nil {
		t.Fatalf("CreateServer() error = %v, want a substring match to still allow the create", err)
	}
}

// --- CreateServer: no resend, and error statuses ---

func TestCreateServerOrderNotRetriedAfter502(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				orderCalls.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte(`{"message":"upstream error"}`))
			}, nil)
	}))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if orderCalls.Load() != 1 {
		t.Fatalf("order calls = %d, want 1: a create must never be retried after a 5xx", orderCalls.Load())
	}
	if !strings.Contains(err.Error(), "list servers") {
		t.Fatalf("err = %v, want a hint to list servers before creating again", err)
	}
}

func TestCreateServerOrderStatuses(t *testing.T) {
	for _, status := range []int{400, 404, 409, 500, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				routeServerWriteRequest(t, w, r, emptyListServersPage,
					func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"message":"failed"}`))
					}, nil)
			}))
			in := validCreateServerInput()
			in.MaxPrice = 1000000
			if _, err := c.CreateServer(context.Background(), in); err == nil {
				t.Fatal("err = nil, want an error")
			}
		})
	}
}

func TestCreateServerNoIDInResponseFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{}}`))
			}, nil)
	}))
	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Message, "list servers") {
		t.Fatalf("err = %v, want an APIError naming list servers before creating again", err)
	}
}

// --- CreateServer: Once ---

// fakeServerTokenSource issues sequential tokens. Every other test in this
// file wires its Client through newTestClient, whose transport has no
// TokenSource at all, so transport.doAuthenticated's 401 branch never runs:
// EnsureToken is a no-op. This type lets the Once tests below build a
// Client with a real one, so a 401 on the create POST actually reaches
// that branch.
type fakeServerTokenSource struct {
	count atomic.Int64
}

func (s *fakeServerTokenSource) Token(context.Context) (transport.Token, error) {
	n := s.count.Add(1)
	return transport.Token{AccessToken: fmt.Sprintf("token-%d", n), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (s *fakeServerTokenSource) Invalidate(string) {}

// newTestClientForOnce builds a Client around server with a real
// TokenSource, wired to the list-servers, quote, and create routes
// routeServerWriteRequest expects, so a test can prove Once's exact
// behavior instead of the generic 4xx handling newTestClient's tokenless
// transport would otherwise also satisfy trivially. Every endpoint points
// at server, including Portal, which the price quote uses.
func newTestClientForOnce(server *httptest.Server) *Client {
	tc := transport.New(transport.Config{HTTPClient: server.Client(), TokenSource: &fakeServerTokenSource{}})
	url := server.URL + "/"
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{
		VServer: url, VLB: url, VNetwork: url, GLB: url, DNS: url, VCR: url,
		Portal: url, Billing: url, Monitor: url, Dashboard: url, IAM: url,
	}, tc)
	return New(cfg)
}

// TestCreateServerOnceNoResendAfter401 checks that the create POST carries
// Once (transport.Request.Once): with a real TokenSource, a 401 reaches
// transport's own invalidate-and-resend branch, but Once must still leave
// the POST sent exactly once, unlike an ordinary request. A 401 is a 4xx,
// so it never gets the ambiguous-create hint: the gateway rejected the
// token before the create logic ever ran, and nothing was created.
func TestCreateServerOnceNoResendAfter401(t *testing.T) {
	var createCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				createCalls.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}, nil)
	}))
	defer server.Close()

	c := newTestClientForOnce(server)
	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if createCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a Once request must never resend after a 401", createCalls.Load())
	}
	if strings.Contains(err.Error(), "list servers") {
		t.Fatalf("a 401 should not get the ambiguous-create hint: it is the gateway rejecting the token before the create logic ever runs, so it creates nothing: %v", err)
	}
}

// TestCreateServerOnceRefusesRedirect checks that the create POST carries
// Once: net/http would otherwise replay a redirected POST's method and
// body at the Location it names, sending the create a second time. A 307
// is not a 4xx, so it does get the ambiguous-create hint: the request may
// have reached a handler at the redirected location.
func TestCreateServerOnceRefusesRedirect(t *testing.T) {
	var createCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				createCalls.Add(1)
				w.Header().Set("Location", "/v2/project-1/servers/moved")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}, nil)
	}))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error for the 307")
	}
	if createCalls.Load() != 1 {
		t.Fatalf("POST calls = %d, want 1: a Once request must never follow a redirect", createCalls.Load())
	}
	if !strings.Contains(err.Error(), "list servers") {
		t.Fatalf("err = %v, want the ambiguous-create hint to list servers", err)
	}
}

// failCreateTransport fails a request whose method and path match method
// and path with failErr, and sends every other request through inner. It
// lets a test simulate a dropped connection for the create POST alone,
// while the pre-create list and quote reads still reach the real server.
type failCreateTransport struct {
	inner  http.RoundTripper
	method string
	path   string
	calls  atomic.Int64
}

func (f *failCreateTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == f.method && req.URL.Path == f.path {
		f.calls.Add(1)
		return nil, &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	}
	return f.inner.RoundTrip(req)
}

// TestCreateServerOnceNoRetryAfterDroppedConnection checks that the create
// POST carries Once: without it, a dropped connection (here, a failed
// dial) is retried up to the transport's own retry count, since a failed
// dial never reached the server and every other write's guard treats that
// as safe to resend automatically. A create must never be resent that way;
// Once limits it to exactly one attempt, and the resulting error gets the
// ambiguous-create hint, since whether the create reached the server on
// that one attempt is unknown.
func TestCreateServerOnceNoRetryAfterDroppedConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("create POST reached the real server: the fake transport should have intercepted it")
			}, nil)
	}))
	defer server.Close()

	rt := &failCreateTransport{inner: server.Client().Transport, method: http.MethodPost, path: "/v2/project-1/servers"}
	tc := transport.New(transport.Config{HTTPClient: &http.Client{Transport: rt}, RetryCount: 3, RetryInterval: time.Millisecond})
	url := server.URL + "/"
	cfg := core.NewTestConfig("hcm-3", "project-1", endpoints.Set{
		VServer: url, VLB: url, VNetwork: url, GLB: url, DNS: url, VCR: url,
		Portal: url, Billing: url, Monitor: url, Dashboard: url, IAM: url,
	}, tc)
	c := New(cfg)

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if err == nil {
		t.Fatal("err = nil, want an error for the dropped connection")
	}
	if rt.calls.Load() != 1 {
		t.Fatalf("POST attempts = %d, want 1: a Once request must never retry after a dropped connection", rt.calls.Load())
	}
	if !strings.Contains(err.Error(), "list servers") {
		t.Fatalf("err = %v, want the ambiguous-create hint to list servers", err)
	}
}

// --- CreateServer: wait ---

func TestCreateServerWaitFailsOnError(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"ERROR"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	out, err := c.CreateServer(context.Background(), in)
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if out == nil || out.Server.UUID != "server-1" {
		t.Fatalf("Output = %+v, want the last read server", out)
	}
}

func TestCreateServerWaitTolerates404(t *testing.T) {
	var getCalls atomic.Int64
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			func(w http.ResponseWriter, r *http.Request) {
				if getCalls.Add(1) == 1 {
					w.WriteHeader(http.StatusNotFound)
					_, _ = w.Write([]byte(`{"message":"not found"}`))
					return
				}
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"ACTIVE"}}`))
			},
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	out, err := c.CreateServer(context.Background(), in)
	if err != nil {
		t.Fatalf("CreateServer() error = %v", err)
	}
	if out.Server.Status != "ACTIVE" {
		t.Fatalf("Status = %q, want ACTIVE", out.Server.Status)
	}
	if getCalls.Load() < 2 {
		t.Fatalf("GET calls = %d, want at least 2: a 404 during the wait must keep polling", getCalls.Load())
	}
}

func TestCreateServerWaitBoundReached(t *testing.T) {
	c := withInstantSleep(newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeServerWriteRequest(t, w, r, emptyListServersPage,
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"data":{"uuid":"server-1"}}`))
			},
			staticGetServer(`{"data":{"uuid":"server-1","status":"CREATING"}}`),
		)
	})))

	in := validCreateServerInput()
	in.MaxPrice = 1000000
	_, err := c.CreateServer(context.Background(), in)
	if !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
}

// TestWaitServerActivePollParameters checks the literal interval and bound
// waitServerActive passes to poll.
func TestWaitServerActivePollParameters(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"uuid":"server-1","status":"CREATING"}}`))
	}))
	var sleeps []time.Duration
	clock := time.Now()
	c.now = func() time.Time { return clock }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		clock = clock.Add(d)
		return ctx.Err()
	}

	if _, err := c.waitServerActive(context.Background(), "op", "server-1"); !errors.Is(err, ErrNotSettled) {
		t.Fatalf("err = %v, want ErrNotSettled", err)
	}
	if len(sleeps) != 180 {
		t.Fatalf("sleep calls = %d, want 180 (a 5s interval over a 15-minute bound)", len(sleeps))
	}
	for _, d := range sleeps {
		if d != 5*time.Second {
			t.Fatalf("sleep duration = %s, want 5s", d)
		}
	}
}

func TestCreateServerZeroQuoteRefusesAndSendsNoOrder(t *testing.T) {
	var orderCalls atomic.Int64
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v2/project-1/servers":
			_, _ = w.Write([]byte(emptyListServersPage))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/price":
			_, _ = w.Write([]byte(`{"optimumPrice":0,"originalPrice":0,"discountPrice":0,"discountPercent":0,"propertiesPrice":[]}`))
		default:
			orderCalls.Add(1)
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	in := validCreateServerInput()
	in.MaxPrice = 1000000
	if _, err := c.CreateServer(context.Background(), in); !errors.Is(err, vngcloud.ErrUnpriced) {
		t.Fatalf("err = %v, want ErrUnpriced", err)
	}
	if orderCalls.Load() != 0 {
		t.Fatalf("order calls = %d, want 0", orderCalls.Load())
	}
}
