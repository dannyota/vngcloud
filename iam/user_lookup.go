package iam

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/transport"
)

const userLookupPageSize = 1000

// userLookupPage requires explicit metadata instead of inferring completeness
// from the tolerant public list output.
type userLookupPage struct {
	Data       *[]User `json:"data"`
	PageNumber *int    `json:"pageNumber"`
	PageSize   *int    `json:"pageSize"`
	TotalItems *int    `json:"totalItems"`
	TotalPages *int    `json:"totalPages"`
}

func (p userLookupPage) complete() bool {
	if p.Data == nil || p.PageNumber == nil || p.PageSize == nil || p.TotalItems == nil || p.TotalPages == nil {
		return false
	}
	count := len(*p.Data)
	return *p.PageNumber == 0 && *p.PageSize > 0 && *p.PageSize <= userLookupPageSize && count <= *p.PageSize && *p.TotalItems == count &&
		((*p.TotalPages == 1) || (count == 0 && *p.TotalPages == 0))
}

func (o userReadOptions) containsCredential(values ...string) bool {
	for _, value := range values {
		for _, secret := range o.Redact {
			if secret != "" && strings.Contains(value, secret) {
				return true
			}
		}
		if o.SentCredentials != nil {
			for _, secret := range *o.SentCredentials {
				if secret != "" && strings.Contains(value, secret) {
					return true
				}
			}
		}
	}
	return false
}

// lookupUser reads at most one filtered page and one exact-name match. Its
// result proves current state only, never ownership by a create invocation.
func (c *Client) lookupUser(ctx context.Context, username string, opts userReadOptions) (*UserDetail, error) {
	const op = "iam.CreateUser"
	if username == "" {
		return nil, fmt.Errorf("iam: %s: missing lookup username", op)
	}
	if err := checkUserName(op, "Username", username); err != nil {
		return nil, err
	}
	var credentials []string
	if opts.SentCredentials == nil {
		opts.SentCredentials = &credentials
	}
	if opts.containsCredential(username) {
		return nil, fmt.Errorf("iam: %s: unsafe lookup identity", op)
	}
	var resp userLookupPage
	var sent []string
	q := pageQuery(0, userLookupPageSize)
	q.Set("name", username)
	req := opts.request(transport.Request{
		Operation: op, Method: http.MethodGet, URL: c.accountsURL([]string{"iam-users"}, q), OK: []int{200},
		StrictJSON: true, MaxBody: userListMaxBody,
	}, &sent)
	err := c.c.DoJSON(ctx, req, &resp)
	opts.collect(sent)
	if err != nil {
		return nil, err
	}
	if !resp.complete() {
		return nil, fmt.Errorf("iam: %s: incomplete user lookup page", op)
	}
	ids := make(map[string]bool)
	names := make(map[string]bool)
	match := ""
	for _, row := range *resp.Data {
		if opts.containsCredential(row.ID, row.Username, row.CreatedAt) || core.CheckPathID(op, "UserID", row.ID) != nil || row.Username == "" || checkUserName(op, "Username", row.Username) != nil {
			return nil, fmt.Errorf("iam: %s: invalid user lookup row", op)
		}
		id := strings.ToLower(row.ID)
		if ids[id] || names[row.Username] {
			return nil, fmt.Errorf("iam: %s: ambiguous user lookup page", op)
		}
		ids[id] = true
		names[row.Username] = true
		if row.Username == username {
			match = row.ID
		}
	}
	if match == "" {
		if len(*resp.Data) != 0 {
			return nil, fmt.Errorf("iam: %s: no exact user lookup match", op)
		}
		return nil, nil
	}
	return c.confirmUser(ctx, match, username, opts)
}

func (c *Client) confirmUser(ctx context.Context, id, username string, opts userReadOptions) (*UserDetail, error) {
	const op = "iam.CreateUser"
	if opts.containsCredential(id, username) || core.CheckPathID(op, "UserID", id) != nil || username == "" || checkUserName(op, "Username", username) != nil {
		return nil, fmt.Errorf("iam: %s: invalid confirmation identity", op)
	}
	var credentials []string
	if opts.SentCredentials == nil {
		opts.SentCredentials = &credentials
	}
	user, err := c.getUser(ctx, op, id, opts)
	if err != nil {
		return nil, err
	}
	if opts.containsCredential(user.ID, user.Username, user.From) || core.CheckPathID(op, "UserID", user.ID) != nil || !strings.EqualFold(user.ID, id) || user.Username != username || user.From != "iam" {
		return nil, fmt.Errorf("iam: %s: user confirmation identity mismatch", op)
	}
	return user, nil
}

// guardReadJSON applies privacy only to the current read. The zero-option path
// retains the request's existing capture and error behavior.
func (c *Client) guardReadJSON(ctx context.Context, label string, req transport.Request, out any, options ...userReadOptions) error {
	if len(options) == 0 {
		return c.c.DoJSON(ctx, req, out)
	}
	opts := guardUserReadOptions(options...)
	var sent []string
	defer func() { clear(sent) }()
	err := c.c.DoJSON(ctx, opts.request(req, &sent), out)
	opts.collect(sent)
	if err != nil {
		return fmt.Errorf("%s: read %s: %w", req.Operation, label, err)
	}
	if opts.guardContainsResponseCredential(reflect.ValueOf(out)) || (userReadOptions{Redact: sent}).guardContainsResponseCredential(reflect.ValueOf(out)) {
		return fmt.Errorf("iam: %s: unsafe guard response", req.Operation)
	}
	return nil
}

func guardCachedResponse(op string, value any, options ...userReadOptions) error {
	if len(options) != 0 && options[0].guardContainsResponseCredential(reflect.ValueOf(value)) {
		return fmt.Errorf("iam: %s: unsafe cached guard response", op)
	}
	return nil
}

func guardReadDecodeError(err error, options ...userReadOptions) error {
	if len(options) != 0 {
		return transport.SanitizeDecodeError(err)
	}
	return err
}

func guardUserReadOptions(options ...userReadOptions) userReadOptions {
	if len(options) != 0 {
		opts := options[0]
		if opts.WithholdMessage == "" {
			opts.WithholdMessage = userWriteWithheld
		}
		if opts.ClassifyError == nil {
			opts.ClassifyError = userWriteErrorCode
		}
		return opts
	}
	return userReadOptions{}
}

// Guard comparisons normalize identities and actions. Check case variants too,
// without changing the credential rules for ordinary reads and confirmations.
func (o userReadOptions) guardContainsResponseCredential(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.String:
		folded := strings.ToLower(value.String())
		for _, secret := range o.Redact {
			if secret != "" && strings.Contains(folded, strings.ToLower(secret)) {
				return true
			}
		}
		if o.SentCredentials != nil {
			for _, secret := range *o.SentCredentials {
				if secret != "" && strings.Contains(folded, strings.ToLower(secret)) {
					return true
				}
			}
		}
	case reflect.Pointer, reflect.Interface:
		return !value.IsNil() && o.guardContainsResponseCredential(value.Elem())
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() && o.guardContainsResponseCredential(value.Field(i)) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if o.guardContainsResponseCredential(value.Index(i)) {
				return true
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if o.guardContainsResponseCredential(iter.Key()) || o.guardContainsResponseCredential(iter.Value()) {
				return true
			}
		}
	}
	return false
}
