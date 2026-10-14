package core

import "danny.vn/vngcloud/internal/transport"

// RedactError scrubs credentials from errors built from successful HTTP
// envelopes. Callers supply every distinct credential sent during the call.
func (c *Client) RedactError(message, code string, values ...string) (string, string) {
	if c.cdnAPIKey != nil {
		values = append(append([]string(nil), values...), c.cdnAPIKey.reveal())
	}
	message = transport.RedactErrorText(message, values...)
	code = transport.RedactErrorText(code, values...)
	return message, code
}
