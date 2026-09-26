package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud/monitor"
)

// TestGoldenMonitorSendChannelOTP checks send-channel-otp's exact output
// shape, {"Ref": ..., "ExpiresAt": ...}: per the monitor design's own CLI
// example ("send-channel-otp --type Email --address <a> prints Ref and
// ExpiresAt"), neither field is redacted.
func TestGoldenMonitorSendChannelOTP(t *testing.T) {
	v := &monitor.SendChannelOTPOutput{Ref: "ref-123", ExpiresAt: time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)}
	checkGolden(t, "monitor-send-channel-otp.json.golden", "json", "", v)
	checkGolden(t, "monitor-send-channel-otp.table.golden", "table", "", v)
	checkGolden(t, "monitor-send-channel-otp.text.golden", "text", "", v)
}

// TestMonitorSendChannelOTPSendsFieldsAndPrintsRef drives send-channel-otp
// with a literal --address for Email, checks the request body, and checks
// that Ref reaches stdout unredacted: the design requires it, since Ref is
// needed as --otp-ref on the following create-channel or update-channel.
func TestMonitorSendChannelOTPSendsFieldsAndPrintsRef(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ref":"ref-abc-123","expiredAt":1790000000000}`))
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "send-channel-otp",
		"--type", monitor.ChannelTypeEmail, "--address", "user@example.com",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("send-channel-otp: %v (stderr=%s)", err, stderr.String())
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["type"] != monitor.ChannelTypeEmail || decoded["address"] != "user@example.com" {
		t.Fatalf("unexpected body: %+v", decoded)
	}

	if !strings.Contains(stdout.String(), "ref-abc-123") {
		t.Fatalf("stdout = %s, want it to print Ref unredacted", stdout.String())
	}
}

// TestMonitorSendChannelOTPRefusesLiteralAddressForSlack checks that the
// literal-address guard applies to send-channel-otp too, for Slack, whose
// Address is a webhook URL: exit code 2, naming --cli-input-json
// file://channel.json, with zero requests.
func TestMonitorSendChannelOTPRefusesLiteralAddressForSlack(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "send-channel-otp",
		"--type", monitor.ChannelTypeSlack, "--address", "https://hooks.slack.example/services/x",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a literal --address refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "--cli-input-json file://channel.json") {
		t.Fatalf("error = %q, want it to name --cli-input-json file://channel.json", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorSendChannelOTPAllowsLiteralAddressForEmailSMSTelegram checks
// that Email, SMS, and Telegram, unlike Slack, may pass a literal --address
// to send-channel-otp, matching channelAddressTypeAllowsLiteral.
func TestMonitorSendChannelOTPAllowsLiteralAddressForEmailSMSTelegram(t *testing.T) {
	for _, typ := range []string{monitor.ChannelTypeEmail, monitor.ChannelTypeSMS, monitor.ChannelTypeTelegram} {
		t.Run(typ, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/notification-gateway/api/v1/notification/otps": jsonHandler(http.StatusOK, `{"ref":"r","expiredAt":1790000000000}`),
			})
			root, _, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{
				"--region", "hcm-3", "monitor", "send-channel-otp",
				"--type", typ, "--address", "some-address",
			})
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("send-channel-otp: %v (stderr=%s)", err, stderr.String())
			}
		})
	}
}

// TestMonitorSendChannelOTPRefusesInlineCLIInputJSONHeaders mirrors
// create-channel's own Headers refusal: Headers has no flag of its own, so
// the only way it reaches argv is through an inline --cli-input-json value,
// refused for every Type including Email.
func TestMonitorSendChannelOTPRefusesInlineCLIInputJSONHeaders(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "send-channel-otp",
		"--type", monitor.ChannelTypeEmail, "--address", "user@example.com",
		"--cli-input-json", `{"Headers":[{"Key":"X-Api-Key","Value":"super-secret"}]}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an inline --cli-input-json Headers refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// monitorEmailChannelListJSON renders one Email channel in the list shape
// GetChannel pages through, so the OTP flow tests below can drive
// update-channel against a channel type that both allows a literal
// --address and needs an OTP to change it.
func monitorEmailChannelListJSON(id, address string) string {
	return `{"lstData":[{"id":"` + id + `","name":"example-email",` +
		`"address":"` + address + `",` +
		`"typeNotification":{"id":"type-email","name":"Email","description":"Email"},` +
		`"createdDate":"2026-09-26T15:46:45"}],` +
		`"page":1,"pageSize":10000,"totalPage":1,"totalItem":1}`
}

// TestMonitorCreateChannelOTPFlowValidatesThenCreates drives create-channel
// with --otp-ref and --otp, the flags a person types after reading
// send-channel-otp's Ref and the code sent to the address, and checks
// Validate OTP runs before Create, with the validated code as the create's
// own otpCode.
func TestMonitorCreateChannelOTPFlowValidatesThenCreates(t *testing.T) {
	var validateBody, createBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps/validate": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			validateBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":"validated-code-1"}`))
		},
		"/notification-gateway/api/v1/notification": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			createBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"channel-9","name":"n","address":"user@example.com","header":"","createdDate":"2026-09-26T16:00:00"}`))
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-channel",
		"--name", "n", "--type", monitor.ChannelTypeEmail, "--address", "user@example.com",
		"--otp-ref", "ref-1", "--otp", "123456",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-channel: %v (stderr=%s)", err, stderr.String())
	}

	var validateDecoded map[string]any
	if err := json.Unmarshal(validateBody, &validateDecoded); err != nil {
		t.Fatalf("validate body is not valid JSON: %v (%s)", err, validateBody)
	}
	if validateDecoded["otp"] != "123456" || validateDecoded["ref"] != "ref-1" || validateDecoded["address"] != "user@example.com" {
		t.Fatalf("unexpected validate body: %+v", validateDecoded)
	}

	var createDecoded map[string]any
	if err := json.Unmarshal(createBody, &createDecoded); err != nil {
		t.Fatalf("create body is not valid JSON: %v (%s)", err, createBody)
	}
	if createDecoded["otpCode"] != "validated-code-1" {
		t.Fatalf("create body otpCode = %v, want the validated code", createDecoded["otpCode"])
	}
}

// TestMonitorCreateChannelOTPRejectedSendsNoCreate checks that a wrong or
// expired OTP (a null validated code) exits with the OTPRejected error
// class and code 1, and sends no create request at all.
func TestMonitorCreateChannelOTPRejectedSendsNoCreate(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps/validate": jsonHandler(http.StatusOK, `{"code":null}`),
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-channel",
		"--name", "n", "--type", monitor.ChannelTypeEmail, "--address", "user@example.com",
		"--otp-ref", "ref-1", "--otp", "wrong-code",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an OTP rejection")
	}
	if got := classify(err).Code; got != "OTPRejected" {
		t.Fatalf("Code = %q, want OTPRejected (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1 (validate only, no create)", n)
	}
}

// TestMonitorUpdateChannelOTPFlowValidatesThenUpdates mirrors
// TestMonitorCreateChannelOTPFlowValidatesThenCreates for update-channel.
// refuseLiteralUpdateChannelAddress refuses Address unconditionally, even
// from an inline --cli-input-json value, so the new address here comes
// from --cli-input-json file://..., the same form
// TestMonitorUpdateChannelFromCLIInputJSONFile-style tests use; --otp-ref
// and --otp stay literal flags, since the guard never checks them.
func TestMonitorUpdateChannelOTPFlowValidatesThenUpdates(t *testing.T) {
	var validateBody, putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK,
			monitorEmailChannelListJSON("channel-1", "old@example.com")),
		"/notification-gateway/api/v1/notification/otps/validate": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			validateBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":"validated-code-2"}`))
		},
		"/notification-gateway/api/v1/notification": func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				defer func() { _ = r.Body.Close() }()
				putBody, _ = io.ReadAll(r.Body)
			}
			w.WriteHeader(http.StatusOK)
		},
	})
	path := writeChannelJSONFile(t, t.TempDir(), `{"Address":"new@example.com"}`)
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1",
		"--otp-ref", "ref-2", "--otp", "654321",
		"--cli-input-json", "file://" + path,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-channel: %v (stderr=%s)", err, stderr.String())
	}

	var validateDecoded map[string]any
	if err := json.Unmarshal(validateBody, &validateDecoded); err != nil {
		t.Fatalf("validate body is not valid JSON: %v (%s)", err, validateBody)
	}
	if validateDecoded["otp"] != "654321" || validateDecoded["ref"] != "ref-2" || validateDecoded["address"] != "new@example.com" {
		t.Fatalf("unexpected validate body: %+v", validateDecoded)
	}

	var putDecoded map[string]any
	if err := json.Unmarshal(putBody, &putDecoded); err != nil {
		t.Fatalf("put body is not valid JSON: %v (%s)", err, putBody)
	}
	if putDecoded["otpCode"] != "validated-code-2" || putDecoded["address"] != "new@example.com" {
		t.Fatalf("unexpected put body: %+v", putDecoded)
	}
}

// TestMonitorUpdateChannelOTPRejectedSendsNoPut mirrors
// TestMonitorCreateChannelOTPRejectedSendsNoCreate for update-channel: a
// null validated code sends the read but never the PUT.
func TestMonitorUpdateChannelOTPRejectedSendsNoPut(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK,
			monitorEmailChannelListJSON("channel-1", "old@example.com")),
		"/notification-gateway/api/v1/notification/otps/validate": jsonHandler(http.StatusOK, `{"code":null}`),
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				t.Errorf("unexpected PUT: %s", r.URL.Path)
			}
		},
	})
	path := writeChannelJSONFile(t, t.TempDir(), `{"Address":"new@example.com"}`)
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1",
		"--otp-ref", "ref-2", "--otp", "wrong-code",
		"--cli-input-json", "file://" + path,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an OTP rejection")
	}
	if got := classify(err).Code; got != "OTPRejected" {
		t.Fatalf("Code = %q, want OTPRejected (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 1 {
		t.Fatalf("exitCode = %d, want 1", got)
	}
}

// otpSecretMarkers are the raw OTP and Ref values the tests below drive
// through create-channel; neither must ever reach a printed error or
// --debug log line.
var otpSecretMarkers = []string{"secret-otp-code-999", "secret-otp-ref-888"}

// TestMonitorCreateChannelOTPNeverLeaksInErrorMessage checks that a server
// error from Validate OTP echoing the OTP or Ref never reaches the CLI's
// printed error message: the SDK itself redacts both (see
// monitor.redactOTPError) before the error ever reaches classify.
func TestMonitorCreateChannelOTPNeverLeaksInErrorMessage(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps/validate": jsonHandler(http.StatusBadRequest,
			`{"message":"otp secret-otp-code-999 for ref secret-otp-ref-888 already used"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-channel",
		"--name", "n", "--type", monitor.ChannelTypeEmail, "--address", "user@example.com",
		"--otp-ref", "secret-otp-ref-888", "--otp", "secret-otp-code-999",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an error from Validate OTP")
	}
	env := classify(err)
	for _, marker := range otpSecretMarkers {
		if strings.Contains(env.Message, marker) {
			t.Fatalf("error message leaked a secret %q: %q", marker, env.Message)
		}
	}
	if stderr.Len() > 0 {
		for _, marker := range otpSecretMarkers {
			if strings.Contains(stderr.String(), marker) {
				t.Fatalf("stderr leaked a secret %q: %q", marker, stderr.String())
			}
		}
	}
}

// TestMonitorCreateChannelOTPNeverLeaksInDebugOutput checks that --debug,
// which logs only the operation name around a write and method/path/status
// for each request (never a body), never prints the literal --otp or
// --otp-ref value the operator passed on argv.
func TestMonitorCreateChannelOTPNeverLeaksInDebugOutput(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/otps/validate": jsonHandler(http.StatusOK, `{"code":"validated-code-3"}`),
		"/notification-gateway/api/v1/notification": jsonHandler(http.StatusOK,
			`{"id":"channel-9","name":"n","address":"user@example.com","header":"","createdDate":"2026-09-26T16:00:00"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--debug", "--region", "hcm-3", "monitor", "create-channel",
		"--name", "n", "--type", monitor.ChannelTypeEmail, "--address", "user@example.com",
		"--otp-ref", "secret-otp-ref-888", "--otp", "secret-otp-code-999",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-channel: %v (stderr=%s)", err, stderr.String())
	}
	for _, marker := range otpSecretMarkers {
		if strings.Contains(stderr.String(), marker) {
			t.Fatalf("--debug output leaked a secret %q:\n%s", marker, stderr.String())
		}
	}
}
