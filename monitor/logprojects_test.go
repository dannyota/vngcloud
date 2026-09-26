package monitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

// TestListLogProjectsDecodesFixture decodes a sanitized live capture: one
// project with the confirmed field shape (see LogProject's doc comment),
// including the extra and certInfos keys the SDK does not model.
func TestListLogProjectsDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/log-api/v1/projects" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjects.json")
	}))

	out, err := client.ListLogProjects(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListLogProjects() error = %v", err)
	}
	if len(out.Items) != 1 {
		t.Fatalf("unexpected items: %+v", out.Items)
	}
	if out.Page != 0 || out.PageSize != 10 || out.TotalPage != 1 || out.TotalItem != 1 {
		t.Fatalf("unexpected paging: %+v", out)
	}
	p := out.Items[0]
	if p.ID == "" || p.ProjectName != "vngcloud-example" || p.ProjectDescription != "" {
		t.Fatalf("unexpected project: %+v", p)
	}
	if p.Status != LogProjectStatusActive || p.BillingStatus != "ACTIVE" || p.ProjectType != "project" {
		t.Fatalf("unexpected project: %+v", p)
	}
	if p.CreatedAt == "" {
		t.Fatalf("unexpected project: %+v", p)
	}
}

// TestListLogProjectsOmitsEmptyFilterParams checks Query and BillingStatus
// left empty are never sent as empty query parameters, and project_type and
// status are never sent at all: the live list treats an empty value for any
// of these four keys as its own filter and returns zero items, rather than
// ignoring it as ListChannels' searchtext and field do.
func TestListLogProjectsOmitsEmptyFilterParams(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		for _, key := range []string{"query", "billing_status", "project_type", "status"} {
			if _, ok := q[key]; ok {
				t.Fatalf("unexpected %q in query: %v", key, q)
			}
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjects.json")
	}))
	if _, err := client.ListLogProjects(context.Background(), nil); err != nil {
		t.Fatalf("ListLogProjects() error = %v", err)
	}
}

// TestListLogProjectsSendsZeroBasedPaging checks Page 0, whether left as the
// zero value or set explicitly, reaches the query string as "0" rather than
// being promoted to "1" the way core.PageQuery would for a 1-based list
// such as ListChannels: the design's list page is 0-based, so page 0 is a
// real, distinct first page, not a sentinel for "unset".
func TestListLogProjectsSendsZeroBasedPaging(t *testing.T) {
	tests := []struct {
		name     string
		in       *ListLogProjectsInput
		wantPage string
		wantSize string
	}{
		{"nil input", nil, "0", strconv.Itoa(logProjectDefaultPageSize)},
		{"zero-value input", &ListLogProjectsInput{}, "0", strconv.Itoa(logProjectDefaultPageSize)},
		{"explicit page 0", &ListLogProjectsInput{Page: 0, Size: 5}, "0", "5"},
		{"page 2", &ListLogProjectsInput{Page: 2, Size: 5}, "2", "5"},
		{"negative page clamps to 0", &ListLogProjectsInput{Page: -1, Size: 5}, "0", "5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("page") != tt.wantPage || q.Get("size") != tt.wantSize {
					t.Fatalf("paging query = %v, want page=%s size=%s", q, tt.wantPage, tt.wantSize)
				}
				testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjects.json")
			}))
			if _, err := client.ListLogProjects(context.Background(), tt.in); err != nil {
				t.Fatalf("ListLogProjects() error = %v", err)
			}
		})
	}
}

// TestListLogProjectsSendsFilters checks Query and BillingStatus reach the
// query string the design names, and that project_type and status, which
// the design's API takes but ListLogProjectsInput does not expose yet,
// always still reach it empty, as ListChannels does for searchtext and
// field.
func TestListLogProjectsSendsFilters(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("query") != "app" || q.Get("billing_status") != "PAID" {
			t.Fatalf("unexpected filter query: %v", q)
		}
		if q.Get("project_type") != "" || q.Get("status") != "" {
			t.Fatalf("unexpected unexposed filter query: %v", q)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjects.json")
	}))
	if _, err := client.ListLogProjects(context.Background(), &ListLogProjectsInput{Query: "app", BillingStatus: "PAID"}); err != nil {
		t.Fatalf("ListLogProjects() error = %v", err)
	}
}

// TestGetLogProjectRequest checks the path and that name and description
// decode into ProjectName and ProjectDescription: the live wire sends them
// under those keys, not projectName and projectDescription, and there is no
// zone key at all.
func TestGetLogProjectRequest(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/log-api/v1/projects/proj-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"proj-1","name":"app","description":"logs","status":"ACTIVE"}`))
	}))

	out, err := client.GetLogProject(context.Background(), &GetLogProjectInput{LogProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("GetLogProject() error = %v", err)
	}
	if out.LogProject.ID != "proj-1" || out.LogProject.ProjectName != "app" || out.LogProject.ProjectDescription != "logs" {
		t.Fatalf("unexpected project: %+v", out.LogProject)
	}
}

// TestGetLogProjectDecodesFixture decodes a sanitized live capture: the
// same confirmed field shape ListLogProjects.json holds, without the
// certInfos key, which the live capture showed only on the list.
func TestGetLogProjectDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/log-api/v1/projects/proj-1" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/GetLogProject.json")
	}))

	out, err := client.GetLogProject(context.Background(), &GetLogProjectInput{LogProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("GetLogProject() error = %v", err)
	}
	p := out.LogProject
	if p.ID == "" || p.ProjectName != "vngcloud-example" || p.ProjectDescription != "" {
		t.Fatalf("unexpected project: %+v", p)
	}
	if p.Status != LogProjectStatusActive || p.BillingStatus != "ACTIVE" || p.ProjectType != "project" {
		t.Fatalf("unexpected project: %+v", p)
	}
	if p.CreatedAt == "" {
		t.Fatalf("unexpected project: %+v", p)
	}
}

func TestGetLogProjectNotFound(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	_, err := client.GetLogProject(context.Background(), &GetLogProjectInput{LogProjectID: "proj-1"})
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("GetLogProject() error = %v, want ErrNotFound", err)
	}
}

func TestGetLogProjectMissingID(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing LogProjectID")
	}))
	if _, err := client.GetLogProject(context.Background(), &GetLogProjectInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetLogProject() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.GetLogProject(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("GetLogProject(nil) error = %v, want ErrInvalidInput", err)
	}
}

func TestGetLogProjectPathIDRejection(t *testing.T) {
	for _, id := range []string{"..", ".", "/", ""} {
		t.Run(fmt.Sprintf("%q", id), func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("unexpected request for a rejected LogProjectID")
			}))
			_, err := client.GetLogProject(context.Background(), &GetLogProjectInput{LogProjectID: id})
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("GetLogProject(%q) error = %v, want ErrInvalidInput", id, err)
			}
		})
	}
}

func TestListLogProjectClassesDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/billing-api/v2/log/quota-class" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
	}))

	out, err := client.ListLogProjectClasses(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListLogProjectClasses() error = %v", err)
	}
	if len(out.Items) != 3 {
		t.Fatalf("unexpected classes: %+v", out.Items)
	}

	basic := out.Items[0]
	if basic.Name != LogProjectClassBasic || basic.Status != LogProjectClassStatusActive {
		t.Fatalf("unexpected basic class: %+v", basic)
	}
	if len(basic.Retentions) != 1 {
		t.Fatalf("unexpected basic retentions: %+v", basic.Retentions)
	}
	r0 := basic.Retentions[0]
	if r0.Amount != 1 || r0.MinSize != 10 || r0.MaxSize != 10 || r0.Step != 1 || r0.PackageID == "" {
		t.Fatalf("unexpected basic retention: %+v", r0)
	}

	pro := out.Items[1]
	if pro.Name != LogProjectClassPro || pro.Status != LogProjectClassStatusActive || len(pro.Retentions) != 6 {
		t.Fatalf("unexpected pro class: %+v", pro)
	}
	if pro.Retentions[0].Amount != 7 || pro.Retentions[0].MinSize != 20 {
		t.Fatalf("unexpected pro retention: %+v", pro.Retentions[0])
	}

	enterprise := out.Items[2]
	if enterprise.Status != LogProjectClassStatusDisabled || len(enterprise.Retentions) != 0 {
		t.Fatalf("unexpected enterprise class: %+v", enterprise)
	}
}

func TestQuoteCreateLogProjectDecodesFixture(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			if r.Method != http.MethodPost {
				t.Fatalf("method = %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["packageId"] != "pkg-pro-7d" {
				t.Fatalf("packageId = %v, want pkg-pro-7d", body["packageId"])
			}
			if body["quantity"] != 140.0 {
				t.Fatalf("quantity = %v, want 140", body["quantity"])
			}
			if body["projectName"] != "app" || body["projectDescription"] != "logs" {
				t.Fatalf("unexpected name/description: %+v", body)
			}
			if body["monthPeriod"] != 1.0 || body["pay"] != true {
				t.Fatalf("unexpected monthPeriod/pay: %+v", body)
			}
			buyWith, ok := body["buyWith"].(map[string]any)
			if !ok || len(buyWith) != 0 {
				t.Fatalf("buyWith = %v, want {}", body["buyWith"])
			}
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateLogProject.json")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))

	out, err := client.QuoteCreateLogProject(context.Background(), &CreateLogProjectInput{
		Name:          "app",
		Description:   "logs",
		Class:         LogProjectClassPro,
		RetentionDays: 7,
		GBPerDay:      20,
	})
	if err != nil {
		t.Fatalf("QuoteCreateLogProject() error = %v", err)
	}
	if out.OptimumPrice != 917000 || out.OriginalPrice != 917000 || out.DiscountPrice != 0 {
		t.Fatalf("unexpected price: %+v", out)
	}
	if out.DiscountPercent != nil {
		t.Fatalf("DiscountPercent = %v, want nil", *out.DiscountPercent)
	}
	if len(out.Properties) != 1 || out.Properties[0].Name != "monitor-platform-log" {
		t.Fatalf("unexpected properties: %+v", out.Properties)
	}
	if out.Properties[0].Description != nil {
		t.Fatalf("Properties[0].Description = %v, want nil", *out.Properties[0].Description)
	}
}

// TestQuoteCreateLogProjectClassListFailureReportsQuoteOperation checks that
// a class-list read failing inside QuoteCreateLogProject reports the quote's
// own operation, not "monitor.ListLogProjectClasses", the operation the
// nested read would report on its own.
func TestQuoteCreateLogProjectClassListFailureReportsQuoteOperation(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"boom"}`))
	}))

	_, err := client.QuoteCreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app"})
	var apiErr *core.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("QuoteCreateLogProject() error = %v, want *core.APIError", err)
	}
	if apiErr.Operation != "monitor.QuoteCreateLogProject" {
		t.Fatalf("Operation = %q, want %q", apiErr.Operation, "monitor.QuoteCreateLogProject")
	}
}

// TestQuoteCreateLogProjectIgnoresMaxPriceAndNoWait checks the quote body
// carries exactly the keys buildLogProjectOrderBody sends, with no trace of
// MaxPrice or NoWait, even when the caller sets both: neither field governs
// a quote, only CreateLogProject's own order and wait.
func TestQuoteCreateLogProjectIgnoresMaxPriceAndNoWait(t *testing.T) {
	wantKeys := []string{
		"redirectUrl", "packageId", "quantity", "buyWith",
		"monthPeriod", "projectName", "projectDescription", "pay",
	}
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if len(body) != len(wantKeys) {
				t.Fatalf("body has %d keys, want %d: %+v", len(body), len(wantKeys), body)
			}
			for _, k := range wantKeys {
				if _, ok := body[k]; !ok {
					t.Fatalf("body missing key %q: %+v", k, body)
				}
			}
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateLogProject.json")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))

	if _, err := client.QuoteCreateLogProject(context.Background(), &CreateLogProjectInput{
		Name:     "app",
		MaxPrice: 1000000,
		NoWait:   true,
	}); err != nil {
		t.Fatalf("QuoteCreateLogProject() error = %v", err)
	}
}

// TestQuoteCreateLogProjectDefaultsBasic checks Class, RetentionDays, and
// GBPerDay left zero default to Basic's only retention option and its
// minimum size, per the design.
func TestQuoteCreateLogProjectDefaultsBasic(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["packageId"] != "pkg-basic-1d" || body["quantity"] != 10.0 {
				t.Fatalf("unexpected body: %+v", body)
			}
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateLogProject.json")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))

	if _, err := client.QuoteCreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app"}); err != nil {
		t.Fatalf("QuoteCreateLogProject() error = %v", err)
	}
}

// TestQuoteCreateLogProjectIsIdempotent checks the created-price POST is
// retried after a 502, per ADR 0002 rule 1: a quote is a read even though
// it uses POST.
func TestQuoteCreateLogProjectIsIdempotent(t *testing.T) {
	quoteCalls := 0
	client := New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/billing-api/v2/log/quota-class":
			testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
		case "/billing-api/v2/log/prices/created-price":
			quoteCalls++
			if quoteCalls == 1 {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			testutil.WriteFixture(t, w, "../testdata/monitor/QuoteCreateLogProject.json")
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	})))

	if _, err := client.QuoteCreateLogProject(context.Background(), &CreateLogProjectInput{Name: "app"}); err != nil {
		t.Fatalf("QuoteCreateLogProject() error = %v", err)
	}
	if quoteCalls != 2 {
		t.Fatalf("quote calls = %d, want 2 (one retry after the 502)", quoteCalls)
	}
}

func TestQuoteCreateLogProjectMissingName(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("unexpected request for a missing Name")
	}))
	if _, err := client.QuoteCreateLogProject(context.Background(), &CreateLogProjectInput{}); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("QuoteCreateLogProject() error = %v, want ErrInvalidInput", err)
	}
	if _, err := client.QuoteCreateLogProject(context.Background(), nil); !errors.Is(err, core.ErrInvalidInput) {
		t.Fatalf("QuoteCreateLogProject(nil) error = %v, want ErrInvalidInput", err)
	}
}

// TestQuoteCreateLogProjectInvalidClassOrRetention checks a class or
// retention the class list lacks returns ErrInvalidInput and never reaches
// the created-price endpoint.
func TestQuoteCreateLogProjectInvalidClassOrRetention(t *testing.T) {
	tests := []struct {
		name string
		in   *CreateLogProjectInput
	}{
		{"unknown class", &CreateLogProjectInput{Name: "app", Class: "Bogus"}},
		{"disabled class", &CreateLogProjectInput{Name: "app", Class: "Enterprise (Coming soon)"}},
		{"ambiguous retention days", &CreateLogProjectInput{Name: "app", Class: LogProjectClassPro}},
		{"unknown retention days", &CreateLogProjectInput{Name: "app", Class: LogProjectClassPro, RetentionDays: 99}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/billing-api/v2/log/quota-class" {
					t.Fatalf("unexpected request to %s", r.URL.Path)
				}
				testutil.WriteFixture(t, w, "../testdata/monitor/ListLogProjectClasses.json")
			}))
			_, err := client.QuoteCreateLogProject(context.Background(), tt.in)
			if !errors.Is(err, core.ErrInvalidInput) {
				t.Fatalf("QuoteCreateLogProject() error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

// TestBuildLogProjectOrderBodyDefaults and
// TestBuildLogProjectOrderBodyExplicitFields test the shared builder
// directly, without the HTTP layer, per the design's ADR 0002 rule 8 note
// that one builder makes the quote and create body.
func TestBuildLogProjectOrderBodyDefaults(t *testing.T) {
	classes := []LogProjectClass{
		{Name: LogProjectClassBasic, Status: LogProjectClassStatusActive, Retentions: []LogProjectRetention{
			{Amount: 1, MinSize: 10, PackageID: "pkg-basic"},
		}},
	}
	body, err := buildLogProjectOrderBody("op", &CreateLogProjectInput{Name: "app", Description: "d"}, classes)
	if err != nil {
		t.Fatalf("buildLogProjectOrderBody() error = %v", err)
	}
	want := logProjectOrderBody{
		RedirectURL:        logProjectRedirectURL,
		PackageID:          "pkg-basic",
		Quantity:           10,
		BuyWith:            map[string]any{},
		MonthPeriod:        1,
		ProjectName:        "app",
		ProjectDescription: "d",
		Pay:                true,
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %+v, want %+v", body, want)
	}
}

func TestBuildLogProjectOrderBodyExplicitFields(t *testing.T) {
	classes := []LogProjectClass{
		{Name: LogProjectClassPro, Status: LogProjectClassStatusActive, Retentions: []LogProjectRetention{
			{Amount: 7, MinSize: 20, PackageID: "pkg-7"},
			{Amount: 14, MinSize: 10, PackageID: "pkg-14"},
		}},
	}
	body, err := buildLogProjectOrderBody("op", &CreateLogProjectInput{
		Name: "app", Class: LogProjectClassPro, RetentionDays: 7, GBPerDay: 50,
	}, classes)
	if err != nil {
		t.Fatalf("buildLogProjectOrderBody() error = %v", err)
	}
	if body.PackageID != "pkg-7" || body.Quantity != 350 {
		t.Fatalf("unexpected body: %+v", body)
	}
}
