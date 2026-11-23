package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/transport"
)

const (
	endpointZone      = "HCM03-1C"
	endpointService   = "vstorage.hcm03"
	endpointPackage   = "Standard"
	endpointOrigin    = "https://hcm-3-vnetwork.console.greennode.ai"
	endpointOtherOrig = "https://han-1-vnetwork.console.greennode.ai"
	endpointRecovery  = "Run network list-endpoints and network get-endpoint in the same scope, then inspect billing before another mutation. Do not repeat this write."
)

var (
	uuidPart       = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	hex24Pattern   = regexp.MustCompile(`^[0-9a-f]{24}$`)
	uuidPattern    = regexp.MustCompile(`^` + uuidPart + `$`)
	projectPattern = regexp.MustCompile(`^pro-` + uuidPart + `$`)
	vpcPattern     = regexp.MustCompile(`^net-` + uuidPart + `$`)
	subnetPattern  = regexp.MustCompile(`^sub-` + uuidPart + `$`)
	endpointIDForm = regexp.MustCompile(`^enp-` + uuidPart + `$`)
)

type endpointUnsettledError struct {
	op    string
	cause error
}

func (e *endpointUnsettledError) Error() string {
	return e.op + ": write outcome is unconfirmed; " + endpointRecovery
}
func (e *endpointUnsettledError) Unwrap() error { return e.cause }

func endpointUnsettled(op string, causes ...error) error {
	safe := []error{ErrNotSettled}
	for _, cause := range causes {
		if cause != nil {
			safe = append(safe, withoutNotFound(endpointSafeError(op, cause)))
		}
	}
	return &endpointUnsettledError{op: op, cause: errors.Join(safe...)}
}

func withoutNotFound(err error) error {
	var api *core.APIError
	if errors.As(err, &api) && (api.StatusCode == http.StatusNotFound || errors.Is(api.Err, core.ErrNotFound)) {
		return &core.APIError{Operation: api.Operation, Message: "endpoint request failed"}
	}
	return err
}

func endpointInvalid(op string) error {
	return &core.APIError{Operation: op, StatusCode: http.StatusOK, Code: "InvalidResponse", Message: "endpoint response had invalid structure"}
}

func endpointSafeError(op string, err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, core.ErrInvalidConfig, core.ErrInvalidInput, core.ErrMissingProjectID, core.ErrProjectNotFound, core.ErrProjectAmbiguous} {
		if errors.Is(err, sentinel) {
			return fmt.Errorf("%s: %w", op, sentinel)
		}
	}
	var api *core.APIError
	if errors.As(err, &api) {
		if api.Code == "InvalidResponse" && api.StatusCode < 400 {
			return endpointInvalid(op)
		}
		return endpointStatusError(op, api.StatusCode, api.Retryable)
	}
	if errors.Is(err, core.ErrAuth) {
		return fmt.Errorf("%s: %w", op, core.ErrAuth)
	}
	return &core.APIError{Operation: op, Message: "endpoint request failed"}
}

func endpointStatusError(op string, status int, retryable bool) error {
	var cause error
	switch status {
	case 401:
		cause = core.ErrAuth
	case 403:
		cause = core.ErrPermission
	case 404:
		cause = core.ErrNotFound
	case 429:
		cause = core.ErrRateLimited
	}
	return &core.APIError{Operation: op, StatusCode: status, Code: core.ResolvedCode(status, ""), Message: "endpoint response withheld", Retryable: retryable, Err: cause}
}

func endpointDefiniteRefusal(err error) bool {
	var api *core.APIError
	return errors.As(err, &api) && api.StatusCode >= 400 && api.StatusCode < 500 && api.StatusCode != http.StatusRequestTimeout
}

func (c *Client) endpointProvenance(op string) error {
	expected := endpoints.ResolveIAMUser("hcm-3", endpoints.Overrides{})
	if c.c.Region() != "hcm-3" || !c.c.UsesIAMUserLogin() || !c.c.UsesDefaultAuthEndpoints() || c.c.VNetworkOverride() ||
		c.c.Endpoint(routes.ProductVServer) != expected.VServer || c.c.Endpoint(routes.ProductVNet) != expected.VNetwork {
		return fmt.Errorf("%w: %s requires hcm-3, SDK IAM-user login, and default endpoints", core.ErrInvalidConfig, op)
	}
	return nil
}

type endpointOp struct {
	c     *Client
	op    string
	creds []string
	scope natScope
}

func (o *endpointOp) carriesCredential(req transport.Request) bool {
	var body []byte
	if req.Body != nil {
		body, _ = json.Marshal(req.Body)
	}
	for _, credential := range o.creds {
		if credential != "" && (strings.Contains(req.URL, credential) || (body != nil && natReflectsCredential(body, credential))) {
			return true
		}
	}
	return false
}

func (o *endpointOp) raw(ctx context.Context, req transport.Request) (json.RawMessage, *bool, error) {
	req.Operation = o.op
	req.Sensitive = true
	req.NoRedirect = true
	req.KeepObservedStatus = true
	req.MaxBody = natMaxBody
	req.WithholdMessage = "endpoint response withheld"
	var sent []string
	req.SentCredentials = &sent
	notSent := false
	req.NotSent = &notSent
	carries := func() error {
		notSent = true
		return fmt.Errorf("%w: %s: the request would carry a credential", core.ErrInvalidInput, o.op)
	}
	if o.carriesCredential(req) {
		return nil, &notSent, carries()
	}
	blocked := false
	ctx = transport.WithCredentialObserver(ctx, func(credential string) ([]string, error) {
		o.creds = appendDistinct(o.creds, credential)
		if credential != "" && o.carriesCredential(req) {
			blocked = true
			return nil, errors.New("credential in request")
		}
		return o.creds, nil
	})
	var raw json.RawMessage
	status, err := o.c.c.DoJSONStatus(ctx, req, &raw)
	o.creds = appendDistinct(o.creds, sent...)
	if blocked {
		return nil, &notSent, carries()
	}
	if err != nil {
		var api *core.APIError
		retryable := errors.As(err, &api) && api.Retryable
		if status == 0 && api != nil {
			status = api.StatusCode
		}
		switch {
		case status >= 400:
			refusal := endpointStatusError(o.op, status, retryable)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, &notSent, errors.Join(refusal, endpointSafeError(o.op, err))
			}
			return nil, &notSent, refusal
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return nil, &notSent, endpointSafeError(o.op, err)
		case errors.Is(err, transport.ErrBodyTooLarge) || (status > 0 && status < 300):
			return nil, &notSent, endpointInvalid(o.op)
		}
		return nil, &notSent, endpointSafeError(o.op, err)
	}
	if natReflectsSentCredentials(raw, o.creds) {
		return nil, &notSent, endpointInvalid(o.op)
	}
	return raw, &notSent, nil
}

func (o *endpointOp) envelope(ctx context.Context, req transport.Request, code *int) (*natEnvelope, error) {
	env, _, err := o.send(ctx, req, code)
	return env, err
}

func (o *endpointOp) send(ctx context.Context, req transport.Request, code *int) (*natEnvelope, *bool, error) {
	raw, notSent, err := o.raw(ctx, req)
	if err != nil {
		return nil, notSent, err
	}
	var env natEnvelope
	if decodeNATEnvelope(raw, &env, reflect.TypeFor[map[string]json.RawMessage]()) != nil || env.Success == nil || !*env.Success || (code != nil && !natNumericCode(env.Code, *code)) {
		return nil, notSent, endpointInvalid(o.op)
	}
	return &env, notSent, nil
}

func (o *endpointOp) route(version string, parts []string, query url.Values) string {
	return o.scope.route(version, parts, query)
}

func (o *endpointOp) discoverScope(ctx context.Context) error {
	project, err := o.project(ctx, o.c.c.ConfiguredProjectID())
	if err != nil {
		return err
	}
	base := endpointOrigin + "/vnetwork-gateway/"
	o.scope = natScope{base: base, project: project}
	region, err := o.region(ctx)
	if err != nil {
		return err
	}
	o.scope.zone = region
	return nil
}

func (o *endpointOp) project(ctx context.Context, configured string) (string, error) {
	raw, _, err := o.raw(ctx, transport.Request{Method: http.MethodGet, URL: o.c.c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v1", Parts: []string{"projects"}}), OK: []int{http.StatusOK}})
	if err != nil {
		return "", err
	}
	var wire struct {
		Projects []struct {
			ID     string `json:"projectId"`
			Region string `json:"region"`
		} `json:"projects"`
	}
	if decodeNATEnvelope(raw, &wire, reflect.TypeFor[struct{}]()) != nil || wire.Projects == nil || checkNATNulls(raw, reflect.TypeOf(wire)) != nil {
		return "", endpointInvalid(o.op)
	}
	id := ""
	for _, p := range wire.Projects {
		if (p.Region != "" && p.Region != o.c.c.Region()) || (configured != "" && p.ID != configured) {
			continue
		}
		if !projectPattern.MatchString(p.ID) || core.CheckPathID(o.op, "discovered ProjectID", p.ID) != nil {
			return "", endpointInvalid(o.op)
		}
		if id != "" {
			return "", fmt.Errorf("%w: %s", core.ErrProjectAmbiguous, o.op)
		}
		id = p.ID
	}
	if id == "" {
		return "", fmt.Errorf("%w: %s", core.ErrProjectNotFound, o.op)
	}
	return id, nil
}

func (o *endpointOp) region(ctx context.Context) (string, error) {
	raw, _, err := o.raw(ctx, transport.Request{Method: http.MethodGet, URL: routes.URL(fixedVNetEndpoint{base: o.scope.base}, routes.Route{Product: routes.ProductVNet, Version: "vnetwork/v1", Parts: []string{"regions"}}), OK: []int{http.StatusOK}})
	if err != nil {
		return "", err
	}
	var env natEnvelope
	if decodeNATEnvelope(raw, &env, reflect.TypeFor[map[string]json.RawMessage]()) != nil || env.Success == nil || !*env.Success || len(env.Data) == 0 || string(env.Data) == "null" {
		return "", endpointInvalid(o.op)
	}
	var regions []struct {
		UUID         string `json:"uuid"`
		GatewayURL   string `json:"gatewayUrl"`
		DashboardURL string `json:"vnetworkDashboard"`
	}
	if decodeNATEnvelope(env.Data, &regions, reflect.TypeOf(regions).Elem()) != nil {
		return "", endpointInvalid(o.op)
	}
	id, matches := "", 0
	for _, region := range regions {
		dashboard, gateway := trimSlash(region.DashboardURL), trimSlash(region.GatewayURL)
		if dashboard != endpointOrigin && gateway != endpointOrigin+"/vnetwork-gateway" {
			continue
		}
		if dashboard == endpointOtherOrig || gateway == endpointOtherOrig+"/vnetwork-gateway" {
			return "", fmt.Errorf("%w: %s: conflicting region mapping", core.ErrInvalidConfig, o.op)
		}
		if !hex24Pattern.MatchString(region.UUID) {
			return "", endpointInvalid(o.op)
		}
		matches++
		id = region.UUID
	}
	if matches != 1 {
		return "", fmt.Errorf("%w: %s: no unique region mapping", core.ErrInvalidConfig, o.op)
	}
	return id, nil
}

func trimSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

type endpointFilter struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func endpointParams(page int, search []endpointFilter) url.Values {
	if search == nil {
		search = []endpointFilter{}
	}
	raw, _ := json.Marshal(struct {
		Search []endpointFilter `json:"search"`
		Sort   struct{}         `json:"sort"`
		Page   int              `json:"page"`
		Size   int              `json:"size"`
	}{Search: search, Page: page, Size: 1000})
	return url.Values{"params": {string(raw)}}
}

func endpointScan[T any](ctx context.Context, o *endpointOp, parts []string, search []endpointFilter, decode func(json.RawMessage) ([]T, error), id func(T) string) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	items := []T{}
	seen := map[string]bool{}
	total, pages, size := -1, -1, -1
	for page := 1; page <= 50; page++ {
		env, err := o.envelope(ctx, transport.Request{Method: http.MethodGet, URL: o.route("vnetwork/v1", parts, endpointParams(page, search)), OK: []int{http.StatusOK}}, nil)
		if err != nil {
			return nil, err
		}
		if env.Page == nil || *env.Page != page || env.Size == nil || *env.Size <= 0 || env.Total == nil || *env.Total < 0 || env.TotalPage == nil || *env.TotalPage < 0 || *env.TotalPage > 50 {
			return nil, endpointInvalid(o.op)
		}
		if page == 1 {
			total, pages, size = *env.Total, *env.TotalPage, *env.Size
			if pages != (total+size-1)/size {
				return nil, endpointInvalid(o.op)
			}
		} else if total != *env.Total || pages != *env.TotalPage || size != *env.Size {
			return nil, endpointInvalid(o.op)
		}
		rows := []T{}
		if len(env.Data) == 0 {
			if total != 0 || pages != 0 {
				return nil, endpointInvalid(o.op)
			}
		} else if rows, err = decode(env.Data); err != nil {
			return nil, err
		}
		expected := size
		if page == pages {
			expected = total - size*(pages-1)
		}
		if total == 0 {
			expected = 0
		}
		if len(rows) != expected {
			return nil, endpointInvalid(o.op)
		}
		for _, row := range rows {
			key := id(row)
			if core.CheckPathID(o.op, "inventory ID", key) != nil || seen[key] {
				return nil, endpointInvalid(o.op)
			}
			seen[key] = true
			items = append(items, row)
		}
		if len(items) > total {
			return nil, endpointInvalid(o.op)
		}
		if page >= pages {
			if len(items) != total {
				return nil, endpointInvalid(o.op)
			}
			return items, nil
		}
	}
	return nil, endpointInvalid(o.op)
}

func appendDistinct(dst []string, add ...string) []string {
	for _, value := range add {
		if value != "" && !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}
