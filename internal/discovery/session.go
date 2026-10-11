// Package discovery bounds one catalog and quote invocation.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/transport"
)

var ErrLimit = errors.New("discovery limit exceeded")

// Session belongs to one serial invocation, never to a shared client.
type Session struct {
	ctx      context.Context
	cancel   context.CancelFunc
	max      int
	requests int
	terminal error
	rows     int
	last     time.Time
	now      func() time.Time
	sleep    func(context.Context, time.Duration) error
	cache    map[string]cachedResponse
}

type cachedResponse struct {
	status int
	raw    json.RawMessage
}

type testClockKey struct{}

type testClock struct {
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// WithTestClock injects per-invocation time without a public SDK option.
func WithTestClock(ctx context.Context, now func() time.Time, sleeper func(context.Context, time.Duration) error) context.Context {
	return context.WithValue(ctx, testClockKey{}, testClock{now: now, sleep: sleeper})
}

type Exchanger interface {
	WrapDiscoveryError(error) error
	DoDiscoveryJSONStatus(context.Context, transport.Request, any, func(context.Context) error) (int, error)
}

func New(ctx context.Context, maxRequests int) (*Session, error) {
	if maxRequests == 0 {
		maxRequests = 500
	}
	if maxRequests < 1 || maxRequests > 2000 {
		return nil, errors.New("MaxRequests must be between 1 and 2000")
	}
	child, cancel := context.WithTimeout(ctx, 10*time.Minute)
	s := &Session{ctx: transport.NewDiscoveryContext(child), cancel: cancel, max: maxRequests, now: time.Now, sleep: sleep, cache: make(map[string]cachedResponse)}
	if clock, ok := ctx.Value(testClockKey{}).(testClock); ok {
		s.now, s.sleep = clock.now, clock.sleep
	}
	return s, nil
}

func (s *Session) Close()                   { s.cancel() }
func (s *Session) Context() context.Context { return s.ctx }
func (s *Session) MaxRequests() int         { return s.max }
func (s *Session) Requests() int            { return s.requests }
func (s *Session) CompletedAt() time.Time   { return s.now().UTC() }

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func (s *Session) BeforeAttempt(ctx context.Context) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.last.IsZero() {
		if wait := 500*time.Millisecond - s.now().Sub(s.last); wait > 0 {
			if err := s.sleep(ctx, wait); err != nil {
				return err
			}
		}
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.last = s.now()
	return nil
}

// Preflight refuses the entire quote phase when its distinct calls cannot fit.
func (s *Session) Preflight(calls int) error {
	if s.terminal != nil {
		return s.terminal
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if calls < 0 || calls > s.max-s.requests {
		return fmt.Errorf("%w: requires %d requests, maximum %d", ErrLimit, s.requests+calls, s.max)
	}
	return nil
}

func (s *Session) AddRows(n int) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	if n < 0 || n > 10000-s.rows {
		return fmt.Errorf("%w: maximum 10000 rows", ErrLimit)
	}
	s.rows += n
	return nil
}

// DoJSON memoizes only successful decoded responses. Validators run before
// memoization and must validate catalog or quote semantics before rows escape.
func (s *Session) DoJSON(c Exchanger, req transport.Request, out any, validators ...func(int, json.RawMessage) error) (int, error) {
	if s.terminal != nil {
		return 0, s.terminal
	}
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if err := Allowed(req); err != nil {
		return 0, err
	}
	body, err := json.Marshal(req.Body)
	if err != nil {
		return 0, err
	}
	headers, err := json.Marshal(req.Headers)
	if err != nil {
		return 0, err
	}
	key := req.Method + " " + req.URL + " " + string(body) + " " + string(headers)
	if cached, ok := s.cache[key]; ok {
		err := s.decode(c, req.Operation, cached.status, cached.raw, out, validators)
		return cached.status, s.failure(c, cached.status, err)
	}
	if err := s.Preflight(1); err != nil {
		return 0, err
	}
	s.requests++
	req.Idempotent = true
	var raw json.RawMessage
	status, err := c.DoDiscoveryJSONStatus(s.ctx, req, &raw, s.BeforeAttempt)
	if err != nil {
		return status, s.failure(c, status, err)
	}
	if err := s.ctx.Err(); err != nil {
		return status, err
	}
	if err := s.decode(c, req.Operation, status, raw, out, validators); err != nil {
		return status, s.failure(c, status, err)
	}
	s.cache[key] = cachedResponse{status: status, raw: append(json.RawMessage(nil), raw...)}
	return status, nil
}

func (s *Session) failure(c Exchanger, status int, err error) error {
	if err == nil {
		return nil
	}
	fatal := status == http.StatusUnauthorized || status == http.StatusTooManyRequests || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	if classifier, ok := c.(interface{ DiscoveryFatal(error) bool }); ok {
		fatal = fatal || classifier.DiscoveryFatal(err)
	}
	if fatal {
		s.terminal = err
	}
	return err
}

func (s *Session) decode(c Exchanger, op string, status int, raw json.RawMessage, out any, validators []func(int, json.RawMessage) error) error {
	for _, validate := range validators {
		if err := validate(status, raw); err != nil {
			return err
		}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return c.WrapDiscoveryError(&transport.APIError{Operation: op, StatusCode: status, Code: "InvalidResponse", Message: "response failed to decode; body withheld"})
		}
	}
	return s.ctx.Err()
}

var catalogPath = regexp.MustCompile(`/v1/(projects|[^/]+/(zones|volume_type_zones|volumes/encryption_types|[^/]+/volume_types|images/os|flavor_zones/(codes|families|product|customs)|flavors/customs|[^/]+/flavors|flavors/families/[^/]+/platforms/[^/]+))$`)
var storagePath = regexp.MustCompile(`/(internal/v1/(regions|billing/(purchase_types|project_types))|billing-api/v1/configurations)$`)

// Allowed accepts only evidenced catalog GETs and fixed create-price POSTs.
// Endpoints and scope keys must come from trusted configuration and inputs.
func Allowed(req transport.Request) error {
	u, err := url.Parse(req.URL)
	if err != nil || u.User != nil || u.Fragment != "" || req.SkipAuth || req.APIKey != "" || req.Once || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("discovery request is outside the read scope")
	}
	for name, value := range req.Headers {
		if (name != "region" && name != "region_id") || value == "" || (!storagePath.MatchString(u.Path) && !strings.HasSuffix(u.Path, "/billing-api/v2/price")) {
			return errors.New("discovery header is outside the read scope")
		}
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return errors.New("discovery path is outside the read scope")
		}
	}
	if req.Method == http.MethodGet && req.Body == nil && (catalogPath.MatchString(u.Path) || storagePath.MatchString(u.Path)) {
		return nil
	}
	if req.Method != http.MethodPost || (!strings.HasSuffix(u.Path, "/v1/price") && !strings.HasSuffix(u.Path, "/billing-api/v2/price")) {
		return errors.New("discovery request is outside the read scope")
	}
	raw, err := json.Marshal(req.Body)
	if err != nil {
		return err
	}
	var body struct {
		ResourceType string                     `json:"resourceType"`
		Action       string                     `json:"action"`
		ResourceInfo map[string]json.RawMessage `json:"resourceInfo"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Action != "create" {
		return errors.New("discovery requires a fixed create quote")
	}
	fields := map[string]string{
		"volume":         "zoneId volumeTypeId size period isPoc encryptionType",
		"server":         "zoneId flavorId imageId rootDiskSize rootDiskTypeId period isPoc encryptionVolume",
		"public-vip":     "type isBuyMorePo isPoc",
		"object_storage": "projectType purchaseTypeId quota",
	}
	allowed, ok := fields[body.ResourceType]
	if !ok || len(body.ResourceInfo) == 0 {
		return errors.New("discovery quote type is outside the read scope")
	}
	required := map[string]string{
		"volume":         "zoneId volumeTypeId size period isPoc",
		"server":         "zoneId flavorId imageId rootDiskSize rootDiskTypeId period isPoc encryptionVolume",
		"public-vip":     "type isBuyMorePo isPoc",
		"object_storage": "projectType purchaseTypeId quota",
	}
	for _, key := range strings.Fields(required[body.ResourceType]) {
		if value, ok := body.ResourceInfo[key]; !ok || string(value) == "null" {
			return errors.New("discovery quote is missing a required field")
		}
	}
	for key, value := range body.ResourceInfo {
		if !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return errors.New("discovery quote field is outside the read scope")
		}
		switch key {
		case "period":
			if string(value) != "1" {
				return errors.New("discovery requires monthly quotes")
			}
		case "isPoc", "isBuyMorePo", "encryptionVolume":
			if string(value) != "false" {
				return errors.New("discovery quote boolean must be false")
			}
		case "type":
			var kind string
			if json.Unmarshal(value, &kind) != nil || (kind != "public" && kind != "public-vm" && kind != "public-mkp") {
				return errors.New("discovery public IP type is outside the read scope")
			}
		}
	}
	if (body.ResourceType == "object_storage") != strings.HasSuffix(u.Path, "/billing-api/v2/price") {
		return errors.New("discovery quote route disagrees with type")
	}
	return nil
}
