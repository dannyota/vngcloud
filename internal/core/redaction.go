package core

import "danny.vn/vngcloud/internal/transport"

// RedactError scrubs credentials from errors built from successful HTTP
// envelopes. Callers supply the credential sent on the final attempt.
func (c *Client) RedactError(message, code string, values ...string) (string, string) {
	if c.cdnAPIKey != nil {
		values = append(append([]string(nil), values...), c.cdnAPIKey.reveal())
	}
	message = transport.RedactValues(message, values...)
	code = transport.RedactValues(code, values...)
	return message, code
}
