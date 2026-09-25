//go:build livewrite

package vngcloud_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/internal/envfile"
)

// liveWriteCaptureDir holds one raw response capture file per operation.
// examples/basic/output/ is git-ignored; nothing under it is published.
const liveWriteCaptureDir = "examples/basic/output/raw/billing/live-write"

// TestLiveWrite exercises budget and threshold writes against the real
// account named in .env. It creates one PAUSED budget with a limit high
// enough that it can never fire an alert, changes it, and deletes it; it
// never leaves a budget behind and never enables a threshold.
func TestLiveWrite(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live budget write test")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	rootEmail := os.Getenv("VNGCLOUD_ROOT_EMAIL")
	username := os.Getenv("VNGCLOUD_USERNAME")
	password := os.Getenv("VNGCLOUD_PASSWORD")
	if rootEmail == "" || username == "" || password == "" {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD in .env")
	}
	iamUser := &vngcloud.IAMUserAuth{
		RootEmail: rootEmail,
		Username:  username,
		Password:  password,
	}
	if secret := os.Getenv("VNGCLOUD_TOTP_SECRET"); secret != "" {
		iamUser.TOTP = &vngcloud.SecretTOTP{Secret: secret}
	}

	region := "hcm-3"
	if raw := strings.TrimSpace(os.Getenv("VNGCLOUD_REGIONS")); raw != "" {
		if first := strings.TrimSpace(strings.Split(raw, ",")[0]); first != "" {
			region = first
		}
	}

	if err := os.MkdirAll(liveWriteCaptureDir, 0o755); err != nil {
		t.Fatalf("create capture directory: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := vngcloud.NewConfig(
		vngcloud.WithRegion(region),
		vngcloud.WithIAMUser(iamUser),
		vngcloud.WithResponseCapture(func(captured vngcloud.ResponseCapture) {
			if err := appendLiveWriteCapture(captured); err != nil {
				t.Errorf("write capture: %v", err)
			}
		}),
	)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	client := billing.New(cfg)

	// Step 1: delete any budget left over from a previous run.
	leftovers, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	if err != nil {
		t.Fatalf("step 1 ListBudgets: %s", safeErr(err))
	}
	deleted := 0
	for _, leftover := range leftovers.Items {
		if !strings.HasPrefix(leftover.Name, "vngcloud-live-") {
			continue
		}
		if _, err := client.DeleteBudget(ctx, &billing.DeleteBudgetInput{BudgetUUID: leftover.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover budget: %s", safeErr(err))
		}
		deleted++
	}
	t.Logf("step 1: deleted %d leftover budget(s)", deleted)

	// Step 2: pick a budget type the account does not already use.
	current, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	if err != nil {
		t.Fatalf("step 2 ListBudgets: %s", safeErr(err))
	}
	usedTypes := map[string]bool{}
	for _, existing := range current.Items {
		usedTypes[existing.Type] = true
	}
	var budgetType string
	switch {
	case !usedTypes[billing.TypeActual]:
		budgetType = billing.TypeActual
	case !usedTypes[billing.TypeForecasted]:
		budgetType = billing.TypeForecasted
	default:
		t.Skip("step 2: account already has a budget of each type")
	}
	t.Logf("step 2: picked budget type %s", budgetType)

	// Step 3: create the budget. LimitAmount is far above any real spend so
	// the budget can never trigger a threshold alert.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	created, err := client.CreateBudget(ctx, &billing.CreateBudgetInput{
		Name:        name,
		PeriodType:  billing.PeriodMonthly,
		Type:        budgetType,
		LimitAmount: 10_000_000_000,
		Status:      billing.StatusPaused,
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the create may
		// still have reached the server. Find and delete it by its exact name.
		deleteBudgetByName(ctx, t, client, name)
		t.Fatalf("step 3 CreateBudget: %s", safeErr(err))
	}
	budgetUUID := created.Budget.UUID
	if budgetUUID == "" {
		deleteBudgetByName(ctx, t, client, name)
		t.Fatal("step 3: CreateBudget returned an empty UUID; the design requires one")
	}
	t.Log("step 3: created budget")

	// Step 4: register the fallback delete immediately, before anything else
	// can fail and skip the explicit delete in step 8.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteBudget(cleanupCtx, &billing.DeleteBudgetInput{BudgetUUID: budgetUUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete budget: %s", safeErr(err))
		}
	})

	// Step 5: update only LimitAmount and confirm every other field, Status
	// in particular, is unchanged.
	if _, err := client.UpdateBudget(ctx, &billing.UpdateBudgetInput{
		BudgetUUID:  budgetUUID,
		LimitAmount: vngcloud.Ptr(int64(9_000_000_000)),
	}); err != nil {
		t.Fatalf("step 5 UpdateBudget: %s", safeErr(err))
	}
	afterUpdate, err := client.GetBudget(ctx, &billing.GetBudgetInput{BudgetUUID: budgetUUID})
	if err != nil {
		t.Fatalf("step 5 GetBudget: %s", safeErr(err))
	}
	b := afterUpdate.Budget
	if b.Name != name || b.PeriodType != billing.PeriodMonthly || b.Type != budgetType || b.Status != billing.StatusPaused {
		t.Fatal("step 5: UpdateBudget changed a field it should not have")
	}
	if b.LimitAmount != 9_000_000_000 {
		t.Fatal("step 5: UpdateBudget did not change LimitAmount")
	}
	t.Log("step 5: updated limit; other fields unchanged")

	// Step 6: create a disabled threshold, update only its reminder
	// interval, confirm it is still disabled, then delete it twice.
	thresholdCreated, err := client.CreateBudgetThreshold(ctx, &billing.CreateBudgetThresholdInput{
		BudgetUUID:          budgetUUID,
		ThresholdType:       budgetType,
		ThresholdPercentage: 100,
		Enabled:             vngcloud.Ptr(false),
	})
	if err != nil {
		t.Fatalf("step 6 CreateBudgetThreshold: %s", safeErr(err))
	}
	thresholdUUID := thresholdCreated.Threshold.UUID
	if thresholdUUID == "" {
		t.Fatal("step 6: CreateBudgetThreshold returned an empty UUID")
	}

	if _, err := client.UpdateBudgetThreshold(ctx, &billing.UpdateBudgetThresholdInput{
		BudgetUUID:            budgetUUID,
		ThresholdUUID:         thresholdUUID,
		ReminderIntervalHours: vngcloud.Ptr(24),
	}); err != nil {
		t.Fatalf("step 6 UpdateBudgetThreshold: %s", safeErr(err))
	}

	thresholds, err := client.ListBudgetThresholds(ctx, &billing.ListBudgetThresholdsInput{BudgetUUID: budgetUUID})
	if err != nil {
		t.Fatalf("step 6 ListBudgetThresholds: %s", safeErr(err))
	}
	found := false
	for _, th := range thresholds.Items {
		if th.UUID != thresholdUUID {
			continue
		}
		found = true
		if th.Enabled {
			t.Fatal("step 6: threshold became enabled; it must stay disabled")
		}
		break
	}
	if !found {
		t.Fatal("step 6: ListBudgetThresholds did not include the created threshold")
	}

	if _, err := client.DeleteBudgetThreshold(ctx, &billing.DeleteBudgetThresholdInput{
		BudgetUUID:    budgetUUID,
		ThresholdUUID: thresholdUUID,
	}); err != nil {
		t.Fatalf("step 6 DeleteBudgetThreshold: %s", safeErr(err))
	}
	// A second delete of the same threshold shows the not-found mapping;
	// only the error code is logged, since the message may name the
	// threshold's UUID.
	_, secondDeleteErr := client.DeleteBudgetThreshold(ctx, &billing.DeleteBudgetThresholdInput{
		BudgetUUID:    budgetUUID,
		ThresholdUUID: thresholdUUID,
	})
	switch {
	case secondDeleteErr == nil:
		t.Log("step 6: second threshold delete succeeded without error")
	case vngcloud.IsNotFound(secondDeleteErr):
		t.Logf("step 6: second threshold delete returned NotFound, code %s", vngcloud.ErrorCode(secondDeleteErr))
	default:
		t.Fatalf("step 6: second threshold delete: unexpected code %s", vngcloud.ErrorCode(secondDeleteErr))
	}

	// Step 7: try a second budget of the same type. Whether the server
	// allows this is an open question the live run settles.
	dupName := name + "-dup"
	dup, err := client.CreateBudget(ctx, &billing.CreateBudgetInput{
		Name:        dupName,
		PeriodType:  billing.PeriodMonthly,
		Type:        budgetType,
		LimitAmount: 10_000_000_000,
		Status:      billing.StatusPaused,
	})
	if err != nil {
		t.Logf("step 7: second budget of the same type rejected, code %s", vngcloud.ErrorCode(err))
	} else {
		dupUUID := dup.Budget.UUID
		if dupUUID != "" {
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if _, err := client.DeleteBudget(cleanupCtx, &billing.DeleteBudgetInput{BudgetUUID: dupUUID}); err != nil && !vngcloud.IsNotFound(err) {
					t.Errorf("cleanup: delete second budget: %s", safeErr(err))
				}
			})
			// Delete it now, not only in the cleanup above, so step 8's count
			// of remaining vngcloud-live budgets is accurate before the test
			// returns; the cleanup is a safety net for an earlier failure.
			if _, err := client.DeleteBudget(ctx, &billing.DeleteBudgetInput{BudgetUUID: dupUUID}); err != nil {
				t.Fatalf("step 7 delete second budget: %s", safeErr(err))
			}
		}
		t.Log("server allows a second budget per type")
	}

	// Step 8: delete the budget and confirm none named vngcloud-live-*
	// remain.
	if _, err := client.DeleteBudget(ctx, &billing.DeleteBudgetInput{BudgetUUID: budgetUUID}); err != nil {
		t.Fatalf("step 8 DeleteBudget: %s", safeErr(err))
	}
	final, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	if err != nil {
		t.Fatalf("step 8 final ListBudgets: %s", safeErr(err))
	}
	remaining := 0
	for _, budget := range final.Items {
		if strings.HasPrefix(budget.Name, "vngcloud-live-") {
			remaining++
		}
	}
	t.Logf("step 8: vngcloud-live budgets remaining: %d", remaining)
	if remaining != 0 {
		t.Fatalf("step 8: expected 0 vngcloud-live budgets, found %d", remaining)
	}
}

// deleteBudgetByName lists budgets and deletes the one matching name. It is
// used after a CreateBudget failure, since a POST that returned an error may
// still have reached the server.
func deleteBudgetByName(ctx context.Context, t *testing.T, client *billing.Client, name string) {
	t.Helper()
	list, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	if err != nil {
		t.Errorf("cleanup: list budgets by name: %s", safeErr(err))
		return
	}
	for _, b := range list.Items {
		if b.Name != name {
			continue
		}
		if _, err := client.DeleteBudget(ctx, &billing.DeleteBudgetInput{BudgetUUID: b.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete budget by name: %s", safeErr(err))
		}
	}
}

// safeErr summarizes err for a test log line without its message text, which
// may name this test's own budget or threshold UUID.
func safeErr(err error) string {
	if err == nil {
		return "none"
	}
	var apiErr *vngcloud.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("status=%d code=%s", apiErr.StatusCode, apiErr.Code)
	}
	return fmt.Sprintf("non-API error (%T)", err)
}

// randomHex returns n*2 lowercase hex characters from a cryptographically
// random source.
func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// liveWriteCall is one captured HTTP response, in the raw capture shape
// examples/basic uses.
type liveWriteCall struct {
	Operation  string          `json:"operation"`
	Method     string          `json:"method"`
	URL        string          `json:"url"`
	StatusCode int             `json:"statusCode"`
	Body       json.RawMessage `json:"body,omitempty"`
	BodyText   string          `json:"bodyText,omitempty"`
}

// appendLiveWriteCapture adds captured to the capture file for its
// operation, so every call to the same operation lands in one file.
func appendLiveWriteCapture(captured vngcloud.ResponseCapture) error {
	name := strings.TrimPrefix(captured.Operation, "billing.")
	path := filepath.Join(liveWriteCaptureDir, name+".json")

	var wrapper struct {
		Calls []liveWriteCall `json:"calls"`
	}
	if existing, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(existing, &wrapper)
	}

	call := liveWriteCall{
		Operation:  captured.Operation,
		Method:     captured.Method,
		URL:        captured.URL,
		StatusCode: captured.StatusCode,
	}
	if json.Valid(captured.Body) {
		call.Body = append(json.RawMessage(nil), captured.Body...)
	} else {
		call.BodyText = string(captured.Body)
	}
	wrapper.Calls = append(wrapper.Calls, call)

	data, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
