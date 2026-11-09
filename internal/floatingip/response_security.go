package floatingip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

// MaxBody bounds security-group responses before capture or decoding.
const MaxBody = 8 << 20

type observerKey struct{}
type componentKey struct{}

// WithObserver shares the operation's credential history with nested quotes.
func WithObserver(ctx context.Context, observe func(string) ([]string, error)) context.Context {
	ctx = context.WithValue(ctx, observerKey{}, observe)
	return transport.WithCredentialObserver(ctx, observe)
}

// Credentials snapshots all credentials observed by the enclosing operation.
func Credentials(ctx context.Context) ([]string, error) {
	observe, ok := ctx.Value(observerKey{}).(func(string) ([]string, error))
	if !ok {
		return nil, nil
	}
	return observe("")
}

// ComponentQuote marks a quote whose complete totals must be validated.
func ComponentQuote(ctx context.Context) bool {
	value, _ := ctx.Value(componentKey{}).(bool)
	return value
}

// InvalidResponse withholds hostile response text while preserving its cause.
func InvalidResponse(op string, cause error) error {
	return &core.APIError{Operation: op, Code: "InvalidResponse", Message: "invalid floating IP response; body withheld", Err: cause}
}

// Reflects checks raw strings and JSON-decoded strings, including unknown keys.
func Reflects(raw []byte, values []string) bool {
	for _, v := range values {
		if v != "" && bytes.Contains(raw, []byte(v)) {
			return true
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	for {
		token, err := dec.Token()
		if err != nil {
			return err != io.EOF
		}
		if s, ok := token.(string); ok {
			for _, v := range values {
				if v != "" && strings.Contains(s, v) {
					return true
				}
			}
		}
	}
}

// ResponseContext supplies credential protection to standalone group reads.
func ResponseContext(ctx context.Context) (context.Context, func([]byte) bool, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	var mu sync.Mutex
	var values []string
	exhausted := false
	observe := func(value string) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		if exhausted {
			return append([]string(nil), values...), InvalidResponse("", nil)
		}
		if value != "" {
			found := false
			for _, known := range values {
				found = found || known == value
			}
			if !found {
				if len(values) == 256 {
					exhausted = true
					return append([]string(nil), values...), InvalidResponse("", nil)
				}
				values = append(values, value)
			}
		}
		return append([]string(nil), values...), nil
	}
	ctx = WithObserver(ctx, observe)
	return ctx, func(raw []byte) bool { values, err := observe(""); return err != nil || Reflects(raw, values) }, cancel
}

// ReadJSON applies the shared response bound before capture and checks raw data.
func ReadJSON(ctx context.Context, c *core.Client, req transport.Request, out any, unsafe func([]byte) bool) error {
	req.MaxBody = MaxBody
	req.Sensitive = true
	req.NoRedirect = true
	req.WithholdMessage = "security group response withheld"
	var raw json.RawMessage
	if err := c.DoJSON(ctx, req, &raw); err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return InvalidResponse(req.Operation, err)
		}
		return err
	}
	if unsafe(raw) || json.Unmarshal(raw, out) != nil {
		return InvalidResponse(req.Operation, nil)
	}
	return nil
}
