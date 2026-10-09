package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

// CORSRule is one cross-origin rule of a bucket.
type CORSRule struct {
	// AllowedOrigins lists the origins the rule matches. Each is non-empty
	// and holds at most one "*".
	AllowedOrigins []string
	// AllowedMethods lists GET, PUT, POST, DELETE, or HEAD, in upper case.
	// The server returns them in its own order, so compare them as a set.
	AllowedMethods []string
	// AllowedHeaders lists the request headers a preflight may ask for.
	AllowedHeaders []string
	// MaxAgeSeconds is how long a browser may cache a preflight answer. Zero
	// sends nothing.
	MaxAgeSeconds int
	// ExposedHeaders is set by the server. A get fills it and a put never
	// sends it. The server derives it itself and leaves it nil for rules put
	// through this SDK, so a browser sees no Access-Control-Expose-Headers.
	ExposedHeaders []string
}

type GetBucketCORSInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
}

type GetBucketCORSOutput struct {
	// Rules is empty, never nil, when the bucket has no rules.
	Rules []CORSRule
}

// corsGetData lists its fields in CORSRule's order, which the conversion to
// CORSRule needs.
type corsGetData struct {
	Rules []struct {
		AllowedOrigins []string `json:"allowedOrigins"`
		AllowedMethods []string `json:"allowedMethods"`
		AllowedHeaders []string `json:"allowedHeaders"`
		MaxAgeSeconds  int      `json:"maxAgeSeconds"`
		ExposedHeaders []string `json:"exposedHeaders"`
	} `json:"rules"`
}

// GetBucketCORS returns a bucket's CORS rules, or an empty Rules and no error
// when it has none. A missing bucket is ErrNotFound: the server answers it
// with an empty body, and the call then reads the bucket to tell.
func (c *Client) GetBucketCORS(ctx context.Context, in *GetBucketCORSInput) (*GetBucketCORSOutput, error) {
	const op = "storage.GetBucketCORS"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	env, err := c.exchangeBucket(ctx, call{
		op:       op,
		method:   http.MethodGet,
		url:      c.route(bucketSettingParts(in.ProjectID, in.Bucket, "cors"), nil),
		regionID: id,
		ok:       []int{http.StatusOK},
	}, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	out := &GetBucketCORSOutput{Rules: []CORSRule{}}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return out, nil
	}
	var data corsGetData
	if env.Data[0] != '{' || json.Unmarshal(env.Data, &data) != nil {
		return nil, invalidResponse(op, "the response's rules are not a list of CORS rules")
	}
	for _, r := range data.Rules {
		out.Rules = append(out.Rules, CORSRule(r))
	}
	return out, nil
}

type PutBucketCORSInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
	// Rules replaces every rule of the bucket. It is not empty.
	Rules []CORSRule `vngcloud:"required"`
}

type PutBucketCORSOutput struct{}

// corsPutRule is the body the server reads: capitalised keys, and no
// ExposeHeaders, which it ignores.
type corsPutRule struct {
	AllowedOrigins []string `json:"AllowedOrigins"`
	AllowedMethods []string `json:"AllowedMethods"`
	AllowedHeaders []string `json:"AllowedHeaders,omitempty"`
	MaxAgeSeconds  int      `json:"MaxAgeSeconds,omitempty"`
}

var corsMethods = []string{"GET", "PUT", "POST", "DELETE", "HEAD"}

// PutBucketCORS replaces every CORS rule of a bucket. Nothing is sent, and the
// error is ErrInvalidInput, for an empty Rules, a rule with no origin or no
// method, an empty origin, an origin with more than one "*", a method other
// than GET, PUT, POST, DELETE, or HEAD, or a negative MaxAgeSeconds. The server
// answers these with a generic code 114 or MalformedXML that names no rule.
// To remove every rule, call DeleteBucketCORS. ExposedHeaders is not sent.
//
// The server still owns the rest: it accepts an origin with no scheme and an
// empty AllowedHeaders entry. Its own refusals, codes 114 and 400, are an
// *APIError with the server's message and no sentinel. A failed put keeps the
// previous rules; a successful one takes effect on the next preflight. A put
// with the same Rules gives the same result, so the call keeps the transport's
// retries.
func (c *Client) PutBucketCORS(ctx context.Context, in *PutBucketCORSInput) (*PutBucketCORSOutput, error) {
	const op = "storage.PutBucketCORS"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkCORSRules(op, in.Rules); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	body := make([]corsPutRule, len(in.Rules))
	for i, r := range in.Rules {
		body[i] = corsPutRule{
			AllowedOrigins: r.AllowedOrigins,
			AllowedMethods: r.AllowedMethods,
			AllowedHeaders: r.AllowedHeaders,
			MaxAgeSeconds:  r.MaxAgeSeconds,
		}
	}
	if _, err := c.exchangeBucket(ctx, call{
		op:       op,
		method:   http.MethodPut,
		url:      c.route(bucketSettingParts(in.ProjectID, in.Bucket, "cors"), nil),
		regionID: id,
		body:     body,
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}, in.Region, in.ProjectID, in.Bucket); err != nil {
		return nil, err
	}
	return &PutBucketCORSOutput{}, nil
}

func checkCORSRules(op string, rules []CORSRule) error {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s requires %s", core.ErrInvalidInput, op, fmt.Sprintf(format, args...))
	}
	if len(rules) == 0 {
		return refuse("Rules to hold at least one rule; call DeleteBucketCORS to remove every rule")
	}
	for i, r := range rules {
		if len(r.AllowedOrigins) == 0 {
			return refuse("Rules[%d].AllowedOrigins to hold at least one origin", i)
		}
		for j, origin := range r.AllowedOrigins {
			if origin == "" {
				return refuse("Rules[%d].AllowedOrigins[%d] to be non-empty", i, j)
			}
			if strings.Count(origin, "*") > 1 {
				return refuse("Rules[%d].AllowedOrigins[%d] to hold at most one *", i, j)
			}
		}
		if len(r.AllowedMethods) == 0 {
			return refuse("Rules[%d].AllowedMethods to hold at least one method", i)
		}
		for j, method := range r.AllowedMethods {
			if !slices.Contains(corsMethods, method) {
				return refuse("Rules[%d].AllowedMethods[%d] to be one of %s", i, j, strings.Join(corsMethods, ", "))
			}
		}
		if r.MaxAgeSeconds < 0 {
			return refuse("Rules[%d].MaxAgeSeconds to be at least 0", i)
		}
	}
	return nil
}

type DeleteBucketCORSInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
}

type DeleteBucketCORSOutput struct{}

// DeleteBucketCORS removes every CORS rule of a bucket. A bucket with none
// also succeeds, so a repeat gives the same result and the call keeps the
// transport's retries. A browser's next preflight is refused.
func (c *Client) DeleteBucketCORS(ctx context.Context, in *DeleteBucketCORSInput) (*DeleteBucketCORSOutput, error) {
	const op = "storage.DeleteBucketCORS"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	if _, err := c.exchangeBucket(ctx, call{
		op:       op,
		method:   http.MethodDelete,
		url:      c.route(bucketSettingParts(in.ProjectID, in.Bucket, "cors"), nil),
		regionID: id,
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}, in.Region, in.ProjectID, in.Bucket); err != nil {
		return nil, err
	}
	return &DeleteBucketCORSOutput{}, nil
}
