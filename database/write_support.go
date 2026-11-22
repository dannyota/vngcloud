package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

func (c *Client) writeProvenance() error {
	expected := endpoints.ResolveIAMUser(c.c.Region(), endpoints.Overrides{})
	if c.c.Region() != "hcm-3" || !c.c.UsesIAMUserLogin() || !c.c.UsesDefaultAuthEndpoints() || c.c.Endpoint(routes.ProductDatabase) != expected.Database {
		return fmt.Errorf("%w: database writes require hcm-3, SDK IAM-user login, and default endpoints", core.ErrInvalidConfig)
	}
	return nil
}

func contextSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var errPollTimeout = errors.New("database: wait timed out")

func (c *Client) poll(ctx context.Context, step func(context.Context) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, pollBound)
	defer cancel()
	deadline := c.now().Add(pollBound)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if done, err := step(ctx); done {
			return err
		}
		remaining := deadline.Sub(c.now())
		if remaining <= 0 {
			return errPollTimeout
		}
		if err := c.sleep(ctx, min(pollInterval, remaining)); err != nil {
			return err
		}
	}
}

func settleWait(err error, what string) error {
	if err == nil || errors.Is(err, ErrFailed) || errors.Is(err, ErrNotSettled) {
		return err
	}
	return fmt.Errorf("%w: %s confirmation interrupted", ErrNotSettled, what)
}

func reflects(inst Instance, password string) bool {
	raw, _ := json.Marshal(inst)
	escaped, _ := json.Marshal(password)
	text := string(raw)
	return strings.Contains(text, password) || strings.Contains(text, strings.Trim(string(escaped), `"`))
}

var unsafeProviderMessage = regexp.MustCompile(`(?i)(\b(?:password|passwd|secret|(?:access|refresh)?token|authorization|bearer|credential|cookie|session|api[-_ ]?key)\b\s*(?:[:=]|\bis\b)\s*[^\s,;]+|\bbearer\s+[^\s,;]+|\b[a-z][a-z0-9]*-[a-z0-9]*[0-9][a-z0-9-]*\b|\b[0-9]{8,}\b|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[\w.+-]+@[\w.-]+\.[a-z]{2,}|[\x00-\x1f\x7f])`)

type fieldDetail struct{ Field, Message string }

const maxFieldErrors = 5

var fieldNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.\[\]]{0,63}$`)

func parseFieldErrors(body []byte) []fieldDetail {
	var env struct {
		FieldError []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"fieldError"`
	}
	if json.Unmarshal(body, &env) != nil {
		return nil
	}
	var out []fieldDetail
	for _, e := range env.FieldError {
		if len(out) == maxFieldErrors {
			break
		}
		out = append(out, fieldDetail{Field: e.Field, Message: e.Message})
	}
	return out
}

func leaksSecret(text string, secrets []string) bool {
	for _, secret := range secrets {
		escaped, _ := json.Marshal(secret)
		if secret != "" && (strings.Contains(text, secret) || strings.Contains(text, strings.Trim(string(escaped), `"`))) {
			return true
		}
	}
	return false
}

func describeFields(fields []fieldDetail, secrets []string) string {
	var parts []string
	for _, f := range fields {
		if !fieldNamePattern.MatchString(f.Field) || leaksSecret(f.Field, secrets) {
			continue
		}
		part := f.Field
		m := strings.TrimSpace(f.Message)
		if m != "" && len(m) <= 200 && !unsafeProviderMessage.MatchString(m) && !leaksSecret(m, secrets) {
			part += ": " + transport.RedactErrorText(m, secrets...)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func writeError(op string, err error, secrets []string, fields []fieldDetail) error {
	safe := safeError(op, err)
	var provider *core.APIError
	if !errors.As(err, &provider) || provider.Message == "" {
		return safe
	}
	message := provider.Message
	withhold := len(message) > 512 || unsafeProviderMessage.MatchString(message)
	for _, secret := range secrets {
		escaped, _ := json.Marshal(secret)
		if secret != "" && (strings.Contains(message, secret) || strings.Contains(message, strings.Trim(string(escaped), `"`))) {
			withhold = true
		}
	}
	if withhold {
		safe.Message = "provider response withheld"
	} else {
		safe.Message = transport.RedactErrorText(message, secrets...)
	}
	if detail := describeFields(fields, secrets); detail != "" {
		safe.Message += ": " + detail
	}
	return safe
}

func definitiveWriteError(err error, notSent bool, status int) bool {
	if notSent {
		return true
	}
	var api *core.APIError
	if errors.As(err, &api) {
		status = api.StatusCode
	}
	return status >= 400 && status < 500
}
