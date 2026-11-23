package globalloadbalancer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/jsonresponse"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

type run struct {
	c     *Client
	op    string
	creds []string

	internalError bool
}

func (c *Client) newRun(op string) *run { return &run{c: c, op: op} }

func (c *Client) childProvenance() error {
	if c.c.Region() != "hcm-3" {
		return fmt.Errorf("%w: GLB child writes support only hcm-3", core.ErrInvalidConfig)
	}
	if err := c.writeProvenance(); err != nil {
		return err
	}
	expected := endpoints.ResolveIAMUser(c.c.Region(), endpoints.Overrides{})
	if c.c.Endpoint(routes.ProductVServer) != expected.VServer {
		return fmt.Errorf("%w: GLB writes require SDK IAM-user login and default endpoints", core.ErrInvalidConfig)
	}
	return nil
}

func notSettled(msg string) error { return fmt.Errorf("%w: %s", ErrNotSettled, msg) }

func refuse(msg string) error { return fmt.Errorf("%w: %s", core.ErrInvalidInput, msg) }

func (r *run) invalidResponse() error {
	return &vngcloud.APIError{Operation: r.op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "response failed validation; body withheld"}
}

func sanitizeChildError(err error) error {
	var api *vngcloud.APIError
	if !errors.As(err, &api) {
		return err
	}
	safe := *api
	safe.Code = core.ResolvedCode(api.StatusCode, "")
	if api.StatusCode >= 400 {
		safe.Message = wireProviderText
	}
	return &safe
}

func leaksDecoded(raw []byte, secrets []string) bool {
	d := json.NewDecoder(bytes.NewReader(raw))
	for {
		token, err := d.Token()
		if errors.Is(err, io.EOF) {
			return false
		}
		if err != nil {
			return true
		}
		if text, ok := token.(string); ok {
			for _, s := range secrets {
				if s != "" && strings.Contains(text, s) {
					return true
				}
			}
		}
	}
}

func (r *run) tainted(texts ...string) bool {
	for _, text := range texts {
		for _, s := range r.creds {
			if s != "" && strings.Contains(text, s) {
				return true
			}
		}
	}
	return false
}

func (r *run) leaksOutput(out any) bool {
	raw, err := json.Marshal(out)
	return err != nil || r.tainted(string(raw))
}

func finishOutput[T any](r *run, out *T, err error) (*T, error) {
	if out != nil && r.leaksOutput(out) {
		return nil, notSettled("output withheld")
	}
	return out, err
}

var errTaintedRequest = errors.New("a request value contains a credential")

func (r *run) guard(ctx context.Context, texts ...string) context.Context {
	return transport.WithCredentialObserver(ctx, func(credential string) ([]string, error) {
		if credential != "" {
			r.remember([]string{credential})
		}
		if r.tainted(texts...) {
			return nil, errTaintedRequest
		}
		return slices.Clone(r.creds), nil
	})
}

func (r *run) remember(sent []string) {
	for _, s := range sent {
		if !slices.Contains(r.creds, s) {
			r.creds = append(r.creds, s)
		}
	}
}

func (r *run) decode(raw json.RawMessage, out any) error {
	if leaksDecoded(raw, r.creds) || jsonresponse.ValidateStrict(raw, out) != nil || json.Unmarshal(raw, out) != nil {
		return r.invalidResponse()
	}
	return nil
}

func (r *run) get(ctx context.Context, url string, out any) error {
	var raw json.RawMessage
	var sent []string
	r.internalError = false
	observe := func(status int, body []byte) {
		r.internalError = status == http.StatusInternalServerError && internalErrorEnvelope(body)
	}
	if r.tainted(url) {
		return refuse("a request value contains a credential")
	}
	ctx = r.guard(ctx, url)
	err := r.c.c.DoJSON(ctx, transport.Request{Operation: r.op, Method: http.MethodGet, URL: url, OK: []int{200}, NoRedirect: true, StrictJSON: true, Sensitive: true, WithholdMessage: wireProviderText, SentCredentials: &sent, ObserveErrorBody: observe}, &raw)
	r.remember(sent)
	if errors.Is(err, errTaintedRequest) {
		return refuse(errTaintedRequest.Error())
	}
	if err != nil {
		var api *vngcloud.APIError
		if errors.As(err, &api) && api.StatusCode < 400 {
			return r.invalidResponse()
		}
		return sanitizeChildError(err)
	}
	return r.decode(raw, out)
}

func (r *run) send(ctx context.Context, method, url string, body any, accept *json.RawMessage) error {
	var dst any
	if accept != nil {
		dst = accept
	}
	encoded, err := json.Marshal(body)
	if err != nil || r.tainted(url, string(encoded)) {
		return refuse("a request value contains a credential")
	}
	ctx = r.guard(ctx, url, string(encoded))
	notSent := false
	var sent []string
	err = r.c.c.DoJSON(ctx, transport.Request{Operation: r.op, Method: method, URL: url, Body: body, OK: []int{200, 202}, Once: true, NoRedirect: true, StrictJSON: accept != nil, Sensitive: true, KeepObservedStatus: true, WithholdMessage: wireProviderText, NotSent: &notSent, SentCredentials: &sent}, dst)
	r.remember(sent)
	if err == nil {
		return nil
	}
	if errors.Is(err, errTaintedRequest) {
		return refuse(errTaintedRequest.Error())
	}
	if definitiveWriteError(err, notSent) {
		return sanitizeChildError(err)
	}
	return notSettled("request unconfirmed")
}

func (r *run) pause(ctx context.Context) bool {
	timer := time.NewTimer(r.c.pollEvery())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (c *Client) glbChildURL(glb string, parts ...string) string {
	return c.glbURL(append([]string{"global-load-balancers", glb}, parts...), nil)
}

func (c *Client) vserverURL(project string, parts ...string) string {
	return c.c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v2", Parts: append([]string{project}, parts...)})
}

func (r *run) pools(ctx context.Context, glb string) ([]wirePool, error) {
	var out []wirePool
	if err := r.get(ctx, r.c.glbChildURL(glb, "global-pools"), &out); err != nil {
		return nil, err
	}
	if out == nil || !validIDs(out) {
		return nil, r.invalidResponse()
	}
	return out, nil
}

func (r *run) listeners(ctx context.Context, glb string) ([]wireListener, error) {
	var out []wireListener
	if err := r.get(ctx, r.c.glbChildURL(glb, "global-listeners"), &out); err != nil {
		return nil, err
	}
	if out == nil || !validIDs(out) {
		return nil, r.invalidResponse()
	}
	return out, nil
}

func (r *run) groups(ctx context.Context, glb, pool string) ([]wireGroup, error) {
	var out []wireGroup
	if err := r.get(ctx, r.c.glbChildURL(glb, "global-pools", pool, "pool-members"), &out); err != nil {
		return nil, err
	}
	if out == nil || !validIDs(out) {
		return nil, r.invalidResponse()
	}
	return out, nil
}

func (r *run) pool(ctx context.Context, glb, id string) (wirePool, error) {
	var out wirePool
	err := r.get(ctx, r.c.glbChildURL(glb, "global-pools", id), &out)
	return out, err
}

func (r *run) group(ctx context.Context, glb, pool, id string) (wireGroup, error) {
	var out wireGroup
	err := r.get(ctx, r.c.glbChildURL(glb, "global-pools", pool, "pool-members", id), &out)
	return out, err
}

func (r *run) listener(ctx context.Context, glb, id string) (wireListener, error) {
	var out wireListener
	err := r.get(ctx, r.c.glbChildURL(glb, "global-listeners", id), &out)
	return out, err
}

func validIDs[T interface{ rowID() string }](rows []T) bool {
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		id := row.rowID()
		if core.CheckPathID("", "", id) != nil || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func internalErrorEnvelope(body []byte) bool {
	if jsonresponse.Validate(body) != nil {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil {
		return false
	}
	for key := range fields {
		if key != "code" && strings.EqualFold(key, "code") {
			return false
		}
	}
	var code string
	return json.Unmarshal(fields["code"], &code) == nil && code == "internal_error"
}
