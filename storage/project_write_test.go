package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/endpoints"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
)

const newProjectJSON = `{"projectId":"new-1","projectName":"sdk-project","regionId":"<region-id-2>","regionName":"HCM04","status":1,"totalQuota":30,"projectType":1,"projectTypeName":"Gold","purchaseTypeId":4,"purchaseTypeName":"Pay monthly","enableAutoRenew":false,"autoRenewPeriod":0}`
const syntheticOrderJSON = `{"success":true,"data":{"redirectUrl":"https://console.example/storage/projects?token=private"}}`

type projectWriteServer struct {
	before, after, buckets, order, quote string
	overrides                            map[string]string
	status                               int
	orders, deletes, lists, prices       int
	lose                                 bool
	lost                                 chan struct{}
	orderBody, deleteBody                map[string]any
}

func (s *projectWriteServer) handler(t *testing.T) http.Handler {
	t.Helper()
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		checkRegionHeaders(t, r, "<region-id-2>")
		body := ""
		switch r.URL.Path {
		case "/internal/v1/billing/project_types":
			body = pricingTypes
		case "/internal/v1/billing/purchase_types":
			body = pricingPurchases
		case "/billing-api/v1/configurations":
			key := r.URL.Query().Get("keys")
			values := map[string]string{"vos_billing_normal_min_quota": "30", "vos_billing_normal_max_quota": "2000000", "max_project_per_user_per_region": "20", "enable_iam_checkout": "true"}
			body = fmt.Sprintf(`{"success":true,"datas":[{"key":%q,"value":%q}]}`, key, values[key])
		case "/billing-api/v2/price":
			s.prices++
			var got map[string]any
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			want := map[string]any{"resourceType": "object_storage", "action": "create", "resourceInfo": map[string]any{"quota": float64(30), "purchaseTypeId": float64(4), "projectType": float64(1)}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("price body: %#v", got)
			}
			body = pricingQuote
			if s.quote != "" {
				body = s.quote
			}
		case "/internal/v1/projects":
			s.lists++
			body = s.before
			if s.orders+s.deletes > 0 {
				body = s.after
			}
			if body == "" {
				body = `{"success":true,"datas":[]}`
			}
		case "/internal/v1/ceph/projects/new-1":
			if r.URL.Query().Get("limit") != "1000" {
				t.Error("bucket limit")
			}
			body = s.buckets
			if body == "" {
				body = `{"success":true,"datas":[]}`
			}
		case "/internal/v2/orders":
			if r.Method != http.MethodPost || s.prices != 1 {
				t.Error("order before quote")
			}
			s.orders++
			if err := json.NewDecoder(r.Body).Decode(&s.orderBody); err != nil {
				t.Error(err)
			}
			if s.lose {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				close(s.lost)
				return
			}
			if s.status != 0 {
				w.Header().Set("Location", "/unapproved")
				w.WriteHeader(s.status)
			}
			body = s.order
			if body == "" && s.status == 0 {
				body = syntheticOrderJSON
			}
		case "/internal/v1/projects/new-1":
			if r.Method != http.MethodDelete {
				t.Error("delete method")
			}
			s.deletes++
			if err := json.NewDecoder(r.Body).Decode(&s.deleteBody); err != nil {
				t.Error(err)
			}
			body = `{"success":true}`
			if s.order != "" {
				body = s.order
			}
			if s.status != 0 {
				w.Header().Set("Location", "/unapproved")
				w.WriteHeader(s.status)
			}
		default:
			t.Errorf("unapproved route %s", r.URL.Path)
		}
		if strings.Contains(r.URL.Path, "configurations") || r.Method != http.MethodGet {
			if r.URL.Query().Get("region_id") != "<region-id-2>" {
				t.Error("query region")
			}
		}
		if replacement, ok := s.overrides[r.URL.Path]; ok {
			body = replacement
		}
		if replacement, ok := s.overrides[r.URL.Query().Get("keys")]; ok {
			body = replacement
		}
		_, _ = w.Write([]byte(body))
	})
}

func validProjectCreate() *CreateProjectInput {
	return &CreateProjectInput{Name: "sdk-project", Type: "Gold", QuotaGB: 30, MaxPrice: 30000}
}
func projectList(p string) string { return `{"success":true,"datas":[` + p + `]}` }
func fakeProjectClock(c *Client) *time.Duration {
	elapsed := time.Duration(0)
	c.now = func() time.Time { return time.Unix(0, 0).Add(elapsed) }
	c.sleep = func(ctx context.Context, d time.Duration) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		elapsed += d
		return nil
	}
	return &elapsed
}

func TestCreateProjectBodyAndConfirmation(t *testing.T) {
	s := &projectWriteServer{after: projectList(newProjectJSON)}
	c := newTestClient(t, s.handler(t))
	out, err := c.CreateProject(context.Background(), validProjectCreate())
	if err != nil || out.Project == nil || out.Project.ID != "new-1" || out.MonthlyPrice != 30000 || out.TotalPrice != 30000 {
		t.Fatalf("output %+v error %v", out, err)
	}
	want := map[string]any{"resourceType": "object_storage", "action": "create", "paymentType": "auto", "resourceInfo": map[string]any{"projectName": "sdk-project", "quota": float64(30), "purchaseTypeId": float64(4), "projectType": float64(1), "projectTypeGroup": "Gold", "archivePeriod": float64(0), "billingTimeType": "block", "isTrial": false, "isPoc": false, "enableAutoRenew": false, "autoRenewPeriod": float64(0)}}
	if !reflect.DeepEqual(s.orderBody, want) || s.orders != 1 || s.lists != 2 {
		t.Fatalf("body %#v orders %d lists %d", s.orderBody, s.orders, s.lists)
	}
}

func TestCreateProjectGuards(t *testing.T) {
	for _, tc := range []struct {
		name          string
		change        func(*CreateProjectInput)
		before, quote string
		overrides     map[string]string
		sentinel      error
	}{
		{name: "missing name", change: func(in *CreateProjectInput) { in.Name = "" }, sentinel: vngcloud.ErrInvalidInput},
		{name: "NaN", change: func(in *CreateProjectInput) { in.MaxPrice = math.NaN() }, sentinel: vngcloud.ErrInvalidInput},
		{name: "infinite", change: func(in *CreateProjectInput) { in.MaxPrice = math.Inf(1) }, sentinel: vngcloud.ErrInvalidInput},
		{name: "negative", change: func(in *CreateProjectInput) { in.MaxPrice = -1 }, sentinel: vngcloud.ErrInvalidInput},
		{name: "zero cap", change: func(in *CreateProjectInput) { in.MaxPrice = 0 }, sentinel: vngcloud.ErrPriceAboveMax},
		{name: "over cap", change: func(in *CreateProjectInput) { in.MaxPrice = 29999 }, sentinel: vngcloud.ErrPriceAboveMax},
		{name: "low quota", change: func(in *CreateProjectInput) { in.QuotaGB = 29 }, sentinel: vngcloud.ErrInvalidInput},
		{name: "high quota", change: func(in *CreateProjectInput) { in.QuotaGB = 2000001 }, sentinel: vngcloud.ErrInvalidInput},
		{name: "unknown type", change: func(in *CreateProjectInput) { in.Type = "gold" }, sentinel: vngcloud.ErrInvalidInput},
		{name: "duplicate", before: projectList(newProjectJSON), sentinel: vngcloud.ErrInvalidInput},
		{name: "full region", before: projectList(newProjectJSON), change: func(in *CreateProjectInput) { in.Name = "other" }, overrides: map[string]string{"max_project_per_user_per_region": `{"success":true,"datas":[{"key":"max_project_per_user_per_region","value":"1"}]}`}, sentinel: vngcloud.ErrInvalidInput},
		{name: "incomplete projects", before: `{"success":true,"datas":[],"isNext":true}`},
		{name: "missing projects", before: `{"success":true}`},
		{name: "null projects", before: `{"success":true,"datas":null}`},
		{name: "bad projects", before: `{"success":true,"datas":[{}]}`},
		{name: "missing configuration", overrides: map[string]string{"vos_billing_normal_min_quota": `{"success":true,"datas":[]}`}},
		{name: "zero quote", quote: `{"success":true,"data":{"optimumPrice":0}}`, sentinel: vngcloud.ErrUnpriced},
		{name: "missing quote", quote: `{"success":true,"data":{}}`},
		{name: "nonfinite quote", quote: `{"success":true,"data":{"optimumPrice":1e999}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &projectWriteServer{before: tc.before, quote: tc.quote, overrides: tc.overrides}
			c := newTestClient(t, s.handler(t))
			in := validProjectCreate()
			if tc.change != nil {
				tc.change(in)
			}
			_, err := c.CreateProject(context.Background(), in)
			if err == nil || s.orders != 0 {
				t.Fatalf("error %v orders %d", err, s.orders)
			}
			if tc.sentinel != nil && (err == nil || tc.sentinel != nil && !errors.Is(err, tc.sentinel)) {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateProjectOnceAndUncertain(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		body     string
		lose     bool
		sentinel error
	}{
		{"5xx", 503, `{}`, false, ErrNotSettled},
		{"401", 401, `{}`, false, vngcloud.ErrAuth},
		{"429", 429, `{}`, false, vngcloud.ErrRateLimited},
		{"redirect", 302, `{}`, false, ErrNotSettled},
		{"redirect 307", 307, `{}`, false, ErrNotSettled},
		{"redirect 308", 308, `{}`, false, ErrNotSettled},
		{"network loss", 0, "", true, ErrNotSettled},
		{"empty", 0, " ", false, ErrNotSettled},
		{"malformed", 0, `not json`, false, ErrNotSettled},
		{"missing success", 0, `{"data":{}}`, false, ErrNotSettled},
		{"refusal", 0, `{"success":false,"code":403,"errorMsg":"refused"}`, false, vngcloud.ErrPermission},
		{"checkout", 0, syntheticOrderJSON, false, ErrPaymentRequired},
		{"unknown", 0, `{"success":true,"data":{"unknown":true}}`, false, ErrNotSettled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &projectWriteServer{status: tc.status, order: tc.body, lose: tc.lose, lost: make(chan struct{})}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			fakeProjectClock(c)
			out, err := c.CreateProject(context.Background(), validProjectCreate())
			if tc.lose {
				<-s.lost
			}
			if s.orders != 1 || out == nil || (err == nil || tc.sentinel != nil && !errors.Is(err, tc.sentinel)) {
				t.Fatalf("orders %d output %+v error %v", s.orders, out, err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "https://") {
				t.Fatal("response URL leaked")
			}
			if errors.Is(err, ErrNotSettled) && !strings.Contains(err.Error(), projectOrderRecovery) {
				t.Fatal("missing recovery")
			}
		})
	}
}

func TestCreateProjectIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, after string
		noWait      bool
	}{
		{"wrong name", projectList(strings.Replace(newProjectJSON, "sdk-project", "other", 1)), false},
		{"wrong type", projectList(strings.Replace(newProjectJSON, `"projectType":1`, `"projectType":7`, 1)), false},
		{"wrong quota", projectList(strings.Replace(newProjectJSON, `"totalQuota":30`, `"totalQuota":31`, 1)), false},
		{"wrong region", projectList(strings.Replace(newProjectJSON, `"regionId":"<region-id-2>"`, `"regionId":"other"`, 1)), false},
		{"renewal true", projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true`, 1)), false},
		{"renewal true no wait", projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true`, 1)), true},
		{"missing renewal", projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false,`, "", 1)), false},
		{"ambiguous name", projectList(newProjectJSON + "," + strings.Replace(newProjectJSON, "new-1", "new-2", 1)), false},
		{"incomplete confirmation", `{"success":true,"datas":[],"isNext":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &projectWriteServer{after: tc.after}
			c := newTestClient(t, s.handler(t))
			fakeProjectClock(c)
			in := validProjectCreate()
			in.NoWait = tc.noWait
			_, err := c.CreateProject(context.Background(), in)
			if err == nil || s.orders != 1 {
				t.Fatalf("error %v orders %d", err, s.orders)
			}
		})
	}
}

func TestDeleteProjectGuardsAndConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, before, buckets string
		want                  error
		deleted               int
	}{
		{"empty", projectList(newProjectJSON), `{"success":true,"datas":[]}`, nil, 1},
		{"any bucket", projectList(newProjectJSON), `{"success":true,"datas":[{"name":"empty","count":0}]}`, ErrProjectNotEmpty, 0},
		{"more buckets", projectList(newProjectJSON), `{"success":true,"datas":[],"isNext":true}`, nil, 0},
		{"missing buckets", projectList(newProjectJSON), `{"success":true}`, nil, 0},
		{"null buckets", projectList(newProjectJSON), `{"success":true,"datas":null}`, nil, 0},
		{"malformed buckets", projectList(newProjectJSON), `{"success":true,"datas":{}}`, nil, 0},
		{"missing project", `{"success":true,"datas":[]}`, "", vngcloud.ErrNotFound, 0},
		{"incomplete projects", `{"success":true,"datas":[],"isNext":true}`, "", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &projectWriteServer{before: tc.before, buckets: tc.buckets}
			c := newTestClient(t, s.handler(t))
			out, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
			if s.deletes != tc.deleted {
				t.Fatalf("deletes %d", s.deletes)
			}
			if tc.deleted == 1 {
				if err != nil || out == nil || len(s.deleteBody) != 0 || s.lists != 2 {
					t.Fatalf("output %+v error %v body %#v", out, err, s.deleteBody)
				}
			} else if err == nil || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatal(err)
			}
		})
	}
}

func TestProjectWaitBounds(t *testing.T) {
	s := &projectWriteServer{before: projectList(newProjectJSON), after: projectList(newProjectJSON)}
	c := newTestClient(t, s.handler(t))
	elapsed := fakeProjectClock(c)
	_, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
	if !errors.Is(err, ErrNotSettled) || *elapsed != 60*time.Second || s.deletes != 1 {
		t.Fatalf("error %v elapsed %s deletes %d", err, *elapsed, s.deletes)
	}
}

func TestCreateProjectWaitAndNoWait(t *testing.T) {
	pending := strings.Replace(newProjectJSON, `"status":1`, `"status":2`, 1)
	for _, tc := range []struct {
		name    string
		noWait  bool
		after   string
		elapsed time.Duration
	}{
		{"pending timeout", false, projectList(pending), 120 * time.Second},
		{"missing renewal timeout", false, projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false,`, "", 1)), 120 * time.Second},
		{"no wait pending", true, projectList(pending), 0},
		{"unknown status timeout", false, projectList(strings.Replace(newProjectJSON, `"status":1`, `"status":999`, 1)), 120 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &projectWriteServer{order: `{"success":true,"data":` + newProjectJSON + `}`, after: tc.after}
			c := newTestClient(t, s.handler(t))
			elapsed := fakeProjectClock(c)
			in := validProjectCreate()
			in.NoWait = tc.noWait
			out, err := c.CreateProject(context.Background(), in)
			if (!tc.noWait && !errors.Is(err, ErrNotSettled)) || (tc.noWait && err != nil) || out == nil || out.Project == nil || *elapsed != tc.elapsed || s.orders != 1 {
				t.Fatalf("output %+v error %v elapsed %s orders %d", out, err, *elapsed, s.orders)
			}
			if *elapsed > 0 && s.lists != int(tc.elapsed/(2*time.Second))+1 {
				t.Fatalf("lists %d", s.lists)
			}
		})
	}
}

func TestCreateProjectWaitSettles(t *testing.T) {
	s := &projectWriteServer{order: `{"success":true,"data":` + newProjectJSON + `}`, after: projectList(strings.Replace(newProjectJSON, `"status":1`, `"status":2`, 1))}
	c := newTestClient(t, s.handler(t))
	elapsed := fakeProjectClock(c)
	originalSleep := c.sleep
	c.sleep = func(ctx context.Context, d time.Duration) error {
		s.after = projectList(newProjectJSON)
		return originalSleep(ctx, d)
	}
	out, err := c.CreateProject(context.Background(), validProjectCreate())
	if err != nil || out.Project.Status != 1 || *elapsed != 2*time.Second || s.orders != 1 {
		t.Fatalf("output %+v error %v elapsed %s", out, err, *elapsed)
	}
}

func TestProjectCancellationAfterWrite(t *testing.T) {
	for _, create := range []bool{true, false} {
		s := &projectWriteServer{order: `{"success":true,"data":` + newProjectJSON + `}`, before: projectList(newProjectJSON), after: projectList(newProjectJSON)}
		if create {
			s.before = ""
			s.after = projectList(strings.Replace(newProjectJSON, `"status":1`, `"status":2`, 1))
		}
		c := newTestClient(t, s.handler(t))
		fakeProjectClock(c)
		ctx, cancel := context.WithCancel(context.Background())
		c.sleep = func(context.Context, time.Duration) error { cancel(); return ctx.Err() }
		var err error
		if create {
			_, err = c.CreateProject(ctx, validProjectCreate())
		} else {
			_, err = c.DeleteProject(ctx, &DeleteProjectInput{ProjectID: "new-1"})
		}
		if !errors.Is(err, ErrNotSettled) || !errors.Is(err, context.Canceled) || s.orders+s.deletes != 1 {
			t.Fatalf("create %v error %v writes %d", create, err, s.orders+s.deletes)
		}
	}
}

func TestDeleteProjectOnce(t *testing.T) {
	for _, status := range []int{401, 429, 503, 302, 204} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := &projectWriteServer{before: projectList(newProjectJSON), status: status}
			c := New(testutil.NewRetryConfig(t, s.handler(t)))
			fakeProjectClock(c)
			_, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
			if err == nil || s.deletes != 1 {
				t.Fatalf("error %v deletes %d", err, s.deletes)
			}
		})
	}
	for _, body := range []string{" ", "malformed", `{"success":false,"code":403,"errorMsg":"https://checkout.example/secret"}`, `{"data":{}}`} {
		s := &projectWriteServer{before: projectList(newProjectJSON), order: body}
		c := newTestClient(t, s.handler(t))
		_, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
		if err == nil || s.deletes != 1 || strings.Contains(err.Error(), "checkout.example") {
			t.Fatalf("error %v deletes %d", err, s.deletes)
		}
	}
}

func TestDeleteProjectNoWaitAndPathGuard(t *testing.T) {
	s := &projectWriteServer{before: projectList(newProjectJSON)}
	c := newTestClient(t, s.handler(t))
	_, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "../other"})
	if !errors.Is(err, vngcloud.ErrInvalidInput) || s.lists != 0 {
		t.Fatalf("error %v lists %d", err, s.lists)
	}
	_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1", NoWait: true})
	if err != nil || s.deletes != 1 || s.lists != 1 {
		t.Fatalf("error %v deletes %d lists %d", err, s.deletes, s.lists)
	}
}

func TestProjectConfirmReadFailure(t *testing.T) {
	for _, create := range []bool{true, false} {
		s := &projectWriteServer{before: projectList(newProjectJSON), after: `{"success":false,"code":403,"errorMsg":"denied"}`}
		if create {
			s.before = ""
		}
		c := newTestClient(t, s.handler(t))
		var err error
		if create {
			_, err = c.CreateProject(context.Background(), validProjectCreate())
		} else {
			_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
		}
		if !errors.Is(err, ErrNotSettled) || !errors.Is(err, vngcloud.ErrPermission) {
			t.Fatal(err)
		}
	}
}

func TestProjectRawFixtureExtensions(t *testing.T) {
	c := newTestClient(t, serve(t, func(w http.ResponseWriter, r *http.Request) {
		testutil.WriteFixture(t, w, fixtures+"list_projects.json")
	}))
	out, err := c.ListProjects(context.Background(), nil)
	if err != nil || len(out.Items) != 1 {
		t.Fatalf("output %+v error %v", out, err)
	}
	p := out.Items[0]
	if p.ProjectType != 1 || p.ProjectTypeName != "Gold" || p.PurchaseTypeID != 4 || p.PurchaseTypeName != "Pay monthly" || p.EnableAutoRenew == nil || *p.EnableAutoRenew || p.AutoRenewPeriod == nil || *p.AutoRenewPeriod != 0 {
		t.Fatalf("project %+v", p)
	}
}

func TestCreateProjectSyntheticOrderFixture(t *testing.T) {
	s := &projectWriteServer{order: testutil.FixtureBody(t, fixtures+"project_order_synthetic_redirect.json"), after: projectList(newProjectJSON)}
	c := newTestClient(t, s.handler(t))
	out, err := c.CreateProject(context.Background(), validProjectCreate())
	if err != nil || out.Project == nil || out.OrderID != "" {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestCreateProjectReturnedRenewal(t *testing.T) {
	for _, noWait := range []bool{false, true} {
		s := &projectWriteServer{order: `{"success":true,"data":` + strings.Replace(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true`, 1) + `}`, after: projectList(newProjectJSON)}
		c := newTestClient(t, s.handler(t))
		in := validProjectCreate()
		in.NoWait = noWait
		_, err := c.CreateProject(context.Background(), in)
		if !errors.Is(err, ErrNotSettled) || s.orders != 1 {
			t.Fatalf("error %v orders %d", err, s.orders)
		}
	}
}

func TestCreateProjectCatalogGuards(t *testing.T) {
	for _, tc := range []struct{ name, path, body string }{
		{"disabled type", "/internal/v1/billing/project_types", strings.Replace(pricingTypes, `"status":1`, `"status":0`, 1)},
		{"wrong period", "/internal/v1/billing/project_types", strings.Replace(pricingTypes, `[1,3]`, `[3]`, 1)},
		{"missing group", "/internal/v1/billing/project_types", strings.Replace(pricingTypes, `"group":"Gold",`, "", 1)},
		{"missing monthly purchase", "/internal/v1/billing/purchase_types", `{"success":true,"datas":[]}`},
		{"ambiguous monthly purchase", "/internal/v1/billing/purchase_types", `{"success":true,"datas":[{"id":4,"name":"Normal","title":"Pay monthly","status":1},{"id":4,"name":"Normal","title":"Pay monthly","status":1}]}`},
		{"null quote", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":null}}`},
		{"negative quote", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":-1}}`},
		{"malformed quote", "/billing-api/v2/price", `{"success":true,"data":{"optimumPrice":"NaN"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &projectWriteServer{overrides: map[string]string{tc.path: tc.body}}
			c := newTestClient(t, s.handler(t))
			_, err := c.CreateProject(context.Background(), validProjectCreate())
			if err == nil || s.orders != 0 {
				t.Fatalf("error %v orders %d", err, s.orders)
			}
		})
	}
}

func TestProjectInvalidInputMakesNoRequests(t *testing.T) {
	calls := 0
	c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	for _, in := range []*CreateProjectInput{nil, {}, {Name: "example", Type: "Gold", QuotaGB: 30, MaxPrice: math.Inf(-1)}, {Name: "example", Type: "Gold", QuotaGB: -1, MaxPrice: 30000}} {
		_, err := c.CreateProject(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	for _, in := range []*DeleteProjectInput{nil, {}, {ProjectID: ".."}, {ProjectID: "project/1"}} {
		_, err := c.DeleteProject(context.Background(), in)
		if !errors.Is(err, vngcloud.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatalf("requests %d", calls)
	}
}

func TestProjectResponsesNeverReachCapture(t *testing.T) {
	s := &projectWriteServer{after: projectList(newProjectJSON)}
	captures := 0
	c := New(testutil.NewConfigWithCapture(t, s.handler(t), func(r transport.Capture) {
		if r.Method != http.MethodGet && strings.Contains(r.URL, "/internal/") {
			captures++
		}
	}))
	_, err := c.CreateProject(context.Background(), validProjectCreate())
	if err != nil {
		t.Fatal(err)
	}
	s.before = projectList(newProjectJSON)
	s.after = ""
	s.orders = 0
	_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
	if err != nil || captures != 0 {
		t.Fatalf("error %v write captures %d", err, captures)
	}
}

type projectRoundTripFunc func(*http.Request) (*http.Response, error)

func (f projectRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProjectOnceOnDialAndNetworkFailure(t *testing.T) {
	for _, create := range []bool{true, false} {
		for _, failure := range []error{&net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic dial failure")}, io.ErrUnexpectedEOF} {
			s := &projectWriteServer{}
			if !create {
				s.before = projectList(newProjectJSON)
			}
			h := s.handler(t)
			attempts := 0
			httpClient := &http.Client{Transport: projectRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Body != nil {
					defer func() { _ = r.Body.Close() }()
				}
				if r.URL.Host != "storage.invalid" {
					t.Fatalf("unexpected host %s", r.URL.Host)
				}
				if r.URL.Path == "/internal/v2/orders" || r.Method == http.MethodDelete {
					attempts++
					return nil, failure
				}
				recorder := httptest.NewRecorder()
				h.ServeHTTP(recorder, r)
				return recorder.Result(), nil
			})}
			cfg := core.NewTestConfig("hcm-3", "", endpoints.Set{Storage: "https://storage.invalid/"}, transport.New(transport.Config{HTTPClient: httpClient, RetryCount: 3, RetryInterval: time.Millisecond}))
			c := New(cfg)
			fakeProjectClock(c)
			var err error
			if create {
				_, err = c.CreateProject(context.Background(), validProjectCreate())
			} else {
				_, err = c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
			}
			if !errors.Is(err, ErrNotSettled) || attempts != 1 {
				t.Fatalf("create %v error %v attempts %d", create, err, attempts)
			}
		}
	}
}

func TestCreateProjectUnclassifiedData(t *testing.T) {
	for _, data := range []string{`{}`, `[]`, `"unrecognized"`} {
		s := &projectWriteServer{order: `{"success":true,"data":` + data + `}`}
		c := newTestClient(t, s.handler(t))
		_, err := c.CreateProject(context.Background(), validProjectCreate())
		if !errors.Is(err, ErrNotSettled) || s.lists != 2 {
			t.Fatalf("error %v reads %d", err, s.lists)
		}
	}
}

func TestDeleteProjectRejectsWrongRegion(t *testing.T) {
	s := &projectWriteServer{before: projectList(strings.Replace(newProjectJSON, `"regionId":"<region-id-2>"`, `"regionId":"other"`, 1))}
	c := newTestClient(t, s.handler(t))
	_, err := c.DeleteProject(context.Background(), &DeleteProjectInput{ProjectID: "new-1"})
	if err == nil || s.deletes != 0 {
		t.Fatalf("error %v deletes %d", err, s.deletes)
	}
}

func TestCreateProjectEchoedIdentityCannotSelectDifferentID(t *testing.T) {
	s := &projectWriteServer{order: `{"success":true,"data":` + newProjectJSON + `}`, after: projectList(strings.Replace(newProjectJSON, "new-1", "new-2", 1))}
	c := newTestClient(t, s.handler(t))
	fakeProjectClock(c)
	out, err := c.CreateProject(context.Background(), validProjectCreate())
	if !errors.Is(err, ErrNotSettled) || out.Project != nil {
		t.Fatalf("output %+v error %v", out, err)
	}
}

func TestCreateProjectQuotaConfirmationDoesNotRound(t *testing.T) {
	s := &projectWriteServer{after: projectList(strings.Replace(newProjectJSON, `"totalQuota":30`, `"totalQuota":30.000000000000000000001`, 1))}
	c := newTestClient(t, s.handler(t))
	_, err := c.CreateProject(context.Background(), validProjectCreate())
	if !errors.Is(err, ErrNotSettled) {
		t.Fatal("rounded quota passed confirmation")
	}
}

func TestProjectNotSettledMessageDoesNotClaimAcceptance(t *testing.T) {
	s := &projectWriteServer{order: " "}
	c := newTestClient(t, s.handler(t))
	_, err := c.CreateProject(context.Background(), validProjectCreate())
	if err == nil || strings.Contains(err.Error(), "delete accepted") {
		t.Fatal("uncertain order message claimed acceptance")
	}
}

func TestCreateProjectRejectsMalformedProjectIdentity(t *testing.T) {
	s := &projectWriteServer{after: projectList(strings.Replace(newProjectJSON, `"projectId":"new-1"`, `"projectId":"https://checkout.example/secret"`, 1))}
	c := newTestClient(t, s.handler(t))
	out, err := c.CreateProject(context.Background(), validProjectCreate())
	if !errors.Is(err, ErrNotSettled) || out.Project != nil {
		t.Fatal("malformed project ID passed confirmation")
	}
}

func TestStorageNotSettledMessage(t *testing.T) {
	if got := ErrNotSettled.Error(); got != "storage: write accepted but not settled" {
		t.Fatalf("message = %q", got)
	}
}

func TestProjectQuotaLimitMessages(t *testing.T) {
	for _, quota := range []int64{29, 2000001} {
		for _, method := range []string{"quote", "create"} {
			t.Run(fmt.Sprintf("%s/%d", method, quota), func(t *testing.T) {
				s := &projectWriteServer{}
				c := newTestClient(t, s.handler(t))
				in := validProjectCreate()
				in.QuotaGB = quota
				var err error
				if method == "quote" {
					_, err = c.QuoteCreateProject(context.Background(), in)
				} else {
					_, err = c.CreateProject(context.Background(), in)
				}
				want := "QuotaGB 29 is below the region minimum of 30 GB"
				if quota > 30 {
					want = "QuotaGB 2000001 is above the region maximum of 2000000 GB"
				}
				if !errors.Is(err, vngcloud.ErrInvalidInput) || !strings.Contains(err.Error(), want) || s.prices != 0 || s.orders != 0 {
					t.Fatalf("error %v prices %d orders %d", err, s.prices, s.orders)
				}
			})
		}
	}
}
