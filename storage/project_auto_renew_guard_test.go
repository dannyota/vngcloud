package storage

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/internal/core"
)

func TestAutoRenewRejectionFailedReconciliation(t *testing.T) {
	for _, body := range []string{`{"success":false}`, projectList(strings.ReplaceAll(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true`))} {
		s := &autoRenewServer{stale: true, overrides: map[string]string{"/gateway/api/v1/resources/autoRenew": `{"code":200,"data":{"successAll":false,"errorAutoRenewResources":[]}}`}}
		original := s.handler(t)
		c := autoRenewClient(t, s)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.puts > 0 && r.URL.Path == "/internal/v1/projects" {
				_, _ = w.Write([]byte(body))
				return
			}
			original.ServeHTTP(w, r)
		}))
		defer server.Close()
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL, Billing: server.URL}))
		if err != nil {
			t.Fatal(err)
		}
		c.c = core.ClientOf(cfg)
		out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
		if !errors.Is(err, ErrNotSettled) || vngcloud.ErrorCode(err) != "AutoRenewRejected" || out.Changed || out.State == nil || s.puts != 1 {
			t.Fatalf("failed reconciliation erased uncertainty: %v", err)
		}
	}
}

func TestAutoRenewIndependentJoinGuards(t *testing.T) {
	resource := `{"artifactId":"new-1","artifactType":"object-storage","product":"vstorage","renewType":"AUTO-RENEW","renewPeriod":3,"endBillingTime":1890000000000}`
	for _, tc := range []struct{ project, rows string }{
		{strings.ReplaceAll(newProjectJSON, `"autoRenewPeriod":0`, `"autoRenewPeriod":3`), resource},
		{strings.ReplaceAll(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true`), strings.ReplaceAll(resource, `"renewType":"AUTO-RENEW","renewPeriod":3`, `"renewType":"MANUAL","renewPeriod":null`)},
		{strings.ReplaceAll(strings.ReplaceAll(newProjectJSON, `"enableAutoRenew":false`, `"enableAutoRenew":true`), `"autoRenewPeriod":0`, `"autoRenewPeriod":1`), resource},
		{newProjectJSON, strings.ReplaceAll(resource, `"renewType":"AUTO-RENEW","renewPeriod":3`, `"renewType":"MANUAL","renewPeriod":null`) + "," + strings.ReplaceAll(resource, `"renewType":"AUTO-RENEW","renewPeriod":3`, `"renewType":"MANUAL","renewPeriod":null`)},
	} {
		s := &autoRenewServer{overrides: map[string]string{"/internal/v1/projects": projectList(tc.project), "/gateway/api/v1/resources": `{"code":200,"data":{"data":[` + tc.rows + `]}}`}}
		c := autoRenewClient(t, s)
		if _, err := c.GetProjectAutoRenew(context.Background(), &GetProjectAutoRenewInput{ProjectID: "new-1"}); err == nil {
			t.Fatal("conflicting or duplicate join accepted")
		}
		if _, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 90000}); err == nil || s.puts != 0 {
			t.Fatal("conflicting preflight wrote")
		}
	}
}

func TestAutoRenewPropagationDisagreement(t *testing.T) {
	for _, periodUpdate := range []bool{false, true} {
		s := &autoRenewServer{}
		if periodUpdate {
			s.enabled = true
			s.months = 1
		}
		c := autoRenewClient(t, s)
		original := s.handler(t)
		lagReads := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if s.puts > 0 && r.URL.Path == "/gateway/api/v1/resources" && lagReads < 2 {
				lagReads++
				recorder := httptest.NewRecorder()
				original.ServeHTTP(recorder, r)
				var wire map[string]any
				if json.Unmarshal(recorder.Body.Bytes(), &wire) != nil {
					t.Fatal("invalid mock")
				}
				row := wire["data"].(map[string]any)["data"].([]any)[0].(map[string]any)
				row["renewType"] = "MANUAL"
				row["renewPeriod"] = nil
				if periodUpdate {
					row["renewType"] = "AUTO-RENEW"
					row["renewPeriod"] = 1
				}
				if json.NewEncoder(w).Encode(wire) != nil {
					t.Fatal("mock encoding failed")
				}
				return
			}
			original.ServeHTTP(w, r)
		}))
		defer server.Close()
		cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL, Billing: server.URL}))
		if err != nil {
			t.Fatal(err)
		}
		c.c = core.ClientOf(cfg)
		out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), PeriodMonths: vngcloud.Ptr(3), MaxPrice: 90000})
		if err != nil || !out.Changed || lagReads != 2 || s.puts != 1 || *out.State.PeriodMonths != 3 {
			t.Fatalf("propagation disagreement aborted: %v", err)
		}
	}
}

func TestAutoRenewPersistentDisagreementBound(t *testing.T) {
	s := &autoRenewServer{}
	c := autoRenewClient(t, s)
	original := s.handler(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.puts > 0 && r.URL.Path == "/gateway/api/v1/resources" {
			s.resources++
			_, _ = w.Write([]byte(`{"code":200,"data":{"data":[{"artifactId":"new-1","artifactType":"object-storage","product":"vstorage","renewType":"MANUAL","renewPeriod":null,"endBillingTime":1890000000000}]}}`))
			return
		}
		original.ServeHTTP(w, r)
	}))
	defer server.Close()
	cfg, err := vngcloud.NewConfig(vngcloud.WithRegion("hcm-3"), vngcloud.WithStaticToken("synthetic"), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{Storage: server.URL, Billing: server.URL}))
	if err != nil {
		t.Fatal(err)
	}
	c.c = core.ClientOf(cfg)
	before := c.now()
	out, err := c.PutProjectAutoRenew(context.Background(), &PutProjectAutoRenewInput{ProjectID: "new-1", Enabled: vngcloud.Ptr(true), MaxPrice: 30000})
	if !errors.Is(err, ErrNotSettled) || out.Changed || *out.State.Enabled || s.puts != 1 || c.now().Sub(before) != autoRenewConfirmBound || s.projects != 16 || s.resources != 16 {
		t.Fatal("persistent disagreement bypassed confirmation bound or replaced last valid state")
	}
}
