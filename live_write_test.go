//go:build livewrite

package vngcloud_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
)

// liveWriteCaptureDir holds one raw response capture file per operation.
// examples/basic/output/ is git-ignored; nothing under it is published.
const liveWriteCaptureDir = "examples/basic/output/raw/billing/live-write"

// TestLiveWrite exercises budget and threshold writes against the real
// account named in .env. It creates one PAUSED budget with a limit high
// enough that it can never fire an alert, changes it, and deletes it. A
// threshold it creates is disabled right after create, while the budget
// stays paused throughout; it never leaves a budget behind.
func TestLiveWrite(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live budget write test")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
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

	// Empty, explicit config and credentials files keep LoadConfig from
	// reading the real ~/.vngcloud, which could hold a different profile
	// than the account named in .env; credentials come from .env's
	// environment variables instead.
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion(region),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
		vngcloud.WithResponseCapture(func(captured vngcloud.ResponseCapture) {
			if err := appendLiveWriteCapture(captured); err != nil {
				t.Errorf("write capture: %v", err)
			}
		}),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
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
		LimitAmount: 9_999_999_999,
		Status:      billing.StatusPaused,
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the create may
		// still have reached the server. Find and delete it by its exact name.
		deleteBudgetByName(t, client, name)
		t.Fatalf("step 3 CreateBudget: %s", safeErr(err))
	}
	budgetUUID := created.Budget.UUID
	if budgetUUID == "" {
		deleteBudgetByName(t, client, name)
		t.Fatal("step 3: CreateBudget returned an empty UUID; the design requires one")
	}
	t.Log("step 3: created budget")

	// Step 4: register the fallback delete immediately, before anything else
	// can fail and skip the explicit delete in step 7.
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

	// Step 6: create a threshold (the server always starts it enabled),
	// disable it at once, update only its reminder interval, confirm it
	// stayed disabled, then delete it twice.
	thresholdCreated, err := client.CreateBudgetThreshold(ctx, &billing.CreateBudgetThresholdInput{
		BudgetUUID:          budgetUUID,
		ThresholdType:       budgetType,
		ThresholdPercentage: 100,
	})
	if err != nil {
		t.Fatalf("step 6 CreateBudgetThreshold: %s", safeErr(err))
	}
	thresholdUUID := thresholdCreated.Threshold.UUID
	if thresholdUUID == "" {
		t.Fatal("step 6: CreateBudgetThreshold returned an empty UUID")
	}

	if _, err := client.UpdateBudgetThreshold(ctx, &billing.UpdateBudgetThresholdInput{
		BudgetUUID:    budgetUUID,
		ThresholdUUID: thresholdUUID,
		Enabled:       vngcloud.Ptr(false),
	}); err != nil {
		t.Fatalf("step 6 UpdateBudgetThreshold (disable): %s", safeErr(err))
	}

	if _, err := client.UpdateBudgetThreshold(ctx, &billing.UpdateBudgetThresholdInput{
		BudgetUUID:            budgetUUID,
		ThresholdUUID:         thresholdUUID,
		ReminderIntervalHours: vngcloud.Ptr(24),
	}); err != nil {
		t.Fatalf("step 6 UpdateBudgetThreshold (reminder): %s", safeErr(err))
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
			t.Fatal("step 6: threshold did not stay disabled")
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

	// Step 7: delete the budget and confirm none named vngcloud-live-*
	// remain. DELETE is retried as a read is, so a retry that reaches the
	// server after an earlier attempt already deleted the budget returns
	// NotFound; that is success for a delete, not a failure.
	if _, err := client.DeleteBudget(ctx, &billing.DeleteBudgetInput{BudgetUUID: budgetUUID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 7 DeleteBudget: %s", safeErr(err))
	}
	final, err := client.ListBudgets(ctx, &billing.ListBudgetsInput{})
	if err != nil {
		t.Fatalf("step 7 final ListBudgets: %s", safeErr(err))
	}
	remaining := 0
	for _, budget := range final.Items {
		if strings.HasPrefix(budget.Name, "vngcloud-live-") {
			remaining++
		}
	}
	t.Logf("step 7: vngcloud-live budgets remaining: %d", remaining)
	if remaining != 0 {
		t.Fatalf("step 7: expected 0 vngcloud-live budgets, found %d", remaining)
	}
}

// deleteBudgetByName lists budgets and deletes the one matching name. It is
// used after a CreateBudget failure, since a POST that returned an error may
// still have reached the server. It runs on its own timeout, not the calling
// test step's context, so it can still clean up after that step's context
// is the reason the step failed.
func deleteBudgetByName(t *testing.T, client *billing.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

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

// emptyWriteFile creates an empty, mode-0600 file named name in a fresh temp
// directory, for a LoadConfig file option that must point at a file which
// exists but has no sections to resolve from.
func emptyWriteFile(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create empty %s: %v", name, err)
	}
	return path
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

// longLiveTestFrequency is the TestFrequency CreateCheck sends for
// TestLiveWriteMonitor's created check, in minutes. A long frequency makes
// the fewest probes of the owner's URL during the run.
const longLiveTestFrequency = 60

// TestLiveWriteMonitor exercises CreateCheck, PauseCheck, ResumeCheck, and
// DeleteCheck against the account named in .env.
//
// VNGCLOUD_LIVE_MONITOR_URL names the URL the created check probes; it
// never enters the repository, and the test skips when it is unset.
// VNGCLOUD_LIVE_MONITOR_QUOTA is the account's check quota named in this
// run's approval; the test skips instead of creating a check when it is
// unset, is not a positive integer, or the account is already at it after
// step 1's cleanup.
//
// It deletes every leftover vngcloud-live-* check first (step 1), then
// creates vngcloud-live-<8 hex> against the approved URL with one location
// and longLiveTestFrequency (step 4), registers the fallback delete as soon
// as the created check's id is known (step 5), and confirms its start
// status is one PauseCheck and ResumeCheck understand. It then pauses and
// resumes the check twice each, in whichever order ends back at the start
// status (steps 6 and 7), logging each call's confirm-read count: the GETs
// the call made, minus the one pre-toggle read that never follows a PUT,
// which leaves only the reads spent confirming the toggle landed. It
// confirms the final status matches the start status (step 8) and deletes
// the check explicitly (step 9); t.Cleanup deletes it again with its own
// context (NotFound there is success, not failure) and asserts no
// vngcloud-live-* check remains.
func TestLiveWriteMonitor(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live monitor write test")
	}
	targetURL := os.Getenv("VNGCLOUD_LIVE_MONITOR_URL")
	if targetURL == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_URL to the approved probe URL to run the live monitor write test")
	}
	quotaRaw := strings.TrimSpace(os.Getenv("VNGCLOUD_LIVE_MONITOR_QUOTA"))
	quota, quotaErr := strconv.Atoi(quotaRaw)
	if quotaRaw == "" || quotaErr != nil || quota <= 0 {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_QUOTA to the account's check quota (a positive integer) named in this run's approval to run the live monitor write test")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	region := "hcm-3"
	if raw := strings.TrimSpace(os.Getenv("VNGCLOUD_REGIONS")); raw != "" {
		if first := strings.TrimSpace(strings.Split(raw, ",")[0]); first != "" {
			region = first
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// getCount counts every GET DoJSON makes as part of a PauseCheck or
	// ResumeCheck call: readCheck sends both the pre-toggle read and every
	// confirm read under the caller's own operation name, so this single
	// counter, sampled before and after each call, gives that call's own
	// read count with no dependency on the monitor package's internals.
	var getCount atomic.Int64
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion(region),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
		vngcloud.WithResponseCapture(func(captured vngcloud.ResponseCapture) {
			if captured.Method == http.MethodGet &&
				(captured.Operation == "monitor.PauseCheck" || captured.Operation == "monitor.ResumeCheck") {
				getCount.Add(1)
			}
		}),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	client := monitor.New(cfg)

	// Step 1: delete every leftover vngcloud-live-* check from a previous run.
	leftovers, err := client.ListChecks(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListChecks: %s", safeErr(err))
	}
	deleted := 0
	for _, leftover := range leftovers.Items {
		if !strings.HasPrefix(leftover.Name, "vngcloud-live-") {
			continue
		}
		if _, err := client.DeleteCheck(ctx, &monitor.DeleteCheckInput{CheckID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover check: %s", safeErr(err))
		}
		deleted++
	}
	t.Logf("step 1: deleted %d leftover check(s)", deleted)

	// Step 2: skip instead of creating a check when the account is already
	// at the quota this run's approval named.
	current, err := client.ListChecks(ctx, nil)
	if err != nil {
		t.Fatalf("step 2 ListChecks: %s", safeErr(err))
	}
	if len(current.Items) >= quota {
		t.Skipf("step 2: account has %d check(s), at the named quota of %d", len(current.Items), quota)
	}

	// Step 3: pick one location; CreateCheck takes a location UUID, never a
	// name.
	locations, err := client.ListLocations(ctx, nil)
	if err != nil {
		t.Fatalf("step 3 ListLocations: %s", safeErr(err))
	}
	if len(locations.Items) == 0 {
		t.Fatal("step 3: account has no probe locations")
	}
	locationID := locations.Items[0].ID
	t.Log("step 3: picked one location")

	// Step 4: create the check.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 4 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	created, err := client.CreateCheck(ctx, &monitor.CreateCheckInput{
		Name:          name,
		URL:           targetURL,
		Locations:     []string{locationID},
		TestFrequency: longLiveTestFrequency,
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the create may
		// still have reached the server. Find and delete it by its exact name.
		deleteCheckByName(t, client, name)
		t.Fatalf("step 4 CreateCheck: %s", safeErr(err))
	}
	checkID := created.Check.ID
	if checkID == "" {
		deleteCheckByName(t, client, name)
		t.Fatal("step 4: CreateCheck returned an empty id; the design requires one")
	}

	// Step 5: register the fallback delete as soon as checkID is known,
	// before the status check below or any later step can fail and skip
	// the explicit delete in step 9.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteCheck(cleanupCtx, &monitor.DeleteCheckInput{CheckID: checkID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete check: %s", safeErr(err))
		}
		final, err := client.ListChecks(cleanupCtx, nil)
		if err != nil {
			t.Errorf("cleanup: final ListChecks: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, c := range final.Items {
			if strings.HasPrefix(c.Name, "vngcloud-live-") {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live check(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live checks, found %d", remaining)
		}
	})

	startStatus := created.Check.Status
	if startStatus != monitor.StatusEnabled && startStatus != monitor.StatusDisabled {
		t.Fatalf("step 4: created check status = %q, want %s or %s; refusing to toggle a status neither PauseCheck nor ResumeCheck understands",
			startStatus, monitor.StatusEnabled, monitor.StatusDisabled)
	}
	t.Logf("step 4: created check, start status %s", startStatus)

	toggle := func(step string, want bool, call func() (bool, error)) {
		before := getCount.Load()
		changed, err := call()
		if err != nil {
			t.Fatalf("%s: %s", step, safeErr(err))
		}
		if changed != want {
			t.Fatalf("%s: Changed = %v, want %v", step, changed, want)
		}
		reads := getCount.Load() - before - 1
		if reads < 0 {
			reads = 0
		}
		t.Logf("%s: confirm reads = %d", step, reads)
	}
	pause := func() (bool, error) {
		out, err := client.PauseCheck(ctx, &monitor.PauseCheckInput{CheckID: checkID})
		return out != nil && out.Changed, err
	}
	resume := func() (bool, error) {
		out, err := client.ResumeCheck(ctx, &monitor.ResumeCheckInput{CheckID: checkID})
		return out != nil && out.Changed, err
	}

	// Steps 6 and 7: pause twice and resume twice, in whichever order ends
	// back at startStatus, so a real toggle and a same-state no-op are both
	// exercised for each operation.
	if startStatus == monitor.StatusEnabled {
		toggle("step 6a pause", true, pause)
		toggle("step 6b pause", false, pause)
		toggle("step 7a resume", true, resume)
		toggle("step 7b resume", false, resume)
	} else {
		toggle("step 6a resume", true, resume)
		toggle("step 6b resume", false, resume)
		toggle("step 7a pause", true, pause)
		toggle("step 7b pause", false, pause)
	}

	// Step 8: confirm the check ended back at startStatus.
	final, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: checkID})
	if err != nil {
		t.Fatalf("step 8 GetCheck: %s", safeErr(err))
	}
	if final.Check.Status != startStatus {
		t.Fatalf("step 8: status = %s, want %s", final.Check.Status, startStatus)
	}

	// Step 9: delete the check explicitly. DELETE is retried as a read is,
	// so a retry that reaches the server after an earlier attempt already
	// deleted the check returns NotFound; that is success for a delete, not
	// a failure. t.Cleanup's own delete above then finds it already gone.
	if _, err := client.DeleteCheck(ctx, &monitor.DeleteCheckInput{CheckID: checkID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 9 DeleteCheck: %s", safeErr(err))
	}
}

// TestLiveWriteDNS exercises CreateHostedZone, UpdateHostedZone, and
// DeleteHostedZone against the account named in .env.
//
// VNGCLOUD_LIVE_DNS_VPC_ID names the VPC every created zone associates
// with; it never enters the repository, and the test skips when it is
// unset. It also skips, rather than failing, when that VPC's own
// dnsStatus is not ENABLED, since a zone made against a VPC still
// ENABLING Private DNS would only reach ERROR. Neither the VPC id nor
// any other VPC field is logged.
//
// It deletes every leftover vngcloud-live-*.internal zone first (step 1;
// this release has no records, so there is nothing to clean up inside one
// first), creates vngcloud-live-<8 hex>.internal against the named VPC
// (step 2), registers the fallback delete as soon as the created zone's id
// is known (step 3), updates its description (step 4), and deletes it
// explicitly (step 5); t.Cleanup deletes it again with its own context
// (NotFound there is success, not failure) and asserts no
// vngcloud-live-*.internal zone remains. Each step logs only the zone's own
// status and how long its wait took, never the VPC id.
func TestLiveWriteDNS(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live DNS write test")
	}
	vpcID := os.Getenv("VNGCLOUD_LIVE_DNS_VPC_ID")
	if vpcID == "" {
		t.Skip("set VNGCLOUD_LIVE_DNS_VPC_ID to a VPC with Private DNS ENABLED to run the live DNS write test")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	region := "hcm-3"
	if raw := strings.TrimSpace(os.Getenv("VNGCLOUD_REGIONS")); raw != "" {
		if first := strings.TrimSpace(strings.Split(raw, ",")[0]); first != "" {
			region = first
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion(region),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	vpc, err := network.New(cfg).GetVPC(ctx, &network.GetVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("GetVPC: %s", safeErr(err))
	}
	if vpc.VPC.DNSStatus != "ENABLED" {
		t.Skip("VNGCLOUD_LIVE_DNS_VPC_ID names a VPC whose Private DNS is not ENABLED")
	}

	client := dns.New(cfg)

	// Step 1: delete every leftover vngcloud-live-*.internal zone from a
	// previous run.
	leftovers, err := client.ListHostedZones(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListHostedZones: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers.Items {
		if !isLiveDNSZoneName(leftover.DomainName) {
			continue
		}
		if _, err := client.DeleteHostedZone(ctx, &dns.DeleteHostedZoneInput{HostedZoneID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover zone: %s", safeErr(err))
		}
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover zone(s)", deletedLeftovers)

	// Step 2: create the zone.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	domainName := "vngcloud-live-" + suffix + ".internal"

	start := time.Now()
	created, err := client.CreateHostedZone(ctx, &dns.CreateHostedZoneInput{
		DomainName:  domainName,
		VPCIDs:      []string{vpcID},
		Description: "vngcloud live write test",
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the zone may
		// still have reached the server. Find and delete it by its exact
		// domain name.
		deleteZoneByName(t, client, domainName)
		t.Fatalf("step 2 CreateHostedZone: %s", safeErr(err))
	}
	zoneID := created.HostedZone.ID
	if zoneID == "" {
		deleteZoneByName(t, client, domainName)
		t.Fatal("step 2: CreateHostedZone returned an empty id; the design requires one")
	}
	t.Logf("step 2: created zone, status %s, wait %s", created.HostedZone.Status, time.Since(start))

	// Step 3: register the fallback delete as soon as zoneID is known,
	// before step 4 or step 5 can fail and skip the explicit delete below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteHostedZone(cleanupCtx, &dns.DeleteHostedZoneInput{HostedZoneID: zoneID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete zone: %s", safeErr(err))
		}
		final, err := client.ListHostedZones(cleanupCtx, nil)
		if err != nil {
			t.Errorf("cleanup: final ListHostedZones: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, z := range final.Items {
			if isLiveDNSZoneName(z.DomainName) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live zone(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live zones, found %d", remaining)
		}
	})

	// Step 4: update the zone's description. CreateHostedZone above already
	// waited for StatusActive (or it would have returned an error instead),
	// and UpdateHostedZone waits for the same status with the sent
	// description, so a nil error here already proves both.
	start = time.Now()
	updated, err := client.UpdateHostedZone(ctx, &dns.UpdateHostedZoneInput{
		HostedZoneID: zoneID,
		Description:  vngcloud.Ptr("vngcloud live write test updated"),
	})
	if err != nil {
		t.Fatalf("step 4 UpdateHostedZone: %s", safeErr(err))
	}
	t.Logf("step 4: updated zone, status %s, wait %s", updated.HostedZone.Status, time.Since(start))

	// Step 5: delete the zone explicitly. DELETE is idempotent, so a retry
	// that reaches the server after an earlier attempt already deleted the
	// zone returns NotFound; that is success for a delete, not a failure.
	// t.Cleanup's own delete above then finds it already gone.
	start = time.Now()
	if _, err := client.DeleteHostedZone(ctx, &dns.DeleteHostedZoneInput{HostedZoneID: zoneID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 5 DeleteHostedZone: %s", safeErr(err))
	}
	t.Logf("step 5: deleted zone, wait %s", time.Since(start))
}

// isLiveDNSZoneName reports whether domainName matches the live DNS write
// test's own naming scheme, vngcloud-live-<8 hex>.internal.
func isLiveDNSZoneName(domainName string) bool {
	return strings.HasPrefix(domainName, "vngcloud-live-") && strings.HasSuffix(domainName, ".internal")
}

// deleteZoneByName lists zones by domainName and deletes any match. It is
// used after a CreateHostedZone failure, since a POST that returned an
// error may still have reached the server. It runs on its own timeout, not
// the calling test step's context, so it can still clean up after that
// step's context is the reason the step failed.
func deleteZoneByName(t *testing.T, client *dns.Client, domainName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := client.ListHostedZones(ctx, &dns.ListHostedZonesInput{Name: domainName})
	if err != nil {
		t.Errorf("cleanup: list zones by name: %s", safeErr(err))
		return
	}
	for _, z := range list.Items {
		if z.DomainName != domainName {
			continue
		}
		if _, err := client.DeleteHostedZone(ctx, &dns.DeleteHostedZoneInput{HostedZoneID: z.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete zone by name: %s", safeErr(err))
		}
	}
}

// deleteCheckByName lists checks and deletes the one matching name. It is
// used after a CreateCheck failure, since a POST that returned an error may
// still have reached the server. It runs on its own timeout, not the
// calling test step's context, so it can still clean up after that step's
// context is the reason the step failed.
func deleteCheckByName(t *testing.T, client *monitor.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := client.ListChecks(ctx, nil)
	if err != nil {
		t.Errorf("cleanup: list checks by name: %s", safeErr(err))
		return
	}
	for _, c := range list.Items {
		if c.Name != name {
			continue
		}
		if _, err := client.DeleteCheck(ctx, &monitor.DeleteCheckInput{CheckID: c.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete check by name: %s", safeErr(err))
		}
	}
}
