package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const policyPath = bucketsPath + "/policy"

// validPolicy is the smallest document PutBucketPolicy accepts. It holds
// characters the JSON string encoding may escape.
const validPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::my-bucket/*&<>"}]}`

func getPolicy(c *Client) (*GetBucketPolicyOutput, error) {
	return c.GetBucketPolicy(context.Background(), &GetBucketPolicyInput{ProjectID: "proj-1", Bucket: "my-bucket"})
}

func putPolicy(c *Client, policy string) error {
	_, err := c.PutBucketPolicy(context.Background(), &PutBucketPolicyInput{ProjectID: "proj-1", Bucket: "my-bucket", Policy: policy})
	return err
}

func deletePolicy(c *Client) error {
	_, err := c.DeleteBucketPolicy(context.Background(), &DeleteBucketPolicyInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	return err
}

func TestGetBucketPolicyRequestAndFixture(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "get_bucket_policy.json")}
	out, err := getPolicy(newTestClient(t, s.handler(t)))
	if err != nil {
		t.Fatal(err)
	}
	got := s.seen()
	if got.method != http.MethodGet || got.path != policyPath || got.query != "" || got.body != "" {
		t.Fatalf("request = %+v", got)
	}
	var doc struct {
		Version   string
		Statement []struct{ Sid string }
	}
	if err := json.Unmarshal([]byte(out.Policy), &doc); err != nil {
		t.Fatalf("Policy is not JSON: %v", err)
	}
	if doc.Version != "2012-10-17" || len(doc.Statement) != 2 || doc.Statement[0].Sid != "Bucket" {
		t.Fatalf("policy = %s", out.Policy)
	}
	var env struct{ Data string }
	if err := json.Unmarshal([]byte(s.body), &env); err != nil || out.Policy != env.Data {
		t.Fatalf("Policy differs from the response's data string (err %v)", err)
	}
}

func TestGetBucketPolicyNoPolicyIsEmpty(t *testing.T) {
	for name, body := range map[string]string{
		"fixture":   fixture(t, "get_bucket_policy_none.json"),
		"null data": `{"code":200,"success":true,"data":null}`,
		"empty":     `{"code":200,"success":true,"data":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := &keyServer{status: 200, body: body}
			out, err := getPolicy(newTestClient(t, s.handler(t)))
			if err != nil || out == nil || out.Policy != "" {
				t.Fatalf("out = %+v, err = %v, want an empty Policy and no error", out, err)
			}
		})
	}
}

func TestGetBucketPolicyDataThatIsNotAString(t *testing.T) {
	for _, data := range []string{`true`, `{"Version":"2012-10-17"}`, `["x"]`, `1`} {
		s := &keyServer{status: 200, body: `{"code":200,"success":true,"data":` + data + `}`}
		out, err := getPolicy(newTestClient(t, s.handler(t)))
		var apiErr *vngcloud.APIError
		if out != nil || !errors.As(err, &apiErr) || apiErr.Operation != "storage.GetBucketPolicy" {
			t.Fatalf("data %s: out = %+v, err = %v, want a decode error", data, out, err)
		}
		if strings.Contains(err.Error(), "2012") {
			t.Fatalf("data %s: the error quotes the response: %v", data, err)
		}
	}
}

// TestGetBucketPolicyEmptyBody covers the live answer for a bucket that does
// not exist: HTTP 200 with no body.
func TestGetBucketPolicyEmptyBody(t *testing.T) {
	s := &keyServer{status: 200, body: ""}
	_, err := getPolicy(newTestClient(t, s.handler(t)))
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "EmptyResponse" {
		t.Fatalf("err = %v, want EmptyResponse", err)
	}
	if strings.Contains(apiErr.Message, "may have happened") {
		t.Fatalf("a read says a change may have happened: %q", apiErr.Message)
	}
}

func TestPutBucketPolicyRequestAndFixture(t *testing.T) {
	for _, status := range []int{200, 204} {
		s := &keyServer{status: status, body: fixture(t, "put_bucket_policy.json")}
		if status == 204 {
			s.body = ""
		}
		out, err := newTestClient(t, s.handler(t)).PutBucketPolicy(context.Background(),
			&PutBucketPolicyInput{ProjectID: "proj-1", Bucket: "my-bucket", Policy: validPolicy})
		if err != nil || out == nil {
			t.Fatalf("status %d: out = %+v, err = %v", status, out, err)
		}
		got := s.seen()
		if got.method != http.MethodPut || got.path != policyPath || got.query != "" || !strings.HasPrefix(got.ctype, "application/json") {
			t.Fatalf("status %d: request = %+v", status, got)
		}
		var body map[string]string
		if err := json.Unmarshal([]byte(got.body), &body); err != nil || len(body) != 1 || body["policy"] != validPolicy {
			t.Fatalf("status %d: body = %s, want one key policy holding the text unchanged", status, got.body)
		}
	}
}

func TestDeleteBucketPolicyRequestAndFixture(t *testing.T) {
	for _, status := range []int{200, 204} {
		s := &keyServer{status: status, body: fixture(t, "delete_bucket_policy.json")}
		if status == 204 {
			s.body = ""
		}
		out, err := newTestClient(t, s.handler(t)).DeleteBucketPolicy(context.Background(),
			&DeleteBucketPolicyInput{ProjectID: "proj-1", Bucket: "my-bucket"})
		if err != nil || out == nil {
			t.Fatalf("status %d: out = %+v, err = %v", status, out, err)
		}
		got := s.seen()
		if got.method != http.MethodDelete || got.path != policyPath || got.query != "" || got.body != "" {
			t.Fatalf("status %d: request = %+v", status, got)
		}
	}
}

func TestDeleteBucketPolicyRepeatSucceeds(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "delete_bucket_policy.json")}
	c := newTestClient(t, s.handler(t))
	for i := 0; i < 2; i++ {
		if err := deletePolicy(c); err != nil {
			t.Fatalf("delete %d: %v", i+1, err)
		}
	}
}

func TestBucketPolicyRegion(t *testing.T) {
	var headers []string
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("region"), r.Header.Get("region_id"))
		_, _ = w.Write([]byte(okEnvelope))
	})
	c := newTestClient(t, h)
	ctx := context.Background()
	if _, err := c.GetBucketPolicy(ctx, &GetBucketPolicyInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutBucketPolicy(ctx, &PutBucketPolicyInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket", Policy: validPolicy}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteBucketPolicy(ctx, &DeleteBucketPolicyInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"<region-id-1>", "<region-id-1>", "<region-id-1>", "<region-id-1>", "<region-id-1>", "<region-id-1>"}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("region headers = %v, want %v", headers, want)
	}
}

func TestPutBucketPolicyRefusesBadPolicyBeforeAnyRequest(t *testing.T) {
	bad := map[string]string{
		"empty":                "",
		"spaces":               "  \n",
		"not JSON":             `{"Statement":[`,
		"JSON array":           `[{"Effect":"Allow"}]`,
		"JSON string":          `"{}"`,
		"JSON null":            `null`,
		"JSON number":          `1`,
		"no Statement":         `{"Version":"2012-10-17"}`,
		"empty object":         `{}`,
		"empty Statement":      `{"Version":"2012-10-17","Statement":[]}`,
		"Statement is object":  `{"Statement":{"Effect":"Allow"}}`,
		"Statement is string":  `{"Statement":"x"}`,
		"Statement is null":    `{"Statement":null}`,
		"lower case statement": `{"statement":[{"Effect":"Allow"}]}`,
		"trailing text":        validPolicy + ` x`,
	}
	for name, policy := range bad {
		t.Run(name, func(t *testing.T) {
			var sent atomic.Int32
			c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
			err := putPolicy(c, policy)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if sent.Load() != 0 {
				t.Fatalf("%d request(s) sent, want 0", sent.Load())
			}
			if doc := strings.TrimSpace(policy); doc != "" && strings.Contains(err.Error(), doc) {
				t.Fatalf("the error quotes the policy: %v", err)
			}
		})
	}
}

func TestPutBucketPolicyRefusesIncompleteStatements(t *testing.T) {
	const (
		eff = `"Effect":"Allow"`
		pr  = `"Principal":"*"`
		act = `"Action":"s3:GetObject"`
		res = `"Resource":"arn:aws:s3:::b/*"`
	)
	tests := []struct {
		name      string
		statement string
		field     string
	}{
		{"empty statement", `{}`, "Effect"},
		{"no Principal", `{` + eff + `,` + act + `,` + res + `}`, "Principal"},
		{"empty Principal object", `{` + eff + `,"Principal":{},` + act + `,` + res + `}`, "Principal"},
		{"empty AWS string", `{` + eff + `,"Principal":{"AWS":""},` + act + `,` + res + `}`, "Principal"},
		{"empty AWS list", `{` + eff + `,"Principal":{"AWS":[]},` + act + `,` + res + `}`, "Principal"},
		{"AWS list with empty string", `{` + eff + `,"Principal":{"AWS":[""]},` + act + `,` + res + `}`, "Principal"},
		{"AWS is a number", `{` + eff + `,"Principal":{"AWS":1},` + act + `,` + res + `}`, "Principal"},
		{"empty Principal string", `{` + eff + `,"Principal":"",` + act + `,` + res + `}`, "Principal"},
		{"Principal is null", `{` + eff + `,"Principal":null,` + act + `,` + res + `}`, "Principal"},
		{"Principal is a number", `{` + eff + `,"Principal":1,` + act + `,` + res + `}`, "Principal"},
		{"no Action", `{` + eff + `,` + pr + `,` + res + `}`, "Action"},
		{"empty Action list", `{` + eff + `,` + pr + `,"Action":[],` + res + `}`, "Action"},
		{"Action list with empty string", `{` + eff + `,` + pr + `,"Action":[""],` + res + `}`, "Action"},
		{"empty Action string", `{` + eff + `,` + pr + `,"Action":"",` + res + `}`, "Action"},
		{"no Resource", `{` + eff + `,` + pr + `,` + act + `}`, "Resource"},
		{"empty Resource list", `{` + eff + `,` + pr + `,` + act + `,"Resource":[]}`, "Resource"},
		{"no Effect", `{` + pr + `,` + act + `,` + res + `}`, "Effect"},
		{"empty Effect", `{"Effect":"",` + pr + `,` + act + `,` + res + `}`, "Effect"},
		{"Effect is not a string", `{"Effect":true,` + pr + `,` + act + `,` + res + `}`, "Effect"},
		{"lower case principal", `{` + eff + `,"principal":"*",` + act + `,` + res + `}`, "Principal"},
		{"statement is a string", `"x"`, "JSON object"},
		{"statement is null", `null`, "JSON object"},
		{"statement is an array", `[]`, "JSON object"},
	}
	good := `{` + eff + `,` + pr + `,` + act + `,` + res + `}`
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent atomic.Int32
			c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
			// The bad statement is second, so the message must name index 1.
			err := putPolicy(c, `{"Statement":[`+good+`,`+tt.statement+`]}`)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), "Statement[1]") || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("err = %v, want it to name Statement[1] and %s", err, tt.field)
			}
			if sent.Load() != 0 {
				t.Fatalf("%d request(s) sent, want 0", sent.Load())
			}
			if strings.Contains(err.Error(), "arn:") || strings.Contains(err.Error(), "s3:GetObject") {
				t.Fatalf("the error quotes the policy: %v", err)
			}
		})
	}
}

func TestPutBucketPolicyAcceptsValidDocuments(t *testing.T) {
	for name, policy := range map[string]string{
		"minimal":              validPolicy,
		"padded":               "\n " + validPolicy + " \n",
		"extra members":        `{"Id":"x","Statement":[{"Effect":"Deny","Principal":"*","Action":"s3:*","Resource":"*"}],"Other":[1]}`,
		"AWS principal list":   `{"Statement":[{"Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/u:sa-n"]},"Action":["s3:GetObject"],"Resource":["arn:aws:s3:::b/*"]}]}`,
		"AWS principal string": `{"Statement":[{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam:::user/u:sa-n"},"Action":"s3:GetObject","Resource":"arn:aws:s3:::b/*"}]}`,
		"template-shaped":      `{"Version":"2012-10-17","Statement":[{"Sid":"Bucket","Effect":"Allow","Principal":{"AWS":["arn:aws:iam:::user/u:sa-n"]},"Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::b"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := &keyServer{status: 200, body: okEnvelope}
			if err := putPolicy(newTestClient(t, s.handler(t)), policy); err != nil {
				t.Fatal(err)
			}
			var body struct{ Policy string }
			if err := json.Unmarshal([]byte(s.seen().body), &body); err != nil || body.Policy != policy {
				t.Fatalf("sent policy differs from the input (err %v)", err)
			}
		})
	}
}

func TestBucketPolicyPathRejection(t *testing.T) {
	for _, v := range []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb"} {
		var sent atomic.Int32
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
		ctx := context.Background()
		_, e1 := c.GetBucketPolicy(ctx, &GetBucketPolicyInput{ProjectID: v, Bucket: "my-bucket"})
		_, e2 := c.GetBucketPolicy(ctx, &GetBucketPolicyInput{ProjectID: "proj-1", Bucket: v})
		_, e3 := c.PutBucketPolicy(ctx, &PutBucketPolicyInput{ProjectID: v, Bucket: "my-bucket", Policy: validPolicy})
		_, e4 := c.PutBucketPolicy(ctx, &PutBucketPolicyInput{ProjectID: "proj-1", Bucket: v, Policy: validPolicy})
		_, e5 := c.DeleteBucketPolicy(ctx, &DeleteBucketPolicyInput{ProjectID: v, Bucket: "my-bucket"})
		_, e6 := c.DeleteBucketPolicy(ctx, &DeleteBucketPolicyInput{ProjectID: "proj-1", Bucket: v})
		for i, err := range []error{e1, e2, e3, e4, e5, e6} {
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("value %q call %d: err = %v, want ErrInvalidInput", v, i, err)
			}
		}
		if sent.Load() != 0 {
			t.Fatalf("value %q: %d request(s) sent, want 0", v, sent.Load())
		}
	}
}

func TestBucketPolicyStatuses(t *testing.T) {
	ops := map[string]func(*Client) error{
		"get":    func(c *Client) error { _, err := getPolicy(c); return err },
		"put":    func(c *Client) error { return putPolicy(c, validPolicy) },
		"delete": deletePolicy,
	}
	tests := []struct {
		status  int
		wantErr bool
		want    error
	}{
		{http.StatusOK, false, nil},
		{http.StatusBadRequest, true, nil},
		{http.StatusForbidden, true, vngcloud.ErrPermission},
		{http.StatusNotFound, true, vngcloud.ErrNotFound},
		{http.StatusInternalServerError, true, nil},
		{http.StatusBadGateway, true, nil},
	}
	for name, op := range ops {
		for _, tt := range tests {
			t.Run(name+"/"+http.StatusText(tt.status), func(t *testing.T) {
				s := &keyServer{status: tt.status, body: okEnvelope}
				if tt.wantErr {
					s.body = `{"message":"x"}`
				}
				err := op(newTestClient(t, s.handler(t)))
				if !tt.wantErr {
					if err != nil {
						t.Fatal(err)
					}
					return
				}
				var apiErr *vngcloud.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
					t.Fatalf("err = %v, want *APIError with status %d", err, tt.status)
				}
				if tt.want != nil && !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
			})
		}
	}
}

func TestBucketPolicyEnvelopeCodes(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		op       func(*Client) error
		code     string
		message  string
		sentinel error
	}{
		{"put code 400", fixture(t, "error_policy_parser.json"), func(c *Client) error { return putPolicy(c, validPolicy) },
			"400", "At character offset 23, `1999-01-01` is not a valid version. Valid versions are `2008-10-17` and `2012-10-17`.", nil},
		{"put code 114", fixture(t, "error_policy_empty.json"), func(c *Client) error { return putPolicy(c, validPolicy) },
			"114", "Error occurred when updating bucket policy.", nil},
		{"get code 404", fixture(t, "error_envelope_not_found.json"), func(c *Client) error { _, err := getPolicy(c); return err },
			"404", "<message>", vngcloud.ErrNotFound},
		{"delete code 404", fixture(t, "error_envelope_not_found.json"), deletePolicy, "404", "<message>", vngcloud.ErrNotFound},
		{"put code 112", `{"code":112,"success":false,"errorMsg":"bad input"}`, func(c *Client) error { return putPolicy(c, validPolicy) },
			"112", "bad input", vngcloud.ErrInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &keyServer{status: 200, body: tt.body}
			err := tt.op(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tt.code || apiErr.Message != tt.message || apiErr.StatusCode != 200 {
				t.Fatalf("err = %v, want code %s with message %q", err, tt.code, tt.message)
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Fatalf("err = %v, want %v", err, tt.sentinel)
			}
			if tt.sentinel == nil && (vngcloud.IsNotFound(err) || errors.Is(err, vngcloud.ErrInvalidInput) || apiErr.Retryable) {
				t.Fatalf("err = %v matched a sentinel or is retryable", err)
			}
		})
	}
}

func TestBucketPolicyWritesKeepTheTransportRetries(t *testing.T) {
	for name, op := range map[string]func(*Client) error{
		"put":    func(c *Client) error { return putPolicy(c, validPolicy) },
		"delete": deletePolicy,
	} {
		t.Run(name, func(t *testing.T) {
			var calls atomic.Int32
			h := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				_, _ = w.Write([]byte(okEnvelope))
			})
			if err := op(New(testutil.NewRetryConfig(t, h))); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 {
				t.Fatalf("calls = %d, want a retry after the 502", calls.Load())
			}
		})
	}
}
