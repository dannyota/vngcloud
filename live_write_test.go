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
	"reflect"
	"regexp"
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

// TestLiveWriteDNS exercises CreateHostedZone and UpdateHostedZone, and
// CreateRecord, UpdateRecord, and DeleteRecord, against the account named
// in .env.
//
// VNGCLOUD_LIVE_DNS_VPC_ID names the VPC every created zone associates
// with; it never enters the repository, and the test skips when it is
// unset. It also skips, rather than failing, when that VPC's own
// dnsStatus is not ENABLED, since a zone made against a VPC still
// ENABLING Private DNS would only reach ERROR. Neither the VPC id nor
// any other VPC field is logged.
//
// It deletes every leftover vngcloud-live-*.internal zone first, deleting
// each one's own user records before the zone itself since a zone holding
// any record but the server's own NS and SOA cannot be deleted (step 1);
// creates vngcloud-live-<8 hex>.internal against the named VPC (step 2);
// registers the fallback cleanup as soon as the created zone's id is known
// (step 3); creates an A record with two values, an MX record with two
// priorities, and a TXT record with two strings (step 4); updates the A
// record's TTL (step 5); deletes all three records explicitly (step 6);
// updates the zone's description (step 7); and deletes the zone explicitly
// (step 8). t.Cleanup deletes any user record left in the zone, then the
// zone itself, with its own context (NotFound at either is success, not
// failure), and asserts no vngcloud-live-*.internal zone remains. Each step
// logs only statuses, counts, and wait times, never a record's own id,
// name, or value.
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
	leftovers, err := listAllHostedZones(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListHostedZones: %s", safeErr(err))
	}
	deletedLeftovers, deletedLeftoverRecords := 0, 0
	for _, leftover := range leftovers {
		if !isLiveDNSZoneName(leftover.DomainName) {
			continue
		}
		// A zone holding any record but the server's own NS and SOA cannot
		// be deleted, so clear its user records first.
		deletedLeftoverRecords += deleteUserRecords(ctx, t, client, leftover.ID)
		if _, err := client.DeleteHostedZone(ctx, &dns.DeleteHostedZoneInput{HostedZoneID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover zone: %s", safeErr(err))
		}
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover zone(s) and %d leftover record(s)", deletedLeftovers, deletedLeftoverRecords)

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

	// Step 3: register the fallback cleanup as soon as zoneID is known,
	// before any later step can fail and skip the explicit deletes below.
	// It removes the zone's own user records before the zone itself, since a
	// zone holding any record but the server's own NS and SOA cannot be
	// deleted.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		remainingRecords := deleteUserRecords(cleanupCtx, t, client, zoneID)
		t.Logf("cleanup: deleted %d record(s) left in the zone", remainingRecords)
		if _, err := client.DeleteHostedZone(cleanupCtx, &dns.DeleteHostedZoneInput{HostedZoneID: zoneID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete zone: %s", safeErr(err))
		}
		final, err := listAllHostedZones(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListHostedZones: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, z := range final {
			if isLiveDNSZoneName(z.DomainName) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live zone(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live zones, found %d", remaining)
		}
	})

	// Step 4: create an A record with two values, an MX record with two
	// priorities, and a TXT record with two strings. Each create waits for
	// the zone to leave the lock the previous one holds it under, so this
	// loop needs no sleep of its own.
	start = time.Now()
	aRecord, err := client.CreateRecord(ctx, &dns.CreateRecordInput{
		HostedZoneID: zoneID,
		Type:         "A",
		Values:       []dns.RecordValue{{Value: "10.0.0.1"}, {Value: "10.0.0.2"}},
	})
	if err != nil {
		t.Fatalf("step 4 CreateRecord (A): %s", safeErr(err))
	}
	if aRecord.Record.ID == "" {
		t.Fatal("step 4: CreateRecord (A) returned an empty id; the design requires one")
	}
	t.Logf("step 4: created A record, status %s, wait %s", aRecord.Record.Status, time.Since(start))

	start = time.Now()
	mxRecord, err := client.CreateRecord(ctx, &dns.CreateRecordInput{
		HostedZoneID: zoneID,
		Type:         "MX",
		Values:       []dns.RecordValue{{Value: "10 mx1.vngcloud-live.internal"}, {Value: "20 mx2.vngcloud-live.internal"}},
	})
	if err != nil {
		t.Fatalf("step 4 CreateRecord (MX): %s", safeErr(err))
	}
	if mxRecord.Record.ID == "" {
		t.Fatal("step 4: CreateRecord (MX) returned an empty id; the design requires one")
	}
	t.Logf("step 4: created MX record, status %s, wait %s", mxRecord.Record.Status, time.Since(start))

	start = time.Now()
	txtRecord, err := client.CreateRecord(ctx, &dns.CreateRecordInput{
		HostedZoneID: zoneID,
		Type:         "TXT",
		Values:       []dns.RecordValue{{Value: "vngcloud-live-test-1"}, {Value: "vngcloud-live-test-2"}},
	})
	if err != nil {
		t.Fatalf("step 4 CreateRecord (TXT): %s", safeErr(err))
	}
	if txtRecord.Record.ID == "" {
		t.Fatal("step 4: CreateRecord (TXT) returned an empty id; the design requires one")
	}
	t.Logf("step 4: created TXT record, status %s, wait %s", txtRecord.Record.Status, time.Since(start))

	// Step 5: update the A record's TTL. UpdateRecord sends only the
	// changed field and waits for the record to show it.
	start = time.Now()
	updatedA, err := client.UpdateRecord(ctx, &dns.UpdateRecordInput{
		HostedZoneID: zoneID,
		RecordID:     aRecord.Record.ID,
		TTL:          vngcloud.Ptr(120),
	})
	if err != nil {
		t.Fatalf("step 5 UpdateRecord (A): %s", safeErr(err))
	}
	t.Logf("step 5: updated A record, status %s, wait %s", updatedA.Record.Status, time.Since(start))
	if updatedA.Record.TTL != 120 {
		t.Error("step 5: UpdateRecord did not change the A record's TTL: fail")
	}

	// Step 6: delete all three records explicitly. DeleteRecord is
	// idempotent, so t.Cleanup's own record delete, which runs after this
	// test function returns, lists records and finds none left to delete.
	start = time.Now()
	deletedRecords := 0
	for _, id := range []string{aRecord.Record.ID, mxRecord.Record.ID, txtRecord.Record.ID} {
		if _, err := client.DeleteRecord(ctx, &dns.DeleteRecordInput{HostedZoneID: zoneID, RecordID: id}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 6 DeleteRecord: %s", safeErr(err))
		}
		deletedRecords++
	}
	t.Logf("step 6: deleted %d record(s), total wait %s", deletedRecords, time.Since(start))

	// Step 7: update the zone's description. CreateHostedZone above already
	// waited for StatusActive (or it would have returned an error instead),
	// and UpdateHostedZone waits for the same status with the sent
	// description, so a nil error here already proves both.
	start = time.Now()
	updated, err := client.UpdateHostedZone(ctx, &dns.UpdateHostedZoneInput{
		HostedZoneID: zoneID,
		Description:  vngcloud.Ptr("vngcloud live write test updated"),
	})
	if err != nil {
		t.Fatalf("step 7 UpdateHostedZone: %s", safeErr(err))
	}
	t.Logf("step 7: updated zone, status %s, wait %s", updated.HostedZone.Status, time.Since(start))
	if len(updated.HostedZone.AssociatedVPCIDs) == 1 && updated.HostedZone.AssociatedVPCIDs[0] == vpcID {
		t.Log("step 7: description-only update kept the zone's VPC: pass")
	} else {
		t.Error("step 7: description-only update did not keep the zone's VPC: fail")
	}

	// Step 8: delete the zone explicitly. DELETE is idempotent, so a retry
	// that reaches the server after an earlier attempt already deleted the
	// zone returns NotFound; that is success for a delete, not a failure.
	// t.Cleanup's own delete above then finds it already gone.
	start = time.Now()
	if _, err := client.DeleteHostedZone(ctx, &dns.DeleteHostedZoneInput{HostedZoneID: zoneID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 8 DeleteHostedZone: %s", safeErr(err))
	}
	t.Logf("step 8: deleted zone, wait %s", time.Since(start))
}

// liveDNSZoneNamePattern is the live DNS write test's own zone naming
// scheme: vngcloud-live-<8 lowercase hex>.internal, exactly, so a name that
// merely starts and ends the right way, but is not one this test itself
// could have generated, is never swept up as a leftover.
var liveDNSZoneNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}\.internal$`)

// isLiveDNSZoneName reports whether domainName matches liveDNSZoneNamePattern.
func isLiveDNSZoneName(domainName string) bool {
	return liveDNSZoneNamePattern.MatchString(domainName)
}

// listAllHostedZones pages through every hosted zone the account has,
// since a leftover cleanup or a remaining-zone check must not miss a zone
// that landed past the first page.
func listAllHostedZones(ctx context.Context, client *dns.Client) ([]dns.HostedZone, error) {
	var all []dns.HostedZone
	for page := 1; ; page++ {
		out, err := client.ListHostedZones(ctx, &dns.ListHostedZonesInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
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

// deleteUserRecords deletes every record in zoneID except the server's own
// NS and SOA records, which it refuses to delete, and returns how many it
// deleted. It is used both to clear a leftover zone from a previous run and
// from t.Cleanup, since a zone holding any other record cannot itself be
// deleted.
func deleteUserRecords(ctx context.Context, t *testing.T, client *dns.Client, zoneID string) int {
	t.Helper()
	records, err := client.ListRecords(ctx, &dns.ListRecordsInput{HostedZoneID: zoneID})
	if vngcloud.IsNotFound(err) {
		// The zone is already gone, so it holds no records.
		return 0
	}
	if err != nil {
		t.Errorf("delete user records: list: %s", safeErr(err))
		return 0
	}
	deleted := 0
	for _, rec := range records.Items {
		if rec.Type == "NS" || rec.Type == "SOA" {
			continue
		}
		if _, err := client.DeleteRecord(ctx, &dns.DeleteRecordInput{HostedZoneID: zoneID, RecordID: rec.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete user records: delete: %s", safeErr(err))
			continue
		}
		deleted++
	}
	return deleted
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

// liveChannelNamePattern is the live channel write test's own naming
// scheme: vngcloud-live-<8 lowercase hex>, exactly, so a name that merely
// starts with vngcloud-live- but was not generated by this test is never
// swept up as a leftover.
var liveChannelNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveChannelName(name string) bool {
	return liveChannelNamePattern.MatchString(name)
}

// listAllChannelsPageCap bounds listAllChannels' page walk, the same rule
// monitor.GetChannel applies to its own paging: a server that never returns
// an empty page and never reports a TotalItem the walk can reach would
// otherwise turn a cleanup helper into an infinite loop.
const listAllChannelsPageCap = 1000

// listAllChannels pages through every notification channel the account has,
// since a leftover cleanup or a remaining-channel check must not miss a
// channel that landed past the first page. It keeps paging while the items
// seen so far are fewer than the list's TotalItem and the last page was not
// empty, rather than stopping once page reaches TotalPage: the server has
// reported TotalPage wrong against the size actually returned, which would
// otherwise stop the walk before every channel is seen.
func listAllChannels(ctx context.Context, client *monitor.Client) ([]monitor.Channel, error) {
	var all []monitor.Channel
	for page := 1; page <= listAllChannelsPageCap; page++ {
		out, err := client.ListChannels(ctx, &monitor.ListChannelsInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if len(out.Items) == 0 || len(all) >= out.TotalItem {
			return all, nil
		}
	}
	return all, nil
}

// deleteChannelByName lists channels and deletes any whose Name matches
// name. It is used after a CreateChannel failure, since a POST that
// returned an error may still have reached the server. It runs on its own
// timeout, not the calling test step's context, so it can still clean up
// after that step's context is the reason the step failed.
func deleteChannelByName(t *testing.T, client *monitor.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := listAllChannels(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list channels by name: %s", safeErr(err))
		return
	}
	for _, ch := range list {
		if ch.Name != name {
			continue
		}
		if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: ch.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete channel by name: %s", safeErr(err))
		}
	}
}

// TestLiveWriteMonitorChannel exercises CreateChannel, GetChannel,
// UpdateChannel, and DeleteChannel against the account named in .env.
//
// VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL names the webhook URL the created
// channel notifies; it never enters the repository, and the test skips
// when it is unset. Neither the URL nor any header value is ever logged,
// only counts and statuses.
//
// It deletes every leftover vngcloud-live-* channel first (step 1), creates
// vngcloud-live-<8 hex> with one header (step 2), registers the fallback
// delete as soon as the created channel's id is known (step 3), reads it
// back (step 4), updates only its Name to a second vngcloud-live-<8 hex>
// and confirms Address and Headers come back unchanged both in the
// response and in a fresh read, proving UpdateChannel's own read-merge
// rather than trusting the server to keep them (step 5), deletes the
// channel (step 6), deletes it again and confirms the server's 400 maps to
// the SDK's ordinary not-found sentinel (step 7), and confirms no
// vngcloud-live-* channel remains (step 8).
func TestLiveWriteMonitorChannel(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live monitor channel write test")
	}
	webhookURL := os.Getenv("VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL")
	if webhookURL == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL to the approved webhook URL to run the live monitor channel write test")
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
	client := monitor.New(cfg)

	// Step 1: delete every leftover vngcloud-live-* channel from a previous
	// run.
	leftovers, err := listAllChannels(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListChannels: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers {
		if !isLiveChannelName(leftover.Name) {
			continue
		}
		if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover channel: %s", safeErr(err))
		}
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover channel(s)", deletedLeftovers)

	// Step 2: create the channel.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	created, err := client.CreateChannel(ctx, &monitor.CreateChannelInput{
		Name:    name,
		Type:    monitor.ChannelTypeWebhook,
		Address: webhookURL,
		Headers: []monitor.ChannelHeader{{Key: "X-vngcloud-live", Value: "1"}},
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the channel
		// may still have reached the server. Find and delete it by its
		// exact name.
		deleteChannelByName(t, client, name)
		t.Fatalf("step 2 CreateChannel: %s", safeErr(err))
	}
	channelID := created.Channel.ID
	if channelID == "" {
		deleteChannelByName(t, client, name)
		t.Fatal("step 2: CreateChannel returned an empty id; the design requires one")
	}
	t.Logf("step 2: created channel, %d header(s)", len(created.Channel.Headers))

	// Step 3: register the fallback delete as soon as channelID is known,
	// before steps 4 through 6 can fail and skip the explicit deletes below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteChannel(cleanupCtx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete channel: %s", safeErr(err))
		}
		final, err := listAllChannels(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListChannels: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, ch := range final {
			if isLiveChannelName(ch.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live channel(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live channels, found %d", remaining)
		}
	})

	// Step 4: read the channel back.
	read, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: channelID})
	if err != nil {
		t.Fatalf("step 4 GetChannel: %s", safeErr(err))
	}
	if read.Channel.Type != monitor.ChannelTypeWebhook {
		t.Fatalf("step 4: Type = %q, want %q", read.Channel.Type, monitor.ChannelTypeWebhook)
	}
	if len(read.Channel.Headers) != 1 {
		t.Fatalf("step 4: got %d header(s), want 1", len(read.Channel.Headers))
	}
	t.Log("step 4: read channel back, type and header count match")

	// Step 5: update only Name, to a second vngcloud-live-<8 hex> name so a
	// leftover from an interrupted run still matches the sweep in step 1,
	// and confirm Address and Headers come back unchanged: UpdateChannel's
	// own read-merge, not the server, is what keeps them, since the API
	// clears any field a PUT leaves out.
	renameSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 5 generate rename suffix: %v", err)
	}
	newName := "vngcloud-live-" + renameSuffix
	updated, err := client.UpdateChannel(ctx, &monitor.UpdateChannelInput{
		ChannelID: channelID,
		Name:      &newName,
	})
	if err != nil {
		t.Fatalf("step 5 UpdateChannel: %s", safeErr(err))
	}
	if updated.Channel.Name != newName {
		t.Fatal("step 5: UpdateChannel did not change Name")
	}
	if updated.Channel.Address != webhookURL {
		t.Fatal("step 5: UpdateChannel changed Address, want unchanged")
	}
	if len(updated.Channel.Headers) != 1 {
		t.Fatalf("step 5: got %d header(s) after update, want 1 (unchanged)", len(updated.Channel.Headers))
	}
	afterUpdate, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: channelID})
	if err != nil {
		t.Fatalf("step 5 GetChannel: %s", safeErr(err))
	}
	if afterUpdate.Channel.Name != newName || afterUpdate.Channel.Address != webhookURL || len(afterUpdate.Channel.Headers) != 1 {
		t.Fatal("step 5: a fresh read did not confirm the read-merge update")
	}
	t.Log("step 5: updated name; address and headers unchanged, confirmed by a fresh read")

	// Step 6: delete the channel explicitly. DELETE is idempotent, so a
	// retry that reaches the server after an earlier attempt already
	// deleted the channel returns NotFound; that is success for a delete,
	// not a failure. t.Cleanup's own delete above then finds it already
	// gone.
	if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 6 DeleteChannel: %s", safeErr(err))
	}
	t.Log("step 6: deleted channel")

	// Step 7: delete it again. The server answers this specific case with a
	// 400, not a 404; DeleteChannel maps that to the SDK's ordinary
	// not-found sentinel. Only the error code is logged, since the message
	// may name the channel's id.
	_, secondDeleteErr := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: channelID})
	switch {
	case secondDeleteErr == nil:
		t.Log("step 7: second delete succeeded without error")
	case vngcloud.IsNotFound(secondDeleteErr):
		t.Logf("step 7: second delete returned NotFound, code %s", vngcloud.ErrorCode(secondDeleteErr))
	default:
		t.Fatalf("step 7: second delete: unexpected code %s", vngcloud.ErrorCode(secondDeleteErr))
	}

	// Step 8: confirm no vngcloud-live-* channel remains.
	final, err := listAllChannels(ctx, client)
	if err != nil {
		t.Fatalf("step 8 final ListChannels: %s", safeErr(err))
	}
	remaining := 0
	for _, ch := range final {
		if isLiveChannelName(ch.Name) {
			remaining++
		}
	}
	t.Logf("step 8: vngcloud-live channels remaining: %d", remaining)
	if remaining != 0 {
		t.Fatalf("step 8: expected 0 vngcloud-live channels, found %d", remaining)
	}
}

// liveCheckNamePattern is TestLiveWriteMonitorCheckNotifications' own check
// naming scheme: vngcloud-live-<8 lowercase hex>, exactly, so a name that
// merely starts with vngcloud-live- but was not generated by this test is
// never swept up as a leftover. TestLiveWriteMonitor's own leftover sweep
// uses a looser prefix check instead; this tighter pattern matches this
// test's own generated names precisely, the same way liveChannelNamePattern
// does for channels.
var liveCheckNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveCheckName(name string) bool {
	return liveCheckNamePattern.MatchString(name)
}

// TestLiveWriteMonitorCheckNotifications exercises how a check's
// Notifications field interacts with a channel and with UpdateCheck:
// creating a webhook channel, naming it in a check's InAlarm list, pausing
// the check, updating its name while paused, and deleting the channel out
// from under the check.
//
// VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL and VNGCLOUD_LIVE_MONITOR_URL name the
// approved webhook and probe URLs; the test skips when either is unset.
// VNGCLOUD_LIVE_MONITOR_QUOTA is the account's check quota named in this
// run's approval; the test skips instead of creating a check when it is
// unset, is not a positive integer, or the account is already at it after
// step 1's cleanup.
//
// It deletes every leftover vngcloud-live-* channel and check from a
// previous run of this test first (step 1), creates a webhook channel
// (step 2), creates a check naming that channel in InAlarm (step 3),
// registers each resource's own fallback delete as soon as its id is
// known, pauses the check and updates only its name while it is paused,
// confirming the update kept it DISABLED and kept the channel in
// Notifications, both in the update's own response and in a fresh read
// (step 4), deletes the channel and confirms a fresh read of the check no
// longer names it (step 5), and deletes the check (step 6). t.Cleanup
// deletes the check, then the channel, each with its own context (NotFound
// at either is success, not failure), pages every channel list, and
// asserts no vngcloud-live-* channel or check remains. Every step logs
// only counts and statuses, never the check's URL, the channel's address,
// or any header value.
func TestLiveWriteMonitorCheckNotifications(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live monitor check notifications write test")
	}
	webhookURL := os.Getenv("VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL")
	if webhookURL == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL to the approved webhook URL to run the live monitor check notifications write test")
	}
	targetURL := os.Getenv("VNGCLOUD_LIVE_MONITOR_URL")
	if targetURL == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_URL to the approved probe URL to run the live monitor check notifications write test")
	}
	quotaRaw := strings.TrimSpace(os.Getenv("VNGCLOUD_LIVE_MONITOR_QUOTA"))
	quota, quotaErr := strconv.Atoi(quotaRaw)
	if quotaRaw == "" || quotaErr != nil || quota <= 0 {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_QUOTA to the account's check quota (a positive integer) named in this run's approval to run the live monitor check notifications write test")
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
	client := monitor.New(cfg)

	// Step 1: delete every leftover vngcloud-live-* channel and check from a
	// previous run of this test, then skip instead of creating a check when
	// the account is already at the quota this run's approval named.
	leftoverChannels, err := listAllChannels(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListChannels: %s", safeErr(err))
	}
	deletedChannels := 0
	for _, leftover := range leftoverChannels {
		if !isLiveChannelName(leftover.Name) {
			continue
		}
		if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover channel: %s", safeErr(err))
		}
		deletedChannels++
	}
	leftoverChecks, err := client.ListChecks(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListChecks: %s", safeErr(err))
	}
	deletedChecks := 0
	for _, leftover := range leftoverChecks.Items {
		if !isLiveCheckName(leftover.Name) {
			continue
		}
		if _, err := client.DeleteCheck(ctx, &monitor.DeleteCheckInput{CheckID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover check: %s", safeErr(err))
		}
		deletedChecks++
	}
	t.Logf("step 1: deleted %d leftover channel(s) and %d leftover check(s)", deletedChannels, deletedChecks)

	current, err := client.ListChecks(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListChecks (quota check): %s", safeErr(err))
	}
	if len(current.Items) >= quota {
		t.Skipf("step 1: account has %d check(s), at the named quota of %d", len(current.Items), quota)
	}

	locations, err := client.ListLocations(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListLocations: %s", safeErr(err))
	}
	if len(locations.Items) == 0 {
		t.Fatal("step 1: account has no probe locations")
	}
	locationID := locations.Items[0].ID

	// Step 2: create the webhook channel.
	channelSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate channel name suffix: %v", err)
	}
	channelName := "vngcloud-live-" + channelSuffix
	createdChannel, err := client.CreateChannel(ctx, &monitor.CreateChannelInput{
		Name:    channelName,
		Type:    monitor.ChannelTypeWebhook,
		Address: webhookURL,
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the channel
		// may still have reached the server. Find and delete it by its exact
		// name.
		deleteChannelByName(t, client, channelName)
		t.Fatalf("step 2 CreateChannel: %s", safeErr(err))
	}
	channelID := createdChannel.Channel.ID
	if channelID == "" {
		deleteChannelByName(t, client, channelName)
		t.Fatal("step 2: CreateChannel returned an empty id; the design requires one")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteChannel(cleanupCtx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete channel: %s", safeErr(err))
		}
		remainingChannels, err := listAllChannels(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListChannels: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, ch := range remainingChannels {
			if isLiveChannelName(ch.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live channel(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live channels, found %d", remaining)
		}
	})
	t.Log("step 2: created channel")

	// Step 3: create the check, naming the channel in InAlarm.
	checkSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate check name suffix: %v", err)
	}
	checkName := "vngcloud-live-" + checkSuffix
	createdCheck, err := client.CreateCheck(ctx, &monitor.CreateCheckInput{
		Name:          checkName,
		URL:           targetURL,
		Locations:     []string{locationID},
		TestFrequency: longLiveTestFrequency,
		Notifications: monitor.CheckNotifications{InAlarm: []string{channelID}},
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the check may
		// still have reached the server. Find and delete it by its exact
		// name.
		deleteCheckByName(t, client, checkName)
		t.Fatalf("step 3 CreateCheck: %s", safeErr(err))
	}
	checkID := createdCheck.Check.ID
	if checkID == "" {
		deleteCheckByName(t, client, checkName)
		t.Fatal("step 3: CreateCheck returned an empty id; the design requires one")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteCheck(cleanupCtx, &monitor.DeleteCheckInput{CheckID: checkID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete check: %s", safeErr(err))
		}
		remainingChecks, err := client.ListChecks(cleanupCtx, nil)
		if err != nil {
			t.Errorf("cleanup: final ListChecks: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, c := range remainingChecks.Items {
			if isLiveCheckName(c.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live check(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live checks, found %d", remaining)
		}
	})
	if len(createdCheck.Check.Notifications.InAlarm) != 1 || createdCheck.Check.Notifications.InAlarm[0] != channelID {
		t.Fatal("step 3: created check's InAlarm notifications did not name the channel")
	}
	t.Log("step 3: created check naming the channel in InAlarm")

	// Step 4: pause the check, then update only its name while paused, and
	// confirm the update kept it DISABLED and kept the channel in
	// Notifications, both in the update's own response and in a fresh read.
	paused, err := client.PauseCheck(ctx, &monitor.PauseCheckInput{CheckID: checkID})
	if err != nil {
		t.Fatalf("step 4 PauseCheck: %s", safeErr(err))
	}
	if paused.Check.Status != monitor.StatusDisabled {
		t.Fatalf("step 4: status after pause = %s, want %s", paused.Check.Status, monitor.StatusDisabled)
	}
	renamedSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 4 generate renamed check name suffix: %v", err)
	}
	renamedCheckName := "vngcloud-live-" + renamedSuffix
	updated, err := client.UpdateCheck(ctx, &monitor.UpdateCheckInput{
		CheckID: checkID,
		Name:    vngcloud.Ptr(renamedCheckName),
	})
	if err != nil {
		t.Fatalf("step 4 UpdateCheck: %s", safeErr(err))
	}
	if updated.Check.Name != renamedCheckName {
		t.Fatal("step 4: UpdateCheck did not change Name")
	}
	if updated.Check.Status != monitor.StatusDisabled {
		t.Fatalf("step 4: status after update = %s, want %s (a PUT must never re-enable a paused check)",
			updated.Check.Status, monitor.StatusDisabled)
	}
	if len(updated.Check.Notifications.InAlarm) != 1 || updated.Check.Notifications.InAlarm[0] != channelID {
		t.Fatal("step 4: UpdateCheck did not keep the channel in InAlarm")
	}
	afterUpdate, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: checkID})
	if err != nil {
		t.Fatalf("step 4 GetCheck: %s", safeErr(err))
	}
	if afterUpdate.Check.Status != monitor.StatusDisabled || afterUpdate.Check.Name != renamedCheckName {
		t.Fatal("step 4: a fresh read did not confirm the update")
	}
	if len(afterUpdate.Check.Notifications.InAlarm) != 1 || afterUpdate.Check.Notifications.InAlarm[0] != channelID {
		t.Fatal("step 4: a fresh read did not confirm the notifications were kept")
	}
	// UpdateCheck only set Name, so the read-merge shape must have resent
	// every other field unchanged: compare structurally without logging
	// either side, since Config.Request can carry a credential.
	if !reflect.DeepEqual(afterUpdate.Check.Config, createdCheck.Check.Config) {
		t.Error("step 4: a fresh read's Config did not match the created check's Config")
	}
	if !reflect.DeepEqual(afterUpdate.Check.Options, createdCheck.Check.Options) {
		t.Error("step 4: a fresh read's Options did not match the created check's Options")
	}
	if !reflect.DeepEqual(afterUpdate.Check.Locations, createdCheck.Check.Locations) {
		t.Error("step 4: a fresh read's Locations did not match the created check's Locations")
	}
	t.Log("step 4: paused and renamed the check; it stayed DISABLED and kept its notifications")

	// Step 5: delete the channel and confirm a fresh read of the check no
	// longer names it.
	if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil {
		t.Fatalf("step 5 DeleteChannel: %s", safeErr(err))
	}
	afterChannelDelete, err := client.GetCheck(ctx, &monitor.GetCheckInput{CheckID: checkID})
	if err != nil {
		t.Fatalf("step 5 GetCheck: %s", safeErr(err))
	}
	for _, id := range afterChannelDelete.Check.Notifications.InAlarm {
		if id == channelID {
			t.Fatal("step 5: the check still names the deleted channel in InAlarm")
		}
	}
	t.Log("step 5: deleted the channel; the check no longer names it")

	// Step 6: delete the check. DELETE is idempotent, so t.Cleanup's own
	// check delete, which runs after this test function returns, finds it
	// already gone.
	if _, err := client.DeleteCheck(ctx, &monitor.DeleteCheckInput{CheckID: checkID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 6 DeleteCheck: %s", safeErr(err))
	}
	t.Log("step 6: deleted the check")
}

// otpFilePollInterval is how often readLiveOTPFile checks for the OTP file
// while waiting for the owner to save it.
const otpFilePollInterval = 2 * time.Second

// otpFileTimeout bounds how long readLiveOTPFile waits for the OTP file to
// appear and hold a code, so a run where the owner never saves it can never
// hang forever; it fails with a clear message instead.
const otpFileTimeout = 5 * time.Minute

// otpFileMaxPerm is the loosest permission bits readOTPFileOnce accepts on
// the OTP file: no group or other access. A looser mode risks another
// local user reading the OTP before this test does.
const otpFileMaxPerm = 0o077

// errOTPFileNotReady is what readOTPFileOnce returns when the OTP file does
// not exist yet, or exists but is empty once trimmed: the caller should
// keep polling rather than fail.
var errOTPFileNotReady = errors.New("otp file not ready")

// readOTPFileOnce reads path once, without waiting. It returns the trimmed
// code when the file exists, has no group or other permission bits, and
// holds a non-empty trimmed line; errOTPFileNotReady when the file does not
// exist yet or is present but still empty; and any other error, including a
// mode looser than otpFileMaxPerm allows, unwrapped. It never logs path's
// content.
func readOTPFileOnce(path string) (string, error) {
	info, err := os.Stat(path) //nolint:gosec // path is VNGCLOUD_LIVE_MONITOR_OTP_FILE, an operator-chosen local path, not untrusted input
	if err != nil {
		if os.IsNotExist(err) {
			return "", errOTPFileNotReady
		}
		return "", err
	}
	if perm := info.Mode().Perm(); perm&otpFileMaxPerm != 0 {
		return "", fmt.Errorf("mode %v is looser than 0600", perm)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is VNGCLOUD_LIVE_MONITOR_OTP_FILE, an operator-chosen local path, not untrusted input
	if err != nil {
		return "", err
	}
	code := strings.TrimSpace(string(data))
	if code == "" {
		return "", errOTPFileNotReady
	}
	return code, nil
}

// pollOTPFile polls path every pollInterval until readOTPFileOnce returns a
// code, bounded by timeout, then deletes path so a later run never reads a
// code this one already spent. The poll interval and timeout are
// parameters, rather than the otpFilePollInterval and otpFileTimeout
// constants directly, so a test can shorten both instead of waiting out the
// real bounds. It calls no *testing.T method, so a test can check its
// returned error directly instead of needing to catch a t.Fatal call; the
// file's content, and the code itself, are never logged, or included in
// the returned error.
func pollOTPFile(path string, pollInterval, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		code, err := readOTPFileOnce(path)
		if err == nil {
			if rmErr := os.Remove(path); rmErr != nil { //nolint:gosec // path is VNGCLOUD_LIVE_MONITOR_OTP_FILE, an operator-chosen local path, not untrusted input
				return "", fmt.Errorf("remove the OTP file: %w", rmErr)
			}
			return code, nil
		}
		if !errors.Is(err, errOTPFileNotReady) {
			return "", fmt.Errorf("refusing the OTP file: %w", err)
		}
		if time.Now().After(deadline) {
			return "", errors.New("timed out waiting for the OTP file")
		}
		time.Sleep(pollInterval)
	}
}

// readLiveOTPFile returns the OTP TestLiveWriteMonitorChannelOTP validates,
// read from the file named by VNGCLOUD_LIVE_MONITOR_OTP_FILE (path), via
// pollOTPFile with the real otpFilePollInterval and otpFileTimeout.
func readLiveOTPFile(t *testing.T, path string) string {
	t.Helper()
	t.Log("waiting for the OTP in the file named by VNGCLOUD_LIVE_MONITOR_OTP_FILE")
	code, err := pollOTPFile(path, otpFilePollInterval, otpFileTimeout)
	if err != nil {
		t.Fatalf("readLiveOTPFile: %v", err)
	}
	return code
}

// TestLiveWriteMonitorChannelOTP exercises the OTP flow SendChannelOTP,
// CreateChannel, GetChannel, and DeleteChannel take for an Email channel.
//
// VNGCLOUD_LIVE_MONITOR_EMAIL names the address that receives the OTP, and
// VNGCLOUD_LIVE_MONITOR_OTP_FILE names a file the owner saves the emailed
// code into; the test skips before sending anything when either is unset,
// so it never messages the address with no way to read the code back.
// readLiveOTPFile polls for that file, refuses to read it unless its mode
// is 0600 or stricter, and deletes it once read; see readLiveOTPFile.
// Neither the address, the OTP, the ref SendChannelOTP returns, nor the
// file's content is ever logged.
//
// It deletes every leftover vngcloud-live-* channel first (step 1), sends
// the OTP (step 2), reads it back from the file (step 3), creates
// vngcloud-live-<8 hex> as an Email channel with the validated OTP (step
// 4), registers the fallback delete as soon as the created channel's id is
// known (step 5), reads the channel back and confirms its type (step 6),
// and deletes it (step 7). t.Cleanup deletes it again with its own context
// (NotFound there is success, not failure), pages every channel list, and
// asserts no vngcloud-live-* channel remains. Every step logs only counts,
// statuses, and the OTP's expiry time, never the address, the OTP, or the
// ref.
func TestLiveWriteMonitorChannelOTP(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live monitor channel OTP write test")
	}
	email := os.Getenv("VNGCLOUD_LIVE_MONITOR_EMAIL")
	if email == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_EMAIL to the approved address to run the live monitor channel OTP write test")
	}
	otpFile := os.Getenv("VNGCLOUD_LIVE_MONITOR_OTP_FILE")
	if otpFile == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_OTP_FILE to a path to poll for the emailed OTP to run the live monitor channel OTP write test")
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
	client := monitor.New(cfg)

	// Step 1: delete every leftover vngcloud-live-* channel from a previous
	// run.
	leftovers, err := listAllChannels(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListChannels: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers {
		if !isLiveChannelName(leftover.Name) {
			continue
		}
		if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover channel: %s", safeErr(err))
		}
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover channel(s)", deletedLeftovers)

	// Step 2: send the OTP.
	sent, err := client.SendChannelOTP(ctx, &monitor.SendChannelOTPInput{
		Type:    monitor.ChannelTypeEmail,
		Address: email,
	})
	if err != nil {
		t.Fatalf("step 2 SendChannelOTP: %s", safeErr(err))
	}
	t.Logf("step 2: sent otp, expires %s", sent.ExpiresAt.Format(time.RFC3339))

	// Step 3: read the OTP back from the file the owner saves it into.
	otp := readLiveOTPFile(t, otpFile)
	t.Log("step 3: read otp from file")

	// Step 4: create the channel with the validated OTP.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 4 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	created, err := client.CreateChannel(ctx, &monitor.CreateChannelInput{
		Name:    name,
		Type:    monitor.ChannelTypeEmail,
		Address: email,
		OTPRef:  sent.Ref,
		OTP:     otp,
	})
	if err != nil {
		if errors.Is(err, monitor.ErrOTPRejected) {
			t.Fatal("step 4 CreateChannel: otp rejected; rerun and enter the latest emailed code")
		}
		// A POST is not retried after an ambiguous failure, so the channel
		// may still have reached the server. Find and delete it by its
		// exact name.
		deleteChannelByName(t, client, name)
		t.Fatalf("step 4 CreateChannel: %s", safeErr(err))
	}
	channelID := created.Channel.ID
	if channelID == "" {
		deleteChannelByName(t, client, name)
		t.Fatal("step 4: CreateChannel returned an empty id; the design requires one")
	}
	t.Log("step 4: created email channel")

	// Step 5: register the fallback delete as soon as channelID is known,
	// before steps 6 and 7 can fail and skip the explicit delete below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteChannel(cleanupCtx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete channel: %s", safeErr(err))
		}
		final, err := listAllChannels(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListChannels: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, ch := range final {
			if isLiveChannelName(ch.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live channel(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live channels, found %d", remaining)
		}
	})

	// Step 6: read the channel back and confirm its type.
	read, err := client.GetChannel(ctx, &monitor.GetChannelInput{ChannelID: channelID})
	if err != nil {
		t.Fatalf("step 6 GetChannel: %s", safeErr(err))
	}
	if read.Channel.Type != monitor.ChannelTypeEmail {
		t.Fatalf("step 6: Type = %q, want %q", read.Channel.Type, monitor.ChannelTypeEmail)
	}
	t.Log("step 6: read channel back, type matches")

	// Step 7: delete the channel explicitly. DELETE is idempotent, so
	// t.Cleanup's own delete above then finds it already gone.
	if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 7 DeleteChannel: %s", safeErr(err))
	}
	t.Log("step 7: deleted channel")
}

// TestReadOTPFileOnce checks readOTPFileOnce's per-call rules against a real
// filesystem: a missing or still-empty file reports errOTPFileNotReady so
// the caller keeps polling, a mode with any group or other bit is refused
// outright, and an owner-only mode returns the trimmed code. This never
// runs against the live API; it needs no VNGCLOUD_LIVE_WRITE.
func TestReadOTPFileOnce(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	t.Run("missing file is not ready", func(t *testing.T) {
		_, err := readOTPFileOnce(filepath.Join(dir, "missing.txt"))
		if !errors.Is(err, errOTPFileNotReady) {
			t.Fatalf("err = %v, want errOTPFileNotReady", err)
		}
	})

	t.Run("empty file is not ready", func(t *testing.T) {
		path := write("empty.txt", "", 0o600)
		_, err := readOTPFileOnce(path)
		if !errors.Is(err, errOTPFileNotReady) {
			t.Fatalf("err = %v, want errOTPFileNotReady", err)
		}
	})

	t.Run("whitespace-only file is not ready", func(t *testing.T) {
		path := write("blank.txt", "  \n\t", 0o600)
		_, err := readOTPFileOnce(path)
		if !errors.Is(err, errOTPFileNotReady) {
			t.Fatalf("err = %v, want errOTPFileNotReady", err)
		}
	})

	t.Run("group-readable mode is refused", func(t *testing.T) {
		path := write("group.txt", "123456", 0o640)
		_, err := readOTPFileOnce(path)
		if err == nil || errors.Is(err, errOTPFileNotReady) {
			t.Fatalf("err = %v, want a mode refusal", err)
		}
	})

	t.Run("other-readable mode is refused", func(t *testing.T) {
		path := write("other.txt", "123456", 0o604)
		_, err := readOTPFileOnce(path)
		if err == nil || errors.Is(err, errOTPFileNotReady) {
			t.Fatalf("err = %v, want a mode refusal", err)
		}
	})

	t.Run("0600 is accepted and trimmed", func(t *testing.T) {
		path := write("ok.txt", " 123456 \n", 0o600)
		code, err := readOTPFileOnce(path)
		if err != nil {
			t.Fatalf("readOTPFileOnce() error = %v", err)
		}
		if code != "123456" {
			t.Fatalf("code = %q, want %q", code, "123456")
		}
	})

	t.Run("0400 is stricter and accepted", func(t *testing.T) {
		path := write("strict.txt", "654321", 0o400)
		code, err := readOTPFileOnce(path)
		if err != nil {
			t.Fatalf("readOTPFileOnce() error = %v", err)
		}
		if code != "654321" {
			t.Fatalf("code = %q, want %q", code, "654321")
		}
	})
}

// TestPollOTPFileReadsAndDeletes checks pollOTPFile returns the code already
// waiting in the file and deletes the file, without needing to poll.
func TestPollOTPFileReadsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "otp.txt")
	if err := os.WriteFile(path, []byte("123456\n"), 0o600); err != nil {
		t.Fatalf("write OTP file: %v", err)
	}

	code, err := pollOTPFile(path, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("pollOTPFile() error = %v", err)
	}
	if code != "123456" {
		t.Fatalf("code = %q, want %q", code, "123456")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected the OTP file to be deleted")
	}
}

// TestPollOTPFileWaitsForFile checks pollOTPFile keeps polling until the
// file appears, rather than failing on its first, empty-handed check.
func TestPollOTPFileWaitsForFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "otp.txt")

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(path, []byte("654321"), 0o600)
	}()

	code, err := pollOTPFile(path, time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("pollOTPFile() error = %v", err)
	}
	if code != "654321" {
		t.Fatalf("code = %q, want %q", code, "654321")
	}
}

// TestPollOTPFileTimesOut checks pollOTPFile returns an error, rather than
// hanging, when the file never appears within timeout.
func TestPollOTPFileTimesOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "otp.txt") // never created

	if _, err := pollOTPFile(path, time.Millisecond, 20*time.Millisecond); err == nil {
		t.Fatal("expected an error when the OTP file never appears")
	}
}
