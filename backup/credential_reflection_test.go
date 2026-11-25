package backup_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/backup"
)

func TestListResponsesRejectReflectedCredentials(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
		body   func(string) string
		call   func(*backup.Client) (any, error)
	}{
		{"backends", []string{`"id":%q`, `"name":%q`}, func(field string) string {
			return `{"items":[{` + field + `}],"page":null,"pageSize":null,"totalPages":1,"totalItems":1}`
		}, func(c *backup.Client) (any, error) { return c.ListBackends(context.Background(), nil) }},
		{"policies", []string{`"id":%q`, `"backendId":%q`, `"projectId":%q`, `"product":%q`, `"name":%q`, `"createdAt":%q`, `"updatedAt":%q`, `"config":{"timeZone":%q}`, `"config":{"dailyConfig":{"backupType":%q}}`}, func(field string) string {
			return `{"items":[{` + field + `}],"page":1,"pageSize":10,"totalPages":1,"totalItems":1}`
		}, func(c *backup.Client) (any, error) { return c.ListPolicies(context.Background(), nil) }},
	} {
		for _, field := range tc.fields {
			for _, token := range []string{"first-secret", "second-secret"} {
				t.Run(tc.name+"/"+field+"/"+token, func(t *testing.T) {
					p := &rotatingProvider{}
					calls := 0
					c := client(t, func(w http.ResponseWriter, _ *http.Request) {
						calls++
						if calls == 1 {
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						_, _ = fmt.Fprint(w, strings.ReplaceAll(tc.body(fmt.Sprintf(field, "prefix-"+token+"-suffix")), "secret", `\u0073ecret`))
					}, vngcloud.WithCredentialsProvider(p))
					out, err := tc.call(c)
					var api *vngcloud.APIError
					if !errors.As(err, &api) || api.StatusCode != 200 || api.Code != "InvalidResponse" || api.Err != nil || calls != 2 || strings.Contains(fmt.Sprintf("%+v", err), "secret") {
						t.Fatalf("reflected credential accepted: output=%v error=%v calls=%d", out, err, calls)
					}
				})
			}
		}
	}
}
