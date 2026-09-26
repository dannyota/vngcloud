package cli

import (
	"net/url"

	"danny.vn/vngcloud/monitor"
)

// redactedPlaceholder replaces every value the monitor design's channel
// security rule covers. There is no flag that reveals the real value: it is
// gone before the Output reaches renderOutput, so it cannot survive any
// output format or a --query expression either.
const redactedPlaceholder = "<redacted>"

// redactChannel returns a copy of ch with its secret-bearing fields
// replaced, per the monitor design: a Webhook or Slack Address can carry a
// bearer token in its path, so only its scheme and host survive; every
// other channel type's Address (an email, phone number, or Telegram chat
// ID) is personal data, not a secret, and is left as the SDK returned it.
// Every header value is redacted regardless of channel type, since only a
// Webhook channel has any.
func redactChannel(ch monitor.Channel) monitor.Channel {
	switch ch.Type {
	case monitor.ChannelTypeWebhook, monitor.ChannelTypeSlack:
		ch.Address = redactAddress(ch.Address)
	}
	if ch.Headers != nil {
		headers := make([]monitor.ChannelHeader, len(ch.Headers))
		for i, h := range ch.Headers {
			headers[i] = monitor.ChannelHeader{Key: h.Key, Value: redactedPlaceholder}
		}
		ch.Headers = headers
	}
	return ch
}

// redactAddress keeps only address's scheme and host and drops the rest,
// per the monitor design's CLI redaction rule. An address that does not
// parse as an absolute URL is redacted whole, rather than print a fragment
// of a secret in a shape the SDK did not expect.
func redactAddress(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return redactedPlaceholder
	}
	return u.Scheme + "://" + u.Host + "/" + redactedPlaceholder
}
