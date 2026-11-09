package compute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/pricing"
)

const serverMaxBody = 8 << 20
const serverReadBound = 30 * time.Second
const serverCredentialLimit = 256

func (c *Client) serverQuote(ctx context.Context, in *pricing.GetQuoteInput) (*pricing.GetQuoteOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, serverReadBound)
	defer cancel()
	return c.pricing.GetQuote(ctx, in)
}

type serverResponseScopeKey struct{}

type serverResponseScope struct {
	mu          sync.Mutex
	credentials []string
	secrets     []string
	exhausted   bool
}

func serverOperationContext(ctx context.Context) (context.Context, *serverResponseScope) {
	if scope, ok := ctx.Value(serverResponseScopeKey{}).(*serverResponseScope); ok {
		return ctx, scope
	}
	scope := &serverResponseScope{}
	ctx = context.WithValue(ctx, serverResponseScopeKey{}, scope)
	return transport.WithCredentialObserver(ctx, scope.observe), scope
}

func (s *serverResponseScope) observe(credential string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exhausted {
		return nil, invalidServerResponse("", 0, nil)
	}
	if credential != "" {
		found := false
		for _, sent := range s.credentials {
			found = found || sent == credential
		}
		if !found {
			if len(s.credentials) == serverCredentialLimit {
				s.exhausted = true
				return nil, invalidServerResponse("", 0, nil)
			}
			s.credentials = append(s.credentials, credential)
		}
	}
	values := append([]string(nil), s.credentials...)
	return append(values, s.secrets...), nil
}

func (s *serverResponseScope) addSecrets(values ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, value := range values {
		if value != "" {
			s.secrets = append(s.secrets, value)
		}
	}
}

func (s *serverResponseScope) unsafe(raw []byte) bool {
	values, err := s.observe("")
	if err != nil {
		return true
	}
	for _, value := range values {
		if bytes.Contains(raw, []byte(value)) {
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
		if text, ok := token.(string); ok {
			for _, value := range values {
				if strings.Contains(text, value) {
					return true
				}
			}
		}
	}
}

func invalidServerResponse(op string, status int, cause error) error {
	return &core.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "invalid server response; body withheld", Err: cause}
}

func finishServerOperation[T any](scope *serverResponseScope, op string, out **T, resultErr *error) {
	scope.mu.Lock()
	values := append([]string(nil), scope.credentials...)
	values = append(values, scope.secrets...)
	exhausted := scope.exhausted
	scope.mu.Unlock()
	raw, marshalErr := json.Marshal(*out)
	if exhausted || marshalErr != nil || scope.unsafe(raw) || serverValueReflectsCredentials(reflect.ValueOf(*out), values) {
		*out = nil
		*resultErr = errors.Join(*resultErr, invalidServerResponse(op, 0, nil))
	}
	*resultErr, _ = sanitizeServerCause(*resultErr, values, scope.safeAPIMetadata)
}

// JSON omits PublicIP, so final output checks must inspect Go values directly.
func serverValueReflectsCredentials(value reflect.Value, credentials []string) bool {
	switch value.Kind() {
	case reflect.String:
		for _, credential := range credentials {
			if credential != "" && strings.Contains(value.String(), credential) {
				return true
			}
		}
	case reflect.Pointer, reflect.Interface:
		if !value.IsNil() {
			return serverValueReflectsCredentials(value.Elem(), credentials)
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if serverValueReflectsCredentials(value.Field(i), credentials) {
				return true
			}
		}
	case reflect.Array, reflect.Slice:
		for i := range value.Len() {
			if serverValueReflectsCredentials(value.Index(i), credentials) {
				return true
			}
		}
	case reflect.Map:
		for rows := value.MapRange(); rows.Next(); {
			if serverValueReflectsCredentials(rows.Key(), credentials) || serverValueReflectsCredentials(rows.Value(), credentials) {
				return true
			}
		}
	}
	return false
}

//nolint:errorlint // Inspect each node directly, including hidden API metadata and every joined cause.
func sanitizeServerCause(err error, credentials []string, allowAPI func(*core.APIError) bool) (error, bool) {
	if err == nil {
		return nil, false
	}
	text := err.Error()
	message := transport.RedactErrorText(text, credentials...)
	changed := message != text
	switch cause := err.(type) {
	case *core.APIError:
		safe := *cause
		var childChanged bool
		safe.Err, childChanged = sanitizeServerCause(cause.Err, credentials, allowAPI)
		safe.Operation = transport.RedactErrorText(cause.Operation, credentials...)
		safe.Code = transport.RedactErrorText(cause.Code, credentials...)
		safe.Message = transport.RedactErrorText(cause.Message, credentials...)
		// APIError can regenerate fixed cause text or a numeric status. Keep
		// identities separate when its rendered metadata is unsafe.
		if !allowAPI(&safe) {
			result := new(serverResponseError)
			result.message = transport.RedactErrorText(safe.Error(), credentials...)
			result.cause = safe.Err
			result.serverErrorIdentity = serverCauseIdentity(err)
			safe.Err = nil
			if safe.Message == "" {
				safe.Message = transport.RedactErrorText("request failed", credentials...)
			}
			result.api, result.allowAPI = &safe, allowAPI
			return result, true
		}
		if changed || childChanged || safe.Operation != cause.Operation || safe.Code != cause.Code || safe.Message != cause.Message {
			return &safe, true
		}
	case interface{ Unwrap() []error }:
		children := cause.Unwrap()
		safe := make([]error, len(children))
		for i, child := range children {
			var childChanged bool
			safe[i], childChanged = sanitizeServerCause(child, credentials, allowAPI)
			changed = changed || childChanged
		}
		if changed {
			result := new(serverResponseJoinedError)
			result.message, result.causes = message, safe
			result.serverErrorIdentity = serverCauseIdentity(err)
			return result, true
		}
	case interface{ Unwrap() error }:
		safe, childChanged := sanitizeServerCause(cause.Unwrap(), credentials, allowAPI)
		if changed || childChanged {
			result := new(serverResponseError)
			result.message, result.cause = message, safe
			result.serverErrorIdentity = serverCauseIdentity(err)
			return result, true
		}
	default:
		if changed {
			result := new(serverResponseError)
			result.message = message
			result.serverErrorIdentity = serverCauseIdentity(err)
			return result, true
		}
	}
	return err, false
}

// Keep the original cause private to the yes/no identity predicate. Unwrap
// exposes only sanitized causes, and As never delegates to this predicate.
type serverErrorIdentity func(error) bool

func serverCauseIdentity(original error) serverErrorIdentity {
	return func(target error) bool { return errors.Is(original, target) }
}

// Is preserves every original identity without exposing its cause text.
func (identity serverErrorIdentity) Is(target error) bool {
	return identity != nil && identity(target)
}

func (s *serverResponseScope) safeAPIMetadata(api *core.APIError) bool {
	if api == nil {
		return false
	}
	credentials, err := s.observe("")
	if err != nil {
		return false
	}
	for _, text := range []string{api.Error(), api.Operation, api.Code, api.Message} {
		for _, credential := range credentials {
			if credential != "" && strings.Contains(text, credential) {
				return false
			}
		}
		if transport.RedactErrorText(text, credentials...) != text {
			return false
		}
	}
	return true
}

type serverResponseError struct {
	serverErrorIdentity
	cause    error
	message  string
	api      *core.APIError
	allowAPI func(*core.APIError) bool
}

// Error returns text checked against every credential observed by the operation.
func (e *serverResponseError) Error() string { return e.message }

// Unwrap exposes only causes checked against the operation's credentials.
func (e *serverResponseError) Unwrap() error { return e.cause }

// As exposes safe API metadata without reintroducing credential-bearing cause text.
func (e *serverResponseError) As(target any) bool {
	if api, ok := target.(**core.APIError); ok && e.api != nil && e.allowAPI != nil && e.allowAPI(e.api) {
		*api = e.api
		return true
	}
	return false
}

type serverResponseJoinedError struct {
	serverErrorIdentity
	message string
	causes  []error
}

// Error returns text checked against every credential observed by the operation.
func (e *serverResponseJoinedError) Error() string { return e.message }

// Unwrap preserves joined identities without retaining credential-bearing causes.
func (e *serverResponseJoinedError) Unwrap() []error { return e.causes }

// doServerJSON bounds the raw response before either transport capture or
// decoding, then checks unknown fields as well as modeled fields.
func (c *Client) doServerJSON(ctx context.Context, req transport.Request, out any) error {
	_, err := c.doServerJSONStatus(ctx, req, out)
	return err
}

func (c *Client) doServerJSONStatus(ctx context.Context, req transport.Request, out any) (int, error) {
	ctx, scope := serverOperationContext(ctx)
	req.MaxBody = serverMaxBody
	req.Sensitive = true
	req.NoRedirect = true
	if out == nil {
		return c.c.DoJSONStatus(ctx, req, nil)
	}
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		if errors.Is(err, transport.ErrBodyTooLarge) {
			return status, invalidServerResponse(req.Operation, status, err)
		}
		return status, err
	}
	if scope.unsafe(raw) || json.Unmarshal(raw, out) != nil {
		return status, invalidServerResponse(req.Operation, status, nil)
	}
	return status, nil
}
