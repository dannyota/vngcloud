package pricing

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// Keep unknown fields until credential checks finish, including JSON keys
// and values that typed decoding would discard or overwrite.
func decodeQuoteResponse(op string, raw json.RawMessage, credentials []string) (quoteResponse, error) {
	var resp quoteResponse
	if quoteReflectsCredentials(raw, credentials) {
		return resp, &core.APIError{Operation: op, Code: "InvalidResponse", Message: "invalid quote response; body withheld"}
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &resp); err != nil {
			return quoteResponse{}, &core.APIError{Operation: op, Err: transport.SanitizeDecodeError(err)}
		}
	}
	return resp, nil
}

func quoteReflectsCredentials(raw []byte, credentials []string) bool {
	for _, credential := range credentials {
		if credential != "" && bytes.Contains(raw, []byte(credential)) {
			return true
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err != nil {
			return err != io.EOF
		}
		if value, ok := token.(string); ok {
			for _, credential := range credentials {
				if credential != "" && strings.Contains(value, credential) {
					return true
				}
			}
		}
	}
}
