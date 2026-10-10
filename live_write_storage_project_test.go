//go:build livewrite

package vngcloud_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/storage"
)

const storageProjectCaptureDir = "examples/basic/output/raw/storage"
const storageProjectLeftover = "Leftover name pattern: vngcloud-live-<8 hex> in HCM04. Inspect the private storage-project-report.json, pending orders, and billing in the console before any retry."

// TestLiveWriteStorageProject consumes one paid order attempt. Gates must be
// set before loading .env so a local file cannot opt a test run into payment.
func TestLiveWriteStorageProject(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_STORAGE_PROJECT") != "1" {
		t.Skip("set both storage project live-write gates for the approved paid check")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatal("could not load live credentials")
	}
	if err := os.MkdirAll(storageProjectCaptureDir, 0o700); err != nil {
		t.Fatal("could not create private capture directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	capture := &storageProjectCaptureTransport{base: http.DefaultTransport}
	cfg, err := vngcloud.LoadConfig(ctx, vngcloud.WithRegion("hcm-3"), vngcloud.WithConfigFile(emptyWriteFile(t, "config")), vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")), vngcloud.WithTransport(capture), vngcloud.WithResponseCapture(func(r vngcloud.ResponseCapture) {
		if r.Operation != "storage.ListProjects" && r.Operation != "storage.QuoteCreateProject" && r.Operation != "storage.ListProjectTypes" && r.Operation != "billing.GetBalances" {
			return
		}
		if err := capture.save(r.Operation, r.StatusCode, r.Body); err != nil {
			t.Error("could not save private read response")
		}
	}))
	if err != nil {
		t.Fatal("could not load live configuration")
	}
	client := storage.New(cfg)
	money := billing.New(cfg)
	baseline, err := client.ListProjects(ctx, &storage.ListProjectsInput{Region: "HCM04"})
	if err != nil {
		t.Fatal("baseline project read failed")
	}
	baselineIDs := map[string]bool{}
	for _, p := range baseline.Items {
		baselineIDs[p.ID] = true
		if strings.HasPrefix(p.Name, "vngcloud-live-") {
			t.Fatal("baseline has a live project; inspect existing resources before ordering")
		}
	}
	t.Logf("baseline projects: %d", len(baseline.Items))
	if _, err := client.ListProjectTypes(ctx, &storage.ListProjectTypesInput{Region: "HCM04"}); err != nil {
		t.Fatal("fresh catalog read failed")
	}
	before, err := storageProjectCash(ctx, money)
	if err != nil {
		t.Fatal("baseline cash read failed")
	}
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatal("could not generate project suffix")
	}
	input := &storage.CreateProjectInput{Region: "HCM04", Name: "vngcloud-live-" + suffix, Type: "Gold", QuotaGB: 30, MaxPrice: 30000}
	quote, err := client.QuoteCreateProject(ctx, input)
	if err != nil || quote.TotalPrice != 30000 || quote.MonthlyPrice != 30000 {
		t.Fatal("Gold 30 GB quote was not 30000 VND")
	}
	if before < quote.TotalPrice {
		t.Fatal("cash balance is below the quoted total")
	}
	if t.Failed() {
		t.Fatal("private capture failed before order")
	}
	report := map[string]any{"region": "HCM04", "attemptedName": input.Name, "baselineCash": before, "quotedTotal": quote.TotalPrice, "baselineProjects": baseline.Items}
	saveReport := func() {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(storageProjectCaptureDir, "storage-project-report.json"), data, 0o600) != nil {
			t.Error("could not save private reconciliation report")
		}
	}
	saveReport()
	var owned *storage.Project
	var after float64
	afterErr := errors.New("post-order cash not read")
	deleted := false
	cleanup := func() {
		if owned == nil || deleted {
			return
		}
		// One cleanup attempt, including after an unexpected debit or renewal state.
		deleted = true
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer cleanupCancel()
		_, deleteErr := client.DeleteProject(cleanupCtx, &storage.DeleteProjectInput{Region: "HCM04", ProjectID: owned.ID})
		report["deleteConfirmed"] = deleteErr == nil
		if deleteErr != nil {
			saveReport()
			t.Error("cleanup was not confirmed. " + storageProjectLeftover)
			return
		}
		remaining, readErr := client.ListProjects(cleanupCtx, &storage.ListProjectsInput{Region: "HCM04"})
		if readErr != nil {
			saveReport()
			t.Error("post-delete project read failed. " + storageProjectLeftover)
			return
		}
		for _, p := range remaining.Items {
			if p.ID == owned.ID {
				saveReport()
				t.Error("project remains in active list. " + storageProjectLeftover)
				return
			}
		}
		t.Logf("projects after cleanup: %d", len(remaining.Items))
		refundDeadline := time.Now().Add(5 * time.Minute)
		for {
			finalCash, cashErr := storageProjectCash(cleanupCtx, money)
			if cashErr != nil {
				saveReport()
				t.Error("refund cash read failed. " + storageProjectLeftover)
				return
			}
			report["afterDeleteCash"] = finalCash
			if afterErr == nil {
				t.Logf("cash delta after delete: %.0f VND; net delta: %.0f VND", finalCash-after, finalCash-before)
			}
			saveReport()
			if afterErr == nil && finalCash > after && finalCash <= before {
				t.Log("cleanup status: removed from active list; free trash left for expiry")
				return
			}
			if finalCash > before || !time.Now().Before(refundDeadline) {
				t.Error("refund remains unexplained. " + storageProjectLeftover)
				return
			}
			timer := time.NewTimer(30 * time.Second)
			select {
			case <-cleanupCtx.Done():
				timer.Stop()
				t.Error("refund wait ended. " + storageProjectLeftover)
				return
			case <-timer.C:
			}
		}
	}
	t.Cleanup(cleanup)
	created, createErr := client.CreateProject(ctx, input)
	if created != nil && created.Project != nil {
		p := created.Project
		if !baselineIDs[p.ID] && p.ID != "" && p.Name == input.Name && p.RegionName == "HCM04" && p.ProjectType == 1 && p.PurchaseTypeID == 4 && p.TotalQuota == 30 {
			copyProject := *p
			owned = &copyProject
		}
	}
	after, afterErr = storageProjectCash(ctx, money)
	if afterErr == nil {
		report["afterOrderCash"] = after
		t.Logf("cash delta after order: %.0f VND", after-before)
	}
	report["orderConfirmed"] = createErr == nil
	if created != nil {
		report["sdkResult"] = created
	}
	// Read-only reconciliation also runs after a refused or uncertain attempt.
	projects, listErr := client.ListProjects(ctx, &storage.ListProjectsInput{Region: "HCM04"})
	knownID := ""
	if owned != nil {
		knownID = owned.ID
	}
	if listErr == nil {
		matches := 0
		for i := range projects.Items {
			p := &projects.Items[i]
			if p.Name == input.Name {
				matches++
				if !baselineIDs[p.ID] && p.ID != "" && (knownID == "" || p.ID == knownID) && p.RegionName == "HCM04" && p.ProjectType == 1 && p.PurchaseTypeID == 4 && p.TotalQuota == 30 {
					copyProject := *p
					owned = &copyProject
				}
			}
		}
		if matches != 1 && knownID == "" {
			owned = nil
		}
		report["projectsAfterOrder"] = projects.Items
	}
	report["projectUniquelyIdentified"] = owned != nil
	saveReport()
	if createErr != nil || afterErr != nil || listErr != nil || owned == nil {
		t.Fatal("order outcome needs manual reconciliation. " + storageProjectLeftover)
	}
	if before-after != quote.TotalPrice {
		t.Fatal("debit differs from quoted total. " + storageProjectLeftover)
	}
	if created == nil || created.Project == nil || created.Project.ID != owned.ID || owned.Status != 1 || owned.ProjectTypeName != "Gold" || owned.EnableAutoRenew == nil || *owned.EnableAutoRenew {
		t.Fatal("project readback did not confirm the requested state. " + storageProjectLeftover)
	}
	t.Logf("project status: %d; quoted total: %.0f VND; debit: %.0f VND", owned.Status, quote.TotalPrice, before-after)
	cleanup()
}

func storageProjectCash(ctx context.Context, c *billing.Client) (float64, error) {
	out, err := c.GetBalances(ctx, nil)
	if err != nil {
		return 0, err
	}
	if out.Balances.Cash == nil || math.IsNaN(*out.Balances.Cash) || math.IsInf(*out.Balances.Cash, 0) {
		return 0, errors.New("cash balance missing or invalid")
	}
	return *out.Balances.Cash, nil
}

type storageProjectCaptureTransport struct {
	base   http.RoundTripper
	mu     sync.Mutex
	counts map[string]int
}

func (c *storageProjectCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if req.URL.Path != "/internal/v2/orders" && (req.Method != http.MethodDelete || !strings.HasPrefix(req.URL.Path, "/internal/v1/projects/")) {
		return resp, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	closeErr := resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil || closeErr != nil {
		_ = resp.Body.Close()
		return nil, errors.New("private write response read failed")
	}
	name := "project-order"
	if req.Method == http.MethodDelete {
		name = "project-delete"
	}
	if err := c.save(name, resp.StatusCode, body); err != nil {
		_ = resp.Body.Close()
		return nil, errors.New("private write response capture failed")
	}
	return resp, nil
}

func (c *storageProjectCaptureTransport) save(name string, status int, body []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	c.counts[name]++
	var value any
	if json.Unmarshal(body, &value) != nil {
		value = "malformed response withheld"
	}
	value = storageProjectCaptureURLs(value)
	data, err := json.MarshalIndent(map[string]any{"status": status, "body": value}, "", "  ")
	if err != nil {
		return err
	}
	file := fmt.Sprintf("%s-%03d.json", name, c.counts[name])
	return os.WriteFile(filepath.Join(storageProjectCaptureDir, file), data, 0o600)
}

// Payment redirects may contain session tokens. Private captures retain the
// host alone, even though the rest of each response stays available locally.
func storageProjectCaptureURLs(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if text, ok := item.(string); ok && strings.Contains(strings.ToLower(key), "url") {
				u, err := url.Parse(text)
				if err != nil || u.Host == "" {
					v[key] = "URL withheld"
				} else {
					v[key] = u.Hostname()
				}
				continue
			}
			v[key] = storageProjectCaptureURLs(item)
		}
	case []any:
		for i, item := range v {
			v[i] = storageProjectCaptureURLs(item)
		}
	case string:
		if strings.Contains(v, "://") || strings.HasPrefix(v, "//") {
			u, err := url.Parse(v)
			if err != nil || u.Host == "" {
				return "URL withheld"
			}
			return u.Hostname()
		}
	}
	return value
}

func TestStorageProjectCaptureURLs(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`{"data":{"redirectUrl":"https://checkout.example/pay?token=synthetic"}}`, "checkout.example"},
		{`{"data":{"redirectUrl":"//checkout.example/pay?token=synthetic"}}`, "checkout.example"},
		{`{"data":{"redirectUrl":"/pay?token=synthetic"}}`, "URL withheld"},
	} {
		var value any
		if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
			t.Fatal("invalid synthetic capture")
		}
		value = storageProjectCaptureURLs(value)
		got := value.(map[string]any)["data"].(map[string]any)["redirectUrl"]
		if got != tc.want {
			t.Fatal("capture did not withhold the payment URL")
		}
	}
}
