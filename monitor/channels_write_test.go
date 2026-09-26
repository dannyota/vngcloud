package monitor

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// TestCreateChannelSendsFields checks the create request body, including
// the header field's JSON-string encoding, with headers set and with none.
func TestCreateChannelSendsFields(t *testing.T) {
	t.Run("with headers", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/notification-gateway/api/v1/notification" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if body["name"] != "vngcloud-live-abcd1234" || body["type"] != ChannelTypeWebhook ||
				body["address"] != "https://example.com/hook" || body["otpCode"] != "" {
				t.Fatalf("unexpected body: %+v", body)
			}
			if body["header"] != `[{"key":"X-Example","value":"secret-value"}]` {
				t.Fatalf("header = %v", body["header"])
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"0123456789abcdef0123456789abcdef","name":"vngcloud-live-abcd1234",
				"address":"https://example.com/hook","header":"[{\"key\":\"X-Example\",\"value\":\"secret-value\"}]",
				"createdDate":"2026-09-26T15:46:45"}`))
		}))

		out, err := client.CreateChannel(context.Background(), &CreateChannelInput{
			Name:    "vngcloud-live-abcd1234",
			Type:    ChannelTypeWebhook,
			Address: "https://example.com/hook",
			Headers: []ChannelHeader{{Key: "X-Example", Value: "secret-value"}},
		})
		if err != nil {
			t.Fatalf("CreateChannel() error = %v", err)
		}
		if out.Channel.ID != "0123456789abcdef0123456789abcdef" || out.Channel.Type != ChannelTypeWebhook {
			t.Fatalf("unexpected channel: %+v", out.Channel)
		}
		if len(out.Channel.Headers) != 1 || out.Channel.Headers[0].Key != "X-Example" {
			t.Fatalf("Headers = %+v", out.Channel.Headers)
		}
	})

	t.Run("no headers", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			if body["header"] != "" {
				t.Fatalf("header = %v, want empty string", body["header"])
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"0123456789abcdef0123456789abcdef","name":"vngcloud-live-abcd1234",
				"address":"https://example.com/hook","createdDate":"2026-09-26T15:46:45"}`))
		}))

		out, err := client.CreateChannel(context.Background(), &CreateChannelInput{
			Name:    "vngcloud-live-abcd1234",
			Type:    ChannelTypeWebhook,
			Address: "https://example.com/hook",
		})
		if err != nil {
			t.Fatalf("CreateChannel() error = %v", err)
		}
		if out.Channel.Headers != nil {
			t.Fatalf("Headers = %+v, want nil", out.Channel.Headers)
		}
	})
}

// TestCreateChannelDecodesFixture checks Create's response, sanitized from
// a live create captured behind the design, decodes into the Output
// Channel: the response carries no type field, so Type must come from the
// request's own Type rather than being lost.
func TestCreateChannelDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		testutil.WriteFixture(t, w, "../testdata/monitor/CreateChannel.json")
	}))

	out, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-abcd1234",
		Type:    ChannelTypeWebhook,
		Address: "https://example.com/<secret>",
		Headers: []ChannelHeader{{Key: "X-Example", Value: "<secret>"}},
	})
	if err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if out.Channel.ID != "0123456789abcdef0123456789abcdef" || out.Channel.Name != "vngcloud-live-abcd1234" {
		t.Fatalf("unexpected channel: %+v", out.Channel)
	}
	if out.Channel.Type != ChannelTypeWebhook {
		t.Fatalf("Type = %q, want %q", out.Channel.Type, ChannelTypeWebhook)
	}
	if len(out.Channel.Headers) != 1 || out.Channel.Headers[0].Key != "X-Example" {
		t.Fatalf("Headers = %+v", out.Channel.Headers)
	}
	if out.Channel.CreatedDate != "2026-09-26T15:46:45" {
		t.Fatalf("CreatedDate = %q", out.Channel.CreatedDate)
	}
}

// TestCreateChannelRejectsNonWebhookType checks a Type other than
// ChannelTypeWebhook is refused before any request: M2 accepts only
// Webhook, since every other type needs an OTP the SDK does not yet send.
func TestCreateChannelRejectsNonWebhookType(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	client := newTestClient(t, failIfCalled)

	_, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-x",
		Type:    ChannelTypeEmail,
		Address: "user@example.com",
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("CreateChannel() error = %v, want ErrInvalidInput", err)
	}
}

func TestCreateChannelRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	cases := []struct {
		name string
		in   *CreateChannelInput
	}{
		{"nil input", nil},
		{"missing name", &CreateChannelInput{Type: ChannelTypeWebhook, Address: "https://example.com"}},
		{"missing type", &CreateChannelInput{Name: "n", Address: "https://example.com"}},
		{"missing address", &CreateChannelInput{Name: "n", Type: ChannelTypeWebhook}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.CreateChannel(context.Background(), tc.in)
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestCreateChannelMissingID checks a 200 response without an id is an
// *core.APIError: the SDK never finds a new channel by listing names.
func TestCreateChannelMissingID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"x"}`))
	}))

	_, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-x",
		Type:    ChannelTypeWebhook,
		Address: "https://example.com",
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *core.APIError, got %v", err)
	}
	if apiErr.Message != "create response had no id" {
		t.Fatalf("Message = %q", apiErr.Message)
	}
}

// TestCreateChannelNotRetriedAfter502 checks a POST create is never retried
// after an ambiguous failure, since a retried create could create a second
// channel.
func TestCreateChannelNotRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-x",
		Type:    ChannelTypeWebhook,
		Address: "https://example.com/hook",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestCreateChannelRedactsAddressAndHeaderInError checks a server error
// message that echoes the sent Address or a header value comes back with
// those values replaced by "<redacted>", so a caller that logs the error
// never leaks either.
func TestCreateChannelRedactsAddressAndHeaderInError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"address https://example.com/secret-path already used with header value top-secret-token"}`))
	}))

	_, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-x",
		Type:    ChannelTypeWebhook,
		Address: "https://example.com/secret-path",
		Headers: []ChannelHeader{{Key: "X-Auth", Value: "top-secret-token"}},
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *core.APIError, got %v", err)
	}
	if got := apiErr.Message; got != "address <redacted> already used with header value <redacted>" {
		t.Fatalf("Message = %q", got)
	}
	// The full error string, which a caller may log directly, must not
	// contain the secret address or header value either.
	full := err.Error()
	if strings.Contains(full, "secret-path") || strings.Contains(full, "top-secret-token") {
		t.Fatalf("Error() = %q, leaked a secret", full)
	}
}

// TestCreateChannelRedactsJSONEscapedHeaderValue checks a header value
// containing a character JSON escapes, such as "&", is also redacted when
// the server echoes back the escaped form (as it would if it echoed the
// JSON request body) rather than the raw value.
func TestCreateChannelRedactsJSONEscapedHeaderValue(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"rejected header value token-a&b-secret"}`))
	}))

	_, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-x",
		Type:    ChannelTypeWebhook,
		Address: "https://example.com/hook",
		Headers: []ChannelHeader{{Key: "X-Auth", Value: "token-a&b-secret"}},
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *core.APIError, got %v", err)
	}
	if got := apiErr.Message; got != "rejected header value <redacted>" {
		t.Fatalf("Message = %q", got)
	}
}

// TestCreateChannelWithholdsMessageForShortHeaderValue checks a header value
// shorter than the redaction's replace-in-place threshold is never cut out
// of the message with a plain ReplaceAll, since that risks also cutting
// unrelated text sharing the same short substring; instead the whole
// message is withheld when that value would otherwise appear in it.
func TestCreateChannelWithholdsMessageForShortHeaderValue(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"header value abc is already used elsewhere"}`))
	}))

	_, err := client.CreateChannel(context.Background(), &CreateChannelInput{
		Name:    "vngcloud-live-x",
		Type:    ChannelTypeWebhook,
		Address: "https://example.com/hook",
		Headers: []ChannelHeader{{Key: "X-Auth", Value: "abc"}},
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *core.APIError, got %v", err)
	}
	if got := apiErr.Message; got != "server message withheld" {
		t.Fatalf("Message = %q, want the whole message withheld", got)
	}
	full := err.Error()
	if strings.Contains(full, "abc") {
		t.Fatalf("Error() = %q, leaked the short secret", full)
	}
}

// TestUpdateChannelReadsMergesAndSendsFullBody checks UpdateChannel reads
// the current channel, resends every field left nil unchanged, and sends
// the fields that are set, including the header JSON-string encoding.
func TestUpdateChannelReadsMergesAndSendsFullBody(t *testing.T) {
	t.Run("only Name set keeps Address and Headers", func(t *testing.T) {
		var getCalls, putCalls int
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				getCalls++
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"old-name","address":"https://example.com/hook",
					"header":"[{\"key\":\"X-Example\",\"value\":\"secret-value\"}]",
					"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
					"createdDate":"2026-09-26T00:00:00"}],
					"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
			case http.MethodPut:
				putCalls++
				if r.URL.Path != "/notification-gateway/api/v1/notification" {
					t.Fatalf("path = %s", r.URL.Path)
				}
				body := decodeBody(t, r)
				if body["id"] != "ch-1" || body["name"] != "new-name" || body["type"] != ChannelTypeWebhook {
					t.Fatalf("unexpected identity fields: %+v", body)
				}
				if body["address"] != "https://example.com/hook" {
					t.Fatalf("address = %v, want unchanged", body["address"])
				}
				if body["header"] != `[{"key":"X-Example","value":"secret-value"}]` {
					t.Fatalf("header = %v, want unchanged", body["header"])
				}
				if body["otpCode"] != "" {
					t.Fatalf("otpCode = %v, want empty", body["otpCode"])
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		}))

		newName := "new-name"
		out, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{
			ChannelID: "ch-1",
			Name:      &newName,
		})
		if err != nil {
			t.Fatalf("UpdateChannel() error = %v", err)
		}
		if getCalls != 1 || putCalls != 1 {
			t.Fatalf("getCalls = %d, putCalls = %d, want 1 each", getCalls, putCalls)
		}
		if out.Channel.Name != "new-name" || out.Channel.Address != "https://example.com/hook" {
			t.Fatalf("unexpected channel: %+v", out.Channel)
		}
		if len(out.Channel.Headers) != 1 || out.Channel.Headers[0].Value != "secret-value" {
			t.Fatalf("Headers = %+v, want unchanged", out.Channel.Headers)
		}
	})

	t.Run("Headers set to empty clears them", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"old-name","address":"https://example.com/hook",
					"header":"[{\"key\":\"X-Example\",\"value\":\"secret-value\"}]",
					"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
					"createdDate":"2026-09-26T00:00:00"}],
					"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
			case http.MethodPut:
				body := decodeBody(t, r)
				if body["header"] != "" {
					t.Fatalf("header = %v, want empty string", body["header"])
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		}))

		out, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{
			ChannelID: "ch-1",
			Headers:   &[]ChannelHeader{},
		})
		if err != nil {
			t.Fatalf("UpdateChannel() error = %v", err)
		}
		if out.Channel.Headers != nil {
			t.Fatalf("Headers = %+v, want nil", out.Channel.Headers)
		}
	})

	t.Run("every field set", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"old-name","address":"https://example.com/old",
					"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
					"createdDate":"2026-09-26T00:00:00"}],
					"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
			case http.MethodPut:
				body := decodeBody(t, r)
				if body["name"] != "new-name" || body["address"] != "https://example.com/new" {
					t.Fatalf("unexpected fields: %+v", body)
				}
				if body["header"] != `[{"key":"X-New","value":"new-value"}]` {
					t.Fatalf("header = %v", body["header"])
				}
				w.WriteHeader(http.StatusOK)
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		}))

		newName, newAddress := "new-name", "https://example.com/new"
		newHeaders := []ChannelHeader{{Key: "X-New", Value: "new-value"}}
		out, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{
			ChannelID: "ch-1",
			Name:      &newName,
			Address:   &newAddress,
			Headers:   &newHeaders,
		})
		if err != nil {
			t.Fatalf("UpdateChannel() error = %v", err)
		}
		if out.Channel.Name != "new-name" || out.Channel.Address != "https://example.com/new" {
			t.Fatalf("unexpected channel: %+v", out.Channel)
		}
	})
}

// TestUpdateChannelPreservesUndecodableHeaderRaw checks a name-only update
// resends the channel's header field exactly as read when it does not
// decode as a JSON array of {key,value} pairs, rather than sending "" and
// silently wiping it.
func TestUpdateChannelPreservesUndecodableHeaderRaw(t *testing.T) {
	const rawHeader = "not a json array"
	var putBody map[string]any
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"old-name","address":"https://example.com/hook",
				"header":"` + rawHeader + `",
				"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
				"createdDate":"2026-09-26T00:00:00"}],
				"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case http.MethodPut:
			putBody = decodeBody(t, r)
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	out, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{
		ChannelID: "ch-1",
		Name:      &newName,
	})
	if err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	if putBody["header"] != rawHeader {
		t.Fatalf("header = %v, want %q unchanged", putBody["header"], rawHeader)
	}
	if out.Channel.Headers != nil {
		t.Fatalf("Headers = %+v, want nil", out.Channel.Headers)
	}
}

// TestUpdateChannelRejectsNonWebhookType checks UpdateChannel returns
// ErrInvalidInput, with no PUT, when the channel read has a Type other than
// Webhook: M2 only knows how to resend a webhook's full body, since every
// other type needs an OTP the SDK does not yet send.
func TestUpdateChannelRejectsNonWebhookType(t *testing.T) {
	var putCalls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"old-name","address":"someone@example.com",
				"typeNotification":{"id":"type-email","name":"Email","description":"Email"},
				"createdDate":"2026-09-26T00:00:00"}],
				"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case http.MethodPut:
			putCalls++
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	_, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{
		ChannelID: "ch-1",
		Name:      &newName,
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateChannel() error = %v, want ErrInvalidInput", err)
	}
	if putCalls != 0 {
		t.Fatalf("putCalls = %d, want 0", putCalls)
	}
}

func TestUpdateChannelRequiresAtLeastOneField(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	client := newTestClient(t, failIfCalled)

	_, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{ChannelID: "ch-1"})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("UpdateChannel() error = %v, want ErrInvalidInput", err)
	}
}

func TestUpdateChannelRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	newName := "n"

	cases := []struct {
		name string
		in   *UpdateChannelInput
	}{
		{"nil input", nil},
		{"missing channel id", &UpdateChannelInput{Name: &newName}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateChannel(context.Background(), tc.in)
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestUpdateChannelPropagatesNotFound checks UpdateChannel returns
// GetChannel's not-found sentinel, and sends no PUT, when the channel does
// not exist.
func TestUpdateChannelPropagatesNotFound(t *testing.T) {
	var putCalls int
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			putCalls++
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lstData":[],"page":1,"pageSize":10000,"totalPage":0,"totalItem":0}`))
	}))

	newName := "n"
	_, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{ChannelID: "ch-missing", Name: &newName})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("UpdateChannel() error = %v, want ErrNotFound", err)
	}
	if putCalls != 0 {
		t.Fatalf("putCalls = %d, want 0", putCalls)
	}
}

// TestUpdateChannelRedactsAddressAndHeaderInError checks a server error
// message that echoes the resent Address or a header value comes back
// redacted, using the merged values UpdateChannel actually sent (not just
// the ones the caller passed), since an unset field resends the channel's
// current one and that can also be echoed back.
func TestUpdateChannelRedactsAddressAndHeaderInError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"old-name","address":"https://example.com/secret-path",
				"header":"[{\"key\":\"X-Auth\",\"value\":\"top-secret-token\"}]",
				"typeNotification":{"id":"type-webhook","name":"Webhook","description":"Webhook"},
				"createdDate":"2026-09-26T00:00:00"}],
				"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
		case http.MethodPut:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"message":"rejected https://example.com/secret-path with header top-secret-token"}`))
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))

	newName := "new-name"
	_, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{ChannelID: "ch-1", Name: &newName})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *core.APIError, got %v", err)
	}
	if got := apiErr.Message; got != "rejected <redacted> with header <redacted>" {
		t.Fatalf("Message = %q", got)
	}
}

func TestDeleteChannelSendsNoBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/notification-gateway/api/v1/notification/ch-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if len(data) != 0 {
			t.Fatalf("body = %s, want empty", data)
		}
		w.WriteHeader(http.StatusOK)
	}))

	_, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{ChannelID: "ch-1"})
	if err != nil {
		t.Fatalf("DeleteChannel() error = %v", err)
	}
}

// TestDeleteChannelTwiceMapsToNotFound checks the second delete of the same
// channel, which the server answers with a 400 "is not found" body rather
// than a 404, comes back as the SDK's ordinary not-found sentinel.
func TestDeleteChannelTwiceMapsToNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Notification with id ch-1 is not found"}`))
	}))

	_, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{ChannelID: "ch-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteChannel() error = %v, want ErrNotFound", err)
	}
}

// TestDeleteChannelOtherBadRequestNotMappedToNotFound checks a 400 whose
// message does not say this channel's notification is not found stays a
// plain APIError, rather than being swept into NotFound by "not found"
// alone: a project-not-found 400, or a not-found 400 naming a different
// channel ID, is a real error the caller must see.
func TestDeleteChannelOtherBadRequestNotMappedToNotFound(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"unrelated message", `{"message":"malformed request"}`},
		{"not found but not this channel", `{"message":"Project not found"}`},
		{"not found but a different channel id", `{"message":"Notification with id ch-2 is not found"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			}))

			_, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{ChannelID: "ch-1"})
			if errors.Is(err, core.ErrNotFound) {
				t.Fatal("error wraps ErrNotFound, want the server's plain error")
			}
			var apiErr *core.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected a plain 400 *core.APIError, got %v", err)
			}
		})
	}
}

// TestDeleteChannelTwiceKeepsAPIErrorInChain checks the mapped not-found
// error still carries the original *core.APIError in its chain, so a
// caller can match either sentinel: errors.Is against core.ErrNotFound for
// the ordinary case, or errors.As against *core.APIError for the status
// code and the server's own message.
func TestDeleteChannelTwiceKeepsAPIErrorInChain(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Notification with id ch-1 is not found"}`))
	}))

	_, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{ChannelID: "ch-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteChannel() error = %v, want ErrNotFound", err)
	}
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("DeleteChannel() error = %v, want an *core.APIError in the chain", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("StatusCode = %d, want %d", apiErr.StatusCode, http.StatusBadRequest)
	}
}

// TestDeleteChannelMatchIsCaseInsensitive checks the not-found match still
// works when the server's own casing of "notification with id" differs.
func TestDeleteChannelMatchIsCaseInsensitive(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"NOTIFICATION WITH ID CH-1 IS NOT FOUND"}`))
	}))

	_, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{ChannelID: "ch-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("DeleteChannel() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteChannelRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	client := newTestClient(t, failIfCalled)

	if _, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteChannel() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.DeleteChannel(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("DeleteChannel(nil) error = %v, want ErrInvalidInput", err)
	}
}

// TestChannelWritePathIDs covers the design's required path ID checks:
// UpdateChannel checks its body id and DeleteChannel checks its path id, and
// both reject a shape-invalid ChannelID before any request.
func TestChannelWritePathIDs(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	newName := "n"

	for _, id := range []string{"..", ".", "/", ""} {
		t.Run("UpdateChannel rejects "+idLabel(id), func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.UpdateChannel(context.Background(), &UpdateChannelInput{ChannelID: id, Name: &newName})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("UpdateChannel(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
		t.Run("DeleteChannel rejects "+idLabel(id), func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.DeleteChannel(context.Background(), &DeleteChannelInput{ChannelID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("DeleteChannel(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}
