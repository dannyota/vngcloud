package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/monitor"
)

// TestGoldenMonitorCreateChannel checks create-channel's exact output shape,
// {"Channel": {...}}, the same shape get-channel uses, with the channel
// already redacted: WriteRedact runs the same redactChannel function Read's
// Redact does for list-channels and get-channel.
func TestGoldenMonitorCreateChannel(t *testing.T) {
	v := &monitor.CreateChannelOutput{Channel: redactChannel(exampleWebhookChannel())}
	checkGolden(t, "monitor-create-channel.json.golden", "json", "", v)
	checkGolden(t, "monitor-create-channel.table.golden", "table", "", v)
	checkGolden(t, "monitor-create-channel.text.golden", "text", "", v)
}

// TestGoldenMonitorUpdateChannel mirrors TestGoldenMonitorCreateChannel for
// update-channel's identical Output shape.
func TestGoldenMonitorUpdateChannel(t *testing.T) {
	v := &monitor.UpdateChannelOutput{Channel: redactChannel(exampleWebhookChannel())}
	checkGolden(t, "monitor-update-channel.json.golden", "json", "", v)
	checkGolden(t, "monitor-update-channel.table.golden", "table", "", v)
	checkGolden(t, "monitor-update-channel.text.golden", "text", "", v)
}

// TestMonitorCreateChannelRefusesLiteralAddressForWebhookAndSlack checks the
// monitor design's literal-address guard: create-channel refuses a literal
// --address for a Webhook or Slack Type with exit code 2 and zero requests,
// naming --cli-input-json file://channel.json.
func TestMonitorCreateChannelRefusesLiteralAddressForWebhookAndSlack(t *testing.T) {
	for _, typ := range []string{monitor.ChannelTypeWebhook, monitor.ChannelTypeSlack} {
		t.Run(typ, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{
				"--region", "hcm-3", "monitor", "create-channel",
				"--name", "n", "--type", typ, "--address", "https://example.com/hook",
			})
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a literal --address refusal")
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
		})
	}
}

// TestMonitorUpdateChannelRefusesLiteralAddressWithZeroRequests checks that
// update-channel refuses every literal --address, since UpdateChannelInput
// carries no Type field for the guard to check: the refusal, and the zero
// requests, must hold before even the read GetChannel would otherwise send.
func TestMonitorUpdateChannelRefusesLiteralAddressWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/notification-gateway/api/v1/notification/list/typeSearch": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1", "--address", "https://example.com/new",
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected a literal --address refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "--cli-input-json file://channel.json") {
		t.Fatalf("error = %q, want it to name --cli-input-json file://channel.json", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0 (guard runs before even the read)", n)
	}
}

// newGuardTestCmd builds a bare *cobra.Command carrying only --address and
// --cli-input-json, the two flags refuseLiteralCreateChannelAddress and
// refuseLiteralUpdateChannelAddress read directly, so the tests below can
// call a guard function on its own without building a whole operation
// command. cmd.Flags().Set marks a flag Changed, exactly like cobra parsing
// a real argv would, so addressChanged reflects the guard's own
// cmd.Flags().Changed("address") check.
func newGuardTestCmd(t *testing.T, address string, addressChanged bool, cliInputJSON string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.Flags().String("address", "", "")
	cmd.Flags().String("cli-input-json", "", "")
	if addressChanged {
		if err := cmd.Flags().Set("address", address); err != nil {
			t.Fatalf("Set(address): %v", err)
		}
	}
	if cliInputJSON != "" {
		if err := cmd.Flags().Set("cli-input-json", cliInputJSON); err != nil {
			t.Fatalf("Set(cli-input-json): %v", err)
		}
	}
	return cmd
}

// TestChannelAddressTypeAllowsLiteral checks the fail-closed allowlist
// finding 2 requires: only Email, SMS, and Telegram allow a literal
// Address, compared with strings.EqualFold, matching redactChannel's own
// rule. Every other type, including a differently-cased Webhook or Slack
// and an unrecognized type, is denied by default.
func TestChannelAddressTypeAllowsLiteral(t *testing.T) {
	tests := []struct {
		typ  string
		want bool
	}{
		{monitor.ChannelTypeEmail, true},
		{"email", true},
		{monitor.ChannelTypeSMS, true},
		{"sms", true},
		{monitor.ChannelTypeTelegram, true},
		{"TELEGRAM", true},
		{monitor.ChannelTypeWebhook, false},
		{"webhook", false},
		{monitor.ChannelTypeSlack, false},
		{"SLACK", false},
		{"Teams", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := channelAddressTypeAllowsLiteral(tt.typ); got != tt.want {
			t.Errorf("channelAddressTypeAllowsLiteral(%q) = %v, want %v", tt.typ, got, tt.want)
		}
	}
}

// TestMonitorRefuseLiteralCreateChannelAddressCaseInsensitive checks that a
// differently-cased Type, "webhook" rather than the SDK's own "Webhook",
// still refuses a literal --address: the allowlist is fail-closed, so a
// casing the guard does not recognize denies rather than allows.
func TestMonitorRefuseLiteralCreateChannelAddressCaseInsensitive(t *testing.T) {
	cmd := newGuardTestCmd(t, "https://example.com/hook", true, "")
	in := &monitor.CreateChannelInput{Type: "webhook", Address: "https://example.com/hook"}
	err := refuseLiteralCreateChannelAddress(cmd, in)
	if err == nil {
		t.Fatal("expected a literal --address refusal for a lowercase webhook Type")
	}
	if !strings.Contains(err.Error(), "--cli-input-json file://channel.json") {
		t.Fatalf("error = %q, want it to name --cli-input-json file://channel.json", err.Error())
	}
}

// TestMonitorRefuseLiteralCreateChannelAddressAllowsEmailSMSTelegram checks
// that a literal --address is allowed through the guard for Email, SMS, and
// Telegram regardless of the address's own shape: even a URL, the shape a
// Webhook channel's Address would also take, is let through, since the
// guard denies by Type alone and never inspects Address.
func TestMonitorRefuseLiteralCreateChannelAddressAllowsEmailSMSTelegram(t *testing.T) {
	for _, typ := range []string{monitor.ChannelTypeEmail, monitor.ChannelTypeSMS, monitor.ChannelTypeTelegram} {
		t.Run(typ, func(t *testing.T) {
			address := "https://phishing.example/callback"
			cmd := newGuardTestCmd(t, address, true, "")
			in := &monitor.CreateChannelInput{Type: typ, Address: address}
			if err := refuseLiteralCreateChannelAddress(cmd, in); err != nil {
				t.Fatalf("refuseLiteralCreateChannelAddress(%s) = %v, want nil", typ, err)
			}
		})
	}
}

// TestMonitorCreateChannelRefusesInlineCLIInputJSONAddress checks the same
// refusal as TestMonitorCreateChannelRefusesLiteralAddressForWebhookAndSlack
// for an inline --cli-input-json value: Address typed directly on the
// command line reaches argv exactly like a literal --address flag does, so
// it is refused the same way, with exit code 2 and zero requests.
func TestMonitorCreateChannelRefusesInlineCLIInputJSONAddress(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-channel",
		"--name", "n", "--type", monitor.ChannelTypeWebhook,
		"--cli-input-json", `{"Address":"https://example.com/hook"}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an inline --cli-input-json Address refusal")
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

// TestMonitorCreateChannelRefusesInlineCLIInputJSONHeaders checks that an
// inline --cli-input-json value setting Headers is refused for every Type,
// including Email, whose Address the guard otherwise allows literally:
// Headers has no flag of its own, so the only way it ever reaches argv is
// through --cli-input-json, and any Webhook channel's header value can hold
// a secret the guard cannot rule out by Type alone.
func TestMonitorCreateChannelRefusesInlineCLIInputJSONHeaders(t *testing.T) {
	for _, typ := range []string{monitor.ChannelTypeWebhook, monitor.ChannelTypeEmail} {
		t.Run(typ, func(t *testing.T) {
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				},
			})
			root, _, stderr := newSvcRoot(t, fixture)
			root.SetArgs([]string{
				"--region", "hcm-3", "monitor", "create-channel",
				"--name", "n", "--type", typ,
				"--cli-input-json", `{"Headers":[{"Key":"X-Api-Key","Value":"super-secret"}]}`,
			})
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatal("expected an inline --cli-input-json Headers refusal")
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
		})
	}
}

// TestMonitorCreateChannelRefusesMixOfLiteralAddressFlagAndInlineHeaders
// checks a command that mixes sources: a literal --address flag for a Type
// the guard would otherwise allow (Email), together with an inline
// --cli-input-json value setting Headers. Both reach argv, and the Headers
// refusal fires regardless of whether the Address by itself would have been
// allowed.
func TestMonitorCreateChannelRefusesMixOfLiteralAddressFlagAndInlineHeaders(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-channel",
		"--name", "n", "--type", monitor.ChannelTypeEmail, "--address", "e@example.com",
		"--cli-input-json", `{"Headers":[{"Key":"X-Api-Key","Value":"super-secret"}]}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an inline --cli-input-json Headers refusal")
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

// TestMonitorUpdateChannelRefusesInlineCLIInputJSONAddress mirrors
// TestMonitorCreateChannelRefusesInlineCLIInputJSONAddress for
// update-channel: an inline --cli-input-json value setting Address is
// refused unconditionally, the same as a literal --address flag, before
// even the read GetChannel would otherwise send.
func TestMonitorUpdateChannelRefusesInlineCLIInputJSONAddress(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/notification-gateway/api/v1/notification/list/typeSearch": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1",
		"--cli-input-json", `{"Address":"https://example.com/new"}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an inline --cli-input-json Address refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "--cli-input-json file://channel.json") {
		t.Fatalf("error = %q, want it to name --cli-input-json file://channel.json", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0 (guard runs before even the read)", n)
	}
}

// TestMonitorUpdateChannelRefusesInlineCLIInputJSONHeaders mirrors
// TestMonitorUpdateChannelRefusesInlineCLIInputJSONAddress for Headers,
// which has no flag of its own on update-channel either.
func TestMonitorUpdateChannelRefusesInlineCLIInputJSONHeaders(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/notification-gateway/api/v1/notification/list/typeSearch": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1",
		"--cli-input-json", `{"Headers":[{"Key":"X-Api-Key","Value":"super-secret"}]}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected an inline --cli-input-json Headers refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "--cli-input-json file://channel.json") {
		t.Fatalf("error = %q, want it to name --cli-input-json file://channel.json", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0 (guard runs before even the read)", n)
	}
}

// TestMonitorUpdateChannelRefusesMixOfLiteralAddressFlagAndInlineHeaders
// mirrors the create-channel mix test: a literal --address flag together
// with an inline --cli-input-json value setting Headers, both refused on
// update-channel regardless of source or field.
func TestMonitorUpdateChannelRefusesMixOfLiteralAddressFlagAndInlineHeaders(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
		"/notification-gateway/api/v1/notification/list/typeSearch": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1", "--address", "https://example.com/new",
		"--cli-input-json", `{"Headers":[{"Key":"X-Api-Key","Value":"super-secret"}]}`,
	})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if !strings.Contains(err.Error(), "--cli-input-json file://channel.json") {
		t.Fatalf("error = %q, want it to name --cli-input-json file://channel.json", err.Error())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0 (guard runs before even the read)", n)
	}
}

// writeChannelJSONFile writes a channel.json file at dir, the same name the
// guard's own refusal message and the generated docs recommend, so the
// tests below drive the exact --cli-input-json file://channel.json form a
// real operator would use.
func writeChannelJSONFile(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "channel.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestMonitorCreateChannelFromCLIInputJSONFile drives create-channel through
// --cli-input-json file://channel.json, the monitor design's only allowed
// way to set a Webhook's Address and Headers, and checks the exact request
// body the SDK sends.
func TestMonitorCreateChannelFromCLIInputJSONFile(t *testing.T) {
	var body []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			body, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"channel-9","name":"vngcloud-test-channel",` +
				`"address":"https://example.com/hooks/incoming?token=super-secret-token",` +
				`"header":"[{\"key\":\"X-Api-Key\",\"value\":\"super-secret-header-value\"}]",` +
				`"createdDate":"2026-09-26T15:46:45"}`))
		},
	})
	path := writeChannelJSONFile(t, t.TempDir(),
		`{"Address":"https://example.com/hooks/incoming?token=super-secret-token",`+
			`"Headers":[{"Key":"X-Api-Key","Value":"super-secret-header-value"}]}`)

	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "create-channel",
		"--name", "vngcloud-test-channel", "--type", monitor.ChannelTypeWebhook,
		"--cli-input-json", "file://" + path,
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("create-channel: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/notification-gateway/api/v1/notification"); !ok || got != http.MethodPost {
		t.Fatalf("create-channel method = %q, ok=%v, want POST", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, body)
	}
	if decoded["address"] != "https://example.com/hooks/incoming?token=super-secret-token" {
		t.Fatalf("body[address] = %v", decoded["address"])
	}
	if decoded["header"] != `[{"key":"X-Api-Key","value":"super-secret-header-value"}]` {
		t.Fatalf("body[header] = %v", decoded["header"])
	}

	assertMonitorRedacted(t, "create-channel", stdout.String(), true)
}

// TestMonitorCreateChannelRedactsAcrossFormatsAndQuery mirrors
// TestMonitorListChannelsRedactsAcrossFormatsAndQuery for create-channel's
// write Output: WriteRedact runs the same redactChannel function before any
// format or --query can see the raw Address or header Value, including on
// the create response itself.
func TestMonitorCreateChannelRedactsAcrossFormatsAndQuery(t *testing.T) {
	path := writeChannelJSONFile(t, t.TempDir(),
		`{"Address":"https://example.com/hooks/incoming?token=super-secret-token",`+
			`"Headers":[{"Key":"X-Api-Key","Value":"super-secret-header-value"}]}`)
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification": jsonHandler(http.StatusOK,
			`{"id":"channel-1","name":"example-webhook",`+
				`"address":"https://example.com/hooks/incoming?token=super-secret-token",`+
				`"header":"[{\"key\":\"X-Api-Key\",\"value\":\"super-secret-header-value\"}]",`+
				`"createdDate":"2026-09-26T15:46:45"}`),
	})

	tests := []struct {
		name     string
		args     []string
		wantHost bool
	}{
		{"json", []string{"--output", "json"}, true},
		{"table", []string{"--output", "table"}, true},
		{"text", []string{"--output", "text"}, true},
		{"query-address", []string{"--query", "Channel.Address"}, true},
		{"query-header", []string{"--query", "Channel.Headers[0].Value"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, stdout, stderr := newSvcRoot(t, fixture)
			args := append([]string{
				"--region", "hcm-3", "monitor", "create-channel",
				"--name", "example-webhook", "--type", monitor.ChannelTypeWebhook,
				"--cli-input-json", "file://" + path,
			}, tc.args...)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("create-channel: %v (stderr=%s)", err, stderr.String())
			}
			assertMonitorRedacted(t, tc.name, stdout.String(), tc.wantHost)
		})
	}
}

// TestMonitorUpdateChannelFromCLIInputJSON drives update-channel with a
// literal --name flag and no Address at all, checking the merged PUT body
// keeps the channel's existing Address and Headers from the read, and that
// the write Output comes back redacted.
func TestMonitorUpdateChannelFromCLIInputJSON(t *testing.T) {
	var putBody []byte
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK, monitorChannelListJSON()),
		"/notification-gateway/api/v1/notification": func(w http.ResponseWriter, r *http.Request) {
			defer func() { _ = r.Body.Close() }()
			putBody, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		},
	})
	root, stdout, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{
		"--region", "hcm-3", "monitor", "update-channel",
		"--channel-id", "channel-1", "--name", "renamed-webhook",
	})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("update-channel: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/notification-gateway/api/v1/notification"); !ok || got != http.MethodPut {
		t.Fatalf("update-channel method = %q, ok=%v, want PUT", got, ok)
	}

	var decoded map[string]any
	if err := json.Unmarshal(putBody, &decoded); err != nil {
		t.Fatalf("body is not valid JSON: %v (%s)", err, putBody)
	}
	if decoded["name"] != "renamed-webhook" {
		t.Fatalf("body[name] = %v, want renamed-webhook", decoded["name"])
	}
	if decoded["address"] != "https://example.com/hooks/incoming?token=super-secret-token" {
		t.Fatalf("body[address] = %v, want the unchanged secret address resent", decoded["address"])
	}

	assertMonitorRedacted(t, "update-channel", stdout.String(), true)
}

// TestMonitorUpdateChannelRedactsAcrossFormatsAndQuery mirrors
// TestMonitorGetChannelRedactsAcrossFormatsAndQuery for update-channel's
// write Output, which resends the channel's existing secret Address and
// header Value read from GetChannel.
func TestMonitorUpdateChannelRedactsAcrossFormatsAndQuery(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/list/typeSearch": jsonHandler(http.StatusOK, monitorChannelListJSON()),
		"/notification-gateway/api/v1/notification":                 jsonHandler(http.StatusOK, `{}`),
	})

	tests := []struct {
		name     string
		args     []string
		wantHost bool
	}{
		{"json", []string{"--output", "json"}, true},
		{"table", []string{"--output", "table"}, true},
		{"text", []string{"--output", "text"}, true},
		{"query-address", []string{"--query", "Channel.Address"}, true},
		{"query-header", []string{"--query", "Channel.Headers[0].Value"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, stdout, stderr := newSvcRoot(t, fixture)
			args := append([]string{
				"--region", "hcm-3", "monitor", "update-channel",
				"--channel-id", "channel-1", "--name", "renamed",
			}, tc.args...)
			root.SetArgs(args)
			if err := root.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("update-channel: %v (stderr=%s)", err, stderr.String())
			}
			assertMonitorRedacted(t, tc.name, stdout.String(), tc.wantHost)
		})
	}
}

// TestMonitorDeleteChannelWithoutYesExitsWithZeroRequests mirrors
// TestMonitorDeleteCheckWithoutYesExitsWithZeroRequests for delete-channel:
// Write and Destructive, so it needs --yes.
func TestMonitorDeleteChannelWithoutYesExitsWithZeroRequests(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/channel-1": func(_ http.ResponseWriter, r *http.Request) {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		},
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "monitor", "delete-channel", "--channel-id", "channel-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected an error without --yes")
	}
	if got := exitCode(err); got != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", got, stderr.String())
	}
	if n := fixture.requestCount(); n != 0 {
		t.Fatalf("requestCount = %d, want 0", n)
	}
}

// TestMonitorDeleteChannelWithYesSendsDelete checks that --yes lets
// delete-channel send exactly one DELETE to the channel's path and succeed.
func TestMonitorDeleteChannelWithYesSendsDelete(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/channel-1": jsonHandler(http.StatusOK, `{}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "monitor", "delete-channel", "--channel-id", "channel-1"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("delete-channel: %v (stderr=%s)", err, stderr.String())
	}
	if got, ok := fixture.methodFor("/notification-gateway/api/v1/notification/channel-1"); !ok || got != http.MethodDelete {
		t.Fatalf("delete-channel method = %q, ok=%v, want DELETE", got, ok)
	}
	if n := fixture.requestCount(); n != 1 {
		t.Fatalf("requestCount = %d, want 1", n)
	}
}

// TestMonitorDeleteChannelMissingExitsFour checks the monitor design's
// not-found mapping for a channel that no longer exists: the server answers
// a 400 whose message says so, which the SDK maps to its ordinary not-found
// sentinel, reaching the CLI as NotFound with exit code 4.
func TestMonitorDeleteChannelMissingExitsFour(t *testing.T) {
	fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
		"/notification-gateway/api/v1/notification/channel-1": jsonHandler(http.StatusBadRequest,
			`{"message":"Notification with id channel-1 is not found"}`),
	})
	root, _, stderr := newSvcRoot(t, fixture)
	root.SetArgs([]string{"--region", "hcm-3", "--yes", "monitor", "delete-channel", "--channel-id", "channel-1"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	if got := classify(err).Code; got != "NotFound" {
		t.Fatalf("Code = %q, want NotFound (stderr=%s)", got, stderr.String())
	}
	if got := exitCode(err); got != 4 {
		t.Fatalf("exitCode = %d, want 4", got)
	}
}

// TestMonitorChannelWritesReadOnlyRefusedWithZeroRequests checks the
// monitor design's read-only rule for create-channel, update-channel, and
// delete-channel: all three are Write operations, so a read-only profile
// refuses each with exit 2 before any request.
func TestMonitorChannelWritesReadOnlyRefusedWithZeroRequests(t *testing.T) {
	tests := []struct {
		op   string
		args []string
	}{
		// Type Email passes the literal-address guard (see
		// TestMonitorCreateChannelAllowsLiteralAddressForEmailSMSTelegram),
		// so this case reaches, and is stopped by, the read-only check the
		// guard runs before, rather than the guard itself.
		{"create-channel", []string{
			"create-channel", "--name", "n", "--type", monitor.ChannelTypeEmail, "--address", "e@example.com",
		}},
		{"update-channel", []string{"update-channel", "--channel-id", "channel-1", "--name", "n"}},
		{"delete-channel", []string{"delete-channel", "--channel-id", "channel-1", "--yes"}},
	}
	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			home := withCleanEnv(t)
			writeConfigFile(t, home, "[profile agent]\nregion = hcm-3\nread_only = true\n")
			writeCredentialsFile(t, home, "[agent]\nusername = u\npassword = p\nroot_email = e@example.com\n")

			refuse := func(_ http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
			fixture := newSvcFixture(map[string]func(http.ResponseWriter, *http.Request){
				"/notification-gateway/api/v1/notification":                 refuse,
				"/notification-gateway/api/v1/notification/channel-1":       refuse,
				"/notification-gateway/api/v1/notification/list/typeSearch": refuse,
			})
			opts := newFakeServer(t, fixture.mux)
			withTestOptions(t, append(opts, vngcloud.WithStaticToken("test-token"))...)

			stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
			root := newRootCmd(strings.NewReader(""), stdout, stderr)
			root.SetArgs(append([]string{"--profile", "agent", "monitor"}, tc.args...))
			err := root.ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("expected a read-only refusal")
			}
			if got := classify(err).Code; got != "ReadOnly" {
				t.Fatalf("Code = %q, want ReadOnly (stderr=%s)", got, stderr.String())
			}
			if got := exitCode(err); got != 2 {
				t.Fatalf("exitCode = %d, want 2", got)
			}
			if n := fixture.requestCount(); n != 0 {
				t.Fatalf("requestCount = %d, want 0", n)
			}
		})
	}
}
