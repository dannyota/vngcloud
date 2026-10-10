//go:build livewrite

package vngcloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/storage"
)

// No transaction read exists. Cash equality cannot rule out offsetting
// unrelated spending or credits; the manager reconciles those separately.
func TestLiveWriteStorageAutoRenew(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_STORAGE_AUTO_RENEW") != "1" {
		t.Skip("auto-renew live-write gates are off")
	}
	projectID := os.Getenv("VNGCLOUD_LIVE_STORAGE_PROJECT_ID")
	if projectID == "" {
		t.Fatal("fail: throwaway project is required")
	}
	if envfile.Load(".env") != nil {
		t.Fatal("fail: credentials unavailable")
	}
	dir := filepath.Join(storageProjectCaptureDir, "auto-renew")
	if os.MkdirAll(dir, 0o700) != nil {
		t.Fatal("fail: private report directory unavailable")
	}
	capture := &autoRenewLiveTransport{base: http.DefaultTransport, dir: dir, projectID: projectID}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"), vngcloud.WithConfigFile(emptyWriteFile(t, "config")), vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")), vngcloud.WithTransport(capture))
	if err != nil {
		t.Fatal("fail: configuration unavailable")
	}
	client := storage.New(cfg)
	money := billing.New(cfg)
	report := map[string]any{"projectId": projectID, "region": "HCM04", "transactionReadAvailable": false}
	save := func() bool {
		raw, encodeErr := json.MarshalIndent(report, "", "  ")
		if encodeErr != nil || os.WriteFile(filepath.Join(dir, "report.json"), raw, 0o600) != nil {
			t.Error("fail: private report write")
			return false
		}
		return true
	}
	baseline, err := client.GetProjectAutoRenew(ctx, &storage.GetProjectAutoRenewInput{Region: "HCM04", ProjectID: projectID})
	if err != nil || baseline == nil || baseline.State == nil || !strings.HasPrefix(baseline.State.ProjectName, "vngcloud-live-") || baseline.State.Enabled == nil || *baseline.State.Enabled || baseline.State.PriceStatus != "Quoted" {
		t.Fatal("fail: throwaway project preflight")
	}
	report["baseline"] = baseline.State
	buckets, err := client.ListBuckets(ctx, &storage.ListBucketsInput{Region: "HCM04", ProjectID: projectID})
	if err != nil || buckets == nil || len(buckets.Items) != 0 || !capture.emptyBuckets() {
		t.Fatal("fail: empty bucket preflight")
	}
	if !save() {
		return
	}
	capture.authorize()
	disableAttempted := false
	toggle := func(callCtx context.Context, label string, enabled bool, period *int, capValue float64) (*storage.PutProjectAutoRenewOutput, error) {
		before, cashErr := storageProjectCash(callCtx, money)
		if cashErr != nil {
			return nil, errors.New("baseline cash unavailable")
		}
		record := map[string]any{"beforeCash": before}
		report[label] = record
		if !save() {
			return nil, errors.New("private report unavailable")
		}
		if !enabled {
			disableAttempted = true
		}
		out, writeErr := client.PutProjectAutoRenew(callCtx, &storage.PutProjectAutoRenewInput{Region: "HCM04", ProjectID: projectID, Enabled: vngcloud.Ptr(enabled), PeriodMonths: period, MaxPrice: capValue})
		record["result"] = out
		record["acceptedAndConfirmed"] = writeErr == nil
		record["errorCode"] = vngcloud.ErrorCode(writeErr)
		record["unsettled"] = errors.Is(writeErr, storage.ErrNotSettled)
		after, afterErr := storageProjectCash(callCtx, money)
		if afterErr == nil {
			record["afterCash"] = after
			record["cashUnchanged"] = before == after
		}
		if !save() {
			return out, errors.New("private report unavailable")
		}
		if writeErr != nil {
			return out, writeErr
		}
		if afterErr != nil || before != after {
			return out, errors.New("cash reconciliation failed")
		}
		if out == nil || out.State == nil || out.State.EndBillingTime != baseline.State.EndBillingTime {
			return out, errors.New("term changed or state missing")
		}
		return out, nil
	}
	// Cleanup reads first and sends at most one disable, on the approved ID.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cleanupCancel()
		state, readErr := client.GetProjectAutoRenew(cleanupCtx, &storage.GetProjectAutoRenewInput{Region: "HCM04", ProjectID: projectID})
		report["cleanupRead"] = state
		if readErr != nil || state == nil || state.State == nil || state.State.Enabled == nil {
			save()
			t.Error("fail: cleanup state unconfirmed")
			return
		}
		cleanupErr := autoRenewCleanup(state.State, disableAttempted, func() error {
			out, disableErr := toggle(cleanupCtx, "cleanupDisable", false, nil, 0)
			if disableErr != nil {
				return disableErr
			}
			if out == nil || out.State == nil || out.State.Enabled == nil || *out.State.Enabled {
				return errors.New("cleanup state unconfirmed")
			}
			return nil
		})
		if cleanupErr != nil {
			report["cleanupDisabled"] = false
			save()
			t.Error("fail: cleanup disable unconfirmed")
			return
		}

		report["cleanupDisabled"] = true
		if save() {
			t.Log("pass: cleanup disabled")
		}
	})
	enabled, err := toggle(ctx, "enableOneMonth", true, vngcloud.Ptr(1), *baseline.State.QuotedRenewalCharge)
	if err != nil || enabled == nil || !enabled.Changed || enabled.State == nil || enabled.State.PeriodMonths == nil || *enabled.State.PeriodMonths != 1 {
		t.Fatal("fail: one-month enable")
	}
	t.Log("pass: one-month enable")
	preview, err := client.GetProjectAutoRenew(ctx, &storage.GetProjectAutoRenewInput{Region: "HCM04", ProjectID: projectID, PeriodMonths: vngcloud.Ptr(3)})
	if err != nil || preview == nil || preview.State == nil || preview.State.QuotedRenewalCharge == nil {
		t.Fatal("fail: three-month preview")
	}
	updated, err := toggle(ctx, "updateThreeMonths", true, vngcloud.Ptr(3), *preview.State.QuotedRenewalCharge)
	if err != nil || updated == nil || updated.State == nil || updated.State.PeriodMonths == nil || *updated.State.PeriodMonths != 3 {
		t.Fatal("fail: three-month update; refusal recorded privately")
	}
	t.Log("pass: three-month update")
	disabled, err := toggle(ctx, "disable", false, nil, 0)
	if err != nil || disabled == nil || disabled.State == nil || disabled.State.Enabled == nil || *disabled.State.Enabled || disabled.State.PeriodMonths != nil {
		t.Fatal("fail: disable")
	}
	t.Log("pass: disable")
}

type autoRenewLiveTransport struct {
	base                   http.RoundTripper
	dir, projectID         string
	mu                     sync.Mutex
	approved, bucketsEmpty bool
	count                  int
}

func (c *autoRenewLiveTransport) authorize() { c.mu.Lock(); defer c.mu.Unlock(); c.approved = true }
func (c *autoRenewLiveTransport) emptyBuckets() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bucketsEmpty
}

func (c *autoRenewLiveTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	if req.Method == http.MethodDelete || (req.Method == http.MethodPost && path == "/internal/v2/orders") {
		return nil, errors.New("unapproved project mutation")
	}
	if req.Method == http.MethodPut {
		c.mu.Lock()
		approved := c.approved
		c.mu.Unlock()
		if !approved || path != "/gateway/api/v1/resources/autoRenew" {
			return nil, errors.New("unapproved setting")
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, errors.New("setting body unavailable")
		}
		if err = req.Body.Close(); err != nil {
			return nil, errors.New("setting body close failed")
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var rows []struct {
			Product      string `json:"product"`
			ArtifactType string `json:"artifactType"`
			ArtifactID   string `json:"artifactId"`
		}
		if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 || rows[0].Product != "vstorage" || rows[0].ArtifactType != "object-storage" || rows[0].ArtifactID != c.projectID {
			return nil, errors.New("setting named unapproved resource")
		}
	}
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	capture := path == "/gateway/api/v1/resources" || path == "/gateway/api/v1/resources/autoRenew" || path == "/gateway/api/v1/home/user-info" || path == "/navbar/balances/v1" || strings.HasPrefix(path, "/internal/v1/") || strings.HasPrefix(path, "/billing-api/")
	if !capture {
		return resp, nil
	}
	raw, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.New("private response capture failed")
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.count++
	if path == "/internal/v1/ceph/projects/"+c.projectID {
		var env struct {
			Success *bool           `json:"success"`
			Data    json.RawMessage `json:"data"`
			Datas   json.RawMessage `json:"datas"`
			IsNext  bool            `json:"isNext"`
		}
		c.bucketsEmpty = json.Unmarshal(raw, &env) == nil && env.Success != nil && *env.Success && !env.IsNext
		present := false
		for _, list := range []json.RawMessage{env.Data, env.Datas} {
			if len(list) == 0 {
				continue
			}
			present = true
			var items []json.RawMessage
			if json.Unmarshal(list, &items) != nil || items == nil || len(items) != 0 {
				c.bucketsEmpty = false
			}
		}
		// The observed empty-region envelope can omit both array keys.
		if !present {
			c.bucketsEmpty = c.bucketsEmpty && storageProjectListComplete(raw)
		}
	}
	// A complete regional list is required before the joined SDK read proceeds.
	if path == "/internal/v1/projects" && !storageProjectListComplete(raw) {
		return nil, errors.New("private project list incomplete")
	}
	record := map[string]any{"path": path, "status": resp.StatusCode, "body": json.RawMessage(raw)}
	data, encodeErr := json.MarshalIndent(record, "", "  ")
	if encodeErr != nil {
		data, encodeErr = json.Marshal(map[string]any{"path": path, "status": resp.StatusCode, "bodyText": string(raw)})
	}
	if encodeErr != nil || os.WriteFile(filepath.Join(c.dir, autoRenewCaptureName(c.count)), data, 0o600) != nil {
		return nil, errors.New("private capture write failed")
	}
	return resp, nil
}

func autoRenewCaptureName(count int) string { return "response-" + strconv.Itoa(count) + ".json" }

func TestStorageAutoRenewCleanupNoRetry(t *testing.T) {
	for _, tc := range []struct {
		enabled, attempted bool
		calls              int
		failed             bool
	}{
		{false, false, 0, false}, {true, false, 1, false}, {true, true, 0, true},
	} {
		calls := 0
		err := autoRenewCleanup(&storage.ProjectAutoRenew{Enabled: vngcloud.Ptr(tc.enabled)}, tc.attempted, func() error { calls++; return nil })
		if calls != tc.calls || (err != nil) != tc.failed {
			t.Fatal("cleanup repeated a disable or missed enabled state")
		}
	}
	if autoRenewCleanup(nil, false, func() error { t.Error("cleanup wrote unknown state"); return nil }) == nil {
		t.Fatal("unknown state accepted")
	}
}

func TestStorageAutoRenewLiveTransportScope(t *testing.T) {
	calls := 0
	c := &autoRenewLiveTransport{dir: t.TempDir(), projectID: "project-1", base: autoRenewLiveHTTP(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":200,"data":{"successAll":true,"errorAutoRenewResources":[]}}`))}, nil
	})}
	for _, tc := range []struct {
		method, path, body string
		approved           bool
	}{
		{"DELETE", "/internal/v1/projects/project-1", `{}`, true},
		{"POST", "/internal/v2/orders", `{}`, true},
		{"PUT", "/gateway/api/v1/resources/autoRenew", `[{"product":"vstorage","artifactType":"object-storage","artifactId":"project-1"}]`, false},
		{"PUT", "/gateway/api/v1/resources/autoRenew", `[{"product":"vstorage","artifactType":"object-storage","artifactId":"other"}]`, true},
	} {
		c.approved = tc.approved
		r, err := http.NewRequestWithContext(context.Background(), tc.method, "https://synthetic.invalid"+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = c.RoundTrip(r); err == nil || calls != 0 {
			t.Fatal("live transport sent unapproved mutation")
		}
	}
}

type autoRenewLiveHTTP func(*http.Request) (*http.Response, error)

func (f autoRenewLiveHTTP) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func autoRenewCleanup(state *storage.ProjectAutoRenew, disableAttempted bool, disable func() error) error {
	if state == nil || state.Enabled == nil {
		return errors.New("cleanup state unconfirmed")
	}
	if !*state.Enabled {
		return nil
	}
	if disableAttempted {
		return errors.New("disable already attempted; reconcile before another setting")
	}
	return disable()
}
