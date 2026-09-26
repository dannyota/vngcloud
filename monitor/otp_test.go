package monitor

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// TestSendChannelOTPSendsFields checks the send request body, with and
// without headers, and that the response's ref and epoch-millisecond
// expiredAt decode into Ref and ExpiresAt.
func TestSendChannelOTPSendsFields(t *testing.T) {
	t.Run("no headers", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			if r.URL.Path != "/notification-gateway/api/v1/notification/otps" {
				t.Fatalf("path = %s", r.URL.Path)
			}
			body := decodeBody(t, r)
			if body["type"] != ChannelTypeEmail || body["address"] != "user@example.com" || body["header"] != "" {
				t.Fatalf("unexpected body: %+v", body)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ref":"ref-123","expiredAt":1790000000000}`))
		}))

		out, err := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{
			Type:    ChannelTypeEmail,
			Address: "user@example.com",
		})
		if err != nil {
			t.Fatalf("SendChannelOTP() error = %v", err)
		}
		if out.Ref != "ref-123" {
			t.Fatalf("Ref = %q", out.Ref)
		}
		want := time.UnixMilli(1790000000000)
		if !out.ExpiresAt.Equal(want) {
			t.Fatalf("ExpiresAt = %v, want %v", out.ExpiresAt, want)
		}
	})

	t.Run("with headers", func(t *testing.T) {
		client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body := decodeBody(t, r)
			if body["header"] != `[{"key":"X-Example","value":"secret-value"}]` {
				t.Fatalf("header = %v", body["header"])
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ref":"ref-456","expiredAt":1790000000000}`))
		}))

		_, err := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{
			Type:    ChannelTypeSlack,
			Address: "https://hooks.slack.example/services/x",
			Headers: []ChannelHeader{{Key: "X-Example", Value: "secret-value"}},
		})
		if err != nil {
			t.Fatalf("SendChannelOTP() error = %v", err)
		}
	})
}

// TestSendChannelOTPDecodesFixture checks the response shape the design
// documents, {ref, expiredAt}, decodes into Ref and ExpiresAt. SendChannelOTP
// is a live write (it messages the address) that the design forbids sending
// during development, so this fixture is built from the design's documented
// shape rather than a live capture.
func TestSendChannelOTPDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		testutil.WriteFixture(t, w, "../testdata/monitor/SendChannelOTP.json")
	}))

	out, err := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{
		Type:    ChannelTypeEmail,
		Address: "user@example.com",
	})
	if err != nil {
		t.Fatalf("SendChannelOTP() error = %v", err)
	}
	if out.Ref == "" {
		t.Fatal("Ref is empty")
	}
	if out.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt is zero")
	}
}

// TestSendChannelOTPRejectsWebhook checks Webhook, which needs no OTP, is
// refused before any request.
func TestSendChannelOTPRejectsWebhook(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})
	client := newTestClient(t, failIfCalled)

	_, err := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{
		Type:    ChannelTypeWebhook,
		Address: "https://example.com/hook",
	})
	if !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("SendChannelOTP() error = %v, want ErrInvalidInput", err)
	}
}

func TestSendChannelOTPRequiredFields(t *testing.T) {
	failIfCalled := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	cases := []struct {
		name string
		in   *SendChannelOTPInput
	}{
		{"nil input", nil},
		{"missing type", &SendChannelOTPInput{Address: "user@example.com"}},
		{"missing address", &SendChannelOTPInput{Type: ChannelTypeEmail}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, failIfCalled)
			_, err := client.SendChannelOTP(context.Background(), tc.in)
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestSendChannelOTPNotRetriedAfter502 checks Send OTP is never retried
// after an ambiguous failure: a retry could message the address a second
// time.
func TestSendChannelOTPNotRetriedAfter502(t *testing.T) {
	calls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})))

	_, err := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{
		Type:    ChannelTypeEmail,
		Address: "user@example.com",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

// TestSendChannelOTPRedactsAddressAndHeaderInError checks a server error
// message that echoes the sent Address or a header value comes back with
// those values replaced by "<redacted>".
func TestSendChannelOTPRedactsAddressAndHeaderInError(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"address user-secret@example.com already has a pending otp, header top-secret-token"}`))
	}))

	_, err := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{
		Type:    ChannelTypeEmail,
		Address: "user-secret@example.com",
		Headers: []ChannelHeader{{Key: "X-Auth", Value: "top-secret-token"}},
	})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *core.APIError, got %v", err)
	}
	if strings.Contains(apiErr.Message, "user-secret@example.com") || strings.Contains(apiErr.Message, "top-secret-token") {
		t.Fatalf("Message = %q, leaked a secret", apiErr.Message)
	}
}
