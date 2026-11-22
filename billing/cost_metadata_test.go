package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"danny.vn/vngcloud"
)

func TestListCostResourcesRequiresMetadata(t *testing.T) {
	const pagination = `{"page":0,"size":200,"total":1,"totalPages":1}`
	const summary = `{"totalAll":123,"activeCount":1,"avgPerResource":123}`
	cases := []struct {
		name, pagination, summary string
	}{
		{"empty objects", `{}`, `{}`},
		{"null totals", `{"page":0,"size":200,"total":null,"totalPages":null}`, `{"totalAll":null,"activeCount":null,"avgPerResource":null}`},
	}
	for _, section := range []struct {
		name, body string
		fields     []string
	}{
		{"pagination", pagination, []string{"page", "size", "total", "totalPages"}},
		{"summary", summary, []string{"totalAll", "activeCount", "avgPerResource"}},
	} {
		for _, field := range section.fields {
			for _, missing := range []bool{true, false} {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal([]byte(section.body), &fields); err != nil {
					t.Fatal(err)
				}
				kind := "null"
				fields[field] = json.RawMessage(`null`)
				if missing {
					kind = "missing"
					delete(fields, field)
				}
				body, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				tc := cases[0]
				tc.name, tc.pagination, tc.summary = section.name+"/"+field+"/"+kind, pagination, summary
				if section.name == "pagination" {
					tc.pagination = string(body)
				} else {
					tc.summary = string(body)
				}
				cases = append(cases, tc)
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"code":200,"data":{"items":[{"resourceId":"resource-1","totalCost":123}],"pagination":%s,"summary":%s}}`, tc.pagination, tc.summary)
			}))
			out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{StartDate: "2026-09-01", EndDate: "2026-09-02"})
			var api *vngcloud.APIError
			if out != nil || !errors.As(err, &api) || api.Code != "InvalidResponse" || api.StatusCode != 200 || api.Retryable {
				t.Fatal("incomplete metadata returned output or lost InvalidResponse and observed status")
			}
		})
	}
}

func TestListCostResourcesExplicitEmptyMetadata(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":200,"data":{"items":[],"pagination":{"page":0,"size":200,"total":0,"totalPages":0},"summary":{"totalAll":0,"activeCount":0,"avgPerResource":0}}}`))
	}))
	out, err := client.ListCostResources(context.Background(), &ListCostResourcesInput{StartDate: "2026-09-01", EndDate: "2026-09-02"})
	if err != nil || out == nil {
		t.Fatal("explicit empty result was rejected")
	}
	if out.Items == nil || len(out.Items) != 0 || out.Page != 1 || out.PageSize != 200 || out.TotalItem != 0 || out.TotalPage != 0 || out.Summary != (CostResourceSummary{}) {
		t.Fatal("explicit empty result lost its zero totals or pagination")
	}
}
