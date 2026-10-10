//go:build livewrite

package livetest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math"
	"net/http"
	"net/url"
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
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/storage"
)

var storageProjectCaptureDir = repoPath("examples/basic/output/raw/storage")

const storageProjectLeftover = "Leftover name pattern: vngcloud-live-<8 hex> in HCM04. Inspect the private storage-project-report.json, pending orders, and billing in the console before any retry."

// TestLiveWriteStorageProject consumes one paid order attempt. Gates must be
// set before loading .env so a local file cannot opt a test run into payment.
// Without a transaction read, unrelated credits or holds during the run cannot
// be distinguished from the project's debit and refund.
func TestLiveWriteStorageProject(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" || os.Getenv("VNGCLOUD_LIVE_STORAGE_PROJECT") != "1" {
		t.Skip("set both storage project live-write gates for the approved paid check")
	}
	if err := envfile.Load(repoPath(".env")); err != nil {
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
		t.Fatal("baseline project read failed. " + storageProjectLeftover)
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
		t.Fatal("quote did not match the expected package")
	}
	if before < quote.TotalPrice {
		t.Fatal("cash balance is below the quoted total")
	}
	if t.Failed() {
		t.Fatal("private capture failed before order")
	}
	report := map[string]any{"region": "HCM04", "attemptedName": input.Name, "baselineCash": before, "quotedTotal": quote.TotalPrice, "baselineProjects": baseline.Items}
	saveReport := func() error {
		report["writeResponses"] = capture.writeResponses()
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(storageProjectCaptureDir, "storage-project-report.json"), data, 0o600) != nil {
			t.Error("could not save private reconciliation report. " + storageProjectLeftover)
			return errors.New("private report write failed")
		}
		return nil
	}
	var owned *storage.Project
	var after float64
	afterErr := errors.New("post-order cash not read")
	deleted := false
	cleanup := func() {
		if owned == nil || deleted {
			return
		}
		// Complete reconciliation proves ownership even when validation fails.
		deleted = true
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 7*time.Minute)
		defer cleanupCancel()
		_, deleteErr := client.DeleteProject(cleanupCtx, &storage.DeleteProjectInput{Region: "HCM04", ProjectID: owned.ID})
		report["deleteConfirmed"] = deleteErr == nil
		if deleteErr != nil {
			_ = saveReport()
			t.Error("cleanup was not confirmed. " + storageProjectLeftover)
			return
		}
		remaining, readErr := client.ListProjects(cleanupCtx, &storage.ListProjectsInput{Region: "HCM04"})
		if readErr != nil {
			_ = saveReport()
			t.Error("post-delete project read failed. " + storageProjectLeftover)
			return
		}
		for _, p := range remaining.Items {
			if p.ID == owned.ID {
				_ = saveReport()
				t.Error("project remains in active list. " + storageProjectLeftover)
				return
			}
		}
		t.Logf("projects after cleanup: %d", len(remaining.Items))
		refundDeadline := time.Now().Add(5 * time.Minute)
		for {
			refundCtx, refundCancel := context.WithDeadline(cleanupCtx, refundDeadline)
			finalCash, cashErr := storageProjectCash(refundCtx, money)
			refundCancel()
			if !time.Now().Before(refundDeadline) {
				cashErr = context.DeadlineExceeded
			}
			if cashErr != nil {
				_ = saveReport()
				t.Error("refund cash read failed. " + storageProjectLeftover)
				return
			}
			report["afterDeleteCash"] = finalCash
			report["refund"] = finalCash - after
			report["netDelta"] = finalCash - before
			if err := saveReport(); err != nil {
				return
			}
			if afterErr == nil && storageProjectRefundConfirmed(before, after, finalCash) {
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
	err = storageProjectAttempt(saveReport, func() (*storage.CreateProjectOutput, error) {
		return client.CreateProject(ctx, input)
	}, func(created *storage.CreateProjectOutput, createErr error) (*storage.Project, error) {
		after, afterErr = storageProjectCash(ctx, money)
		if afterErr == nil {
			report["afterOrderCash"] = after
			report["debit"] = before - after
		}
		report["orderConfirmed"] = createErr == nil
		if created != nil {
			report["sdkResult"] = created
		}
		// Reconcile ownership after every order outcome before any cleanup.
		projects, listErr := client.ListProjects(ctx, &storage.ListProjectsInput{Region: "HCM04"})
		if listErr == nil {
			owned = storageProjectOwned(projects.Items, baselineIDs, input.Name)
			report["projectsAfterOrder"] = projects.Items
		}
		report["projectUniquelyIdentified"] = owned != nil
		if err := saveReport(); err != nil {
			return owned, err
		}
		if afterErr != nil || listErr != nil || t.Failed() {
			return owned, errors.New("reconciliation failed")
		}
		return owned, nil
	}, func(created *storage.CreateProjectOutput, createErr error, owned *storage.Project) error {
		if err := storageProjectValidate(created, createErr, owned, before, after, quote.TotalPrice); err != nil {
			return err
		}
		t.Logf("project status: %d", owned.Status)
		return nil
	}, func(project *storage.Project) { owned = project; cleanup() })
	if err != nil {
		t.Fatal("paid project check failed. " + storageProjectLeftover)
	}
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
	base           http.RoundTripper
	mu             sync.Mutex
	counts         map[string]int
	shapes         map[string]any
	listIncomplete bool
}

func (c *storageProjectCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	projectList := req.Method == http.MethodGet && req.URL.Path == "/internal/v1/projects"
	if projectList {
		c.mu.Lock()
		incomplete := c.listIncomplete
		c.mu.Unlock()
		if incomplete {
			return nil, errors.New("project list was incomplete; stopped")
		}
	}
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if !projectList && req.URL.Path != "/internal/v2/orders" && (req.Method != http.MethodDelete || !strings.HasPrefix(req.URL.Path, "/internal/v1/projects/")) {
		return resp, nil
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	closeErr := resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if readErr != nil || closeErr != nil {
		_ = resp.Body.Close()
		return nil, errors.New("private write response read failed")
	}
	if projectList {
		if !storageProjectListComplete(body) {
			c.mu.Lock()
			c.listIncomplete = true
			c.mu.Unlock()
			_ = resp.Body.Close()
			return nil, errors.New("project list is incomplete or malformed")
		}
		return resp, nil
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
	shape := map[string]any{"status": status, "body": value}
	if name == "project-order" || name == "project-delete" {
		if c.shapes == nil {
			c.shapes = map[string]any{}
		}
		c.shapes[name] = shape
	}
	data, err := json.MarshalIndent(shape, "", "  ")
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

func TestStorageProjectRefundBounds(t *testing.T) {
	for _, tc := range []struct {
		before, after, final float64
		want                 bool
	}{
		{100000, 70000, 99500, true}, {100000, 70000, 99499, false},
		{100000, 70000, 70001, false}, {100000, 70000, 100001, false},
		{100000, 70000, 100000, true},
	} {
		if storageProjectRefundConfirmed(tc.before, tc.after, tc.final) != tc.want {
			t.Fatal("refund bound mismatch")
		}
	}
	if !storageProjectDebitConfirmed(100000, 70001, 30000) || storageProjectDebitConfirmed(100000, 70002, 30000) {
		t.Fatal("debit tolerance mismatch")
	}
}

func TestStorageProjectCompleteLists(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"absent", testutil.FixtureBody(t, repoPath("testdata/storage/project_list_empty.json")), true},
		{"datas empty", `{"success":true,"datas":[],"isNext":false}`, true},
		{"data empty", `{"success":true,"data":[]}`, true},
		{"datas null", `{"success":true,"datas":null}`, false},
		{"data null", `{"success":true,"data":null}`, false},
		{"null with fallback", `{"success":true,"datas":null,"data":[]}`, false},
		{"null secondary", `{"success":true,"datas":[],"data":null}`, false},
		{"non-array", `{"success":true,"datas":{}}`, false},
		{"non-array data", `{"success":true,"data":"invalid"}`, false},
		{"malformed secondary", `{"success":true,"datas":[],"data":{}}`, false},
		{"malformed JSON", `{"success":true,"datas":[invalid}`, false},
		{"incomplete absent", `{"success":true,"isNext":true}`, false},
		{"incomplete array", `{"success":true,"datas":[],"isNext":true}`, false},
		{"refusal", `{"success":false}`, false},
		{"missing success", `{"code":200}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if storageProjectListComplete([]byte(tc.raw)) != tc.valid {
				t.Fatal("complete list classification mismatch")
			}
		})
	}
}

func TestStorageProjectReportFailureStopsOrder(t *testing.T) {
	calls := 0
	err := storageProjectBeforeOrder(func() error { return errors.New("synthetic write failure") }, func() { calls++ })
	if err == nil || calls != 0 {
		t.Fatal("order ran without private report")
	}
}

func storageProjectDebitConfirmed(before, after, quote float64) bool {
	return math.Abs((before-after)-quote) <= 1
}

func storageProjectRefundConfirmed(before, after, final float64) bool {
	return final-after >= before-after-500 && final >= before-500 && final <= before
}

func storageProjectBeforeOrder(save func() error, order func()) error {
	if err := save(); err != nil {
		return err
	}
	order()
	return nil
}

func storageProjectListComplete(body []byte) bool {
	var env struct {
		Success *bool           `json:"success"`
		IsNext  bool            `json:"isNext"`
		Data    json.RawMessage `json:"data"`
		Datas   json.RawMessage `json:"datas"`
	}
	if json.Unmarshal(body, &env) != nil || env.Success == nil || !*env.Success || env.IsNext {
		return false
	}
	// Empty regions omit both keys. Validate every present list field.
	items := []storage.Project{}
	for _, raw := range []json.RawMessage{env.Data, env.Datas} {
		if len(raw) == 0 {
			continue
		}
		var decoded []storage.Project
		if json.Unmarshal(raw, &decoded) != nil || decoded == nil {
			return false
		}
		items = decoded
	}
	seen := map[string]bool{}
	for _, p := range items {
		if p.ID == "" || p.Name == "" || seen[p.ID] {
			return false
		}
		seen[p.ID] = true
	}
	return true
}

func storageProjectOwned(items []storage.Project, baseline map[string]bool, name string) *storage.Project {
	var owned *storage.Project
	matches := 0
	for _, p := range items {
		if p.Name != name {
			continue
		}
		matches++
		if !baseline[p.ID] && p.ID != "" && p.RegionName == "HCM04" && p.ProjectType == 1 && p.PurchaseTypeID == 4 && p.TotalQuota == 30 {
			copyProject := p
			owned = &copyProject
		}
	}
	if matches != 1 {
		return nil
	}
	return owned
}

func (c *storageProjectCaptureTransport) writeResponses() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]any{}
	for k, v := range c.shapes {
		out[k] = v
	}
	return out
}

func TestStorageProjectOwnership(t *testing.T) {
	p := storage.Project{ID: "synthetic-new", Name: "vngcloud-live-12345678", RegionName: "HCM04", ProjectType: 1, PurchaseTypeID: 4, TotalQuota: 30}
	if storageProjectOwned([]storage.Project{p}, map[string]bool{p.ID: true}, p.Name) != nil {
		t.Fatal("baseline project acquired")
	}
	if storageProjectOwned([]storage.Project{p}, nil, "other") != nil {
		t.Fatal("another name acquired")
	}
	if storageProjectOwned([]storage.Project{p, p}, nil, p.Name) != nil {
		t.Fatal("ambiguous project acquired")
	}
	if storageProjectOwned([]storage.Project{p}, nil, p.Name) == nil {
		t.Fatal("new exact project rejected")
	}
}

func TestStorageProjectAttemptCleansValidationFailures(t *testing.T) {
	for _, problem := range []string{"uncertain response", "debit mismatch", "renewal true"} {
		t.Run(problem, func(t *testing.T) {
			orders, deletes, bucketReads := 0, 0, 0
			renewal := problem == "renewal true"
			p := storage.Project{ID: "synthetic-new", Name: "vngcloud-live-12345678", RegionID: "<region-id-2>", RegionName: "HCM04", ProjectType: 1, ProjectTypeName: "Gold", PurchaseTypeID: 4, TotalQuota: 30, Status: 1, EnableAutoRenew: &renewal}
			projectJSON, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			list := []byte(`{"success":true,"datas":[` + string(projectJSON) + `]}`)
			client := storage.New(testutil.NewRetryConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body string
				switch r.URL.Path {
				case "/internal/v1/regions":
					body = testutil.FixtureBody(t, repoPath("testdata/storage/list_regions.json"))
				case "/internal/v1/billing/project_types":
					body = testutil.FixtureBody(t, repoPath("testdata/storage/project_types.json"))
				case "/internal/v1/billing/purchase_types":
					body = testutil.FixtureBody(t, repoPath("testdata/storage/project_purchase_types.json"))
				case "/billing-api/v1/configurations":
					body = testutil.FixtureBody(t, repoPath("testdata/storage/project_configuration_"+r.URL.Query().Get("keys")+".json"))
				case "/billing-api/v2/price":
					body = testutil.FixtureBody(t, repoPath("testdata/storage/project_quote_gold.json"))
				case "/internal/v1/projects":
					body = `{"success":true,"datas":[]}`
					if orders > 0 && deletes == 0 {
						body = string(list)
					}
				case "/internal/v2/orders":
					orders++
					if problem == "uncertain response" {
						w.WriteHeader(503)
						body = `{"success":false,"code":503}`
					} else {
						body = `{"success":true,"data":{"redirectUrl":"https://checkout.example/synthetic"}}`
					}
				case "/internal/v1/ceph/projects/synthetic-new":
					bucketReads++
					body = `{"success":true,"datas":[]}`
				case "/internal/v1/projects/synthetic-new":
					if r.Method != http.MethodDelete || bucketReads != 1 {
						t.Error("delete bypassed bucket guard")
					}
					var got map[string]any
					if json.NewDecoder(r.Body).Decode(&got) != nil || got == nil || len(got) != 0 {
						t.Error("delete body was not empty object")
					}
					deletes++
					body = `{"success":true,"data":{}}`
				default:
					t.Errorf("unexpected mock route %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(body))
			})))
			baseline, err := client.ListProjects(context.Background(), &storage.ListProjectsInput{Region: "HCM04"})
			if err != nil || len(baseline.Items) != 0 {
				t.Fatal("mock baseline failed")
			}
			before, after := 913579.0, 883579.0
			if problem == "debit mismatch" {
				after -= 20
			}
			err = storageProjectAttempt(func() error { return nil }, func() (*storage.CreateProjectOutput, error) {
				return client.CreateProject(context.Background(), &storage.CreateProjectInput{Region: "HCM04", Name: p.Name, Type: "Gold", QuotaGB: 30, MaxPrice: 30000})
			}, func(*storage.CreateProjectOutput, error) (*storage.Project, error) {
				if !storageProjectListComplete(list) {
					t.Fatal("mock reconciliation was incomplete")
				}
				projects, err := client.ListProjects(context.Background(), &storage.ListProjectsInput{Region: "HCM04"})
				if err != nil {
					return nil, err
				}
				return storageProjectOwned(projects.Items, map[string]bool{}, p.Name), nil
			}, func(out *storage.CreateProjectOutput, orderErr error, owned *storage.Project) error {
				return storageProjectValidate(out, orderErr, owned, before, after, 30000)
			}, func(owned *storage.Project) {
				if owned == nil || owned.ID != p.ID {
					t.Fatal("delete lacked ownership")
				}
				_, err := client.DeleteProject(context.Background(), &storage.DeleteProjectInput{Region: "HCM04", ProjectID: owned.ID})
				if err != nil {
					t.Fatal("guarded cleanup failed")
				}
			})
			if err == nil || orders != 1 || deletes != 1 || bucketReads != 1 {
				t.Fatalf("orders %d deletes %d bucket reads %d error %v", orders, deletes, bucketReads, err)
			}
		})
	}
}

func TestStorageProjectPublicLog(t *testing.T) {
	source, err := os.ReadFile(repoPath("livetest", "live_write_storage_project_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := storageProjectCheckPublicLog(source); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`package example; func TestLiveWriteStorageProject(t T){t.Logf("cash: %f", before)}`,
		`package example; func TestLiveWriteStorageProject(t T){t.Logf("delta: %f", final-after)}`,
		`package example; func TestLiveWriteStorageProject(t T){t.Log(report["debit"])}`,
	} {
		if storageProjectCheckPublicLog([]byte(source)) == nil {
			t.Fatal("financial public log accepted")
		}
	}
}

// Cleanup depends on reconciled ownership, not acceptance or money validation.
func storageProjectAttempt(save func() error, order func() (*storage.CreateProjectOutput, error), reconcile func(*storage.CreateProjectOutput, error) (*storage.Project, error), validate func(*storage.CreateProjectOutput, error, *storage.Project) error, cleanup func(*storage.Project)) error {
	var created *storage.CreateProjectOutput
	var createErr error
	if err := storageProjectBeforeOrder(save, func() { created, createErr = order() }); err != nil {
		return err
	}
	owned, err := reconcile(created, createErr)
	if owned != nil {
		defer cleanup(owned)
	}
	if err != nil {
		return err
	}
	return validate(created, createErr, owned)
}

func storageProjectCheckPublicLog(source []byte) error {
	f, err := parser.ParseFile(token.NewFileSet(), "livetest/live_write_storage_project_test.go", source, 0)
	if err != nil {
		return err
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestLiveWriteStorageProject" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "Log", "Logf", "Error", "Errorf", "Fatal", "Fatalf":
			default:
				return true
			}
			for _, arg := range call.Args {
				if !storageProjectPublicLogArg(arg) {
					err = errors.New("public log contains a value outside counts, statuses, and fixed messages")
				}
			}
			return true
		})
	}
	return err
}

func storageProjectPublicLogArg(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return false
		}
		text, err := strconv.Unquote(value.Value)
		if err != nil || strings.Contains(text, "VND") {
			return false
		}
		for _, r := range text {
			if r >= '0' && r <= '9' {
				return false
			}
		}
		return true
	case *ast.Ident:
		return value.Name == "storageProjectLeftover"
	case *ast.BinaryExpr:
		return value.Op == token.ADD && storageProjectPublicLogArg(value.X) && storageProjectPublicLogArg(value.Y)
	case *ast.SelectorExpr:
		owner, ok := value.X.(*ast.Ident)
		return ok && owner.Name == "owned" && value.Sel.Name == "Status"
	case *ast.CallExpr:
		name, ok := value.Fun.(*ast.Ident)
		if !ok || name.Name != "len" || len(value.Args) != 1 {
			return false
		}
		field, ok := value.Args[0].(*ast.SelectorExpr)
		if !ok || field.Sel.Name != "Items" {
			return false
		}
		owner, ok := field.X.(*ast.Ident)
		return ok && (owner.Name == "baseline" || owner.Name == "remaining")
	}
	return false
}

func storageProjectValidate(created *storage.CreateProjectOutput, createErr error, owned *storage.Project, before, after, quote float64) error {
	if createErr != nil || owned == nil {
		return errors.New("order outcome is unconfirmed")
	}
	if !storageProjectDebitConfirmed(before, after, quote) {
		return errors.New("debit differs from quoted total")
	}
	if created == nil || created.Project == nil || created.Project.ID != owned.ID || owned.Status != 1 || owned.ProjectTypeName != "Gold" || owned.EnableAutoRenew == nil || *owned.EnableAutoRenew {
		return errors.New("project state is unconfirmed")
	}
	return nil
}
