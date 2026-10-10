package core

import "danny.vn/vngcloud/internal/transport"

// RedactError scrubs credentials from errors built from successful HTTP
// envelopes, which the transport leaves intact for service decoding.
func (c *Client) RedactError(message, code string, values ...string) (string, string) {
	if c.cdnAPIKey != nil {
		values = append(append([]string(nil), values...), c.cdnAPIKey.reveal())
	}
	message = transport.RedactValues(message, values...)
	code = transport.RedactValues(code, values...)
	if c.transport != nil {
		message = c.transport.RedactCurrentToken(message)
		code = c.transport.RedactCurrentToken(code)
	}
	return message, code
}
