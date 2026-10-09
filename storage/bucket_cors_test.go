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

const corsPath = bucketsPath + "/cors"

func getCORS(c *Client) (*GetBucketCORSOutput, error) {
	return c.GetBucketCORS(context.Background(), &GetBucketCORSInput{ProjectID: "proj-1", Bucket: "my-bucket"})
}

func putCORS(c *Client, rules ...CORSRule) error {
	_, err := c.PutBucketCORS(context.Background(), &PutBucketCORSInput{ProjectID: "proj-1", Bucket: "my-bucket", Rules: rules})
	return err
}

func deleteCORS(c *Client) error {
	_, err := c.DeleteBucketCORS(context.Background(), &DeleteBucketCORSInput{ProjectID: "proj-1", Bucket: "my-bucket"})
	return err
}

func goodRule() CORSRule {
	return CORSRule{AllowedOrigins: []string{"https://app.example.com"}, AllowedMethods: []string{"GET", "PUT"}}
}

func TestGetBucketCORSFixture(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "get_bucket_cors.json")}
	out, err := getCORS(newTestClient(t, s.handler(t)))
	if err != nil {
		t.Fatal(err)
	}
	got := s.seen()
	if got.method != http.MethodGet || got.path != corsPath || got.query != "" || got.body != "" {
		t.Fatalf("request = %+v", got)
	}
	want := []CORSRule{{
		AllowedOrigins: []string{"https://app.example.com"},
		AllowedMethods: []string{"GET", "PUT"},
		AllowedHeaders: []string{"x-amz-meta-test"},
		MaxAgeSeconds:  600,
	}}
	if !reflect.DeepEqual(out.Rules, want) {
		t.Fatalf("Rules = %+v, want %+v", out.Rules, want)
	}
}

func TestGetBucketCORSWithNoRulesIsAnEmptyList(t *testing.T) {
	for name, body := range map[string]string{
		"fixture":    fixture(t, "get_bucket_cors_none.json"),
		"null data":  `{"code":200,"success":true,"data":null}`,
		"null rules": `{"code":200,"success":true,"data":{"rules":null}}`,
		"no rules":   `{"code":200,"success":true,"data":{"rules":[]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := &keyServer{status: 200, body: body}
			out, err := getCORS(newTestClient(t, s.handler(t)))
			if err != nil {
				t.Fatal(err)
			}
			if out.Rules == nil || len(out.Rules) != 0 {
				t.Fatalf("Rules = %#v, want an empty non-nil slice", out.Rules)
			}
			b, _ := json.Marshal(out)
			if string(b) != `{"Rules":[]}` {
				t.Fatalf("JSON = %s, want {\"Rules\":[]}", b)
			}
		})
	}
}

func TestGetBucketCORSFillsExposedHeaders(t *testing.T) {
	s := &keyServer{status: 200, body: `{"code":200,"success":true,"data":{"rules":[{"id":null,"allowedOrigins":["*"],"allowedMethods":["GET"],"allowedHeaders":["x-a"],"exposedHeaders":["x-a"],"maxAgeSeconds":0}]}}`}
	out, err := getCORS(newTestClient(t, s.handler(t)))
	if err != nil || len(out.Rules) != 1 || !reflect.DeepEqual(out.Rules[0].ExposedHeaders, []string{"x-a"}) {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestGetBucketCORSNullHeadersStayNil(t *testing.T) {
	s := &keyServer{status: 200, body: `{"code":200,"success":true,"data":{"rules":[{"id":null,"allowedOrigins":["*"],"allowedMethods":["GET"],"allowedHeaders":null,"exposedHeaders":null,"maxAgeSeconds":0}]}}`}
	out, err := getCORS(newTestClient(t, s.handler(t)))
	if err != nil || len(out.Rules) != 1 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
	if r := out.Rules[0]; r.AllowedHeaders != nil || r.ExposedHeaders != nil || r.MaxAgeSeconds != 0 {
		t.Fatalf("rule = %+v", r)
	}
}

func TestGetBucketCORSDataThatIsNotRules(t *testing.T) {
	for _, data := range []string{`true`, `"x"`, `[1]`, `{"rules":{"a":1}}`, `{"rules":[{"maxAgeSeconds":"x"}]}`} {
		s := &keyServer{status: 200, body: `{"code":200,"success":true,"data":` + data + `}`}
		out, err := getCORS(newTestClient(t, s.handler(t)))
		var apiErr *vngcloud.APIError
		if out != nil || !errors.As(err, &apiErr) || apiErr.Operation != "storage.GetBucketCORS" || apiErr.Code != "InvalidResponse" {
			t.Fatalf("data %s: out = %+v, err = %v, want an InvalidResponse error", data, out, err)
		}
	}
}

func TestPutBucketCORSBody(t *testing.T) {
	tests := []struct {
		name  string
		rules []CORSRule
		body  string
	}{
		{"minimal", []CORSRule{goodRule()},
			`[{"AllowedOrigins":["https://app.example.com"],"AllowedMethods":["GET","PUT"]}]`},
		{"all fields", []CORSRule{{
			AllowedOrigins: []string{"https://a.example.com", "https://*.example.org"},
			AllowedMethods: []string{"GET", "PUT", "POST", "DELETE", "HEAD"},
			AllowedHeaders: []string{"x-amz-meta-test", "content-type"},
			MaxAgeSeconds:  600,
		}}, `[{"AllowedOrigins":["https://a.example.com","https://*.example.org"],"AllowedMethods":["GET","PUT","POST","DELETE","HEAD"],"AllowedHeaders":["x-amz-meta-test","content-type"],"MaxAgeSeconds":600}]`},
		{"exposed headers are never sent", []CORSRule{{
			AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, ExposedHeaders: []string{"x-amz-id-2"},
		}}, `[{"AllowedOrigins":["*"],"AllowedMethods":["GET"]}]`},
		{"empty headers omitted", []CORSRule{{
			AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, AllowedHeaders: []string{},
		}}, `[{"AllowedOrigins":["*"],"AllowedMethods":["GET"]}]`},
		{"two rules", []CORSRule{goodRule(), {AllowedOrigins: []string{"*"}, AllowedMethods: []string{"HEAD"}}},
			`[{"AllowedOrigins":["https://app.example.com"],"AllowedMethods":["GET","PUT"]},{"AllowedOrigins":["*"],"AllowedMethods":["HEAD"]}]`},
	}
	for _, tt := range tests {
		for _, status := range []int{200, 204} {
			t.Run(tt.name, func(t *testing.T) {
				s := &keyServer{status: status, body: fixture(t, "put_bucket_cors.json")}
				if status == 204 {
					s.body = ""
				}
				if err := putCORS(newTestClient(t, s.handler(t)), tt.rules...); err != nil {
					t.Fatal(err)
				}
				got := s.seen()
				if got.method != http.MethodPut || got.path != corsPath || got.query != "" || got.body != tt.body || !strings.HasPrefix(got.ctype, "application/json") {
					t.Fatalf("request = %+v, want body %s", got, tt.body)
				}
			})
		}
	}
}

func TestPutBucketCORSRefusesBadRulesBeforeAnyRequest(t *testing.T) {
	with := func(f func(*CORSRule)) []CORSRule {
		r := goodRule()
		f(&r)
		return []CORSRule{goodRule(), r}
	}
	tests := []struct {
		name  string
		rules []CORSRule
		want  string
	}{
		{"nil list", nil, "Rules"},
		{"empty list", []CORSRule{}, "Rules"},
		{"no origins", with(func(r *CORSRule) { r.AllowedOrigins = nil }), "Rules[1].AllowedOrigins"},
		{"empty origin list", with(func(r *CORSRule) { r.AllowedOrigins = []string{} }), "Rules[1].AllowedOrigins"},
		{"empty origin", with(func(r *CORSRule) { r.AllowedOrigins = []string{"https://a.example.com", ""} }), "Rules[1].AllowedOrigins[1]"},
		{"two wildcards", with(func(r *CORSRule) { r.AllowedOrigins = []string{"https://*.*.example.com"} }), "Rules[1].AllowedOrigins[0]"},
		{"two wildcards in a row", with(func(r *CORSRule) { r.AllowedOrigins = []string{"**"} }), "Rules[1].AllowedOrigins[0]"},
		{"no methods", with(func(r *CORSRule) { r.AllowedMethods = nil }), "Rules[1].AllowedMethods"},
		{"empty method list", with(func(r *CORSRule) { r.AllowedMethods = []string{} }), "Rules[1].AllowedMethods"},
		{"lower case method", with(func(r *CORSRule) { r.AllowedMethods = []string{"GET", "get"} }), "Rules[1].AllowedMethods[1]"},
		{"OPTIONS", with(func(r *CORSRule) { r.AllowedMethods = []string{"OPTIONS"} }), "Rules[1].AllowedMethods[0]"},
		{"unknown method", with(func(r *CORSRule) { r.AllowedMethods = []string{"FOO"} }), "Rules[1].AllowedMethods[0]"},
		{"padded method", with(func(r *CORSRule) { r.AllowedMethods = []string{"GET "} }), "Rules[1].AllowedMethods[0]"},
		{"negative max age", with(func(r *CORSRule) { r.MaxAgeSeconds = -1 }), "Rules[1].MaxAgeSeconds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sent atomic.Int32
			c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
			err := putCORS(c, tt.rules...)
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("err = %v, want ErrInvalidInput", err)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to name %s", err, tt.want)
			}
			if sent.Load() != 0 {
				t.Fatalf("%d request(s) sent, want 0", sent.Load())
			}
		})
	}
}

func TestPutBucketCORSLeavesWhatTheServerOwnsToTheServer(t *testing.T) {
	for name, rule := range map[string]CORSRule{
		"origin without a scheme": {AllowedOrigins: []string{"app.example.com"}, AllowedMethods: []string{"GET"}},
		"one wildcard":            {AllowedOrigins: []string{"https://*.example.com"}, AllowedMethods: []string{"HEAD"}},
		"zero max age":            {AllowedOrigins: []string{"*"}, AllowedMethods: []string{"DELETE"}, MaxAgeSeconds: 0},
		"empty header":            {AllowedOrigins: []string{"*"}, AllowedMethods: []string{"POST"}, AllowedHeaders: []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			s := &keyServer{status: 200, body: okEnvelope}
			if err := putCORS(newTestClient(t, s.handler(t)), rule); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPutBucketCORSDoesNotChangeTheCallersRules(t *testing.T) {
	rules := []CORSRule{{AllowedOrigins: []string{"*"}, AllowedMethods: []string{"GET"}, ExposedHeaders: []string{"x"}}}
	s := &keyServer{status: 200, body: okEnvelope}
	if err := putCORS(newTestClient(t, s.handler(t)), rules...); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rules[0].ExposedHeaders, []string{"x"}) {
		t.Fatalf("rules changed: %+v", rules)
	}
}

func TestDeleteBucketCORSRequestAndRepeat(t *testing.T) {
	for _, status := range []int{200, 204} {
		s := &keyServer{status: status, body: fixture(t, "delete_bucket_cors.json")}
		if status == 204 {
			s.body = ""
		}
		c := newTestClient(t, s.handler(t))
		for i := 0; i < 2; i++ {
			if err := deleteCORS(c); err != nil {
				t.Fatalf("status %d, delete %d: %v", status, i+1, err)
			}
		}
		got := s.seen()
		if got.method != http.MethodDelete || got.path != corsPath || got.query != "" || got.body != "" {
			t.Fatalf("status %d: request = %+v", status, got)
		}
	}
}

func TestBucketCORSRegion(t *testing.T) {
	var headers []string
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		headers = append(headers, r.Header.Get("region"), r.Header.Get("region_id"))
		_, _ = w.Write([]byte(okEnvelope))
	}))
	ctx := context.Background()
	if _, err := c.GetBucketCORS(ctx, &GetBucketCORSInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PutBucketCORS(ctx, &PutBucketCORSInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket", Rules: []CORSRule{goodRule()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteBucketCORS(ctx, &DeleteBucketCORSInput{Region: "han02", ProjectID: "proj-1", Bucket: "my-bucket"}); err != nil {
		t.Fatal(err)
	}
	if len(headers) != 6 {
		t.Fatalf("headers = %v", headers)
	}
	for i, h := range headers {
		if h != "<region-id-1>" {
			t.Fatalf("header %d = %q, want the HAN02 region id", i, h)
		}
	}
}

func TestBucketCORSPathRejection(t *testing.T) {
	for _, v := range []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb"} {
		var sent atomic.Int32
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
		ctx := context.Background()
		rules := []CORSRule{goodRule()}
		_, e1 := c.GetBucketCORS(ctx, &GetBucketCORSInput{ProjectID: v, Bucket: "my-bucket"})
		_, e2 := c.GetBucketCORS(ctx, &GetBucketCORSInput{ProjectID: "proj-1", Bucket: v})
		_, e3 := c.PutBucketCORS(ctx, &PutBucketCORSInput{ProjectID: v, Bucket: "my-bucket", Rules: rules})
		_, e4 := c.PutBucketCORS(ctx, &PutBucketCORSInput{ProjectID: "proj-1", Bucket: v, Rules: rules})
		_, e5 := c.DeleteBucketCORS(ctx, &DeleteBucketCORSInput{ProjectID: v, Bucket: "my-bucket"})
		_, e6 := c.DeleteBucketCORS(ctx, &DeleteBucketCORSInput{ProjectID: "proj-1", Bucket: v})
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

func TestBucketCORSStatuses(t *testing.T) {
	ops := map[string]func(*Client) error{
		"get":    func(c *Client) error { _, err := getCORS(c); return err },
		"put":    func(c *Client) error { return putCORS(c, goodRule()) },
		"delete": deleteCORS,
	}
	for name, op := range ops {
		for _, tt := range statusCases {
			t.Run(name+"/"+http.StatusText(tt.status), func(t *testing.T) {
				checkStatusCase(t, tt, func(body string, status int) error {
					s := &keyServer{status: status, body: body}
					return op(newTestClient(t, s.handler(t)))
				}, okEnvelope)
			})
		}
	}
}

func TestBucketCORSEnvelopeCodes(t *testing.T) {
	notFound := fixture(t, "error_envelope_not_found.json")
	put := func(c *Client) error { return putCORS(c, goodRule()) }
	tests := []struct {
		name     string
		body     string
		op       func(*Client) error
		code     string
		message  string
		sentinel error
	}{
		{"put code 114", fixture(t, "error_cors_method.json"), put, "114", "Error occurred when updating bucket CORS.", nil},
		{"put code 400", fixture(t, "error_cors_malformed.json"), put, "400", "MalformedXML", nil},
		{"put code 112", `{"code":112,"success":false,"errorMsg":"bad input"}`, put, "112", "bad input", vngcloud.ErrInvalidInput},
		{"get code 404", notFound, func(c *Client) error { _, err := getCORS(c); return err }, "404", "<message>", vngcloud.ErrNotFound},
		{"delete code 404", notFound, deleteCORS, "404", "<message>", vngcloud.ErrNotFound},
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

func TestBucketCORSWritesKeepTheTransportRetries(t *testing.T) {
	for name, op := range map[string]func(*Client) error{
		"put":    func(c *Client) error { return putCORS(c, goodRule()) },
		"delete": deleteCORS,
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
