//go:build livewrite

package vngcloud_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/compute"
	"danny.vn/vngcloud/containerregistry"
	"danny.vn/vngcloud/dns"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
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
func randomHex(n int) (string, error) { //nolint:unparam // every caller wants 4 bytes today; n keeps the length explicit at each call
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

// liveLogProjectNamePattern is the live log project write test's own naming
// scheme: vngcloud-live-<8 lowercase hex>, exactly, so a name that merely
// starts with vngcloud-live- but was not generated by this test is never
// swept up as a leftover.
var liveLogProjectNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveLogProjectName(name string) bool {
	return liveLogProjectNamePattern.MatchString(name)
}

// countLiveLogProjects counts how many of projects are named
// vngcloud-live-*.
func countLiveLogProjects(projects []monitor.LogProject) int {
	n := 0
	for _, p := range projects {
		if isLiveLogProjectName(p.ProjectName) {
			n++
		}
	}
	return n
}

// listAllLogProjectsPageCap bounds listAllLogProjects' page walk, the same
// rule listAllChannels applies to its own paging: a server that never
// returns an empty page and never reports a TotalItem the walk can reach
// would otherwise turn a cleanup helper into an infinite loop.
const listAllLogProjectsPageCap = 1000

// listAllLogProjects pages through every log project the account has.
// Unlike listAllChannels' 1-based page, ListLogProjects' own page is
// 0-based, so the walk starts at page 0.
func listAllLogProjects(ctx context.Context, client *monitor.Client) ([]monitor.LogProject, error) {
	var all []monitor.LogProject
	for page := 0; page < listAllLogProjectsPageCap; page++ {
		out, err := client.ListLogProjects(ctx, &monitor.ListLogProjectsInput{Page: page})
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

// deleteLiveLogProjects deletes and purges every log project whose name
// matches liveLogProjectNamePattern, on its own context, and returns how
// many it swept up. It never touches a project whose name does not match:
// the account may hold other, non-test projects. NoWait skips each
// project's own settle wait, since this helper's caller does its own
// final check that none remain.
//
// A 409 Conflict from the delete, seen live once for a project about an
// hour old with no known cause, is logged rather than treated as a test
// failure, and the sweep moves on to the next project: the SDK adds no
// retry of its own for a 409, and the project that hit it was gone on its
// own shortly after, so the caller's own final check is what confirms
// whether it is still there.
func deleteLiveLogProjects(ctx context.Context, t *testing.T, client *monitor.Client) int {
	t.Helper()
	all, err := listAllLogProjects(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list log projects: %s", safeErr(err))
		return 0
	}
	swept := 0
	for _, p := range all {
		if !isLiveLogProjectName(p.ProjectName) {
			continue
		}
		_, err := client.DeleteLogProject(ctx, &monitor.DeleteLogProjectInput{
			LogProjectID: p.ID, Purge: true, NoWait: true,
		})
		switch {
		case err == nil, vngcloud.IsNotFound(err):
			swept++
		case isConflictErr(err):
			t.Logf("cleanup: delete/purge log project returned Conflict, code %s", vngcloud.ErrorCode(err))
		default:
			t.Errorf("cleanup: delete/purge log project: %s", safeErr(err))
		}
	}
	return swept
}

// isConflictErr reports whether err is a *vngcloud.APIError with status
// 409, the status a live sweep saw once for a log project delete with no
// known cause.
func isConflictErr(err error) bool {
	var apiErr *vngcloud.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict
}

// TestLiveWriteMonitorLogProject exercises CreateLogProject, GetLogProject,
// and DeleteLogProject (delete and purge in one call) against the account
// named in .env.
//
// The Basic class allows only 3 orders or recoveries a month before an
// order returns the server's own 409 "Exceeded quota"; this test orders
// one project, then deletes and purges it, per the owner's one-time
// approval for this specific run. VNGCLOUD_LIVE_MONITOR_LOG_PROJECT must
// be set to "1" in addition to VNGCLOUD_LIVE_WRITE, so this test never
// runs alongside the account's other live write tests by accident.
//
// It deletes and purges every leftover vngcloud-live-* log project first,
// in case an earlier aborted run left one in trash or still active (step
// 1); quotes the Basic class and refuses to go any further if it does not
// price at 0 VND (step 2); registers the fallback cleanup by name before
// ordering anything, since a POST that fails ambiguously may still have
// reached the server (step 3); orders the project with MaxPrice 0,
// recording how long CreateLogProject's own wait took to see it reach
// ACTIVE and whether the order response carried an OrderID, seen empty for
// a free order (step 4); reads it back (step 5); deletes and purges it in
// one DeleteLogProject call (step 6) -- a combined delete-then-purge this
// test itself has not yet run live, though a separate purge sent right
// after an earlier, already-settled delete was seen live to return a
// plain 409 Conflict once; confirms a read of it now returns not-found
// (step 7); and quotes, but does not order, a second Basic project, to
// record whether a fresh quote still prices free right after a purge
// (step 8). A free project was seen live to leave the log-api list, the
// billing list, and trash on its own within a few seconds of a delete, so
// a second, separate delete or purge call on the same, already-removed
// project is not exercised here: it is not a useful test, and was seen
// live to return a plain 409 Conflict or 404 instead of settling. Every
// step logs only counts, statuses, field names, and timings, never the
// project's name, id, or any other field value.
func TestLiveWriteMonitorLogProject(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live monitor log project write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_MONITOR_LOG_PROJECT") != "1" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_LOG_PROJECT=1 to run the live log project write test; " +
			"the Basic class allows only 3 orders or recoveries a month, and this test orders, deletes, and purges one")
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

	// Step 1: sweep up leftovers from an earlier aborted run first, so an
	// earlier run's project does not linger as an extra live resource.
	leftovers := deleteLiveLogProjects(ctx, t, client)
	t.Logf("step 1: deleted and purged %d leftover log project(s)", leftovers)

	// Step 2: quote the Basic class and refuse to order anything unless it
	// prices at 0 VND, the design's own recorded live price for Basic.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix
	quote, err := client.QuoteCreateLogProject(ctx, &monitor.CreateLogProjectInput{
		Name: name, Class: monitor.LogProjectClassBasic,
	})
	if err != nil {
		t.Fatalf("step 2 QuoteCreateLogProject: %s", safeErr(err))
	}
	if quote.OptimumPrice != 0 {
		t.Fatalf("step 2: Basic quote = %.0f VND, want 0; refusing to order", quote.OptimumPrice)
	}
	t.Log("step 2: Basic quote is 0 VND")

	// Step 3: register the fallback cleanup by name before ordering, since
	// a POST that fails ambiguously may still have reached the server.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		swept := deleteLiveLogProjects(cleanupCtx, t, client)
		t.Logf("cleanup: deleted and purged %d vngcloud-live log project(s)", swept)

		all, err := listAllLogProjects(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final list log projects: %s", safeErr(err))
			return
		}
		remaining := countLiveLogProjects(all)
		if remaining != 0 {
			// A project the sweep above hit a Conflict on can still
			// disappear on its own shortly after (seen live within about
			// an hour, cause unknown); wait once for that rather than
			// treating it as leaked on the first list.
			time.Sleep(30 * time.Second)
			all, err = listAllLogProjects(cleanupCtx, client)
			if err != nil {
				t.Errorf("cleanup: final list log projects (recheck): %s", safeErr(err))
				return
			}
			remaining = countLiveLogProjects(all)
		}
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live log projects, found %d", remaining)
		}
	})

	// Step 4: order the free Basic project. MaxPrice 0 refuses the order if
	// the price changed between step 2's quote and CreateLogProject's own.
	createStart := time.Now()
	created, err := client.CreateLogProject(ctx, &monitor.CreateLogProjectInput{
		Name: name, Class: monitor.LogProjectClassBasic, MaxPrice: 0,
	})
	createElapsed := time.Since(createStart)
	if err != nil {
		t.Fatalf("step 4 CreateLogProject: %s", safeErr(err))
	}
	t.Logf("step 4: settled after %s, order id present: %v, status %s",
		createElapsed, created.OrderID != "", created.LogProject.Status)
	if created.LogProject.Status != monitor.LogProjectStatusActive {
		t.Fatalf("step 4: status = %s, want %s", created.LogProject.Status, monitor.LogProjectStatusActive)
	}
	if created.LogProject.ID == "" {
		t.Fatal("step 4: CreateLogProject settled with no id")
	}
	projectID := created.LogProject.ID

	// Step 5: read the project back to confirm GetLogProject decodes this
	// order's own project the same way ListLogProjects did.
	if _, err := client.GetLogProject(ctx, &monitor.GetLogProjectInput{LogProjectID: projectID}); err != nil {
		t.Fatalf("step 5 GetLogProject: %s", safeErr(err))
	}
	t.Log("step 5: read the project back")

	// Step 6: delete and purge it in one call, the main way Purge is used.
	deleteStart := time.Now()
	if _, err := client.DeleteLogProject(ctx, &monitor.DeleteLogProjectInput{LogProjectID: projectID, Purge: true}); err != nil {
		t.Fatalf("step 6 DeleteLogProject(Purge): %s", safeErr(err))
	}
	t.Logf("step 6: delete and purge settled after %s", time.Since(deleteStart))

	// Step 7: confirm the project is gone.
	if _, err := client.GetLogProject(ctx, &monitor.GetLogProjectInput{LogProjectID: projectID}); !vngcloud.IsNotFound(err) {
		t.Fatalf("step 7: GetLogProject after purge = %s, want NotFound", safeErr(err))
	}
	t.Log("step 7: confirmed the project is gone")

	// Step 8: quote, but do not order, a second Basic project, to record
	// whether a fresh quote still prices free right after the purge above.
	secondSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 8 generate name suffix: %v", err)
	}
	secondQuote, err := client.QuoteCreateLogProject(ctx, &monitor.CreateLogProjectInput{
		Name: "vngcloud-live-" + secondSuffix, Class: monitor.LogProjectClassBasic,
	})
	if err != nil {
		t.Fatalf("step 8 QuoteCreateLogProject: %s", safeErr(err))
	}
	t.Logf("step 8: a second Basic quote after purge prices at %.0f VND", secondQuote.OptimumPrice)
}

// liveSecurityGroupNamePattern is the live network write test's own naming
// scheme: vngcloud-live-<8 lowercase hex>, exactly, so a name that merely
// starts with vngcloud-live- but was not generated by this test is never
// swept up as a leftover.
var liveSecurityGroupNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveSecurityGroupName(name string) bool {
	return liveSecurityGroupNamePattern.MatchString(name)
}

// listAllSecurityGroups pages through every security group the account has,
// since a leftover cleanup or a remaining-group check must not miss a group
// that landed past the first page.
func listAllSecurityGroups(ctx context.Context, client *network.Client) ([]network.SecurityGroup, error) {
	var all []network.SecurityGroup
	for page := 1; ; page++ {
		out, err := client.ListSecurityGroups(ctx, &network.ListSecurityGroupsInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// deleteSecurityGroupAndRules deletes every rule in groupID, then the group
// itself, ignoring NotFound at either step: it is used both for a leftover
// group from a previous run and from t.Cleanup, so it never fails the test
// merely because the group or a rule is already gone.
func deleteSecurityGroupAndRules(ctx context.Context, t *testing.T, client *network.Client, groupID string) {
	t.Helper()
	rules, err := client.ListSecurityGroupRules(ctx, &network.ListSecurityGroupRulesInput{SecurityGroupID: groupID})
	switch {
	case err == nil:
		for _, rule := range rules.Items {
			if _, err := client.DeleteSecurityGroupRule(ctx, &network.DeleteSecurityGroupRuleInput{
				SecurityGroupID: groupID, SecurityGroupRuleID: rule.ID,
			}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("delete security group: delete rule: %s", safeErr(err))
			}
		}
	case vngcloud.IsNotFound(err):
		// The group is already gone; it holds no rules to delete.
	default:
		t.Errorf("delete security group: list rules: %s", safeErr(err))
	}
	if _, err := client.DeleteSecurityGroup(ctx, &network.DeleteSecurityGroupInput{SecurityGroupID: groupID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Errorf("delete security group: delete group: %s", safeErr(err))
	}
}

// deleteSecurityGroupByName lists security groups and deletes any exact
// match for name. It is used after a CreateSecurityGroup failure, since a
// POST that returned an error may still have reached the server. It runs on
// its own timeout, not the calling test step's context, so it can still
// clean up after that step's context is the reason the step failed.
func deleteSecurityGroupByName(t *testing.T, client *network.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := listAllSecurityGroups(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list security groups by name: %s", safeErr(err))
		return
	}
	for _, g := range list {
		if g.Name != name {
			continue
		}
		deleteSecurityGroupAndRules(ctx, t, client, g.ID)
	}
}

// TestLiveWriteNetworkSecurityGroup exercises CreateSecurityGroup,
// UpdateSecurityGroup, DeleteSecurityGroup, CreateSecurityGroupRule, and
// DeleteSecurityGroupRule against the account named in .env. Security
// groups and rules cost nothing (see the design), unlike every other
// vServer write.
//
// It deletes every leftover vngcloud-live-* group with no servers attached
// first, deleting each one's rules before the group itself (step 1);
// creates vngcloud-live-<8 hex> (step 2); registers the fallback cleanup as
// soon as the created group's id is known (step 3); creates the same name
// again and logs the server's refusal (step 4); updates the group three
// ways, a new description, an explicit empty description, and a
// description left out, confirming each leaves Name unchanged (step 5);
// creates a tcp/22 rule from 203.0.113.0/24 (step 6); creates the identical
// rule again and logs the duplicate refusal (step 7); creates a second rule
// from a host prefix, 203.0.113.5/24, and logs whether the server stored it
// as sent or masked it to the network address (step 8); creates an icmp
// rule (step 9); deletes the tcp/22 rule right after its own create and
// logs the delete's timing (step 10); attempts to delete that same rule
// again, but through a second, unrelated group's id: the SDK lists the
// named group's own rules first and refuses a rule not found there, so this
// sends no request, and whether the server itself would also ignore the
// group named in the path stays an open question this test cannot answer
// through the public SDK (step 11); repeats the rule delete against its
// real group and logs the second delete's status (step 12); deletes the
// group and logs the status and the time until a GetSecurityGroup read
// 404s (step 13); and repeats the group delete, logging its status (step
// 14). Every step logs only statuses, counts, field names, and timings,
// never a group, rule, or server's own id or name.
func TestLiveWriteNetworkSecurityGroup(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network security group write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_SECGROUP") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_SECGROUP=1 to run the live network security group write test")
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
	client := network.New(cfg)

	// Step 1: delete every leftover vngcloud-live-* group with no servers
	// attached, from a previous run. A group still holding a server, or the
	// project's own system group, is left alone.
	leftovers, err := listAllSecurityGroups(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListSecurityGroups: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers {
		if !isLiveSecurityGroupName(leftover.Name) || leftover.System {
			continue
		}
		servers, err := client.ListServersBySecurityGroup(ctx, &network.ListServersBySecurityGroupInput{SecurityGroupID: leftover.ID})
		if err != nil {
			t.Fatalf("step 1 ListServersBySecurityGroup: %s", safeErr(err))
		}
		if len(servers.Items) > 0 {
			continue
		}
		deleteSecurityGroupAndRules(ctx, t, client, leftover.ID)
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover group(s)", deletedLeftovers)

	// Step 2: create the group.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	start := time.Now()
	created, err := client.CreateSecurityGroup(ctx, &network.CreateSecurityGroupInput{
		Name:        name,
		Description: "vngcloud live write test",
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the group may
		// still have reached the server. Find and delete it by its exact name.
		deleteSecurityGroupByName(t, client, name)
		t.Fatalf("step 2 CreateSecurityGroup: %s", safeErr(err))
	}
	groupID := created.SecurityGroup.ID
	if groupID == "" {
		deleteSecurityGroupByName(t, client, name)
		t.Fatal("step 2: CreateSecurityGroup returned an empty id; the design requires one")
	}
	t.Logf("step 2: created group, status %s, wait %s", created.SecurityGroup.Status, time.Since(start))

	// Step 3: register the fallback cleanup as soon as groupID is known,
	// before any later step can fail and skip the explicit deletes below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteSecurityGroupAndRules(cleanupCtx, t, client, groupID)
		final, err := listAllSecurityGroups(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListSecurityGroups: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, g := range final {
			if isLiveSecurityGroupName(g.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live group(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live groups, found %d", remaining)
		}
	})

	// Step 4: create the same name again; names are unique per project, so
	// the design expects a refusal. An unexpected success is cleaned up
	// too, since it would otherwise leak a second group under the same name.
	dup, dupErr := client.CreateSecurityGroup(ctx, &network.CreateSecurityGroupInput{Name: name, NoWait: true})
	if dupErr == nil {
		t.Error("step 4: creating a duplicate name succeeded; the design expects a refusal")
		if dup.SecurityGroup.ID != "" {
			deleteSecurityGroupAndRules(ctx, t, client, dup.SecurityGroup.ID)
		}
	} else {
		t.Logf("step 4: duplicate name refused, %s", safeErr(dupErr))
	}

	// Step 5: update the group three ways. A rename leaves Description
	// out, so the step checks the read-first update kept it.
	update := func(step string, newName, desc *string) {
		out, err := client.UpdateSecurityGroup(ctx, &network.UpdateSecurityGroupInput{
			SecurityGroupID: groupID,
			Name:            newName,
			Description:     desc,
		})
		if err != nil {
			t.Fatalf("%s UpdateSecurityGroup: %s", step, safeErr(err))
		}
		if newName != nil {
			name = *newName
		}
		if out.SecurityGroup.Name != name {
			t.Errorf("%s: Name is not the expected value", step)
		}
		t.Logf("%s: updated group, description length %d", step, len(out.SecurityGroup.Description))
	}
	const updatedDescription = "vngcloud live write test updated"
	update("step 5a (new description)", nil, vngcloud.Ptr(updatedDescription))
	renameSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 5b generate name suffix: %v", err)
	}
	update("step 5b (rename, description left out)", vngcloud.Ptr("vngcloud-live-"+renameSuffix), nil)
	if got, err := client.GetSecurityGroup(ctx, &network.GetSecurityGroupInput{SecurityGroupID: groupID}); err != nil {
		t.Fatalf("step 5b GetSecurityGroup: %s", safeErr(err))
	} else if got.SecurityGroup.Description != updatedDescription {
		t.Errorf("step 5b: rename changed Description (length %d)", len(got.SecurityGroup.Description))
	}
	update("step 5c (empty description)", nil, vngcloud.Ptr(""))

	// Step 6: create a tcp/22 rule from 203.0.113.0/24.
	start = time.Now()
	ruleCreated, err := client.CreateSecurityGroupRule(ctx, &network.CreateSecurityGroupRuleInput{
		SecurityGroupID: groupID,
		Direction:       "ingress",
		Protocol:        "tcp",
		RemoteIPPrefix:  "203.0.113.0/24",
		PortRangeMin:    22,
	})
	if err != nil {
		t.Fatalf("step 6 CreateSecurityGroupRule: %s", safeErr(err))
	}
	ruleID := ruleCreated.SecurityGroupRule.ID
	if ruleID == "" {
		t.Fatal("step 6: CreateSecurityGroupRule returned an empty id; the design requires one")
	}
	t.Logf("step 6: created rule, etherType %s, portRangeMax %d, wait %s",
		ruleCreated.SecurityGroupRule.EtherType, ruleCreated.SecurityGroupRule.PortRangeMax, time.Since(start))

	// Step 7: create the identical rule again; the design expects the
	// server's own SecurityGroupRuleExists refusal. An unexpected success's
	// rule lives in groupID, so t.Cleanup's own rule sweep deletes it along
	// with every other rule in the group.
	_, dupRuleErr := client.CreateSecurityGroupRule(ctx, &network.CreateSecurityGroupRuleInput{
		SecurityGroupID: groupID,
		Direction:       "ingress",
		Protocol:        "tcp",
		RemoteIPPrefix:  "203.0.113.0/24",
		PortRangeMin:    22,
	})
	if dupRuleErr == nil {
		t.Error("step 7: creating a duplicate rule succeeded; the design expects a refusal")
	} else {
		t.Logf("step 7: duplicate rule refused, %s", safeErr(dupRuleErr))
	}

	// Step 8: create a second rule from a host prefix and log whether the
	// server stored it as sent or masked it to the network address. It uses
	// its own port: the server refuses a rule that overlaps step 6's.
	hostRule, err := client.CreateSecurityGroupRule(ctx, &network.CreateSecurityGroupRuleInput{
		SecurityGroupID: groupID,
		Direction:       "ingress",
		Protocol:        "tcp",
		RemoteIPPrefix:  "203.0.113.5/24",
		PortRangeMin:    2222,
	})
	if err != nil {
		t.Fatalf("step 8 CreateSecurityGroupRule: %s", safeErr(err))
	}
	if hostRule.SecurityGroupRule.RemoteIPPrefix == "203.0.113.5/24" {
		t.Log("step 8: server stored the prefix as sent")
	} else {
		t.Log("step 8: server masked the prefix to the network address")
	}

	// Step 9: create an icmp rule; icmp needs no port range, so PortRangeMin
	// and PortRangeMax are left at 0.
	icmpRule, err := client.CreateSecurityGroupRule(ctx, &network.CreateSecurityGroupRuleInput{
		SecurityGroupID: groupID,
		Direction:       "ingress",
		Protocol:        "icmp",
		RemoteIPPrefix:  "203.0.113.0/24",
	})
	if err != nil {
		t.Fatalf("step 9 CreateSecurityGroupRule (icmp): %s", safeErr(err))
	}
	t.Logf("step 9: created icmp rule, portRangeMin %d, portRangeMax %d",
		icmpRule.SecurityGroupRule.PortRangeMin, icmpRule.SecurityGroupRule.PortRangeMax)

	// Step 10: delete the tcp/22 rule right after its own create.
	start = time.Now()
	if _, err := client.DeleteSecurityGroupRule(ctx, &network.DeleteSecurityGroupRuleInput{
		SecurityGroupID: groupID, SecurityGroupRuleID: ruleID,
	}); err != nil {
		t.Fatalf("step 10 DeleteSecurityGroupRule: %s", safeErr(err))
	}
	t.Logf("step 10: deleted rule, wait %s", time.Since(start))

	// Step 11: attempt to delete the same rule again, but through a second,
	// unrelated group's id.
	otherSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 11 generate name suffix: %v", err)
	}
	otherName := "vngcloud-live-" + otherSuffix
	otherGroup, err := client.CreateSecurityGroup(ctx, &network.CreateSecurityGroupInput{
		Name: otherName, NoWait: true,
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the group may
		// still have reached the server. Find and delete it by its exact name.
		deleteSecurityGroupByName(t, client, otherName)
		t.Fatalf("step 11 CreateSecurityGroup (other group): %s", safeErr(err))
	}
	otherGroupID := otherGroup.SecurityGroup.ID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteSecurityGroupAndRules(cleanupCtx, t, client, otherGroupID)
	})
	_, crossGroupErr := client.DeleteSecurityGroupRule(ctx, &network.DeleteSecurityGroupRuleInput{
		SecurityGroupID: otherGroupID, SecurityGroupRuleID: ruleID,
	})
	if !vngcloud.IsNotFound(crossGroupErr) {
		t.Errorf("step 11: err = %s, want the SDK's own NotFound guard", safeErr(crossGroupErr))
	} else {
		t.Log("step 11: the SDK's rule-in-group guard refused the cross-group delete")
	}

	// Step 12: repeat the rule delete against its real group and log the
	// second delete's status.
	_, secondRuleDeleteErr := client.DeleteSecurityGroupRule(ctx, &network.DeleteSecurityGroupRuleInput{
		SecurityGroupID: groupID, SecurityGroupRuleID: ruleID,
	})
	switch {
	case secondRuleDeleteErr == nil:
		t.Log("step 12: second rule delete succeeded without error")
	case vngcloud.IsNotFound(secondRuleDeleteErr):
		t.Log("step 12: second rule delete returned NotFound")
	default:
		t.Logf("step 12: second rule delete: %s", safeErr(secondRuleDeleteErr))
	}

	// Step 13: delete the group and log the status and the time until a
	// GetSecurityGroup read 404s.
	start = time.Now()
	if _, err := client.DeleteSecurityGroup(ctx, &network.DeleteSecurityGroupInput{SecurityGroupID: groupID}); err != nil {
		t.Fatalf("step 13 DeleteSecurityGroup: %s", safeErr(err))
	}
	for {
		if _, err := client.GetSecurityGroup(ctx, &network.GetSecurityGroupInput{SecurityGroupID: groupID}); vngcloud.IsNotFound(err) {
			break
		}
		if time.Since(start) > time.Minute {
			t.Error("step 13: group still readable one minute after delete")
			break
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("step 13: deleted group, 404 confirmed after %s", time.Since(start))

	// Step 14: repeat the group delete and log its status.
	_, secondGroupDeleteErr := client.DeleteSecurityGroup(ctx, &network.DeleteSecurityGroupInput{SecurityGroupID: groupID})
	switch {
	case secondGroupDeleteErr == nil:
		t.Log("step 14: second group delete succeeded without error")
	case vngcloud.IsNotFound(secondGroupDeleteErr):
		t.Log("step 14: second group delete returned NotFound")
	default:
		t.Logf("step 14: second group delete: %s", safeErr(secondGroupDeleteErr))
	}
}

// sshRSAPublicKeyLine returns pub encoded as an OpenSSH public key line
// ("ssh-rsa <base64> <comment>"), built directly from the SSH wire format
// (RFC 4253 section 6.6): a length-prefixed "ssh-rsa" string followed by
// the mpints e and n, both length-prefixed and base64-encoded together.
// This is a test-only helper, built from the standard library alone rather
// than adding a dependency for one key type.
func sshRSAPublicKeyLine(pub *rsa.PublicKey, comment string) string {
	var buf bytes.Buffer
	fields := [][]byte{[]byte("ssh-rsa"), sshMPInt(big.NewInt(int64(pub.E))), sshMPInt(pub.N)}
	for _, field := range fields {
		var length [4]byte
		binary.BigEndian.PutUint32(length[:], uint32(len(field))) //nolint:gosec // G115: field is "ssh-rsa" or one RSA-3072 mpint, well under 2^32 bytes
		buf.Write(length[:])
		buf.Write(field)
	}
	return "ssh-rsa " + base64.StdEncoding.EncodeToString(buf.Bytes()) + " " + comment
}

// sshMPInt encodes n as an SSH mpint (RFC 4253 section 5): the minimal
// big-endian byte representation, with a leading zero byte added when the
// high bit of the first byte would otherwise be read as a sign bit. n must
// not be negative; e and an RSA modulus never are.
func sshMPInt(n *big.Int) []byte {
	b := n.Bytes()
	if len(b) > 0 && b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}

// firstPEMLine returns key's PEM header ("-----BEGIN ...-----"), which
// names a private key's type without revealing any of its material, or ""
// for anything else. It caps the result at the closing "-----" of the
// BEGIN marker rather than returning the whole first line: a key with no
// newlines at all, body and footer included, would otherwise still count
// as "the first line" and be returned whole. It never returns any other
// part of key, so a caller logging its result never risks printing key
// content by mistake.
func firstPEMLine(key string) string {
	const beginPrefix = "-----BEGIN "
	line, _, _ := strings.Cut(key, "\n")
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, beginPrefix) {
		return ""
	}
	closer := strings.Index(line[len(beginPrefix):], "-----")
	if closer < 0 {
		return ""
	}
	return line[:len(beginPrefix)+closer+len("-----")]
}

// TestFirstPEMLine checks that firstPEMLine never returns more than the
// BEGIN header, even when a key arrives with no newlines at all: cutting
// only on "\n" would then return the whole key, body included. header and
// body are kept as separate literals, joined only at run time, and no
// footer is used at all, so no fake PEM ever appears as one contiguous
// string in source.
func TestFirstPEMLine(t *testing.T) {
	const header = "-----BEGIN OPENSSH PRIVATE KEY-----"
	const body = "AAAAB3NzaC1yc2Vub3RhcmVhbGtleWZha2Vib2R5Zm9ydGVzdHM"

	tests := map[string]struct {
		key  string
		want string
	}{
		"multi-line PEM":        {key: header + "\n" + body + "\n", want: header},
		"one line, no newlines": {key: header + body, want: header},
		"not a PEM":             {key: "just some ordinary text", want: ""},
		"empty":                 {key: "", want: ""},
		"BEGIN with no closer":  {key: "-----BEGIN " + body, want: ""},
	}
	for name, tt := range tests {
		if got := firstPEMLine(tt.key); got != tt.want {
			t.Errorf("%s: firstPEMLine(...) = %q, want %q", name, got, tt.want)
		}
		if tt.want == "" && strings.Contains(firstPEMLine(tt.key), body) {
			t.Errorf("%s: firstPEMLine(...) leaked the body", name)
		}
	}
}

// isLiveSSHKeyName reports whether name is one this test's own runs create.
func isLiveSSHKeyName(name string) bool {
	return strings.HasPrefix(name, "vngcloud-live-")
}

// listAllSSHKeys pages through every SSH key the account has, since a
// leftover cleanup or a remaining-key check must not miss one that landed
// past the first page.
func listAllSSHKeys(ctx context.Context, client *compute.Client) ([]compute.SSHKey, error) {
	var all []compute.SSHKey
	for page := 1; ; page++ {
		out, err := client.ListSSHKeys(ctx, &compute.ListSSHKeysInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// deleteLiveSSHKeys deletes every SSH key whose name isLiveSSHKeyName
// reports true for, and returns how many it deleted. It is used to sweep up
// a previous run's leftovers before this test creates its own keys.
func deleteLiveSSHKeys(ctx context.Context, t *testing.T, client *compute.Client) int {
	t.Helper()
	keys, err := listAllSSHKeys(ctx, client)
	if err != nil {
		t.Errorf("delete live ssh keys: list: %s", safeErr(err))
		return 0
	}
	deleted := 0
	for _, key := range keys {
		if !isLiveSSHKeyName(key.Name) {
			continue
		}
		if _, err := client.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{SSHKeyID: key.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live ssh keys: delete: %s", safeErr(err))
			continue
		}
		deleted++
	}
	return deleted
}

// deleteSSHKeyByName lists keys by the exact name and deletes any match. It
// is used after an ImportSSHKey or CreateSSHKey failure, since a POST that
// returned an error may still have reached the server; the live-only name
// filter is confirmed to match exactly, but this still checks for an exact
// match itself rather than trust that. It runs on its own timeout, not the
// calling step's context, so it can still clean up after that step's
// context is the reason the step failed.
func deleteSSHKeyByName(t *testing.T, client *compute.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := client.ListSSHKeys(ctx, &compute.ListSSHKeysInput{Name: name})
	if err != nil {
		t.Errorf("cleanup: list ssh keys by name: %s", safeErr(err))
		return
	}
	for _, key := range list.Items {
		if key.Name != name {
			continue
		}
		if _, err := client.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{SSHKeyID: key.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete ssh key by name: %s", safeErr(err))
		}
	}
}

// VNGCLOUD_LIVE_SSH_KEY must be set to "1" in addition to VNGCLOUD_LIVE_WRITE,
// since the create step below has GreenNode generate and see a private key,
// even though every SSH key write costs nothing.
//
// It deletes every leftover vngcloud-live-* SSH key from a previous run
// first (step 1); imports a throwaway RSA-3072 public key generated in this
// test, so its matching private key never leaves this process (step 2);
// registers its cleanup, and the final remaining-key check, as soon as its
// id is known (step 3); reads it back (step 4); deletes it explicitly
// (step 5); has GreenNode generate a key pair with CreateSSHKey, registering
// its cleanup as soon as its id is known (step 6); reads it back (step 7);
// and deletes it explicitly (step 8). The private key CreateSSHKey returns
// is held only as its vngcloud.Secret value: it is never written to disk or
// logged, only whether it is present and its PEM header line are. Every
// step logs only statuses, counts, that boolean and header line, and
// timings, never a key's own id, name, or public or private material.
func TestLiveWriteSSHKey(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live ssh key write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_SSH_KEY") != "1" {
		t.Skip("set VNGCLOUD_LIVE_SSH_KEY=1 to run the live ssh key write test; " +
			"the create step has GreenNode generate and see a private key")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	client := compute.New(cfg)

	// Step 1: sweep up leftovers from an earlier run.
	leftovers := deleteLiveSSHKeys(ctx, t, client)
	t.Logf("step 1: deleted %d leftover ssh key(s)", leftovers)

	// Step 2: import a throwaway RSA-3072 public key made for this run. The
	// matching private key is generated here and discarded; it is never
	// sent anywhere. The server accepts RSA public keys only; see
	// ImportSSHKey's doc comment.
	importSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	importName := "vngcloud-live-" + importSuffix
	rsaKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatalf("step 2 generate rsa key: %v", err)
	}
	publicKey := sshRSAPublicKeyLine(&rsaKey.PublicKey, importName)

	start := time.Now()
	imported, err := client.ImportSSHKey(ctx, &compute.ImportSSHKeyInput{Name: importName, PublicKey: publicKey})
	if err != nil {
		deleteSSHKeyByName(t, client, importName)
		t.Fatalf("step 2 ImportSSHKey: %s", safeErr(err))
	}
	importedID := imported.SSHKey.ID
	if importedID == "" {
		deleteSSHKeyByName(t, client, importName)
		t.Fatal("step 2: ImportSSHKey returned an empty id; the design requires one")
	}
	t.Logf("step 2: imported ssh key, status %s, wait %s", imported.SSHKey.Status, time.Since(start))

	// Step 3: register the fallback cleanup, and the final remaining-key
	// check, as soon as importedID is known, before any later step can fail
	// and skip the explicit deletes below. This runs after the created
	// key's own cleanup (step 6), since t.Cleanup runs in last-registered,
	// first-run order, so the count below reflects both keys.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteSSHKey(cleanupCtx, &compute.DeleteSSHKeyInput{SSHKeyID: importedID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete imported key: %s", safeErr(err))
		}
		final, err := listAllSSHKeys(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListSSHKeys: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, key := range final {
			if isLiveSSHKeyName(key.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live ssh key(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live ssh keys, found %d", remaining)
		}
	})

	// Step 4: read it back.
	gotImported, err := client.GetSSHKey(ctx, &compute.GetSSHKeyInput{SSHKeyID: importedID})
	if err != nil {
		t.Fatalf("step 4 GetSSHKey: %s", safeErr(err))
	}
	t.Logf("step 4: read imported key, status %s", gotImported.SSHKey.Status)

	// Step 5: delete it explicitly.
	if _, err := client.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{SSHKeyID: importedID}); err != nil {
		t.Fatalf("step 5 DeleteSSHKey: %s", safeErr(err))
	}
	t.Log("step 5: deleted imported key")

	// Step 6: have GreenNode generate a key pair. The private key is held
	// only in memory as a vngcloud.Secret; only whether it is present and
	// its PEM header line are recorded, never the key itself, and nothing
	// is written to disk.
	createSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 6 generate name suffix: %v", err)
	}
	createName := "vngcloud-live-" + createSuffix

	start = time.Now()
	created, err := client.CreateSSHKey(ctx, &compute.CreateSSHKeyInput{Name: createName})
	if err != nil {
		deleteSSHKeyByName(t, client, createName)
		t.Fatalf("step 6 CreateSSHKey: %s", safeErr(err))
	}
	createdID := created.SSHKey.ID
	if createdID == "" {
		deleteSSHKeyByName(t, client, createName)
		t.Fatal("step 6: CreateSSHKey returned an empty id; the design requires one")
	}
	privateKey := created.PrivateKey.Reveal()
	t.Logf("step 6: created ssh key, status %s, private key present=%v type=%q, wait %s",
		created.SSHKey.Status, privateKey != "", firstPEMLine(privateKey), time.Since(start))

	// Register the created key's cleanup as soon as createdID is known; it
	// runs before step 3's cleanup above, per t.Cleanup's ordering.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteSSHKey(cleanupCtx, &compute.DeleteSSHKeyInput{SSHKeyID: createdID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete created key: %s", safeErr(err))
		}
	})

	// Step 7: read it back.
	gotCreated, err := client.GetSSHKey(ctx, &compute.GetSSHKeyInput{SSHKeyID: createdID})
	if err != nil {
		t.Fatalf("step 7 GetSSHKey: %s", safeErr(err))
	}
	t.Logf("step 7: read created key, status %s", gotCreated.SSHKey.Status)

	// Step 8: delete it explicitly.
	if _, err := client.DeleteSSHKey(ctx, &compute.DeleteSSHKeyInput{SSHKeyID: createdID}); err != nil {
		t.Fatalf("step 8 DeleteSSHKey: %s", safeErr(err))
	}
	t.Log("step 8: deleted created key")
}

// liveServerGroupNamePattern is the live server group write test's own
// naming scheme: vngcloud-live-<8 lowercase hex>, exactly, so a name that
// merely starts with vngcloud-live- but was not generated by this test is
// never swept up as a leftover.
var liveServerGroupNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveServerGroupName(name string) bool {
	return liveServerGroupNamePattern.MatchString(name)
}

// listAllServerGroups pages through every server group the account has,
// since a leftover cleanup or a remaining-group check must not miss one
// that landed past the first page. ListServerGroupsInput.Page is an offset,
// not a page number, so the walk starts at 0 and advances by the number of
// items the previous call actually returned. It fails rather than
// returning a silently short list if an empty page comes back before the
// server's own TotalItem is reached.
func listAllServerGroups(ctx context.Context, client *compute.Client) ([]compute.ServerGroup, error) {
	var all []compute.ServerGroup
	offset := 0
	for {
		out, err := client.ListServerGroups(ctx, &compute.ListServerGroupsInput{Page: offset})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if len(all) >= out.TotalItem {
			return all, nil
		}
		if len(out.Items) == 0 {
			return all, fmt.Errorf("listAllServerGroups: collected %d server group(s), server reports %d", len(all), out.TotalItem)
		}
		offset += len(out.Items)
	}
}

// TestListAllServerGroupsStartsAtOffsetZero checks that listAllServerGroups
// requests offset 0 first, not 1: ListServerGroups' own Page field is an
// offset into the list, not a page number, so starting at 1 would silently
// skip the first server group on every run.
func TestListAllServerGroupsStartsAtOffsetZero(t *testing.T) {
	var offsets []string
	client := compute.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		w.Header().Set("Content-Type", "application/json")
		switch offset {
		case "0":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"group-0","name":"a"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`))
		case "1":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"group-1","name":"b"}],"page":2,"pageSize":1,"totalPage":2,"totalItem":2}`))
		default:
			t.Fatalf("unexpected offset %q", offset)
		}
	})))

	groups, err := listAllServerGroups(context.Background(), client)
	if err != nil {
		t.Fatalf("listAllServerGroups() error = %v", err)
	}
	if len(offsets) != 2 || offsets[0] != "0" || offsets[1] != "1" {
		t.Fatalf("offsets requested = %v, want [0 1]", offsets)
	}
	if len(groups) != 2 || groups[0].UUID != "group-0" || groups[1].UUID != "group-1" {
		t.Fatalf("groups = %+v, want group-0 then group-1", groups)
	}
}

// TestListAllServerGroupsFailsWhenServerUnderreports checks that
// listAllServerGroups returns an error, instead of a silently short list,
// when the server's own TotalItem is larger than what the walk actually
// collected before an empty page ended it.
func TestListAllServerGroupsFailsWhenServerUnderreports(t *testing.T) {
	client := compute.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("offset") {
		case "0":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"group-0","name":"a"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":5}`))
		case "1":
			_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":1,"totalPage":1,"totalItem":5}`))
		default:
			t.Fatalf("unexpected offset %q", r.URL.Query().Get("offset"))
		}
	})))

	if _, err := listAllServerGroups(context.Background(), client); err == nil {
		t.Fatal("listAllServerGroups() error = nil, want an error for a short collection")
	}
}

// deleteServerGroupByName lists server groups and deletes any exact match
// for name. It is used after a CreateServerGroup failure, since a POST that
// returned an error may still have reached the server. It runs on its own
// timeout, not the calling test step's context, so it can still clean up
// after that step's context is the reason the step failed.
func deleteServerGroupByName(t *testing.T, client *compute.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	groups, err := listAllServerGroups(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list server groups by name: %s", safeErr(err))
		return
	}
	for _, g := range groups {
		if g.Name != name {
			continue
		}
		if _, err := client.DeleteServerGroup(ctx, &compute.DeleteServerGroupInput{ServerGroupID: g.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete server group by name: %s", safeErr(err))
		}
	}
}

// VNGCLOUD_LIVE_SERVER_GROUP must be set to "1" in addition to
// VNGCLOUD_LIVE_WRITE.
//
// It deletes every leftover vngcloud-live-* server group with no servers
// attached from a previous run first (step 1); reads the available server
// group policies and picks SOFT AFFINITY when present, else the first one
// (step 2); creates a group under that policy (step 3); registers its
// cleanup, and the final remaining-group check, as soon as its id is known
// (step 4), before any later step can fail and skip the explicit delete;
// reads it back (step 5); creates the same name again, expecting the
// server's own refusal (step 6); renames it with the description left out,
// then sets a new description with the name left out, then clears the
// description, each time confirming the field left out of that call's body
// was resent unchanged, that PolicyID never changed, and, for the last
// call, that the description came back empty rather than merely logging
// its length (step 7); deletes the group and confirms a not-found read
// (step 8); and repeats the delete, logging its status, since a second
// delete's behavior is not confirmed live (step 9). Every step logs only
// statuses, counts, field lengths, and timings, never a group's own name.
func TestLiveWriteServerGroup(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live server group write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_SERVER_GROUP") != "1" {
		t.Skip("set VNGCLOUD_LIVE_SERVER_GROUP=1 to run the live server group write test")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	client := compute.New(cfg)

	// Step 1: sweep up leftovers from an earlier run. A leftover still
	// holding a server is left alone; DeleteServerGroup itself would
	// refuse it anyway.
	leftovers, err := listAllServerGroups(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListServerGroups: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers {
		if !isLiveServerGroupName(leftover.Name) || len(leftover.Servers) > 0 {
			continue
		}
		if _, err := client.DeleteServerGroup(ctx, &compute.DeleteServerGroupInput{ServerGroupID: leftover.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("step 1 delete leftover: %s", safeErr(err))
			continue
		}
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover server group(s)", deletedLeftovers)

	// Step 2: the create needs a policy id; prefer SOFT AFFINITY, since it
	// is confirmed to exist, else fall back to the first policy listed.
	policies, err := client.ListServerGroupPolicies(ctx, nil)
	if err != nil {
		t.Fatalf("step 2 ListServerGroupPolicies: %s", safeErr(err))
	}
	if len(policies.Items) == 0 {
		t.Fatal("step 2: account has no server group policies to create against")
	}
	policy := policies.Items[0]
	for _, p := range policies.Items {
		if strings.EqualFold(p.Name, "SOFT AFFINITY") {
			policy = p
			break
		}
	}
	t.Logf("step 2: using policy %q (%d available)", policy.Name, len(policies.Items))

	// Step 3: create the group.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix
	const originalDescription = "vngcloud live write test"

	start := time.Now()
	created, err := client.CreateServerGroup(ctx, &compute.CreateServerGroupInput{
		Name:        name,
		PolicyID:    policy.UUID,
		Description: originalDescription,
	})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the group
		// may still have reached the server. Find and delete it by its
		// exact name.
		deleteServerGroupByName(t, client, name)
		t.Fatalf("step 3 CreateServerGroup: %s", safeErr(err))
	}
	groupID := created.ServerGroup.UUID
	if groupID == "" {
		deleteServerGroupByName(t, client, name)
		t.Fatal("step 3: CreateServerGroup returned an empty id; the design requires one")
	}
	t.Logf("step 3: created group, wait %s", time.Since(start))

	// Step 4: register the fallback cleanup, and the final remaining-group
	// check, as soon as groupID is known, before any later step can fail
	// and skip the explicit delete below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteServerGroup(cleanupCtx, &compute.DeleteServerGroupInput{ServerGroupID: groupID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete server group: %s", safeErr(err))
		}
		final, err := listAllServerGroups(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListServerGroups: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, g := range final {
			if isLiveServerGroupName(g.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live server group(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live server groups, found %d", remaining)
		}
	})

	// Step 5: read it back.
	got, err := client.GetServerGroup(ctx, &compute.GetServerGroupInput{ServerGroupID: groupID})
	if err != nil {
		t.Fatalf("step 5 GetServerGroup: %s", safeErr(err))
	}
	t.Logf("step 5: read group, description length %d", len(got.ServerGroup.Description))

	// Step 6: create the same name again; names are unique per project, so
	// the design expects a refusal. An unexpected success is cleaned up
	// too, since it would otherwise leak a second group under the same
	// name.
	dup, dupErr := client.CreateServerGroup(ctx, &compute.CreateServerGroupInput{Name: name, PolicyID: policy.UUID})
	if dupErr == nil {
		t.Error("step 6: creating a duplicate name succeeded; the design expects a refusal")
		if dup.ServerGroup.UUID != "" {
			if _, err := client.DeleteServerGroup(ctx, &compute.DeleteServerGroupInput{ServerGroupID: dup.ServerGroup.UUID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("step 6 cleanup: delete duplicate group: %s", safeErr(err))
			}
		}
	} else {
		t.Logf("step 6: duplicate name refused, %s", safeErr(dupErr))
	}

	// Step 7: update the group three ways, confirming each call resent the
	// field it left out unchanged.
	update := func(step string, newName, desc *string) *compute.UpdateServerGroupOutput {
		out, err := client.UpdateServerGroup(ctx, &compute.UpdateServerGroupInput{
			ServerGroupID: groupID,
			Name:          newName,
			Description:   desc,
		})
		if err != nil {
			t.Fatalf("%s UpdateServerGroup: %s", step, safeErr(err))
		}
		if newName != nil {
			name = *newName
		}
		if out.ServerGroup.Name != name {
			t.Errorf("%s: Name is not the expected value", step)
		}
		if out.ServerGroup.PolicyID != policy.UUID {
			t.Errorf("%s: PolicyID changed, want it to stay %q", step, policy.UUID)
		}
		t.Logf("%s: updated group, description length %d", step, len(out.ServerGroup.Description))
		return out
	}
	renameSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 7a generate name suffix: %v", err)
	}
	update("step 7a (rename, description left out)", vngcloud.Ptr("vngcloud-live-"+renameSuffix), nil)
	if got, err := client.GetServerGroup(ctx, &compute.GetServerGroupInput{ServerGroupID: groupID}); err != nil {
		t.Fatalf("step 7a GetServerGroup: %s", safeErr(err))
	} else if got.ServerGroup.Description != originalDescription {
		t.Errorf("step 7a: rename changed Description (length %d)", len(got.ServerGroup.Description))
	}
	const updatedDescription = "vngcloud live write test updated"
	update("step 7b (new description, name left out)", nil, vngcloud.Ptr(updatedDescription))
	cleared := update("step 7c (empty description)", nil, vngcloud.Ptr(""))
	if len(cleared.ServerGroup.Description) != 0 {
		t.Errorf("step 7c: description length %d, want 0 (the empty description was not cleared)", len(cleared.ServerGroup.Description))
	}

	// Step 8: delete the group and log the status and the time until a
	// GetServerGroup read returns NotFound.
	start = time.Now()
	if _, err := client.DeleteServerGroup(ctx, &compute.DeleteServerGroupInput{ServerGroupID: groupID}); err != nil {
		t.Fatalf("step 8 DeleteServerGroup: %s", safeErr(err))
	}
	for {
		if _, err := client.GetServerGroup(ctx, &compute.GetServerGroupInput{ServerGroupID: groupID}); vngcloud.IsNotFound(err) {
			break
		}
		if time.Since(start) > time.Minute {
			t.Error("step 8: group still readable one minute after delete")
			break
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("step 8: deleted group, not-found confirmed after %s", time.Since(start))

	// Step 9: repeat the group delete and log its status; whether a second
	// delete of a server group 404s, no-ops, or errors some other way is
	// not confirmed live.
	_, secondDeleteErr := client.DeleteServerGroup(ctx, &compute.DeleteServerGroupInput{ServerGroupID: groupID})
	switch {
	case secondDeleteErr == nil:
		t.Log("step 9: second group delete succeeded without error")
	case vngcloud.IsNotFound(secondDeleteErr):
		t.Log("step 9: second group delete returned NotFound")
	default:
		t.Logf("step 9: second group delete: %s", safeErr(secondDeleteErr))
	}
}

// TestLiveWriteNetworkVPC creates: a VPC is one /16 from 10.0.0.0/8, and a
// subnet is a /24 or /28 inside it.
const (
	liveVPCCIDR      = "10.250.0.0/16"
	liveSubnet24CIDR = "10.250.1.0/24"
	liveSubnet28CIDR = "10.250.2.0/28"
)

// listAllVPCs pages through every VPC the account has, since a leftover
// cleanup or a remaining-VPC check must not miss one that landed past the
// first page.
func listAllVPCs(ctx context.Context, client *network.Client) ([]network.VPC, error) {
	var all []network.VPC
	for page := 1; ; page++ {
		out, err := client.ListVPCs(ctx, &network.ListVPCsInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// pickEnabledZoneID returns the uuid of the first zone portal.ListZones
// reports enabled. The test account's default zone is disabled, so a
// subnet create needs this rather than any zone the account has.
func pickEnabledZoneID(ctx context.Context, client *portal.Client) (string, error) {
	zones, err := client.ListZones(ctx, nil)
	if err != nil {
		return "", err
	}
	for _, zone := range zones.Items {
		enabled, _ := zone["isEnabled"].(bool)
		uuid, _ := zone["uuid"].(string)
		if enabled && uuid != "" {
			return uuid, nil
		}
	}
	return "", fmt.Errorf("no enabled zone among %d zone(s)", len(zones.Items))
}

// deleteVPCAndSubnets deletes every subnet of vpcID with NoWait, then
// retries DeleteVPC every 30 seconds for up to 20 minutes: the server keeps
// refusing a VPC delete for minutes after its last subnet's delete leaves
// the VPC's subnet list (see the design). ErrInUse and ErrNotSettled both
// mean try again; NotFound at any step means the VPC is already gone. It is
// used both for a leftover VPC from a previous run and from t.Cleanup, so
// it never fails the test merely because the VPC or a subnet is already
// gone, and it runs on its own context rather than the calling step's, so
// it can still clean up after that step's context is the reason it failed.
func deleteVPCAndSubnets(ctx context.Context, t *testing.T, client *network.Client, vpcID string) {
	t.Helper()

	subnets, err := client.ListSubnetsByVPC(ctx, &network.ListSubnetsByVPCInput{VPCID: vpcID})
	switch {
	case err == nil:
		for _, subnet := range subnets.Items {
			if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{
				VPCID: vpcID, SubnetID: subnet.UUID, NoWait: true,
			}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("delete VPC: delete subnet: %s", safeErr(err))
			}
		}
	case vngcloud.IsNotFound(err):
		return
	default:
		t.Errorf("delete VPC: list subnets: %s", safeErr(err))
		return
	}

	deadline := time.Now().Add(20 * time.Minute)
	for {
		_, err := client.DeleteVPC(ctx, &network.DeleteVPCInput{VPCID: vpcID})
		switch {
		case err == nil, vngcloud.IsNotFound(err):
			return
		case errors.Is(err, network.ErrInUse), errors.Is(err, network.ErrNotSettled):
			// The server may still be holding a just-deleted subnet against
			// this VPC; retry until the deadline.
		default:
			t.Errorf("delete VPC: %s", safeErr(err))
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("delete VPC: still not deleted after 20 minutes")
			return
		}
		select {
		case <-ctx.Done():
			t.Errorf("delete VPC: context ended before delete settled: %s", safeErr(ctx.Err()))
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// deleteVPCByName lists VPCs and deletes any exact match for name. It is
// used after a CreateVPC failure, since a POST that returned an error may
// still have reached the server.
func deleteVPCByName(t *testing.T, client *network.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	list, err := listAllVPCs(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list vpcs by name: %s", safeErr(err))
		return
	}
	for _, v := range list {
		if v.Name != name {
			continue
		}
		deleteVPCAndSubnets(ctx, t, client, v.UUID)
	}
}

// TestLiveWriteNetworkVPC exercises CreateVPC, UpdateVPC, DeleteVPC,
// CreateSubnet, UpdateSubnet, DeleteSubnet, and ListServersBySubnet against
// the account named in .env, in hcm-3. Neither a VPC nor a subnet appears
// on any pricing page (see the design), so both are treated as free.
//
// The account's VPC quota leaves one free VPC, so this test creates at
// most one. It deletes every leftover vngcloud-live-* VPC first, deleting
// each one's subnets before the VPC itself (step 1); picks the account's
// enabled zone, since the default zone is disabled for the test account
// (step 2); creates vngcloud-live-<8 hex> (step 3); registers the fallback
// cleanup as soon as the created VPC's id is known (step 4); renames it to
// a new vngcloud-live-<8 hex> name and then to that same name again (step
// 5); creates a /24 and a /28 subnet in the enabled zone (step 6); creates
// an overlapping subnet and one with the /24 subnet's name and logs the
// refusals (step 7); renames the /24 subnet (step 8); lists servers on it,
// expecting none (step 9); optionally enables Private DNS, behind its own
// VNGCLOUD_LIVE_NETWORK_PRIVATE_DNS gate, since it takes about 6 minutes
// (step 10); deletes the /28 subnet and waits for it to leave the VPC's
// subnet list (step 11); repeats that delete and logs its status (step
// 12); deletes the VPC while the /24 subnet remains, expecting ErrInUse
// (step 13); deletes the /24 subnet, then retries the VPC delete every 30
// seconds until the server stops refusing it and confirms a 404 (step 14);
// and repeats the VPC delete, logging its status (step 15). Every step logs
// only statuses, counts, field names, and timings, never a VPC or subnet's
// own id, name, or CIDR.
func TestLiveWriteNetworkVPC(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network VPC write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_VPC") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_VPC=1 to run the live network VPC write test")
	}
	privateDNS := os.Getenv("VNGCLOUD_LIVE_NETWORK_PRIVATE_DNS") == "1"
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	region := "hcm-3"
	if raw := strings.TrimSpace(os.Getenv("VNGCLOUD_REGIONS")); raw != "" {
		if first := strings.TrimSpace(strings.Split(raw, ",")[0]); first != "" {
			region = first
		}
	}

	timeout := 30 * time.Minute
	if privateDNS {
		timeout = 40 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
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
	client := network.New(cfg)
	portalClient := portal.New(cfg)

	// Step 1: delete every leftover vngcloud-live-* VPC from a previous run.
	leftovers, err := listAllVPCs(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListVPCs: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers {
		if !isLiveSecurityGroupName(leftover.Name) {
			continue
		}
		deleteVPCAndSubnets(ctx, t, client, leftover.UUID)
		deletedLeftovers++
	}
	t.Logf("step 1: deleted %d leftover VPC(s)", deletedLeftovers)

	// Step 2: pick the account's enabled zone.
	zoneID, err := pickEnabledZoneID(ctx, portalClient)
	if err != nil {
		t.Fatalf("step 2 pick enabled zone: %s", safeErr(err))
	}
	t.Log("step 2: picked an enabled zone")

	// Step 3: create the VPC.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	start := time.Now()
	created, err := client.CreateVPC(ctx, &network.CreateVPCInput{Name: name, CIDR: liveVPCCIDR})
	if err != nil {
		deleteVPCByName(t, client, name)
		t.Fatalf("step 3 CreateVPC: %s", safeErr(err))
	}
	vpcID := created.VPC.UUID
	if vpcID == "" {
		deleteVPCByName(t, client, name)
		t.Fatal("step 3: CreateVPC returned an empty id; the design requires one")
	}
	t.Logf("step 3: created VPC, status %s, wait %s", created.VPC.Status, time.Since(start))

	// Step 4: register the fallback cleanup as soon as vpcID is known,
	// before any later step can fail and skip the explicit deletes below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()
		deleteVPCAndSubnets(cleanupCtx, t, client, vpcID)
		final, err := listAllVPCs(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListVPCs: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, v := range final {
			if isLiveSecurityGroupName(v.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live VPC(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live VPCs, found %d", remaining)
		}
	})

	// Step 5: rename to a new live-pattern name, then rename to that same
	// name again.
	renameSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 5 generate name suffix: %v", err)
	}
	name = "vngcloud-live-" + renameSuffix
	if _, err := client.UpdateVPC(ctx, &network.UpdateVPCInput{VPCID: vpcID, Name: name}); err != nil {
		t.Fatalf("step 5a UpdateVPC: %s", safeErr(err))
	}
	sameName, err := client.UpdateVPC(ctx, &network.UpdateVPCInput{VPCID: vpcID, Name: name})
	if err != nil {
		t.Fatalf("step 5b UpdateVPC (same name): %s", safeErr(err))
	}
	t.Logf("step 5: renamed VPC twice, final status %s", sameName.VPC.Status)

	// Step 6: create a /24 and a /28 subnet in the enabled zone.
	start = time.Now()
	sub24, err := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-a", CIDR: liveSubnet24CIDR,
	})
	if err != nil {
		t.Fatalf("step 6a CreateSubnet (/24): %s", safeErr(err))
	}
	subnet24ID := sub24.Subnet.UUID
	if subnet24ID == "" {
		t.Fatal("step 6a: CreateSubnet returned an empty id; the design requires one")
	}
	t.Logf("step 6a: created /24 subnet, status %s, wait %s", sub24.Subnet.Status, time.Since(start))

	start = time.Now()
	sub28, err := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-b", CIDR: liveSubnet28CIDR,
	})
	if err != nil {
		t.Fatalf("step 6b CreateSubnet (/28): %s", safeErr(err))
	}
	subnet28ID := sub28.Subnet.UUID
	if subnet28ID == "" {
		t.Fatal("step 6b: CreateSubnet returned an empty id; the design requires one")
	}
	t.Logf("step 6b: created /28 subnet, status %s, wait %s", sub28.Subnet.Status, time.Since(start))

	// Step 7: an overlapping subnet, and one with the /24 subnet's name,
	// both expected to be refused. An unexpected success is left for
	// t.Cleanup's subnet sweep to remove along with every other subnet.
	_, overlapErr := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-c", CIDR: liveSubnet24CIDR, NoWait: true,
	})
	if overlapErr == nil {
		t.Error("step 7a: creating an overlapping subnet succeeded; the design expects a refusal")
	} else {
		t.Logf("step 7a: overlapping subnet refused, %s", safeErr(overlapErr))
	}
	// Step 7b: a duplicate subnet name. The server allows it (seen live), so
	// the extra subnet is deleted at once: a VPC delete refuses while it
	// remains.
	dupSubnet, dupNameErr := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-a", CIDR: "10.250.3.0/24",
	})
	if dupNameErr != nil {
		t.Logf("step 7b: duplicate-named subnet refused, %s", safeErr(dupNameErr))
	} else {
		t.Log("step 7b: duplicate-named subnet allowed")
		if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: dupSubnet.Subnet.UUID}); err != nil {
			t.Fatalf("step 7b DeleteSubnet (duplicate name): %s", safeErr(err))
		}
	}

	// Step 8: rename the /24 subnet.
	renamed, err := client.UpdateSubnet(ctx, &network.UpdateSubnetInput{
		VPCID: vpcID, SubnetID: subnet24ID, Name: name + "-a-renamed",
	})
	if err != nil {
		t.Fatalf("step 8 UpdateSubnet: %s", safeErr(err))
	}
	t.Logf("step 8: renamed /24 subnet, status %s", renamed.Subnet.Status)

	// Step 9: list servers on the /24 subnet; the design expects none.
	servers, err := client.ListServersBySubnet(ctx, &network.ListServersBySubnetInput{SubnetID: subnet24ID})
	if err != nil {
		t.Fatalf("step 9 ListServersBySubnet: %s", safeErr(err))
	}
	t.Logf("step 9: servers on /24 subnet: %d", len(servers.Items))

	// Step 10: optionally enable Private DNS on this run's own VPC. It
	// takes about 6 minutes to settle, so it stays behind its own gate.
	if privateDNS {
		start = time.Now()
		dnsOut, err := client.EnableVPCPrivateDNS(ctx, &network.EnableVPCPrivateDNSInput{VPCID: vpcID})
		if err != nil {
			t.Errorf("step 10 EnableVPCPrivateDNS: %s", safeErr(err))
		} else {
			t.Logf("step 10: Private DNS enable, changed=%v, wait %s", dnsOut.Changed, time.Since(start))
		}
	} else {
		t.Log("step 10: skipped (set VNGCLOUD_LIVE_NETWORK_PRIVATE_DNS=1 to run it; takes about 6 minutes)")
	}

	// Step 11: delete the /28 subnet and wait for it to leave the VPC's
	// subnet list.
	start = time.Now()
	if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: subnet28ID}); err != nil {
		t.Fatalf("step 11 DeleteSubnet (/28): %s", safeErr(err))
	}
	t.Logf("step 11: deleted /28 subnet, wait %s", time.Since(start))

	// Step 12: repeat that delete and log its status.
	_, secondSubnetDeleteErr := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: subnet28ID})
	switch {
	case secondSubnetDeleteErr == nil:
		t.Log("step 12: second subnet delete succeeded without error")
	case vngcloud.IsNotFound(secondSubnetDeleteErr):
		t.Log("step 12: second subnet delete returned NotFound")
	default:
		t.Logf("step 12: second subnet delete: %s", safeErr(secondSubnetDeleteErr))
	}

	// Step 13: delete the VPC while the /24 subnet remains; the design
	// expects ErrInUse, sending nothing.
	_, inUseErr := client.DeleteVPC(ctx, &network.DeleteVPCInput{VPCID: vpcID, NoWait: true})
	if !errors.Is(inUseErr, network.ErrInUse) {
		t.Errorf("step 13: err = %s, want ErrInUse", safeErr(inUseErr))
	} else {
		t.Log("step 13: VPC delete refused with ErrInUse while a subnet remains")
	}

	// Step 14: delete the /24 subnet, then retry the VPC delete every 30
	// seconds until the server stops refusing it, and confirm a 404.
	if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: subnet24ID}); err != nil {
		t.Fatalf("step 14 DeleteSubnet (/24): %s", safeErr(err))
	}
	start = time.Now()
	deadline := start.Add(20 * time.Minute)
	for {
		_, err := client.DeleteVPC(ctx, &network.DeleteVPCInput{VPCID: vpcID})
		if err == nil || vngcloud.IsNotFound(err) {
			break
		}
		if !errors.Is(err, network.ErrInUse) && !errors.Is(err, network.ErrNotSettled) {
			t.Fatalf("step 14 DeleteVPC: %s", safeErr(err))
		}
		if time.Now().After(deadline) {
			t.Fatal("step 14: VPC delete still refused after 20 minutes")
		}
		select {
		case <-ctx.Done():
			t.Fatalf("step 14: context ended before delete settled: %s", safeErr(ctx.Err()))
		case <-time.After(30 * time.Second):
		}
	}
	t.Logf("step 14: deleted VPC, wait %s", time.Since(start))

	// Step 15: repeat the VPC delete and log its status.
	_, secondVPCDeleteErr := client.DeleteVPC(ctx, &network.DeleteVPCInput{VPCID: vpcID, NoWait: true})
	switch {
	case secondVPCDeleteErr == nil:
		t.Log("step 15: second VPC delete succeeded without error")
	case vngcloud.IsNotFound(secondVPCDeleteErr):
		t.Log("step 15: second VPC delete returned NotFound")
	default:
		t.Logf("step 15: second VPC delete: %s", safeErr(secondVPCDeleteErr))
	}
}

// isLiveRouteTableName reports whether name is one this test's own runs
// create.
func isLiveRouteTableName(name string) bool {
	return strings.HasPrefix(name, "vngcloud-live-")
}

// listAllRouteTables pages through every route table the account has.
func listAllRouteTables(ctx context.Context, client *network.Client) ([]network.RouteTable, error) {
	var all []network.RouteTable
	for page := 1; ; page++ {
		out, err := client.ListRouteTables(ctx, &network.ListRouteTablesInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// deleteRouteTableLeftover deletes a leftover vngcloud-live-* route table
// found by a previous run of this same VPC, and reports whether it deleted
// one. The caller passes only a table whose NetworkID equals the run's own
// VPC ID, so a table belonging to some other VPC, including one that is
// currently that VPC's main table, is never reached here. This still
// ignores ErrDefaultResource, ErrInUse, and NotFound as a second guard:
// even within this VPC, a matched table might currently be its main table
// with a dependent subnet, or still named by a subnet, and this cleanup
// must never send those a DELETE.
func deleteRouteTableLeftover(ctx context.Context, t *testing.T, client *network.Client, routeTableID string) bool {
	t.Helper()
	_, err := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: routeTableID})
	switch {
	case err == nil:
		return true
	case errors.Is(err, network.ErrDefaultResource), errors.Is(err, network.ErrInUse), vngcloud.IsNotFound(err):
		return false
	default:
		t.Errorf("cleanup: delete leftover route table: %s", safeErr(err))
		return false
	}
}

// deleteRouteTableByName finds a route table by its exact name in the
// given VPC and deletes it: the recovery TestLiveWriteNetworkRouteTable
// takes after a create whose own response never arrived, since the POST is
// not resent and the table may still exist under the name it was given.
// Requiring vpcID too, alongside the exact name, keeps this from ever
// deleting a table that belongs to some other VPC.
func deleteRouteTableByName(t *testing.T, client *network.Client, vpcID, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := listAllRouteTables(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list route tables by name: %s", safeErr(err))
		return
	}
	for _, rt := range list {
		if rt.Name != name || rt.NetworkID != vpcID {
			continue
		}
		if _, err := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: rt.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete route table by name: %s", safeErr(err))
		}
	}
}

// deleteRouteTableRetryNotFound deletes routeTableID, retrying a few times
// on NotFound before giving up: a table this same test just created may not
// be readable yet by DeleteRouteTable's own guard read, so treating an
// immediate NotFound as already gone would leak it.
func deleteRouteTableRetryNotFound(ctx context.Context, t *testing.T, client *network.Client, routeTableID string) {
	t.Helper()
	const attempts = 5
	for i := 0; i < attempts; i++ {
		_, err := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: routeTableID})
		if err == nil {
			return
		}
		if !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete route table: %s", safeErr(err))
			return
		}
		if i == attempts-1 {
			t.Errorf("cleanup: delete route table: still not found after %d attempts; it may have leaked", attempts)
			return
		}
		time.Sleep(2 * time.Second)
	}
}

// TestLiveWriteNetworkRouteTable exercises route table and route writes
// against the real account named in .env: CreateRouteTable, AddRoute,
// RemoveRoute, and DeleteRouteTable. It targets an existing VPC named by
// VNGCLOUD_LIVE_NETWORK_VPC_ID, which some other step of the same live run
// must create; this test never creates or deletes a VPC itself, and it
// never sends AddRoute or RemoveRoute to any route table but the one it
// creates here. It never leaves a route table behind.
func TestLiveWriteNetworkRouteTable(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network route table write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_ROUTE_TABLE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_ROUTE_TABLE=1 to run the live network route table write test")
	}
	vpcID := strings.TrimSpace(os.Getenv("VNGCLOUD_LIVE_NETWORK_VPC_ID"))
	if vpcID == "" {
		t.Fatal("set VNGCLOUD_LIVE_NETWORK_VPC_ID to an existing VPC's id; this test never creates or deletes a VPC")
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
	client := network.New(cfg)

	// This test needs a VPC with no main route table and no subnets: the
	// first route table it creates becomes the VPC's main table (confirmed
	// live), and step 10's delete assumes no subnet relies on that main
	// table. Skip rather than run against a VPC some other step already
	// populated.
	vpcState, err := client.GetVPC(ctx, &network.GetVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("check VPC state: GetVPC: %s", safeErr(err))
	}
	if vpcState.VPC.RouteTableID != "" {
		t.Skip("VPC already has a main route table; this test needs an empty VPC the run created")
	}
	existingSubnets, err := client.ListSubnetsByVPC(ctx, &network.ListSubnetsByVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("check VPC state: ListSubnetsByVPC: %s", safeErr(err))
	}
	if len(existingSubnets.Items) != 0 {
		t.Skipf("VPC already has %d subnet(s); this test needs an empty VPC the run created", len(existingSubnets.Items))
	}

	// Step 1: delete every leftover vngcloud-live-* route table from a
	// previous run of this same VPC. listAllRouteTables pages through the
	// whole account, which can include another concurrent run's tables in a
	// different VPC; the NetworkID check keeps this sweep from ever
	// touching one of those, including one that is currently that other
	// VPC's main table. Within this VPC, a table that is still the main
	// table with a dependent subnet, or is still named by a subnet, is also
	// left alone.
	leftovers, err := listAllRouteTables(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListRouteTables: %s", safeErr(err))
	}
	deletedLeftovers := 0
	for _, leftover := range leftovers {
		if !isLiveRouteTableName(leftover.Name) || leftover.NetworkID != vpcID {
			continue
		}
		if deleteRouteTableLeftover(ctx, t, client, leftover.UUID) {
			deletedLeftovers++
		}
	}
	t.Logf("step 1: deleted %d leftover route table(s)", deletedLeftovers)

	// Step 2: create the route table.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	start := time.Now()
	created, err := client.CreateRouteTable(ctx, &network.CreateRouteTableInput{VPCID: vpcID, Name: name})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the table may
		// still have reached the server. Find and delete it by its exact
		// name.
		deleteRouteTableByName(t, client, vpcID, name)
		t.Fatalf("step 2 CreateRouteTable: %s", safeErr(err))
	}
	routeTableID := created.RouteTable.UUID
	if routeTableID == "" {
		deleteRouteTableByName(t, client, vpcID, name)
		t.Fatal("step 2: CreateRouteTable returned an empty id; the design requires one")
	}
	t.Logf("step 2: created route table, status %s, routes %d, wait %s",
		created.RouteTable.Status, len(created.RouteTable.Routes), time.Since(start))

	vpcAfterCreate, err := client.GetVPC(ctx, &network.GetVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("step 2 GetVPC (check main table): %s", safeErr(err))
	}
	becameMain := vpcAfterCreate.VPC.RouteTableID == routeTableID
	t.Logf("step 2: new route table became the VPC's main route table: %v", becameMain)

	// Step 3: register the fallback cleanup as soon as routeTableID is
	// known, before any later step can fail and skip the explicit delete
	// below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := client.DeleteRouteTable(cleanupCtx, &network.DeleteRouteTableInput{RouteTableID: routeTableID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete route table: %s", safeErr(err))
		}
		final, err := listAllRouteTables(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListRouteTables: %s", safeErr(err))
			return
		}
		// Scoped to this run's own VPC: the account may hold another
		// concurrent run's own vngcloud-live-* table in a different VPC,
		// which is that run's responsibility, not this one's.
		remaining := 0
		for _, rt := range final {
			if isLiveRouteTableName(rt.Name) && rt.NetworkID == vpcID {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live route table(s) remaining in this VPC: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live route tables in this VPC, found %d", remaining)
		}
	})

	// Step 4: create the same name again. Whether route table names are
	// unique per VPC or per project is not yet confirmed live, so either a
	// refusal or a second table is possible; an unexpected success is
	// cleaned up too, since it would otherwise leak a second table. NoWait
	// means the delete's own guard read may hit the new table before it is
	// readable, so a NotFound there is retried briefly rather than treated
	// as already gone.
	dup, dupErr := client.CreateRouteTable(ctx, &network.CreateRouteTableInput{VPCID: vpcID, Name: name, NoWait: true})
	if dupErr == nil {
		t.Log("step 4: creating a duplicate name succeeded")
		if dup.RouteTable.UUID != "" {
			deleteRouteTableRetryNotFound(ctx, t, client, dup.RouteTable.UUID)
		}
	} else {
		t.Logf("step 4: duplicate name refused, %s", safeErr(dupErr))
	}

	// Step 5: add a route. The target address is not known to be a live
	// interface in the VPC; whether the server requires that is exactly
	// what this step observes.
	start = time.Now()
	added, err := client.AddRoute(ctx, &network.AddRouteInput{
		RouteTableID:    routeTableID,
		DestinationCIDR: "10.251.200.0/24",
		Target:          "10.251.200.10",
	})
	if err != nil {
		t.Fatalf("step 5 AddRoute: %s", safeErr(err))
	}
	t.Logf("step 5: added route, changed %v, status %s, routes %d, wait %s",
		added.Changed, added.RouteTable.Status, len(added.RouteTable.Routes), time.Since(start))

	// Step 6: add the same route again; the design expects a no-op.
	again, err := client.AddRoute(ctx, &network.AddRouteInput{
		RouteTableID:    routeTableID,
		DestinationCIDR: "10.251.200.0/24",
		Target:          "10.251.200.10",
		NoWait:          true,
	})
	if err != nil {
		t.Fatalf("step 6 AddRoute (repeat): %s", safeErr(err))
	}
	if again.Changed {
		t.Error("step 6: repeating the same add reported Changed true, want false")
	} else {
		t.Log("step 6: repeat add was a no-op as expected")
	}

	// Step 7: add a conflicting target for the same destination; the
	// design expects a refusal naming the current target, nothing sent.
	_, conflictErr := client.AddRoute(ctx, &network.AddRouteInput{
		RouteTableID:    routeTableID,
		DestinationCIDR: "10.251.200.0/24",
		Target:          "10.251.200.99",
		NoWait:          true,
	})
	if !errors.Is(conflictErr, vngcloud.ErrInvalidInput) {
		t.Errorf("step 7: conflicting target err = %s, want ErrInvalidInput", safeErr(conflictErr))
	} else {
		t.Log("step 7: conflicting target refused as expected")
	}

	// Step 8: remove the route.
	start = time.Now()
	removed, err := client.RemoveRoute(ctx, &network.RemoveRouteInput{RouteTableID: routeTableID, DestinationCIDR: "10.251.200.0/24"})
	if err != nil {
		t.Fatalf("step 8 RemoveRoute: %s", safeErr(err))
	}
	t.Logf("step 8: removed route, changed %v, status %s, routes %d, wait %s",
		removed.Changed, removed.RouteTable.Status, len(removed.RouteTable.Routes), time.Since(start))

	// Step 9: remove it again; the design expects NotFound.
	_, removedAgainErr := client.RemoveRoute(ctx, &network.RemoveRouteInput{RouteTableID: routeTableID, DestinationCIDR: "10.251.200.0/24", NoWait: true})
	if !vngcloud.IsNotFound(removedAgainErr) {
		t.Errorf("step 9: repeat remove err = %s, want NotFound", safeErr(removedAgainErr))
	} else {
		t.Log("step 9: repeat remove returned NotFound as expected")
	}

	// Step 10: delete the table explicitly.
	start = time.Now()
	if _, err := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: routeTableID}); err != nil {
		t.Fatalf("step 10 DeleteRouteTable: %s", safeErr(err))
	}
	t.Logf("step 10: deleted route table, wait %s", time.Since(start))

	// Step 11: repeat delete; the design expects NotFound, since the
	// delete's own guard reads run first.
	_, repeatErr := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: routeTableID, NoWait: true})
	if !vngcloud.IsNotFound(repeatErr) {
		t.Errorf("step 11: repeat delete err = %s, want NotFound", safeErr(repeatErr))
	} else {
		t.Log("step 11: repeat delete returned NotFound as expected")
	}
}

// liveCertificateNamePattern is the live vLB certificate write test's own
// naming scheme: vngcloud-live-<8 lowercase hex>, exactly, so a name that
// merely starts with vngcloud-live- but was not generated by this test is
// never swept up as a leftover.
var liveCertificateNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveCertificateName(name string) bool {
	return liveCertificateNamePattern.MatchString(name)
}

// listAllCertificates pages through every certificate the account has.
func listAllCertificates(ctx context.Context, client *loadbalancer.Client) ([]loadbalancer.Certificate, error) {
	var all []loadbalancer.Certificate
	for page := 1; ; page++ {
		out, err := client.ListCertificates(ctx, &loadbalancer.ListCertificatesInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// deleteLiveCertificates deletes every certificate isLiveCertificateName
// reports true for and that is not InUse, and returns how many it deleted.
// It is used to sweep up a previous run's leftovers before this test
// imports its own certificates; it never touches a certificate without the
// vngcloud-live- prefix, and leaves an in-use one alone, matching
// DeleteCertificate's own guard.
func deleteLiveCertificates(ctx context.Context, t *testing.T, client *loadbalancer.Client) int {
	t.Helper()
	certs, err := listAllCertificates(ctx, client)
	if err != nil {
		t.Errorf("delete live certificates: list: %s", safeErr(err))
		return 0
	}
	deleted := 0
	for _, cert := range certs {
		if !isLiveCertificateName(cert.Name) || cert.InUse {
			continue
		}
		if _, err := client.DeleteCertificate(ctx, &loadbalancer.DeleteCertificateInput{CertificateID: cert.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live certificates: delete: %s", safeErr(err))
			continue
		}
		deleted++
	}
	return deleted
}

// deleteCertificateByName lists certificates by the exact name and deletes
// any match. It is used after an ImportCertificate failure, since a POST
// that returned an error may still have reached the server. It runs on its
// own timeout, not the calling step's context, so it can still clean up
// after that step's context is the reason the step failed.
func deleteCertificateByName(t *testing.T, client *loadbalancer.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := client.ListCertificates(ctx, &loadbalancer.ListCertificatesInput{Name: name})
	if err != nil {
		t.Errorf("cleanup: list certificates by name: %s", safeErr(err))
		return
	}
	for _, cert := range list.Items {
		if cert.Name != name {
			continue
		}
		if _, err := client.DeleteCertificate(ctx, &loadbalancer.DeleteCertificateInput{CertificateID: cert.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete certificate by name: %s", safeErr(err))
		}
	}
}

// generateSelfSignedCertPEM builds a throwaway self-signed certificate PEM
// for key, entirely in memory: nothing here is written to disk, and the
// matching private key is discarded once the run using it ends.
func generateSelfSignedCertPEM(key crypto.Signer, commonName string) (string, error) {
	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

func rsaPrivateKeyPEM(key *rsa.PrivateKey) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

func ecdsaPrivateKeyPEM(key *ecdsa.PrivateKey) (string, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), nil
}

// encryptedRSAPrivateKeyPEM returns key's PKCS#1 DER encrypted under
// passphrase with legacy RFC 1423 PEM encryption: the only key format the
// product docs describe for an encrypted PEM key, and adequate for a
// throwaway key that lives only in memory for the length of one live run.
func encryptedRSAPrivateKeyPEM(key *rsa.PrivateKey, passphrase string) (string, error) {
	der := x509.MarshalPKCS1PrivateKey(key)
	block, err := x509.EncryptPEMBlock(rand.Reader, "RSA PRIVATE KEY", der, []byte(passphrase), x509.PEMCipherAES256) //nolint:staticcheck // SA1019: EncryptPEMBlock is deprecated but is the only stdlib way to build this live-only throwaway fixture, which is imported and deleted within the same run
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(block)), nil
}

// importCertificateMessageWithheld reports whether err's own message is
// exactly loadbalancer.ImportCertificateWithheldMessage: ImportCertificate
// withholds the server's message on every failing status (see the design's
// Error redaction section), so this must always be true for a non-nil err
// from ImportCertificate. A nil err is never withheld.
func importCertificateMessageWithheld(err error) bool {
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Message == loadbalancer.ImportCertificateWithheldMessage
}

// keyBase64Body returns key's base64-encoded body with every PEM header,
// footer, and newline removed, by decoding the PEM block and re-encoding its
// raw bytes: the same text a PEM encoder would have wrapped into lines,
// without depending on how it happened to wrap them. It returns "" if key is
// not valid PEM.
func keyBase64Body(key string) string {
	block, _ := pem.Decode([]byte(key))
	if block == nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(block.Bytes)
}

// errorContainsKeyWindow reports whether err's own message contains any
// 16-character contiguous window of key's base64 body (PEM header, footer,
// and newlines removed). A window this size is unlikely to occur in
// unrelated text by chance, but short enough to survive a server that
// escapes a character, truncates a line, or re-wraps the key at a different
// width than this process's PEM encoder used: exact whole-line matching,
// which transport.Request.Redact itself relies on, can miss all of those. A
// nil err never matches.
func errorContainsKeyWindow(err error, key string) bool {
	if err == nil {
		return false
	}
	const window = 16
	body := keyBase64Body(key)
	msg := err.Error()
	if len(body) < window {
		return body != "" && strings.Contains(msg, body)
	}
	for i := 0; i+window <= len(body); i++ {
		if strings.Contains(msg, body[i:i+window]) {
			return true
		}
	}
	return false
}

// certificateHasPrivateKeyText reports whether any string field, or string
// slice element, of cert contains PEM private key text. The design already
// requires that no read return a key and that typed models drop unknown
// fields, so this is a paranoid confirmation over the fields the SDK's model
// does know about, not the live check for an undocumented field; step 2 of
// TestLiveWriteLBCertificate covers that with a raw response capture
// instead.
func certificateHasPrivateKeyText(cert loadbalancer.Certificate) bool {
	v := reflect.ValueOf(cert)
	for i := range v.NumField() {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			if strings.Contains(f.String(), "PRIVATE KEY") {
				return true
			}
		case reflect.Slice:
			for j := range f.Len() {
				if s, ok := f.Index(j).Interface().(string); ok && strings.Contains(s, "PRIVATE KEY") {
					return true
				}
			}
		}
	}
	return false
}

// liveCertificateFieldCapture stores the most recently captured raw response
// body for a live check that must see the server's actual field names: a
// typed decode into loadbalancer.Certificate silently drops any field the
// SDK's model does not yet know about, which would hide exactly the field
// this check looks for.
type liveCertificateFieldCapture struct {
	mu   sync.Mutex
	body []byte
}

func (c *liveCertificateFieldCapture) capture(captured vngcloud.ResponseCapture) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = append([]byte(nil), captured.Body...)
}

// fieldNames returns the sorted top-level JSON key names of the most
// recently captured body, and whether any of them contains "key", "pass", or
// "secret" case-insensitively. It never returns a value, only names.
func (c *liveCertificateFieldCapture) fieldNames() (names []string, suspicious bool, err error) {
	c.mu.Lock()
	body := append([]byte(nil), c.body...)
	c.mu.Unlock()

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, false, err
	}
	for k := range raw {
		names = append(names, k)
		lower := strings.ToLower(k)
		if strings.Contains(lower, "key") || strings.Contains(lower, "pass") || strings.Contains(lower, "secret") {
			suspicious = true
		}
	}
	slices.Sort(names)
	return names, suspicious, nil
}

// VNGCLOUD_LIVE_LB_CERTIFICATE must be set to "1" in addition to
// VNGCLOUD_LIVE_WRITE, since certificate import sends a private key to
// GreenNode even though every certificate write costs nothing (see the
// design's Cost section).
//
// Every certificate and key is generated in this process with the standard
// library; no key is written to disk or logged, only whether one is present
// and its PEM header line. It deletes every leftover vngcloud-live-* not
// in-use certificate from a previous run first (step 1); lists certificates
// and, when any exist, reads one back, logging only the raw field names and
// whether any looks like it might hold a key, a passphrase, or a secret
// (step 2); imports a throwaway RSA 2048 TLS/SSL certificate, registering
// cleanup and the final remaining-count check as soon as its id is known
// (step 3); reads it back, logging field names and a boolean for whether any
// holds PRIVATE KEY text (step 4); imports the same name again to observe
// whether names are unique (step 5); imports an ECDSA P-256 certificate, an
// RSA certificate with an encrypted key and its passphrase, and a CA
// certificate with no key (step 6); imports a malformed key and a key that
// does not match the certificate, logging status and a boolean for whether
// the SDK withheld the server's message (it must), and asserting the error
// holds no 16-character window of the sent key's base64 body (step 7); and
// deletes every certificate this run created, including a repeat delete and a
// GET after delete, logging only statuses (step 8).
func TestLiveWriteLBCertificate(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live vLB certificate write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_LB_CERTIFICATE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_LB_CERTIFICATE=1 to run the live vLB certificate write test; " +
			"the import step sends a private key to GreenNode")
	}
	if err := envfile.Load(".env"); err != nil {
		t.Fatalf("load .env: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var capture liveCertificateFieldCapture
	cfg, err := vngcloud.LoadConfig(ctx,
		vngcloud.WithRegion("hcm-3"),
		vngcloud.WithConfigFile(emptyWriteFile(t, "config")),
		vngcloud.WithSharedCredentialsFile(emptyWriteFile(t, "credentials")),
		vngcloud.WithResponseCapture(capture.capture),
	)
	if errors.Is(err, vngcloud.ErrNoCredentials) {
		t.Fatal("set VNGCLOUD_ROOT_EMAIL, VNGCLOUD_USERNAME, and VNGCLOUD_PASSWORD (and optionally VNGCLOUD_TOTP_SECRET) in .env")
	}
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	client := loadbalancer.New(cfg)

	// Step 1: sweep up leftovers from an earlier run.
	leftovers := deleteLiveCertificates(ctx, t, client)
	t.Logf("step 1: deleted %d leftover certificate(s)", leftovers)

	// Step 2: read-only pass. List, then read one back if any exist, using
	// the raw captured body so an undocumented field is not hidden by the
	// typed decode.
	list, err := client.ListCertificates(ctx, nil)
	if err != nil {
		t.Fatalf("step 2 ListCertificates: %s", safeErr(err))
	}
	t.Logf("step 2: %d certificate(s) listed", len(list.Items))
	if len(list.Items) > 0 {
		if _, err := client.GetCertificate(ctx, &loadbalancer.GetCertificateInput{CertificateID: list.Items[0].UUID}); err != nil {
			t.Fatalf("step 2 GetCertificate: %s", safeErr(err))
		}
		names, suspicious, err := capture.fieldNames()
		if err != nil {
			t.Fatalf("step 2 read captured fields: %v", err)
		}
		t.Logf("step 2: get certificate raw fields %v, suspicious field name=%v", names, suspicious)
	}

	// Step 3: import a throwaway RSA 2048 TLS/SSL certificate.
	importSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	importName := "vngcloud-live-" + importSuffix
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("step 3 generate rsa key: %v", err)
	}
	certPEM, err := generateSelfSignedCertPEM(rsaKey, importName)
	if err != nil {
		t.Fatalf("step 3 generate self-signed certificate: %v", err)
	}
	keyPEM := rsaPrivateKeyPEM(rsaKey)

	start := time.Now()
	imported, err := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        importName,
		Type:        loadbalancer.CertificateTypeTLS,
		Certificate: certPEM,
		PrivateKey:  vngcloud.Secret(keyPEM),
	})
	if err != nil {
		deleteCertificateByName(t, client, importName)
		t.Fatalf("step 3 ImportCertificate: %s", safeErr(err))
	}
	importedID := imported.Certificate.UUID
	if importedID == "" {
		deleteCertificateByName(t, client, importName)
		t.Fatal("step 3: ImportCertificate returned an empty id; the design requires one")
	}
	t.Logf("step 3: imported certificate, type %s, inUse %v, wait %s",
		imported.Certificate.CertificateType, imported.Certificate.InUse, time.Since(start))

	// Register cleanup, and the final remaining-count check, as soon as
	// importedID is known, before any later step can fail and skip the
	// explicit deletes in step 8. createdIDs also collects every other
	// certificate this run imports below; the closure reads it after the
	// test function returns, once every append below has already run.
	createdIDs := []string{importedID}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		for _, id := range createdIDs {
			if _, err := client.DeleteCertificate(cleanupCtx, &loadbalancer.DeleteCertificateInput{CertificateID: id}); err != nil &&
				!vngcloud.IsNotFound(err) && !errors.Is(err, loadbalancer.ErrCertificateInUse) {
				t.Errorf("cleanup: delete certificate %s: %s", id, safeErr(err))
			}
		}
		final, err := listAllCertificates(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListCertificates: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, cert := range final {
			if isLiveCertificateName(cert.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vngcloud-live certificate(s) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live certificates, found %d", remaining)
		}
	})

	// Step 4: read it back; log field names and only a boolean for whether
	// any holds PRIVATE KEY text, never a value.
	gotImported, err := client.GetCertificate(ctx, &loadbalancer.GetCertificateInput{CertificateID: importedID})
	if err != nil {
		t.Fatalf("step 4 GetCertificate: %s", safeErr(err))
	}
	names, suspicious, capErr := capture.fieldNames()
	if capErr != nil {
		t.Fatalf("step 4 read captured fields: %v", capErr)
	}
	t.Logf("step 4: read imported certificate, raw fields %v, suspicious field name=%v, any field holds PRIVATE KEY text=%v",
		names, suspicious, certificateHasPrivateKeyText(gotImported.Certificate))

	// Step 5: import the same name again, to observe whether names are
	// unique.
	_, dupErr := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        importName,
		Type:        loadbalancer.CertificateTypeTLS,
		Certificate: certPEM,
		PrivateKey:  vngcloud.Secret(keyPEM),
	})
	t.Logf("step 5: duplicate name import: %s", safeErr(dupErr))
	if dupErr == nil {
		// The server allowed a duplicate name; find the second id so cleanup
		// deletes it too, instead of leaking it.
		dup, listErr := client.ListCertificates(ctx, &loadbalancer.ListCertificatesInput{Name: importName})
		if listErr != nil {
			t.Errorf("step 5 list after duplicate import: %s", safeErr(listErr))
		} else {
			for _, cert := range dup.Items {
				if cert.Name == importName && cert.UUID != importedID {
					createdIDs = append(createdIDs, cert.UUID)
				}
			}
		}
	}

	// Step 6a: an ECDSA P-256 certificate.
	ecdsaSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 6a generate name suffix: %v", err)
	}
	ecdsaName := "vngcloud-live-" + ecdsaSuffix
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("step 6a generate ecdsa key: %v", err)
	}
	ecdsaCertPEM, err := generateSelfSignedCertPEM(ecdsaKey, ecdsaName)
	if err != nil {
		t.Fatalf("step 6a generate ecdsa self-signed certificate: %v", err)
	}
	ecdsaKeyPEM, err := ecdsaPrivateKeyPEM(ecdsaKey)
	if err != nil {
		t.Fatalf("step 6a encode ecdsa key: %v", err)
	}
	ecdsaImported, ecdsaErr := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        ecdsaName,
		Type:        loadbalancer.CertificateTypeTLS,
		Certificate: ecdsaCertPEM,
		PrivateKey:  vngcloud.Secret(ecdsaKeyPEM),
	})
	t.Logf("step 6a: ECDSA P-256 import: %s", safeErr(ecdsaErr))
	if ecdsaErr == nil {
		createdIDs = append(createdIDs, ecdsaImported.Certificate.UUID)
	} else {
		deleteCertificateByName(t, client, ecdsaName)
	}

	// Step 6b: an RSA certificate with an encrypted key and its passphrase.
	encSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 6b generate name suffix: %v", err)
	}
	encName := "vngcloud-live-" + encSuffix
	encRSAKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("step 6b generate rsa key: %v", err)
	}
	encCertPEM, err := generateSelfSignedCertPEM(encRSAKey, encName)
	if err != nil {
		t.Fatalf("step 6b generate self-signed certificate: %v", err)
	}
	const encPassphrase = "vngcloud-live-throwaway-passphrase"
	encKeyPEM, err := encryptedRSAPrivateKeyPEM(encRSAKey, encPassphrase)
	if err != nil {
		t.Fatalf("step 6b encrypt rsa key: %v", err)
	}
	encImported, encErr := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        encName,
		Type:        loadbalancer.CertificateTypeTLS,
		Certificate: encCertPEM,
		PrivateKey:  vngcloud.Secret(encKeyPEM),
		Passphrase:  vngcloud.Secret(encPassphrase),
	})
	t.Logf("step 6b: encrypted RSA key import: %s", safeErr(encErr))
	if encErr == nil {
		createdIDs = append(createdIDs, encImported.Certificate.UUID)
	} else {
		deleteCertificateByName(t, client, encName)
	}

	// Step 6c: a CA certificate with no key.
	caSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 6c generate name suffix: %v", err)
	}
	caName := "vngcloud-live-" + caSuffix
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("step 6c generate ca key: %v", err)
	}
	caCertPEM, err := generateSelfSignedCertPEM(caKey, caName)
	if err != nil {
		t.Fatalf("step 6c generate ca self-signed certificate: %v", err)
	}
	caImported, caErr := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        caName,
		Type:        loadbalancer.CertificateTypeCA,
		Certificate: caCertPEM,
	})
	t.Logf("step 6c: CA import: %s", safeErr(caErr))
	if caErr == nil {
		createdIDs = append(createdIDs, caImported.Certificate.UUID)
	} else {
		deleteCertificateByName(t, client, caName)
	}

	// Step 7a: a malformed key, a valid PEM header around random bytes.
	garbage := make([]byte, 256)
	if _, err := rand.Read(garbage); err != nil {
		t.Fatalf("step 7a generate garbage: %v", err)
	}
	malformedKeyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: garbage}))
	malformedSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 7a generate name suffix: %v", err)
	}
	malformedName := "vngcloud-live-" + malformedSuffix
	_, malformedErr := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        malformedName,
		Type:        loadbalancer.CertificateTypeTLS,
		Certificate: certPEM,
		PrivateKey:  vngcloud.Secret(malformedKeyPEM),
	})
	t.Logf("step 7a: malformed key import: %s, message withheld=%v",
		safeErr(malformedErr), importCertificateMessageWithheld(malformedErr))
	if malformedErr != nil && !importCertificateMessageWithheld(malformedErr) {
		t.Error("step 7a: ImportCertificate did not withhold the server message")
	}
	if errorContainsKeyWindow(malformedErr, malformedKeyPEM) {
		t.Error("step 7a: error message contains a 16-character window of the sent key's base64 body")
	}
	deleteCertificateByName(t, client, malformedName)

	// Step 7b: a key that does not match the certificate.
	mismatchedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("step 7b generate mismatched key: %v", err)
	}
	mismatchedKeyPEM := rsaPrivateKeyPEM(mismatchedKey)
	mismatchedSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 7b generate name suffix: %v", err)
	}
	mismatchedName := "vngcloud-live-" + mismatchedSuffix
	_, mismatchedErr := client.ImportCertificate(ctx, &loadbalancer.ImportCertificateInput{
		Name:        mismatchedName,
		Type:        loadbalancer.CertificateTypeTLS,
		Certificate: certPEM,
		PrivateKey:  vngcloud.Secret(mismatchedKeyPEM),
	})
	t.Logf("step 7b: mismatched key import: %s, message withheld=%v",
		safeErr(mismatchedErr), importCertificateMessageWithheld(mismatchedErr))
	if mismatchedErr != nil && !importCertificateMessageWithheld(mismatchedErr) {
		t.Error("step 7b: ImportCertificate did not withhold the server message")
	}
	if errorContainsKeyWindow(mismatchedErr, mismatchedKeyPEM) {
		t.Error("step 7b: error message contains a 16-character window of the sent key's base64 body")
	}
	deleteCertificateByName(t, client, mismatchedName)

	// Step 8: delete every certificate this run created, then repeat the
	// first delete and GET after delete, logging only statuses.
	for _, id := range createdIDs {
		if _, err := client.DeleteCertificate(ctx, &loadbalancer.DeleteCertificateInput{CertificateID: id}); err != nil {
			t.Errorf("step 8 DeleteCertificate %s: %s", id, safeErr(err))
		}
	}
	t.Logf("step 8: deleted %d certificate(s)", len(createdIDs))

	_, repeatErr := client.DeleteCertificate(ctx, &loadbalancer.DeleteCertificateInput{CertificateID: importedID})
	t.Logf("step 8: repeat delete: %s", safeErr(repeatErr))
	if !vngcloud.IsNotFound(repeatErr) {
		t.Errorf("step 8: repeat delete err = %s, want NotFound", safeErr(repeatErr))
	}

	_, getAfterDeleteErr := client.GetCertificate(ctx, &loadbalancer.GetCertificateInput{CertificateID: importedID})
	t.Logf("step 8: get after delete: %s", safeErr(getAfterDeleteErr))

	// Step 9: the next day's bill showing no vLB line, and get-balances
	// staying unchanged, is a manual owner check the day after this run,
	// since the design treats import as free (see its Cost section) and
	// there is no same-run way to confirm a bill that has not posted yet.
	t.Log("step 9: check the next day's bill shows no vLB line, and get-balances is unchanged (manual, outside this test run)")
}

// vcrLiveNameSuffixPattern is the live vCR write test's own repository
// naming scheme: a leftover sweep and the cleanup's remaining-count check
// both look for a name ending with vcrlive-<8 lowercase hex>, exactly. The
// server applies no account prefix, so a plain equality check would also
// work; the suffix match costs nothing and stays correct if that changes.
// The short vcrlive- prefix, rather than vngcloud-live-, keeps the
// generated name within the server's 20-character limit for repoName.
var vcrLiveNameSuffixPattern = regexp.MustCompile(`vcrlive-[0-9a-f]{8}$`)

// isLiveVCRRepositoryName reports whether name ends with
// vcrLiveNameSuffixPattern.
func isLiveVCRRepositoryName(name string) bool {
	return vcrLiveNameSuffixPattern.MatchString(name)
}

// isVCRPaymentRefusal reports whether err is a *vngcloud.APIError with a 4xx
// status whose message mentions balance, credit, payment, or order: the
// cost probe's own signal that a vCR repository is paid and the account has
// no funds to cover it. TestLiveWriteContainerRegistryRepository stops at
// the first such refusal rather than retrying it, since a paid create must
// never be resent blind.
func isVCRPaymentRefusal(err error) bool {
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode < 400 || apiErr.StatusCode >= 500 {
		return false
	}
	lower := strings.ToLower(apiErr.Message)
	for _, word := range []string{"balance", "credit", "payment", "order"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// deleteVCRRepositoryByName lists repositories and deletes any whose name
// exactly matches name and holds no images, skipping any id in skipIDs. It
// is used after a CreateRepository call returns an error or an empty id,
// since a POST that returned an error may still have reached the server,
// and the server applies no account prefix: a created repository's name
// equals the input name exactly. skipIDs lets a caller that already tracks
// one of these repositories by id, such as the original create's
// repositoryID, exclude it here rather than deleting it by this by-name
// sweep. It runs on its own timeout, not the calling test step's context, so
// it can still clean up after that step's context is the reason the step
// failed.
func deleteVCRRepositoryByName(t *testing.T, client *containerregistry.Client, name string, skipIDs ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := client.ListRepositories(ctx, &containerregistry.ListRepositoriesInput{Name: name})
	if err != nil {
		t.Errorf("cleanup: list repositories by name: %s", safeErr(err))
		return
	}
	for _, r := range list.Items {
		if r.Name != name || r.ImageCount > 0 || slices.Contains(skipIDs, r.ID) {
			continue
		}
		if _, err := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: r.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete repository by name: %s", safeErr(err))
		}
	}
}

// TestLiveWriteContainerRegistryRepository exercises CreateRepository,
// GetRepository, and DeleteRepository against the account named in .env.
//
// Whether a vCR repository costs money is unknown until the design's cost
// probe runs; this test stops at the first create if the server refuses it
// for payment (a 4xx naming balance, credit, payment, or order), logs the
// status and code, and skips rather than fails: a paid create must not be
// resent blind, and nothing was left behind to clean up. It never pushes an
// image, so DeleteRepository's guard against a non-empty repository is not
// exercised.
//
// It deletes every leftover vcrlive-* repository holding no images first
// (step 1); creates vcrlive-<8 hex> with QuotaLimitGB 1, registering a
// by-name fallback cleanup at once if the create returns an error or an
// empty id (step 2); registers the fallback delete by id as soon as the
// created repository's id is known (step 3); reads it back and confirms the
// id matches (step 4); creates the same name again, logging the server's
// response either way and cleaning up any second repository by name, other
// than the original, whether that create succeeded or its ambiguous
// failure may still have reached the server (step 5); and deletes the
// repository, confirming a repeat delete returns NotFound (step 6).
func TestLiveWriteContainerRegistryRepository(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live vCR write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_VCR") != "1" {
		t.Skip("set VNGCLOUD_LIVE_VCR=1 to run the live vCR write test")
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
	client := containerregistry.New(cfg)

	// Step 1: delete every leftover vcrlive-* repository holding no images
	// from a previous run.
	leftovers, err := client.ListRepositories(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListRepositories: %s", safeErr(err))
	}
	deleted := 0
	for _, leftover := range leftovers.Items {
		if !isLiveVCRRepositoryName(leftover.Name) || leftover.ImageCount > 0 {
			continue
		}
		if _, err := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover repository: %s", safeErr(err))
		}
		deleted++
	}
	t.Logf("step 1: deleted %d leftover repository(ies)", deleted)

	// Step 2: create the repository. Whether this is free is the open
	// question the design's cost probe answers; this test stops here,
	// skipping rather than failing, if the server refuses for payment.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vcrlive-" + suffix

	created, err := client.CreateRepository(ctx, &containerregistry.CreateRepositoryInput{
		Name:         name,
		QuotaLimitGB: 1,
	})
	var repositoryID string
	if err == nil {
		repositoryID = created.Repository.ID
	}
	if err != nil || repositoryID == "" {
		// A POST is not retried after an ambiguous failure, so the create may
		// still have reached the server despite the error, or with a response
		// that carried no id. Register a fallback that finds it by its exact
		// name (the server applies no account prefix) and deletes it, so this
		// runs during test cleanup regardless of which branch below exits the
		// test.
		t.Cleanup(func() { deleteVCRRepositoryByName(t, client, name) })
	}
	if isVCRPaymentRefusal(err) {
		var apiErr *vngcloud.APIError
		errors.As(err, &apiErr)
		t.Skipf("step 2: server refused the create for payment, status=%d code=%s; a repository is paid, never retrying", apiErr.StatusCode, apiErr.Code)
	}
	if err != nil {
		t.Fatalf("step 2 CreateRepository: %s", safeErr(err))
	}
	if repositoryID == "" {
		t.Fatal("step 2: CreateRepository returned an empty id; the design requires one")
	}
	t.Logf("step 2: created repository, quotaLimitGB %d", created.Repository.QuotaLimitGB)

	// Step 3: register the fallback delete as soon as repositoryID is
	// known, before any later step can fail and skip the explicit delete in
	// step 6.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteRepository(cleanupCtx, &containerregistry.DeleteRepositoryInput{RepositoryID: repositoryID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete repository: %s", safeErr(err))
		}
		final, err := client.ListRepositories(cleanupCtx, nil)
		if err != nil {
			t.Errorf("cleanup: final ListRepositories: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, r := range final.Items {
			if isLiveVCRRepositoryName(r.Name) {
				remaining++
			}
		}
		t.Logf("cleanup: vcrlive repository(ies) remaining: %d", remaining)
		if remaining != 0 {
			t.Errorf("cleanup: expected 0 vcrlive repositories, found %d", remaining)
		}
	})

	// Step 4: read the repository back and confirm its id matches the
	// create response.
	fetched, err := client.GetRepository(ctx, &containerregistry.GetRepositoryInput{RepositoryID: repositoryID})
	if err != nil {
		t.Fatalf("step 4 GetRepository: %s", safeErr(err))
	}
	if fetched.Repository.ID != repositoryID {
		t.Fatal("step 4: GetRepository returned a different id")
	}
	t.Logf("step 4: read repository back, imageCount %d", fetched.Repository.ImageCount)

	// Step 5: create the same name again and log the server's response,
	// without failing the test either way: whether names collide is an
	// open question. The create is never retried after an ambiguous
	// failure, so a non-nil dupErr may still mean a second repository
	// reached the server under this name despite the error; that case
	// registers the same by-name cleanup step 2 uses. Either way the
	// cleanup skips repositoryID, so it never deletes the original ahead
	// of step 6's own explicit delete.
	_, dupErr := client.CreateRepository(ctx, &containerregistry.CreateRepositoryInput{
		Name:         name,
		QuotaLimitGB: 1,
		NoWait:       true,
	})
	if dupErr == nil {
		t.Log("step 5: creating a duplicate name succeeded")
		deleteVCRRepositoryByName(t, client, name, repositoryID)
	} else {
		t.Cleanup(func() { deleteVCRRepositoryByName(t, client, name, repositoryID) })
		t.Logf("step 5: duplicate name response, %s", safeErr(dupErr))
	}

	// Step 6: delete the repository and confirm a repeat delete returns
	// NotFound.
	if _, err := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: repositoryID}); err != nil {
		t.Fatalf("step 6 DeleteRepository: %s", safeErr(err))
	}
	_, secondErr := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: repositoryID})
	if !vngcloud.IsNotFound(secondErr) {
		t.Fatalf("step 6: repeat delete = %v, want NotFound", secondErr)
	}
}
