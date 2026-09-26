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
// replaced, per the monitor design: only Email, SMS, and Telegram carry an
// Address that is personal data rather than a secret (an email, phone
// number, or Telegram chat ID), so those three types alone are left as the
// SDK returned them. Every other type, known or not, is denied by default:
// its Address goes through redactAddress, since a Webhook, Slack, Teams,
// or future channel type's Address can carry a bearer token in its path.
// Every header value is redacted regardless of channel type, since only a
// Webhook channel has any today.
func redactChannel(ch monitor.Channel) monitor.Channel {
	switch ch.Type {
	case monitor.ChannelTypeEmail, monitor.ChannelTypeSMS, monitor.ChannelTypeTelegram:
	default:
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
// per the monitor design's CLI redaction rule, but only for http and
// https: any other scheme (javascript:, data:, or one the SDK never
// documented) is redacted whole, since there is no reviewed reason to
// trust what such a URL puts in its host component. An address that does
// not parse as an absolute URL is likewise redacted whole, rather than
// print a fragment of a secret in a shape the SDK did not expect.
func redactAddress(address string) string {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return redactedPlaceholder
	}
	return u.Scheme + "://" + u.Host + "/" + redactedPlaceholder
}
