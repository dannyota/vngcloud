package portal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/floatingip"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/transport"
)

func (c *Client) readJSON(ctx context.Context, req transport.Request, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var credentials []string
	req.SentCredentials = &credentials
	req.MaxBody = 8 << 20
	req.Sensitive = true
	req.NoRedirect = true
	req.WithholdMessage = "portal response withheld"
	var raw json.RawMessage
	status, err := c.c.DoJSONStatus(ctx, req, &raw)
	if err != nil {
		if errors.Is(err, core.ErrInvalidConfig) {
			return core.ErrInvalidConfig
		}
		var cause error
		for _, identity := range []error{context.Canceled, context.DeadlineExceeded, transport.ErrBodyTooLarge, core.ErrAuth, core.ErrPermission, core.ErrNotFound, core.ErrRateLimited} {
			if errors.Is(err, identity) {
				cause = errors.Join(cause, identity)
			}
		}
		var api *core.APIError
		retryable := errors.As(err, &api) && api.Retryable
		return &core.APIError{Operation: req.Operation, StatusCode: status, Code: core.ResolvedCode(status, ""), Message: "portal response withheld", Retryable: retryable, Err: cause}
	}
	if floatingip.Reflects(raw, credentials) || jsonresponse.ValidateStrict(raw, out) != nil || json.Unmarshal(raw, out) != nil {
		return &core.APIError{Operation: req.Operation, StatusCode: status, Code: "InvalidResponse", Message: "invalid portal response; body withheld"}
	}
	return nil
}

func validPortalList[T any](data []byte) bool {
	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("[")) {
		return jsonresponse.ValidateStrict(data, new([]T)) == nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return false
	}
	found := 0
	for key, raw := range fields {
		for _, name := range []string{"listData", "data", "items", "content", "results", "projects", "regions", "zones", "quotas", "quotaUsed", "volumeTypes", "volumeTypeZones"} {
			if !strings.EqualFold(key, name) {
				continue
			}
			found++
			if key != name || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) || jsonresponse.ValidateStrict(raw, new([]T)) != nil {
				return false
			}
		}
	}
	return found == 1
}
