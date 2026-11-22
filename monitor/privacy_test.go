package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

func TestMonitorWriteSecretsWithheld(t *testing.T) {
	const secret = "synthetic-request-header-canary"
	for _, operation := range []string{"channel-create", "channel-update-read", "channel-update-write", "send-otp", "validate-otp", "check-create", "check-update", "check-followup"} {
		t.Run(operation, func(t *testing.T) {
			captures := 0
			reads := 0
			client := New(testutil.NewConfigWithCapture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads++
					if operation == "channel-update-write" {
						_, _ = w.Write([]byte(`{"lstData":[{"id":"ch-1","name":"channel","type":"Webhook","address":"https://example.com","header":""}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`))
						return
					}
					if operation != "channel-update-read" && reads == 1 {
						_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
						return
					}
				}
				if operation == "check-followup" && r.Method == http.MethodPut {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": secret, "message": secret})
			}), func(transport.Capture) { captures++ }))
			var err error
			switch operation {
			case "channel-create", "validate-otp":
				input := &CreateChannelInput{Name: "channel", Type: ChannelTypeWebhook, Address: "https://example.com", Headers: []ChannelHeader{{Key: "X-Custom", Value: secret}}}
				if operation == "validate-otp" {
					input.Type, input.OTPRef, input.OTP = ChannelTypeEmail, "reference", "123456"
				}
				_, err = client.CreateChannel(context.Background(), input)
			case "channel-update-read", "channel-update-write":
				headers := []ChannelHeader{{Key: "X-Custom", Value: secret}}
				_, err = client.UpdateChannel(context.Background(), &UpdateChannelInput{ChannelID: "ch-1", Headers: &headers})
			case "send-otp":
				_, err = client.SendChannelOTP(context.Background(), &SendChannelOTPInput{Type: ChannelTypeEmail, Address: "example@example.com", Headers: []ChannelHeader{{Key: "X-Custom", Value: secret}}})
			case "check-create":
				_, err = client.CreateCheck(context.Background(), &CreateCheckInput{Name: "check", URL: "https://example.com", Locations: []string{"loc-1"}, Headers: map[string]string{"X-Custom": secret}})
			default:
				headers := map[string]string{"X-Custom": secret}
				_, err = client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Headers: &headers})
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.StatusCode != 400 {
				t.Fatalf("error = %v; want API status 400", err)
			}
			assertMonitorErrorPrivate(t, err, secret)
			if captures != 0 {
				t.Fatalf("sensitive response captures = %d, want 0", captures)
			}
		})
	}
}

func TestMonitorErrorWithholdsNestedCauses(t *testing.T) {
	const secret = "synthetic-cause-canary"
	err := redactChannelSecrets(&core.APIError{
		Code: secret, Message: "safe", StatusCode: 403,
		Err: errors.Join(fmt.Errorf("%s: %w", secret, context.Canceled), core.ErrPermission),
	}, []string{secret})
	assertMonitorErrorPrivate(t, err, secret)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, core.ErrPermission) {
		t.Fatalf("lost safe identities: %v", err)
	}
}

func TestMonitorRejectsReflectedWriteModels(t *testing.T) {
	const secret = "synthetic-model-header-canary"
	for _, operation := range []string{"channel-create", "channel-update-read", "channel-update-output", "check-create", "check-update-read", "check-update-response", "check-followup", "otp-ref"} {
		t.Run(operation, func(t *testing.T) {
			var writes int
			reads := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads++
					if strings.HasPrefix(operation, "channel-update") {
						name := "channel"
						if operation == "channel-update-read" {
							name = secret
						}
						_, _ = fmt.Fprintf(w, `{"lstData":[{"id":"ch-1","name":%q,"address":"https://example.com","typeNotification":{"name":"Webhook"}}],"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`, name)
						return
					}
					body := fmt.Sprintf(currentCheckJSON, StatusEnabled)
					if operation == "check-update-read" || reads > 1 {
						body = strings.ReplaceAll(body, "old-name", secret)
					}
					_, _ = w.Write([]byte(body))
					return
				}
				writes++
				switch operation {
				case "channel-create":
					_, _ = fmt.Fprintf(w, `{"id":"ch-1","name":%q}`, secret)
				case "otp-ref":
					_, _ = fmt.Fprintf(w, `{"ref":%q,"expiredAt":0}`, secret)
				case "check-followup":
					_, _ = w.Write([]byte(`{}`))
				default:
					if operation == "check-create" {
						w.WriteHeader(http.StatusCreated)
					}
					_, _ = fmt.Fprintf(w, `{"id":"chk-1","name":%q}`, secret)
				}
			}))
			var err error
			var hasOutput bool
			switch operation {
			case "channel-create":
				out, callErr := client.CreateChannel(context.Background(), &CreateChannelInput{Name: "channel", Type: ChannelTypeWebhook, Address: "https://example.com", Headers: []ChannelHeader{{Key: "X-Custom", Value: secret}}})
				err, hasOutput = callErr, out != nil
			case "channel-update-read", "channel-update-output":
				headers := []ChannelHeader{{Key: "X-Custom", Value: secret}}
				input := &UpdateChannelInput{ChannelID: "ch-1", Headers: &headers}
				if operation == "channel-update-output" {
					input.Name = new(secret)
				}
				out, callErr := client.UpdateChannel(context.Background(), input)
				err, hasOutput = callErr, out != nil
			case "check-create":
				out, callErr := client.CreateCheck(context.Background(), &CreateCheckInput{Name: "check", URL: "https://example.com", Locations: []string{"loc-1"}, Headers: map[string]string{"X-Custom": secret}})
				err, hasOutput = callErr, out != nil
			case "otp-ref":
				out, callErr := client.SendChannelOTP(context.Background(), &SendChannelOTPInput{Type: ChannelTypeEmail, Address: "example@example.com", Headers: []ChannelHeader{{Key: "X-Custom", Value: secret}}})
				err, hasOutput = callErr, out != nil
			default:
				headers := map[string]string{"X-Custom": secret}
				out, callErr := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Headers: &headers})
				err, hasOutput = callErr, out != nil
			}
			if hasOutput || err == nil {
				t.Fatal("credential reflection returned as a successful model")
			}
			assertMonitorErrorPrivate(t, err, secret)
			if strings.HasSuffix(operation, "-read") && writes != 0 {
				t.Fatalf("writes after unsafe prerequisite = %d, want 0", writes)
			}
		})
	}
}

func TestChannelRefreshCredentialsStayPrivate(t *testing.T) {
	const first, second = "synthetic-first-iam-token", "synthetic-second-iam-token"
	for _, credential := range []string{first, second} {
		for _, field := range []string{"name", "header", "error"} {
			t.Run(credential+"/"+field, func(t *testing.T) {
				creates := 0
				client := monitorRefreshClient(t, first, second, "/notification-gateway/api/v1/notification/otps/validate", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/validate") {
						_, _ = w.Write([]byte(`{"code":"synthetic-validated-code"}`))
						return
					}
					creates++
					if field == "error" {
						w.WriteHeader(http.StatusBadRequest)
						_ = json.NewEncoder(w).Encode(map[string]string{"code": credential, "message": credential})
						return
					}
					response := map[string]string{"id": "ch-1", "name": "channel"}
					response[field] = credential
					if field == "header" {
						response[field] = fmt.Sprintf(`[{"key":"X-Custom","value":%q}]`, credential)
					}
					_ = json.NewEncoder(w).Encode(response)
				}))
				out, err := client.CreateChannel(context.Background(), &CreateChannelInput{
					Name: "channel", Type: ChannelTypeEmail, Address: "synthetic@example.com", OTPRef: "synthetic-reference", OTP: "123456",
				})
				if out != nil || err == nil || creates != 1 {
					t.Fatalf("output=%+v error=%v creates=%d; want one create and a withheld response", out, err, creates)
				}
				assertMonitorCredentialsAbsent(t, out, err, first, second)
			})
		}
	}
}

func TestMonitorStructuredCredentialsWithheld(t *testing.T) {
	const secret = "synthetic-embedded-credential"
	for _, carrier := range []string{"bearer-header", "url-password", "url-query", "json-body", "channel-header", "channel-url"} {
		t.Run(carrier, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": secret, "message": secret})
			}))
			var err error
			if strings.HasPrefix(carrier, "channel-") {
				input := &CreateChannelInput{Name: "channel", Type: ChannelTypeWebhook, Address: "https://example.com"}
				if carrier == "channel-url" {
					input.Address += "?token=" + secret
				} else {
					input.Headers = []ChannelHeader{{Key: "Authorization", Value: "Bearer " + secret}}
				}
				_, err = client.CreateChannel(context.Background(), input)
			} else {
				input := &CreateCheckInput{Name: "check", URL: "https://example.com", Locations: []string{"loc-1"}}
				switch carrier {
				case "bearer-header":
					input.Headers = map[string]string{"Authorization": "Bearer " + secret}
				case "url-password":
					input.URL = "https://synthetic:" + secret + "@example.com"
				case "url-query":
					input.URL += "?token=" + secret
				case "json-body":
					input.Body = fmt.Sprintf(`{"nested":{"password":%q}}`, secret)
				}
				_, err = client.CreateCheck(context.Background(), input)
			}
			assertMonitorErrorPrivate(t, err, secret)
		})
	}
}

func TestUpdateCheckRejectsMismatchedConfirmation(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(strconv.FormatBool(fallback), func(t *testing.T) {
			reads := 0
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					reads++
					if reads == 1 {
						_, _ = fmt.Fprintf(w, currentCheckJSON, StatusEnabled)
						return
					}
				} else if fallback {
					_, _ = w.Write([]byte(`{}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"chk-other","name":"different-check"}`))
			}))
			name := "updated-check"
			out, err := client.UpdateCheck(context.Background(), &UpdateCheckInput{CheckID: "chk-1", Name: &name})
			if out != nil || err == nil {
				t.Fatal("mismatched check confirmed as success")
			}
		})
	}
}

func assertMonitorErrorPrivate(t *testing.T, err error, secret string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", err, err), secret) {
		t.Fatal("error exposed request secret")
	}
	var api *core.APIError
	if errors.As(err, &api) && strings.Contains(api.Code+api.Message, secret) {
		t.Fatal("API error fields exposed request secret")
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range multi.Unwrap() {
			assertMonitorErrorPrivate(t, child, secret)
		}
	} else if child := errors.Unwrap(err); child != nil {
		assertMonitorErrorPrivate(t, child, secret)
	}
}
