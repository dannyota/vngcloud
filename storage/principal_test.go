package storage

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/testutil"
)

const userDetailsPath = "/internal/v1/users/details"

func ensure(c *Client) (*EnsureServiceAccountPrincipalOutput, error) {
	return c.EnsureServiceAccountPrincipal(context.Background(),
		&EnsureServiceAccountPrincipalInput{ProjectID: "proj-1", ServiceAccountID: "sa-id-1"})
}

func TestEnsureServiceAccountPrincipalRequestAndFixture(t *testing.T) {
	s := &keyServer{status: 200, body: fixture(t, "service_account_principal.json")}
	out, err := ensure(newTestClient(t, s.handler(t)))
	if err != nil {
		t.Fatal(err)
	}
	got := s.seen()
	if got.method != http.MethodGet || got.path != userDetailsPath || got.body != "" {
		t.Fatalf("request = %+v", got)
	}
	q, err := url.ParseQuery(got.query)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"generated": {"true"}, "project_id": {"proj-1"}, "iam_account_id": {"sa-sa-id-1"}}
	if len(q) != len(want) {
		t.Fatalf("query = %v", q)
	}
	for k, v := range want {
		if q.Get(k) != v[0] {
			t.Fatalf("query %s = %q, want %q", k, q.Get(k), v[0])
		}
	}
	if out.SubUserID != "<account-user>:sa-<name>" || out.PrincipalARN != "arn:aws:iam:::user/<account-user>:sa-<name>" {
		t.Fatalf("out = %+v", out)
	}
}

func TestEnsureServiceAccountPrincipalRegion(t *testing.T) {
	var header string
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("region")
		testutil.WriteFixture(t, w, fixtures+"service_account_principal.json")
	})
	_, err := newTestClient(t, h).EnsureServiceAccountPrincipal(context.Background(),
		&EnsureServiceAccountPrincipalInput{Region: "han02", ProjectID: "proj-1", ServiceAccountID: "sa-id-1"})
	if err != nil || header != "<region-id-1>" {
		t.Fatalf("region header = %q, err = %v", header, err)
	}
}

func TestEnsureServiceAccountPrincipalRefusesBadSubUserID(t *testing.T) {
	bodies := map[string]string{
		"null":      fixture(t, "service_account_principal_null.json"),
		"empty":     `{"code":200,"success":true,"data":{"subUserId":""}}`,
		"missing":   `{"code":200,"success":true,"data":{}}`,
		"no data":   `{"code":200,"success":true}`,
		"null data": `{"code":200,"success":true,"data":null}`,
		"iam user":  `{"code":200,"success":true,"data":{"subUserId":"<account-user>:iam-<name>"}}`,
		"bare":      `{"code":200,"success":true,"data":{"subUserId":"<account-user>"}}`,
		"prefix":    `{"code":200,"success":true,"data":{"subUserId":"sa-<name>"}}`,
		"not text":  `{"code":200,"success":true,"data":{"subUserId":7}}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			s := &keyServer{status: 200, body: body}
			out, err := ensure(newTestClient(t, s.handler(t)))
			if out != nil || err == nil {
				t.Fatalf("out = %+v, err = %v, want an error and no Output", out, err)
			}
			if strings.Contains(err.Error(), "<account-user>") {
				t.Fatalf("error %q repeats the sub-user", err)
			}
		})
	}
}

func TestEnsureServiceAccountPrincipalErrors(t *testing.T) {
	tests := []struct {
		status int
		body   string
		want   error
	}{
		{http.StatusBadRequest, `{"message":"x"}`, nil},
		{http.StatusForbidden, fixture(t, "error_permission.json"), vngcloud.ErrPermission},
		{http.StatusNotFound, `{"message":"x"}`, vngcloud.ErrNotFound},
		{http.StatusInternalServerError, `{"message":"x"}`, nil},
		{http.StatusOK, `{"code":114,"success":false,"errorMsg":"<message>"}`, nil},
	}
	for _, tt := range tests {
		t.Run(http.StatusText(tt.status), func(t *testing.T) {
			s := &keyServer{status: tt.status, body: tt.body}
			out, err := ensure(newTestClient(t, s.handler(t)))
			var apiErr *vngcloud.APIError
			if out != nil || !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status {
				t.Fatalf("out = %+v, err = %v, want *APIError with status %d", out, err, tt.status)
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestEnsureServiceAccountPrincipalKeepsRetries(t *testing.T) {
	var gets atomic.Int32
	h := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if gets.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		testutil.WriteFixture(t, w, fixtures+"service_account_principal.json")
	})
	if _, err := ensure(New(testutil.NewRetryConfig(t, h))); err != nil {
		t.Fatal(err)
	}
	if gets.Load() != 2 {
		t.Fatalf("%d GETs, want 2", gets.Load())
	}
}

func TestEnsureServiceAccountPrincipalPathRejection(t *testing.T) {
	for _, v := range []string{"", "..", ".", "a/b", "a?b", "a b", "a%2Fb", "a&generated=false", "a#b"} {
		var sent atomic.Int32
		c := newTestClient(t, serve(t, func(http.ResponseWriter, *http.Request) { sent.Add(1) }))
		_, e1 := c.EnsureServiceAccountPrincipal(context.Background(), &EnsureServiceAccountPrincipalInput{ProjectID: v, ServiceAccountID: "sa-id-1"})
		_, e2 := c.EnsureServiceAccountPrincipal(context.Background(), &EnsureServiceAccountPrincipalInput{ProjectID: "proj-1", ServiceAccountID: v})
		for i, err := range []error{e1, e2} {
			if !errors.Is(err, vngcloud.ErrInvalidInput) {
				t.Fatalf("value %q call %d: err = %v, want ErrInvalidInput", v, i, err)
			}
		}
		if sent.Load() != 0 {
			t.Fatalf("value %q: %d request(s) sent, want 0", v, sent.Load())
		}
	}
}
