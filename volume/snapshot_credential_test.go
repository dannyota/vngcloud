package volume

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
)

type snapshotRotatingCredentials struct{ calls int }

func (p *snapshotRotatingCredentials) Token(context.Context) (vngcloud.Token, error) {
	p.calls++
	if p.calls == 1 {
		return vngcloud.Token{AccessToken: "first-secret"}, nil
	}
	return vngcloud.Token{AccessToken: "second-secret"}, nil
}
func (*snapshotRotatingCredentials) Invalidate(string) {}

func TestSnapshotResponsesRejectReflectedCredentials(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
		body   func(string) string
		call   func(*Client) (any, error)
	}{
		{"backends", []string{`"id":%q`, `"name":%q`}, func(field string) string { return `{"items":[{` + field + `}]}` }, func(c *Client) (any, error) {
			return c.ListSnapshotBackends(context.Background(), &ListSnapshotBackendsInput{Name: "example"})
		}},
		{"policies", []string{`"id":%q`, `"name":%q`, `"policyType":%q`, `"createdAt":%q`, `"updatedAt":%q`, `"config":{"timeZone":%q}`, `"config":{"statusSendEmail":[%q]}`}, func(field string) string {
			return `{"items":[{` + field + `}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`
		}, func(c *Client) (any, error) {
			return c.ListSnapshotPolicies(context.Background(), &ListSnapshotPoliciesInput{BackendID: "backend-1"})
		}},
	} {
		for _, field := range tc.fields {
			for _, token := range []string{"first-secret", "second-secret"} {
				t.Run(tc.name+"/"+field+"/"+token, func(t *testing.T) {
					p := &snapshotRotatingCredentials{}
					calls := 0
					client := &http.Client{Transport: snapshotTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						if calls == 1 {
							return &http.Response{StatusCode: 401, Header: make(http.Header), Body: http.NoBody}, nil
						}
						return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.ReplaceAll(tc.body(fmt.Sprintf(field, "prefix-"+token+"-suffix")), "secret", `\u0073ecret`)))}, nil
					})}
					cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithProjectID("project-1"), vngcloud.WithCredentialsProvider(p), vngcloud.WithHTTPClient(client))
					if err != nil {
						t.Fatal(err)
					}
					out, err := tc.call(New(cfg))
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" || api.Err != nil || calls != 2 || strings.Contains(fmt.Sprintf("%+v", err), "secret") {
						t.Fatalf("reflected credential accepted: output=%v error=%v calls=%d", out, err, calls)
					}
				})
			}
		}
	}
}
