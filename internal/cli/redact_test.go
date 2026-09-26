package cli

import (
	"testing"

	"danny.vn/vngcloud/monitor"
)

// TestRedactChannelWebhookAndSlackKeepSchemeAndHost checks the monitor
// design's CLI redaction rule: a Webhook or Slack Address keeps only its
// scheme and host, with the rest of the URL, which can carry a bearer
// token, replaced.
func TestRedactChannelWebhookAndSlackKeepSchemeAndHost(t *testing.T) {
	tests := []struct {
		typ  string
		addr string
		want string
	}{
		{monitor.ChannelTypeWebhook, "https://example.com/hooks/incoming?token=super-secret-token", "https://example.com/<redacted>"},
		{monitor.ChannelTypeSlack, "https://hooks.slack.com/services/T000/B000/super-secret-token", "https://hooks.slack.com/<redacted>"},
	}
	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			ch := redactChannel(monitor.Channel{Type: tt.typ, Address: tt.addr})
			if ch.Address != tt.want {
				t.Fatalf("Address = %q, want %q", ch.Address, tt.want)
			}
		})
	}
}

// TestRedactChannelOtherTypesLeaveAddressAlone checks that Email, SMS, and
// Telegram addresses, which are personal data rather than a secret the
// monitor design asks the CLI to redact, pass through unchanged.
func TestRedactChannelOtherTypesLeaveAddressAlone(t *testing.T) {
	for _, typ := range []string{monitor.ChannelTypeEmail, monitor.ChannelTypeSMS, monitor.ChannelTypeTelegram, "Teams"} {
		t.Run(typ, func(t *testing.T) {
			const addr = "someone@example.com"
			ch := redactChannel(monitor.Channel{Type: typ, Address: addr})
			if ch.Address != addr {
				t.Fatalf("Address = %q, want %q unchanged", ch.Address, addr)
			}
		})
	}
}

// TestRedactChannelMalformedAddressIsFullyRedacted checks that a Webhook or
// Slack Address which does not parse as an absolute URL is redacted whole,
// rather than risk printing a fragment of a secret in an unexpected shape.
func TestRedactChannelMalformedAddressIsFullyRedacted(t *testing.T) {
	for _, addr := range []string{"", "not a url", "/relative/path", "example.com/no-scheme"} {
		ch := redactChannel(monitor.Channel{Type: monitor.ChannelTypeWebhook, Address: addr})
		if ch.Address != redactedPlaceholder {
			t.Fatalf("redactChannel(%q).Address = %q, want %q", addr, ch.Address, redactedPlaceholder)
		}
	}
}

// TestRedactChannelHeadersAlwaysRedactValueKeepsKey checks that every header
// value is replaced regardless of channel type, per the monitor design,
// while the key, which names the header rather than carrying its secret,
// stays visible.
func TestRedactChannelHeadersAlwaysRedactValueKeepsKey(t *testing.T) {
	ch := redactChannel(monitor.Channel{
		Type: monitor.ChannelTypeWebhook,
		Headers: []monitor.ChannelHeader{
			{Key: "X-Api-Key", Value: "super-secret-header-value"},
			{Key: "Authorization", Value: "Bearer super-secret-token"},
		},
	})
	want := []monitor.ChannelHeader{
		{Key: "X-Api-Key", Value: redactedPlaceholder},
		{Key: "Authorization", Value: redactedPlaceholder},
	}
	if len(ch.Headers) != len(want) {
		t.Fatalf("Headers = %+v, want %+v", ch.Headers, want)
	}
	for i, h := range ch.Headers {
		if h != want[i] {
			t.Fatalf("Headers[%d] = %+v, want %+v", i, h, want[i])
		}
	}
}

// TestRedactChannelNilHeadersStayNil checks that a channel with no Headers
// field (nil, as decodeChannelHeaders leaves it for an empty or absent
// header string) is not turned into a non-nil empty slice by redaction.
func TestRedactChannelNilHeadersStayNil(t *testing.T) {
	ch := redactChannel(monitor.Channel{Type: monitor.ChannelTypeEmail})
	if ch.Headers != nil {
		t.Fatalf("Headers = %+v, want nil", ch.Headers)
	}
}
