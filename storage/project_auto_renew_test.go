package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/testutil"
)

type autoRenewServer struct {
	enabled                                     bool
	months                                      int
	channel                                     any
	overrides                                   map[string]string
	putBody                                     string
	putStatus                                   int
	puts, accounts, projects, resources, prices int
	stale, lose                                 bool
}

func (s *autoRenewServer) handler(t *testing.T) http.Handler {
	return serve(t, func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case "/gateway/api/v1/resources":
			s.resources++
			period := "null"
			renew := "MANUAL"
			if s.enabled {
				renew = "AUTO-RENEW"
				period = strconv.Itoa(s.months)
			}
			channel := s.channel
			if channel == nil {
				channel = 0
			}
			ch, _ := json.Marshal(channel)
			body = fmt.Sprintf(`{"code":200,"data":{"data":[{"artifactId":"new-1","artifactType":"object-storage","product":"vstorage","renewType":%q,"billingType":"PREPAID","channel":%s,"isRenewing":false,"endBillingTime":1890000000000,"renewPeriod":%s}]}}`, renew, ch, period)
		case "/gateway/api/v1/home/user-info":
			s.accounts++
			body = testutil.FixtureBody(t, "../testdata/billing/ResourceUserInfo.json")
		case "/gateway/api/v1/resources/autoRenew":
			s.puts++
			if r.Method != http.MethodPut || r.Header.Get("portal-user-id") != "12345" || r.Header.Get("Content-Type") != "application/json" {
				t.Error("PUT method or identity headers")
			}
			var got []struct {
				Product      string `json:"product"`
				ArtifactType string `json:"artifactType"`
				ArtifactID   string `json:"artifactId"`
				Channel      int    `json:"channel"`
				Info         struct {
					Enabled bool `json:"isEnable"`
					Period  int  `json:"period"`
				} `json:"autoRenewInfo"`
			}
			if json.NewDecoder(r.Body).Decode(&got) != nil || len(got) != 1 {
				t.Fatal("PUT not single resource array")
			}
			b, _ := json.Marshal(got)
			s.putBody = string(b)
			if got[0].Product != "vstorage" || got[0].ArtifactType != "object-storage" || got[0].ArtifactID != "new-1" || got[0].Channel != 0 {
				t.Error("wrong resource identity or channel")
			}
			if !s.stale {
				s.enabled = got[0].Info.Enabled
				s.months = got[0].Info.Period / 43200
				if !s.enabled {
					s.months = 0
				}
			}
			if s.lose {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
				return
			}
			if s.putStatus != 0 {
				w.Header().Set("Location", "/forbidden")
				w.WriteHeader(s.putStatus)
			}
			body = testutil.FixtureBody(t, "../testdata/billing/PutResourceAutoRenew.json")
		case "/internal/v1/projects":
			s.projects++
			body = projectList(strings.Replace(newProjectJSON, `"enableAutoRenew":false,"autoRenewPeriod":0`, fmt.Sprintf(`"enableAutoRenew":%t,"autoRenewPeriod":%d`, s.enabled, s.months), 1))
		default:
			if strings.HasPrefix(r.URL.Path, "/gateway/") {
				t.Error("unexpected billing route")
			}
			if strings.HasPrefix(r.URL.Path, "/billing-api/v2/price") {
				s.prices++
			}
			fallback := (&projectWriteServer{overrides: s.overrides}).handler(t)
			fallback.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/gateway/") {
			for _, key := range []string{"region", "region_id", "project-id"} {
				if r.Header.Get(key) != "" {
					t.Error("billing sent regional header")
				}
			}
			if r.URL.RawQuery != "" {
				t.Error("billing query")
			}
		} else {
			checkRegionHeaders(t, r, "<region-id-2>")
		}
		if replacement, ok := s.overrides[r.URL.Path]; ok {
			body = replacement
		}
		_, _ = w.Write([]byte(body))
	})
}

func autoRenewClient(t *testing.T, s *autoRenewServer) *Client {
	c := New(testutil.NewRetryConfig(t, s.handler(t)))
	now := time.UnixMilli(1790000000000)
	c.now = func() time.Time { return now }
	c.sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	return c
}

func TestProjectAutoRenewEnableDisable(t *testing.T) {
	s := &autoRenewServer{}
	c := autoRenewClient(t, s)
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
	if err != nil || !out.Changed || out.State == nil || !*out.State.Enabled || *out.State.PeriodMonths != 1 || *out.State.NextCharge != 30000 || out.State.EndTime.Location() != time.UTC {
		t.Fatalf("enable: %+v %v", out, err)
	}
	if s.puts != 1 || s.accounts != 1 || s.projects != 2 || s.resources != 2 || s.prices != 1 || !strings.Contains(s.putBody, `"period":43200`) {
		t.Fatalf("attempt counts: %+v", s)
	}
	out, err = c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(false)})
	if err != nil || !out.Changed || *out.State.Enabled || out.State.PeriodMonths != nil || out.State.NextCharge != nil || out.State.MonthlyPrice != nil || out.State.PriceStatus != "Unavailable" {
		t.Fatalf("disable: %+v %v", out, err)
	}
	if s.puts != 2 || s.accounts != 2 || s.prices != 1 {
		t.Fatal("disable quoted or identity reused")
	}
}

func TestProjectAutoRenewPreviewNoopPeriod(t *testing.T) {
	s := &autoRenewServer{enabled: true, months: 3}
	c := autoRenewClient(t, s)
	got, err := c.GetProjectAutoRenew(context.Background(), &GetProjectAutoRenewInput{ProjectID: "new-1", PeriodMonths: vngcloud.Ptr(6)})
	if err != nil || *got.State.NextCharge != 90000 || *got.State.QuotedRenewalCharge != 180000 {
		t.Fatalf("preview: %+v %v", got, err)
	}
	noop, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true)})
	if err != nil || noop.Changed || s.puts != 0 || s.accounts != 0 {
		t.Fatal("no-op wrote or required consent")
	}
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), PeriodMonths: vngcloud.Ptr(12), MaxPrice: 360000})
	if err != nil || !out.Changed || *out.State.PeriodMonths != 12 || !strings.Contains(s.putBody, `"period":518400`) {
		t.Fatalf("period change: %v", err)
	}
}

func TestProjectAutoRenewInvalidInput(t *testing.T) {
	for _, in := range []*PutProjectAutoRenewInput{nil, {ProjectID: "new-1"}, {ProjectID: "../new", Enabled: vngcloud.Ptr(false)}, {ProjectID: "new-1", Enabled: vngcloud.Ptr(false), PeriodMonths: vngcloud.Ptr(1)}, {ProjectID: "new-1", Enabled: vngcloud.Ptr(true), PeriodMonths: vngcloud.Ptr(2)}, {ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: -1}} {
		c := newTestClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("invalid input sent request") }))
		if _, err := c.PutProjectAutoRenew(context.Background(), in); !errors.Is(err, core.ErrInvalidInput) {
			t.Fatalf("input: %v", err)
		}
	}
}

func TestProjectAutoRenewSendOnce(t *testing.T) {
	for _, status := range []int{200, 400, 401, 403, 404, 429, 500, 503, 307} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			s := &autoRenewServer{putStatus: status}
			c := autoRenewClient(t, s)
			out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			if s.puts != 1 || s.accounts != 1 || out == nil || out.State == nil {
				t.Fatalf("attempt count %d", s.puts)
			}
			if status == 200 {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || ((status/100 != 4) != errors.Is(err, ErrNotSettled)) {
				t.Fatalf("status error %v", err)
			}
		})
	}
}

func TestProjectAutoRenewNoRenewalInProgress(t *testing.T) {
	fixture := testutil.FixtureBody(t, "../testdata/billing/ListResourcesManualNullRenewing.json")
	for _, tc := range []struct {
		name, body string
	}{
		{"null", fixture},
		{"absent", strings.ReplaceAll(fixture, `"isRenewing": null,`, "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &autoRenewServer{overrides: map[string]string{"/gateway/api/v1/resources": tc.body}}
			base := s.handler(t)
			cfg := testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					delete(s.overrides, "/gateway/api/v1/resources")
				}
				base.ServeHTTP(w, r)
			}))
			c := autoRenewClient(t, s)
			c.c = core.ClientOf(cfg)
			out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			if err != nil || !out.Changed || out.State == nil || out.State.Enabled == nil || !*out.State.Enabled || s.puts != 1 || s.accounts != 1 {
				t.Fatalf("enable without renewal in progress: %+v error %v, PUTs %d, account reads %d", out, err, s.puts, s.accounts)
			}
		})
	}
}

func TestProjectAutoRenewAccountIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		accepted   bool
	}{
		{"account only", testutil.FixtureBody(t, "../testdata/billing/ResourceUserInfo.json"), true},
		{"user only", testutil.FixtureBody(t, "../testdata/billing/ResourceUserInfoIAM.json"), true},
		{"both equal", `{"code":200,"data":{"accountId":12345,"userId":12345.0}}`, true},
		{"both different", `{"code":200,"data":{"accountId":12345,"userId":54321}}`, false},
		{"neither", `{"code":200,"data":{}}`, false},
		{"null account with user", `{"code":200,"data":{"accountId":null,"userId":12345}}`, false},
		{"account with invalid user", `{"code":200,"data":{"accountId":12345,"userId":1.5}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &autoRenewServer{overrides: map[string]string{"/gateway/api/v1/home/user-info": tc.body}}
			out, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			if tc.accepted {
				if err != nil || !out.Changed || s.puts != 1 || s.accounts != 1 {
					t.Fatalf("valid identity refused: %v, PUTs %d, account reads %d", err, s.puts, s.accounts)
				}
				return
			}
			var api *core.APIError
			if !errors.As(err, &api) || api.Operation != "billingresources.GetAccount" || api.Code != "InvalidResponse" || api.Message != "billing response had invalid structure" || s.puts != 0 || s.accounts != 1 {
				t.Fatalf("invalid identity accepted: %v, PUTs %d, account reads %d", err, s.puts, s.accounts)
			}
		})
	}
}

func TestProjectAutoRenewGuards(t *testing.T) {
	baseResource := `{"code":200,"data":{"data":[{"artifactId":"new-1","artifactType":"object-storage","product":"vstorage","renewType":"MANUAL","billingType":"PREPAID","channel":0,"isRenewing":false,"endBillingTime":1890000000000,"renewPeriod":null}]}}`
	for _, tc := range []struct {
		name, path, body string
		missing          bool
	}{
		{"no project", "/internal/v1/projects", `{"success":true,"datas":[]}`, true},
		{"duplicate project", "/internal/v1/projects", projectList(newProjectJSON + "," + newProjectJSON), false},
		{"partial projects", "/internal/v1/projects", `{"success":true,"datas":[],"isNext":true}`, false},
		{"conflicting lists", "/internal/v1/projects", `{"success":true,"data":[` + newProjectJSON + `],"datas":[]}`, false},
		{"wrong region", "/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, "HCM04", "HAN02")), false},
		{"missing enable", "/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":null`)), false},
		{"unknown renew", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, "MANUAL", "FUTURE"), false},
		{"fixed artifact", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, "object-storage", "OBJECT_STORAGE_6"), false},
		{"wrong product", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, "vstorage", "vmonitor"), false},
		{"missing billing type", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, `"billingType":"PREPAID"`, `"billingType":null`), false},
		{"missing channel", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, `"channel":0`, `"channel":null`), false},
		{"renewing", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, `"isRenewing":false`, `"isRenewing":true`), false},
		{"missing timestamp", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, `"endBillingTime":1890000000000`, `"endBillingTime":null`), false},
		{"expired", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, "1890000000000", "1690000000000"), false},
		{"not prepaid", "/gateway/api/v1/resources", strings.ReplaceAll(baseResource, "PREPAID", "POSTPAID"), false},
		{"fraction quota", "/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, `"totalQuota":30`, `"totalQuota":30.000000000000000001`)), false},
		{"overflow quota", "/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, `"totalQuota":30`, `"totalQuota":9223372036854775808`)), false},
		{"trial purchase", "/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, `"purchaseTypeId":4`, `"purchaseTypeId":2`)), false},
		{"fixed period", "/internal/v1/projects", projectList(strings.TrimSuffix(newProjectJSON, "}") + `,"period":6}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &autoRenewServer{overrides: map[string]string{tc.path: tc.body}}
			out, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			if err == nil || s.puts != 0 || s.accounts != 0 {
				t.Fatalf("guard wrote: %+v error %v", out, err)
			}
			if tc.missing && !errors.Is(err, core.ErrNotFound) {
				t.Fatal("missing project not NotFound")
			}
		})
	}
}

func TestProjectAutoRenewPricingFailures(t *testing.T) {
	for _, quote := range []string{`{"success":true,"data":{}}`, `{"success":true,"data":{"optimumPrice":null}}`, `{"success":true,"data":{"optimumPrice":0}}`, `{"success":true,"data":{"optimumPrice":-1}}`, `{"success":true,"data":{"optimumPrice":"NaN"}}`, `{"success":true,"data":{"optimumPrice":1e308}}`} {
		t.Run(quote, func(t *testing.T) {
			s := &autoRenewServer{overrides: map[string]string{"/billing-api/v2/price": quote}}
			c := autoRenewClient(t, s)
			out, err := c.GetProjectAutoRenew(context.Background(), &GetProjectAutoRenewInput{ProjectID: "new-1", PeriodMonths: vngcloud.Ptr(12)})
			if err != nil || out.State.PriceStatus != "Unavailable" || out.State.MonthlyPrice != nil || out.State.NextCharge != nil || out.State.Currency != "" || out.State.PriceErrorCode == "" {
				t.Fatalf("pricing error hidden: %+v %v", out, err)
			}
			_, err = c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), PeriodMonths: vngcloud.Ptr(12), MaxPrice: 1e308})
			if err == nil || s.puts != 0 {
				t.Fatal("unpriced enable wrote")
			}
		})
	}
	s := &autoRenewServer{}
	c := autoRenewClient(t, s)
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 29999})
	if !errors.Is(err, core.ErrPriceAboveMax) || s.puts != 0 || out.State.MonthlyPrice == nil {
		t.Fatal("cap bypassed or quote dropped")
	}
}

func TestProjectAutoRenewUnsettled(t *testing.T) {
	for _, tc := range []struct {
		name        string
		stale, lose bool
	}{{"stale", true, false}, {"lost", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &autoRenewServer{stale: tc.stale, lose: tc.lose}
			c := autoRenewClient(t, s)
			before := c.now()
			out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			if !errors.Is(err, ErrNotSettled) || out.Changed || out.State == nil || s.puts != 1 || c.now().Sub(before) > 30*time.Second {
				t.Fatalf("unsettled: %+v %v attempts %d", out, err, s.puts)
			}
			if tc.stale && (s.projects != 16 || s.resources != 16) {
				t.Fatalf("confirmation reads %d %d", s.projects, s.resources)
			}
		})
	}
}

func TestProjectAutoRenewPUTEnvelopes(t *testing.T) {
	for _, tc := range []struct {
		body     string
		rejected bool
	}{
		{`{}`, false}, {`{"code":200,"data":{"successAll":true}}`, false}, {`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":null}}`, false},
		{`{"code":200,"data":{"successAll":false,"errorAutoRenewResources":[]}}`, true},
		{`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[{"message":"12345 synthetic-secret"}]}}`, true},
		{testutil.FixtureBody(t, "../testdata/billing/PutResourceAutoRenewRejected.json"), true},
	} {
		for _, stale := range []bool{true, false} {
			s := &autoRenewServer{stale: stale, overrides: map[string]string{"/gateway/api/v1/resources/autoRenew": tc.body}}
			out, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
			var api *core.APIError
			if !errors.As(err, &api) || out.Changed || s.puts != 1 || strings.Contains(err.Error(), "12345") || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("unsafe PUT error %v", err)
			}
			if tc.rejected && api.Code != "AutoRenewRejected" {
				t.Fatalf("rejection code %s", api.Code)
			}
			if errors.Is(err, ErrNotSettled) != (!tc.rejected || !stale) {
				t.Fatalf("response erased: %v", err)
			}
		}
	}
}

func TestProjectAutoRenewJoinedFixtures(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "manual"
		index := 0
		if enabled {
			name = "enabled"
			index = 1
		}
		var resources map[string]any
		if json.Unmarshal([]byte(testutil.FixtureBody(t, "../testdata/billing/ListResources.json")), &resources) != nil {
			t.Fatal("invalid resource fixture")
		}
		data := resources["data"].(map[string]any)
		row := data["data"].([]any)[index].(map[string]any)
		row["artifactId"] = "new-1"
		data["data"] = []any{row}
		raw, err := json.Marshal(resources)
		if err != nil {
			t.Fatal(err)
		}
		s := &autoRenewServer{overrides: map[string]string{
			"/gateway/api/v1/resources": string(raw),
			"/internal/v1/projects":     testutil.FixtureBody(t, fixtures+"project_auto_renew_"+name+".json"),
		}}
		out, err := autoRenewClient(t, s).GetProjectAutoRenew(context.Background(), &GetProjectAutoRenewInput{ProjectID: "new-1"})
		if err != nil || out.State.Enabled == nil || *out.State.Enabled != enabled || out.State.PriceStatus != "Quoted" || *out.State.MonthlyPrice != 30000 || out.State.EndBillingTime != 1890000000000 {
			t.Fatalf("joined fixture: %+v %v", out, err)
		}
	}
}

func TestProjectAutoRenewInputJSON(t *testing.T) {
	for _, raw := range []string{`{"ProjectID":"new-1"}`, `{"ProjectID":"new-1","Enabled":false}`} {
		var in PutProjectAutoRenewInput
		if json.Unmarshal([]byte(raw), &in) != nil {
			t.Fatal("invalid JSON input")
		}
		s := &autoRenewServer{enabled: true, months: 1}
		out, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &in)
		if in.Enabled == nil {
			if !errors.Is(err, core.ErrInvalidInput) || s.projects != 0 {
				t.Fatal("omitted Enabled accepted")
			}
		} else if err != nil || !out.Changed || s.puts != 1 {
			t.Fatal("explicit false rejected")
		}
	}
}

func TestProjectAutoRenewUnknownGuardData(t *testing.T) {
	for _, tc := range []struct{ path, body string }{
		{"/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, `"status":1`, `"status":999`))},
		{"/internal/v1/projects", projectList(strings.ReplaceAll(newProjectJSON, `"purchaseTypeId":4`, `"purchaseTypeId":999`))},
		{"/gateway/api/v1/resources", `{"code":200,"data":{"data":[{"artifactId":"new-1","artifactType":"object-storage","product":"vstorage","renewType":"MANUAL","billingType":"FUTURE","channel":0,"isRenewing":false,"endBillingTime":1890000000000}]}}`},
	} {
		s := &autoRenewServer{overrides: map[string]string{tc.path: tc.body}}
		_, err := autoRenewClient(t, s).PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
		var api *core.APIError
		if !errors.As(err, &api) || s.puts != 0 {
			t.Fatal("unknown guard did not return APIError")
		}
	}
}
