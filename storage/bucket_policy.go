package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"danny.vn/vngcloud/internal/core"
)

type GetBucketPolicyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
}

type GetBucketPolicyOutput struct {
	// Policy is the bucket policy document as the server stores it, or ""
	// when the bucket has none.
	Policy string
}

// GetBucketPolicy returns a bucket's policy, or an empty Policy and no error
// when the bucket has none. A bucket with no policy is a normal state, so it
// is not ErrNotFound; a missing bucket is. The server answers a missing
// bucket with an empty body, so the call reads the bucket once to tell.
//
// Policy is the server's string, returned unchanged. The server may change
// whitespace and key order, so compare decoded documents, not strings.
func (c *Client) GetBucketPolicy(ctx context.Context, in *GetBucketPolicyInput) (*GetBucketPolicyOutput, error) {
	const op = "storage.GetBucketPolicy"
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
		url:      c.route(policyParts(in.ProjectID, in.Bucket), nil),
		regionID: id,
		ok:       []int{http.StatusOK},
	}, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return &GetBucketPolicyOutput{}, nil
	}
	var policy string
	if err := json.Unmarshal(env.Data, &policy); err != nil {
		return nil, &core.APIError{
			Operation:  op,
			StatusCode: http.StatusOK,
			Code:       "InvalidResponse",
			Message:    "the policy in the response is not a JSON string",
		}
	}
	return &GetBucketPolicyOutput{Policy: policy}, nil
}

type PutBucketPolicyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
	// Policy is the policy document as JSON text. It must be a JSON object
	// whose Statement member is an array of complete statements; see
	// PutBucketPolicy.
	Policy string `vngcloud:"required"`
}

type PutBucketPolicyOutput struct{}

type putBucketPolicyBody struct {
	Policy string `json:"policy"`
}

// PutBucketPolicy sets a bucket's policy, replacing any policy it has. The
// text is sent unchanged as a JSON string. To remove every statement, call
// DeleteBucketPolicy.
//
// A Policy that is not a JSON object with a non-empty Statement array is
// ErrInvalidInput, and nothing is sent. So is a statement that is not a JSON
// object, or lacks a non-empty Effect string, Principal, Action, or Resource.
// The server accepts a statement with no Principal, and the bucket's console
// calls and bucket delete then fail until the policy is removed through the
// S3 data plane. A statement with only NotPrincipal is refused for that
// reason. A member name repeated in the document, a statement, or a Principal
// is also ErrInvalidInput.
//
// A Principal of "*" grants anonymous access when the put succeeds. This call
// does not ask for consent; PolicyHasPublicPrincipal tells a caller whether
// to.
//
// The server refuses a document it cannot parse as an *APIError with code 400
// and the parser's message, and an empty one with code 114; neither matches a
// sentinel.
//
// The server does not check principals. A policy that names a sub-user that
// does not exist, a mistyped ARN, or a deleted service account is accepted
// and grants nothing, and this call cannot detect it. Copy PrincipalARN from
// EnsureServiceAccountPrincipal, and check access with the attached key. The
// data plane follows a put within about a second.
//
// A put with the same Policy gives the same result, so the call keeps the
// transport's retries.
func (c *Client) PutBucketPolicy(ctx context.Context, in *PutBucketPolicyInput) (*PutBucketPolicyOutput, error) {
	const op = "storage.PutBucketPolicy"
	if err := core.CheckRequired(op, in); err != nil {
		return nil, err
	}
	if err := checkPolicyDocument(op, in.Policy); err != nil {
		return nil, err
	}
	id, err := c.checkBucketPaths(ctx, op, in.Region, in.ProjectID, in.Bucket)
	if err != nil {
		return nil, err
	}
	if _, err := c.exchangeBucket(ctx, call{
		op:       op,
		method:   http.MethodPut,
		url:      c.route(policyParts(in.ProjectID, in.Bucket), nil),
		regionID: id,
		body:     putBucketPolicyBody{Policy: in.Policy},
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}, in.Region, in.ProjectID, in.Bucket); err != nil {
		return nil, err
	}
	return &PutBucketPolicyOutput{}, nil
}

type DeleteBucketPolicyInput struct {
	// Region is the vStorage region name; see ListBucketsInput.
	Region    string
	ProjectID string `vngcloud:"required"`
	Bucket    string `vngcloud:"required"`
}

type DeleteBucketPolicyOutput struct{}

// DeleteBucketPolicy removes a bucket's policy. A bucket with no policy also
// succeeds, so a repeat delete returns the same result and the call keeps the
// transport's retries. After it, a key attached to a service account has no
// rights in the bucket. The data plane follows within about a second.
func (c *Client) DeleteBucketPolicy(ctx context.Context, in *DeleteBucketPolicyInput) (*DeleteBucketPolicyOutput, error) {
	const op = "storage.DeleteBucketPolicy"
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
		url:      c.route(policyParts(in.ProjectID, in.Bucket), nil),
		regionID: id,
		ok:       []int{http.StatusOK, http.StatusNoContent},
		write:    true,
	}, in.Region, in.ProjectID, in.Bucket); err != nil {
		return nil, err
	}
	return &DeleteBucketPolicyOutput{}, nil
}

func policyParts(project, bucket string) []string {
	return []string{"ceph", "projects", project, "buckets", bucket, "policy"}
}

// checkPolicyDocument requires a JSON object with a non-empty Statement
// array. The error never quotes the document, which names principals. The
// member name must match exactly: the server reads "Statement", and a
// case-folding decoder would accept "statement". A repeated member name is
// refused at the top level, in each statement, and in each Principal: Go
// keeps the last occurrence and the server's parser reads every one, so a
// document could look private here and public there.
func checkPolicyDocument(op, policy string) error {
	refuse := func(why string) error {
		return fmt.Errorf("%w: %s requires Policy to be a JSON object with a non-empty Statement array (%s)", core.ErrInvalidInput, op, why)
	}
	raw := bytes.TrimSpace([]byte(policy))
	if len(raw) == 0 {
		return refuse("it is empty")
	}
	if !json.Valid(raw) {
		return refuse("it is not valid JSON")
	}
	if raw[0] != '{' {
		return refuse("it is not a JSON object")
	}
	members, err := decodeObject(raw)
	if errors.Is(err, errRepeatedMember) {
		return refuse("it repeats a member name")
	}
	if err != nil {
		return refuse("it is not a JSON object")
	}
	statements := bytes.TrimSpace(members["Statement"])
	if len(statements) == 0 || statements[0] != '[' {
		return refuse("Statement is missing or not an array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(statements, &items); err != nil || len(items) == 0 {
		return refuse("Statement is empty")
	}
	for i, item := range items {
		if field, why := checkStatement(item); field != "" {
			return fmt.Errorf("%w: %s requires every statement to be complete (Statement[%d]: %s %s)", core.ErrInvalidInput, op, i, field, why)
		}
	}
	return nil
}

var errRepeatedMember = errors.New("repeated member name")

// decodeObject returns the members of one JSON object, each as raw JSON. It
// returns errRepeatedMember when a name appears twice, and another error when
// raw is not exactly one object.
func decodeObject(raw []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	members := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := tok.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if _, seen := members[name]; seen {
			return nil, errRepeatedMember
		}
		members[name] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("not a JSON object")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data")
	}
	return members, nil
}

// checkStatement returns the first field of one statement that is missing or
// empty, with the reason, or "" when the statement is complete. The server
// accepts a statement with no Principal, and the bucket's console calls and
// bucket delete then fail until the policy is removed through the S3 data
// plane, so the check is stricter than the server's. A statement with
// NotPrincipal and no Principal is refused for the same reason.
func checkStatement(raw json.RawMessage) (field, why string) {
	members, err := decodeObject(raw)
	if errors.Is(err, errRepeatedMember) {
		return "statement", "repeats a member name"
	}
	if err != nil || members == nil {
		return "statement", "is not a JSON object"
	}
	var effect string
	if err := json.Unmarshal(members["Effect"], &effect); err != nil || strings.TrimSpace(effect) == "" {
		return "Effect", "is missing or not a non-empty string"
	}
	if why := principalProblem(members["Principal"]); why != "" {
		return "Principal", why
	}
	for _, name := range []string{"Action", "Resource"} {
		if !nonEmptyStrings(members[name]) {
			return name, "is missing or empty"
		}
	}
	return "", ""
}

// principalProblem returns why raw is not a usable Principal, or "". A usable
// one is a non-blank string, or a non-empty object with no repeated name
// whose values are each non-blank strings or non-empty arrays of them.
func principalProblem(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return "is missing or empty"
		}
		return ""
	}
	members, err := decodeObject(raw)
	if errors.Is(err, errRepeatedMember) {
		return "repeats a member name"
	}
	if err != nil || len(members) == 0 {
		return "is missing or empty"
	}
	for _, v := range members {
		if !nonEmptyStrings(v) {
			return "is missing or empty"
		}
	}
	return ""
}

// nonEmptyStrings accepts a non-blank string or a non-empty array of
// non-blank strings.
func nonEmptyStrings(raw json.RawMessage) bool {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return strings.TrimSpace(text) != ""
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return false
	}
	for _, v := range list {
		if strings.TrimSpace(v) == "" {
			return false
		}
	}
	return true
}
