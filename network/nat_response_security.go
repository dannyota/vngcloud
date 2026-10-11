package network

import (
	"bytes"
	"encoding/json"
	"strings"
)

const natMaxBody = 4 << 20

func natReflectsCredential(raw []byte, credential string) bool {
	if credential == "" {
		return false
	}
	if bytes.Contains(raw, []byte(credential)) {
		return true
	}
	// A JSON string can reflect the token through Unicode escapes.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if value, ok := token.(string); ok && strings.Contains(value, credential) {
			return true
		}
	}
}
