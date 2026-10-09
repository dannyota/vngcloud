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
	"net/netip"
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
	"danny.vn/vngcloud/iam"
	"danny.vn/vngcloud/internal/core"
	"danny.vn/vngcloud/internal/envfile"
	"danny.vn/vngcloud/internal/routes"
	"danny.vn/vngcloud/internal/testutil"
	"danny.vn/vngcloud/internal/transport"
	"danny.vn/vngcloud/loadbalancer"
	"danny.vn/vngcloud/monitor"
	"danny.vn/vngcloud/network"
	"danny.vn/vngcloud/portal"
	"danny.vn/vngcloud/tagging"
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

// sdkSentinelErrs lists the SDK's own hand-built sentinel errors: every one
// of these is constructed in this codebase from an operation name plus IDs,
// names, counts, or statuses already known to the caller (see each error's
// own doc comment), and none is ever built by wrapping a raw transport
// failure or an *vngcloud.APIError, whose Message field echoes server text
// safeErr must not print. safeErr prints the full message of an error that
// wraps one of these; every other error stays redacted.
var sdkSentinelErrs = []error{
	vngcloud.ErrInvalidInput,
	network.ErrInUse,
	network.ErrBusy,
	network.ErrDefaultResource,
	network.ErrFailed,
	network.ErrNotSettled,
	network.ErrSecurityGroupInUse,
	network.ErrSystemGroup,
	network.ErrUnexpectedStatus,
	iam.ErrSelfChange,
	iam.ErrPrivilegedChange,
	iam.ErrNoSecret,
	iam.ErrCreateUnconfirmed,
	iam.ErrManagedPolicy,
	iam.ErrInUse,
	dns.ErrZoneBusy,
	dns.ErrNotSettled,
	dns.ErrFailed,
	compute.ErrServerGroupInUse,
	compute.ErrNotSettled,
	containerregistry.ErrUserNotFound,
	containerregistry.ErrRepositoryNotEmpty,
	containerregistry.ErrNotSettled,
	loadbalancer.ErrCertificateInUse,
	monitor.ErrOTPRejected,
	monitor.ErrUnexpectedStatus,
	monitor.ErrStatusUnconfirmed,
}

// safeErr summarizes err for a test log line. A *vngcloud.APIError's own
// Message may echo request data, so only its StatusCode and Code print. An
// SDK sentinel may wrap IDs or addresses, so only its constant message
// prints. Anything else prints only its Go type.
func safeErr(err error) string {
	if err == nil {
		return "none"
	}
	var apiErr *vngcloud.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("status=%d code=%s", apiErr.StatusCode, apiErr.Code)
	}
	for _, sentinel := range sdkSentinelErrs {
		if errors.Is(err, sentinel) {
			return sentinel.Error()
		}
	}
	return fmt.Sprintf("non-API error (%T)", err)
}

// TestSafeErr checks that safeErr redacts API and sentinel detail.
func TestSafeErr(t *testing.T) {
	if got := safeErr(nil); got != "none" {
		t.Errorf("safeErr(nil) = %q, want %q", got, "none")
	}

	apiErr := &vngcloud.APIError{Operation: "network.DeleteVirtualIPAddress", StatusCode: 400, Code: "InvalidUsage", Message: "vip-secret-name is not deletable"}
	if got := safeErr(apiErr); got != "status=400 code=InvalidUsage" {
		t.Errorf("safeErr(apiErr) = %q, want status=400 code=InvalidUsage", got)
	}
	if strings.Contains(safeErr(apiErr), apiErr.Message) {
		t.Errorf("safeErr(apiErr) = %q, must not include the server message", safeErr(apiErr))
	}

	invalidInputErr := fmt.Errorf("%w: network.DeleteVirtualIPAddress: virtual IP vip-1 has type %q, not a private virtual IP; it must be deleted through the public virtual IP call instead", vngcloud.ErrInvalidInput, "public-vm")
	if got := safeErr(invalidInputErr); got != vngcloud.ErrInvalidInput.Error() {
		t.Errorf("safeErr(invalidInputErr) = %q, want %q", got, vngcloud.ErrInvalidInput.Error())
	}
	if strings.Contains(safeErr(invalidInputErr), "vip-1") || strings.Contains(safeErr(invalidInputErr), "public-vm") {
		t.Errorf("safeErr(invalidInputErr) = %q, must not include virtual IP detail", safeErr(invalidInputErr))
	}

	inUseErr := fmt.Errorf("%w: virtual IP vip-1 at 10.0.0.10 has 2 address pairs", network.ErrInUse)
	if got := safeErr(inUseErr); got != network.ErrInUse.Error() {
		t.Errorf("safeErr(inUseErr) = %q, want %q", got, network.ErrInUse.Error())
	}
	if strings.Contains(safeErr(inUseErr), "vip-1") || strings.Contains(safeErr(inUseErr), "10.0.0.10") {
		t.Errorf("safeErr(inUseErr) = %q, must not include virtual IP detail", safeErr(inUseErr))
	}

	transportErr := fmt.Errorf("Get \"https://example.invalid/v2/token=abc123\": dial tcp: connection refused")
	if got := safeErr(transportErr); got != fmt.Sprintf("non-API error (%T)", transportErr) {
		t.Errorf("safeErr(transportErr) = %q, want the redacted form", got)
	}
	if strings.Contains(safeErr(transportErr), "example.invalid") || strings.Contains(safeErr(transportErr), "abc123") {
		t.Errorf("safeErr(transportErr) = %q, must not include the URL", safeErr(transportErr))
	}
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

// pickFreeVPCCIDR scans /16 blocks from 10.250.0.0/16 down to 10.200.0.0/16
// and returns the first that overlaps no VPC CIDR the project already
// lists: GreenNode refuses CreateVPC with 400 BadRequest "VPC is overlap
// with another." when the new VPC's CIDR overlaps any existing VPC in the
// project, and the test account keeps a permanent VPC at a fixed /16 (see
// useLiveVPCAndSubnet) that a hardcoded constant here would collide with.
// The scan order is deterministic, so repeated runs favor the same free
// block while other runs hold no VPC.
func pickFreeVPCCIDR(ctx context.Context, client *network.Client) (string, error) {
	existing, err := listAllVPCs(ctx, client)
	if err != nil {
		return "", fmt.Errorf("list VPCs: %w", err)
	}
	used := make([]netip.Prefix, 0, len(existing))
	for _, v := range existing {
		p, err := netip.ParsePrefix(v.CIDR)
		if err != nil {
			continue
		}
		used = append(used, p.Masked())
	}

	for second := 250; second >= 200; second-- {
		candidate := netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(second), 0, 0}), 16).Masked()
		free := true
		for _, u := range used {
			if candidate.Overlaps(u) {
				free = false
				break
			}
		}
		if free {
			return candidate.String(), nil
		}
	}
	return "", fmt.Errorf("no free /16 CIDR between 10.200.0.0/16 and 10.250.0.0/16, %d VPC(s) already used", len(used))
}

// vpcBlockCIDR returns the CIDR of the /24 block at index block (0-255)
// inside vpcCIDR, a /16 under 10.0.0.0/8, masked to bits (24 or 28). Every
// subnet a live VPC test creates comes from this, so the subnets always sit
// inside whatever /16 pickFreeVPCCIDR chose rather than a fixed constant.
func vpcBlockCIDR(vpcCIDR string, block byte, bits int) (string, error) {
	prefix, err := netip.ParsePrefix(vpcCIDR)
	if err != nil {
		return "", fmt.Errorf("parse VPC CIDR %q: %w", vpcCIDR, err)
	}
	addr := prefix.Masked().Addr().As4()
	addr[2] = block
	addr[3] = 0
	return netip.PrefixFrom(netip.AddrFrom4(addr), bits).String(), nil
}

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

// vpcIDsHeldByNetworkACLs returns the set of VPC ids that some network ACL
// belongs to, account-wide. A listed ACL's own NetworkID is confirmed live
// to sometimes come back empty for an ACL that does belong to a VPC, so
// this also reads every ACL's own detail with GetNetworkACL and checks its
// VPCID. ok is false when the ACL listing, or any one read, fails other
// than NotFound; the caller must then treat every VPC as held rather than
// risk deleting one a network ACL still holds.
func vpcIDsHeldByNetworkACLs(ctx context.Context, client *network.Client) (held map[string]bool, ok bool) {
	acls, err := listAllNetworkACLs(ctx, client)
	if err != nil {
		return nil, false
	}
	held = make(map[string]bool)
	for _, acl := range acls {
		if acl.NetworkID != "" {
			held[acl.NetworkID] = true
		}
		detail, err := client.GetNetworkACL(ctx, &network.GetNetworkACLInput{NetworkACLID: acl.UUID})
		if err != nil {
			if vngcloud.IsNotFound(err) {
				continue
			}
			return nil, false
		}
		if detail.ACL.VPCID != "" {
			held[detail.ACL.VPCID] = true
		}
	}
	return held, true
}

// leftoverVPCsToDelete filters candidates, a leftover VPC sweep's own
// name-matched VPCs, down to the ones safe to delete this run, in toDelete.
// The account can hold a permanently stuck VPC: one a network ACL still
// belongs to, or one with Private DNS on, whose delete always fails.
// TestLiveWriteNetworkVPC enables Private DNS on its own VPC, behind
// VNGCLOUD_LIVE_NETWORK_PRIVATE_DNS, and neither case is ever true of a VPC
// that test created past its own run: the ACL association and the Private
// DNS enable both happen only on that run's own VPC, and its own cleanup
// then tries to delete it regardless. So a candidate held or with Private
// DNS on always marks a leftover from an earlier run that must be left
// alone rather than fail the test on its delete; skippedIDs names every
// such candidate, by UUID, for the caller to exclude from its own later
// "did this run clean up after itself" check. When the network ACL check
// itself fails, every candidate is skipped and logged, and skippedIDs holds
// all of them, instead of risking a delete against a VPC a network ACL
// still holds.
func leftoverVPCsToDelete(ctx context.Context, t *testing.T, client *network.Client, candidates []network.VPC) (toDelete []network.VPC, skippedIDs map[string]bool) {
	t.Helper()
	if len(candidates) == 0 {
		return nil, nil
	}
	held, ok := vpcIDsHeldByNetworkACLs(ctx, client)
	if !ok {
		t.Logf("step 1: could not list or read every network ACL; skipping all %d leftover VPC(s) this run", len(candidates))
		skippedIDs = make(map[string]bool, len(candidates))
		for _, vpc := range candidates {
			skippedIDs[vpc.UUID] = true
		}
		return nil, skippedIDs
	}
	skippedIDs = make(map[string]bool)
	aclHeld := 0
	nonDisabledDNS := 0
	for _, vpc := range candidates {
		switch {
		case held[vpc.UUID]:
			aclHeld++
			skippedIDs[vpc.UUID] = true
		case vpc.DNSStatus != "DISABLED":
			nonDisabledDNS++
			t.Logf("step 1: skipping a leftover VPC: DNS status is %s, not DISABLED", vpc.DNSStatus)
			skippedIDs[vpc.UUID] = true
		default:
			toDelete = append(toDelete, vpc)
		}
	}
	if aclHeld > 0 {
		t.Logf("step 1: skipping %d leftover VPC(s) held by a network ACL", aclHeld)
	}
	if nonDisabledDNS > 0 {
		t.Logf("step 1: skipping %d leftover VPC(s) with Private DNS enabled", nonDisabledDNS)
	}
	return toDelete, skippedIDs
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
//
// blocked, when not nil, is checked once before anything else: a non-empty
// return names a reason this call must delete nothing at all, neither a
// subnet nor the VPC itself, and instead fail the test loudly, naming vpcID,
// for manual cleanup. A caller whose own resource (such as a network ACL)
// can still hold one of this VPC's subnets after its own cleanup passes a
// blocked that reports so, since deleting a subnet still held that way is
// what stranded a network ACL on a past live run. Every other caller passes
// nil.
func deleteVPCAndSubnets(ctx context.Context, t *testing.T, client *network.Client, vpcID string, blocked func() string) {
	t.Helper()
	if blocked != nil {
		if reason := blocked(); reason != "" {
			t.Errorf("delete VPC: not deleting VPC %s or its subnets: %s; clean it up manually", vpcID, reason)
			return
		}
	}

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
		deleteVPCAndSubnets(ctx, t, client, v.UUID, nil)
	}
}

// createLiveVPCAndSubnet creates a vngcloud-live-<8 hex> VPC and a /24
// subnet in it, in the account's enabled zone, for a test whose own writes
// must land on a VPC this same run created and never on any other: the
// design requires this for every write that changes a whole VPC, such as a
// main route table's routes, ACL association, or Private DNS. It registers
// the VPC's t.Cleanup, deleting its subnets and then the VPC with the same
// ErrInUse retry TestLiveWriteNetworkVPC's own cleanup uses, as soon as the
// VPC's id is known, so a later failure in the calling test still cleans it
// up.
//
// blocked is passed straight through to that registered deleteVPCAndSubnets
// call; see its own doc comment. A caller with no other resource that could
// still hold one of this VPC's subnets passes nil.
//
// When VNGCLOUD_LIVE_NETWORK_VPC_ID names a VPC, this reuses it instead:
// see useLiveVPCAndSubnet. The account's VPC quota otherwise leaves no VPC
// free for a second live-write test to run against.
func createLiveVPCAndSubnet(ctx context.Context, t *testing.T, client *network.Client, portalClient *portal.Client, blocked func() string) (vpcID, subnetID string) {
	t.Helper()

	if reuseID := strings.TrimSpace(os.Getenv("VNGCLOUD_LIVE_NETWORK_VPC_ID")); reuseID != "" {
		return useLiveVPCAndSubnet(ctx, t, client, portalClient, reuseID)
	}

	zoneID, err := pickEnabledZoneID(ctx, portalClient)
	if err != nil {
		t.Fatalf("pick enabled zone: %s", safeErr(err))
	}

	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("generate VPC name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	vpcCIDR, err := pickFreeVPCCIDR(ctx, client)
	if err != nil {
		t.Fatalf("pick a free VPC CIDR: %s", err)
	}

	createdVPC, err := client.CreateVPC(ctx, &network.CreateVPCInput{Name: name, CIDR: vpcCIDR})
	if err != nil {
		deleteVPCByName(t, client, name)
		t.Fatalf("CreateVPC: %s", safeErr(err))
	}
	vpcID = createdVPC.VPC.UUID
	if vpcID == "" {
		deleteVPCByName(t, client, name)
		t.Fatal("CreateVPC returned an empty id; the design requires one")
	}
	t.Logf("created VPC, status %s", createdVPC.VPC.Status)

	// Registered as soon as vpcID is known, before the subnet create below,
	// or anything the calling test does afterward, can fail and skip it.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()
		deleteVPCAndSubnets(cleanupCtx, t, client, vpcID, blocked)
	})

	subnet24CIDR, err := vpcBlockCIDR(vpcCIDR, 1, 24)
	if err != nil {
		t.Fatalf("derive /24 subnet CIDR: %s", err)
	}
	createdSubnet, err := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-a", CIDR: subnet24CIDR,
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %s", safeErr(err))
	}
	subnetID = createdSubnet.Subnet.UUID
	if subnetID == "" {
		t.Fatal("CreateSubnet returned an empty id; the design requires one")
	}
	t.Logf("created /24 subnet, status %s", createdSubnet.Subnet.Status)
	return vpcID, subnetID
}

// liveReuseVPCNamePattern is the exact name a VPC named by
// VNGCLOUD_LIVE_NETWORK_VPC_ID must have before useLiveVPCAndSubnet ever
// writes to it: the same shape every live network test's own VPC create
// uses, so an unrelated VPC id pasted into that variable by mistake is
// refused rather than silently written to.
var liveReuseVPCNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

// useLiveVPCAndSubnet reads the VPC named by vpcID and creates a /24 subnet
// of the test's own inside it, for a run where the account's VPC quota
// holds no spare VPC to create: one VPC is a stuck vngcloud-live- VPC a
// past run's network ACL could not delete (see
// vserver-network-writes-checks.md), and the owner approved reusing its
// VPC as a permanent live test fixture rather than leaving every network
// write test unable to run at all.
//
// It refuses, before any write, a VPC whose name does not exactly match
// liveReuseVPCNamePattern: VNGCLOUD_LIVE_NETWORK_VPC_ID must name one of
// this suite's own throwaway VPCs, never one that holds real data. The
// subnet's CIDR is chosen inside the VPC's own CIDR, avoiding every subnet
// the VPC already has, since the reused VPC is not empty: it keeps
// whatever subnets earlier runs left behind, including the one the stuck
// ACL still associates.
//
// Cleanup deletes only the subnet this call creates, disassociating its
// network ACL first if one still lists it (see deleteLiveSubnetOnly): it
// never deletes the VPC itself. Deleting a subnet an ACL still holds is
// what stranded the account's one undeletable ACL, and reusing this VPC
// forever depends on never repeating that mistake.
func useLiveVPCAndSubnet(ctx context.Context, t *testing.T, client *network.Client, portalClient *portal.Client, vpcID string) (string, string) {
	t.Helper()

	got, err := client.GetVPC(ctx, &network.GetVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("GetVPC %s (VNGCLOUD_LIVE_NETWORK_VPC_ID): %s", vpcID, safeErr(err))
	}
	if !liveReuseVPCNamePattern.MatchString(got.VPC.Name) {
		t.Fatalf("VNGCLOUD_LIVE_NETWORK_VPC_ID names a VPC whose name does not match %s; refusing to write to it",
			liveReuseVPCNamePattern.String())
	}

	zoneID, err := pickEnabledZoneID(ctx, portalClient)
	if err != nil {
		t.Fatalf("pick enabled zone: %s", safeErr(err))
	}

	existing, err := client.ListSubnetsByVPC(ctx, &network.ListSubnetsByVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("ListSubnetsByVPC %s: %s", vpcID, safeErr(err))
	}
	usedCIDRs := make([]string, 0, len(existing.Items))
	for _, s := range existing.Items {
		usedCIDRs = append(usedCIDRs, s.CIDR)
	}
	cidr, err := pickFreeSubnetCIDR(got.VPC.CIDR, usedCIDRs)
	if err != nil {
		t.Fatalf("pick a free /24 subnet in the reused VPC: %s", err)
	}

	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("generate subnet name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	createdSubnet, err := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name, CIDR: cidr,
	})
	if err != nil {
		t.Fatalf("CreateSubnet: %s", safeErr(err))
	}
	subnetID := createdSubnet.Subnet.UUID
	if subnetID == "" {
		t.Fatal("CreateSubnet returned an empty id; the design requires one")
	}
	t.Logf("created /24 subnet in the reused VPC, status %s", createdSubnet.Subnet.Status)

	// Registered as soon as subnetID is known. Unlike createLiveVPCAndSubnet,
	// this never deletes vpcID: the VPC is a permanent fixture, not this
	// run's own.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		deleteLiveSubnetOnly(cleanupCtx, t, client, vpcID, subnetID)
	})

	return vpcID, subnetID
}

// pickFreeSubnetCIDR returns a /24 prefix inside vpcCIDR that overlaps none
// of usedCIDRs, for a reused VPC that already holds other subnets. It walks
// every /24 block of vpcCIDR in order and returns the first that overlaps
// nothing in usedCIDRs, so repeated runs against the same VPC pick the same
// CIDR whenever the earlier run's subnet was cleaned up first.
func pickFreeSubnetCIDR(vpcCIDR string, usedCIDRs []string) (string, error) {
	vpcPrefix, err := netip.ParsePrefix(vpcCIDR)
	if err != nil {
		return "", fmt.Errorf("parse VPC CIDR %q: %w", vpcCIDR, err)
	}
	vpcPrefix = vpcPrefix.Masked()
	if !vpcPrefix.Addr().Is4() {
		return "", fmt.Errorf("VPC CIDR %q is not IPv4", vpcCIDR)
	}
	if vpcPrefix.Bits() > 24 {
		return "", fmt.Errorf("VPC CIDR %q leaves no room for a /24 subnet", vpcCIDR)
	}

	used := make([]netip.Prefix, 0, len(usedCIDRs))
	for _, c := range usedCIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			continue
		}
		used = append(used, p.Masked())
	}

	baseAddr := vpcPrefix.Addr().As4()
	base := binary.BigEndian.Uint32(baseAddr[:])
	blocks := uint32(1) << (24 - vpcPrefix.Bits())
	for i := uint32(0); i < blocks; i++ {
		var addr [4]byte
		binary.BigEndian.PutUint32(addr[:], base+i<<8)
		candidate := netip.PrefixFrom(netip.AddrFrom4(addr), 24)
		free := true
		for _, u := range used {
			if candidate.Contains(u.Addr()) || u.Contains(candidate.Addr()) {
				free = false
				break
			}
		}
		if free {
			return candidate.String(), nil
		}
	}
	return "", fmt.Errorf("no free /24 subnet inside VPC CIDR %q, %d subnet(s) already used", vpcCIDR, len(used))
}

// deleteLiveSubnetOnly deletes subnetID from vpcID without ever touching
// vpcID itself, for a subnet created inside a reused, permanent VPC (see
// useLiveVPCAndSubnet). It disassociates the subnet's network ACL first,
// when GetSubnet still shows one: deleting a subnet an ACL still holds is
// what left the account with one network ACL a delete can never remove
// (see vserver-network-writes-checks.md), and this must never repeat that
// on the VPC this suite depends on for every future run.
func deleteLiveSubnetOnly(ctx context.Context, t *testing.T, client *network.Client, vpcID, subnetID string) {
	t.Helper()

	subnet, err := client.GetSubnet(ctx, &network.GetSubnetInput{VPCID: vpcID, SubnetID: subnetID})
	switch {
	case err == nil:
		if aclID := subnet.Subnet.InterfaceACLPolicyUUID; aclID != "" {
			if _, err := retryACLBusy(ctx, t, func() (*network.DisassociateNetworkACLSubnetOutput, error) {
				return client.DisassociateNetworkACLSubnet(ctx, &network.DisassociateNetworkACLSubnetInput{
					NetworkACLID: aclID, SubnetID: subnetID,
				})
			}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: disassociate reused-VPC subnet from its network ACL: %s", safeErr(err))
			}
		}
	case vngcloud.IsNotFound(err):
		return
	default:
		t.Errorf("cleanup: get reused-VPC subnet before delete: %s", safeErr(err))
	}

	if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{
		VPCID: vpcID, SubnetID: subnetID, NoWait: true,
	}); err != nil && !vngcloud.IsNotFound(err) {
		t.Errorf("cleanup: delete reused-VPC subnet: %s", safeErr(err))
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

	// Step 1: delete every leftover vngcloud-live-* VPC from a previous run,
	// skipping one a network ACL still holds or with Private DNS on (see
	// leftoverVPCsToDelete): the account can hold a permanently stuck VPC
	// like that, and this test must not fail on its delete.
	leftovers, err := listAllVPCs(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListVPCs: %s", safeErr(err))
	}
	var candidates []network.VPC
	for _, leftover := range leftovers {
		if isLiveSecurityGroupName(leftover.Name) {
			candidates = append(candidates, leftover)
		}
	}
	deletable, skippedVPCIDs := leftoverVPCsToDelete(ctx, t, client, candidates)
	deletedLeftovers := 0
	for _, leftover := range deletable {
		deleteVPCAndSubnets(ctx, t, client, leftover.UUID, nil)
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

	vpcCIDR, err := pickFreeVPCCIDR(ctx, client)
	if err != nil {
		t.Fatalf("step 3 pick a free VPC CIDR: %s", err)
	}

	start := time.Now()
	created, err := client.CreateVPC(ctx, &network.CreateVPCInput{Name: name, CIDR: vpcCIDR})
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
	// Its final count excludes skippedVPCIDs: a VPC step 1 left alone on
	// purpose is not this run's to clean up, and must not fail every later
	// run until someone deletes it by hand.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
		defer cancel()
		deleteVPCAndSubnets(cleanupCtx, t, client, vpcID, nil)
		final, err := listAllVPCs(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListVPCs: %s", safeErr(err))
			return
		}
		remaining := 0
		for _, v := range final {
			if isLiveSecurityGroupName(v.Name) && !skippedVPCIDs[v.UUID] {
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
	subnet24CIDR, err := vpcBlockCIDR(vpcCIDR, 1, 24)
	if err != nil {
		t.Fatalf("step 6 derive /24 subnet CIDR: %s", err)
	}
	subnet28CIDR, err := vpcBlockCIDR(vpcCIDR, 2, 28)
	if err != nil {
		t.Fatalf("step 6 derive /28 subnet CIDR: %s", err)
	}
	dupNameSubnetCIDR, err := vpcBlockCIDR(vpcCIDR, 3, 24)
	if err != nil {
		t.Fatalf("step 6 derive duplicate-name subnet CIDR: %s", err)
	}

	start = time.Now()
	sub24, err := client.CreateSubnet(ctx, &network.CreateSubnetInput{
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-a", CIDR: subnet24CIDR,
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
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-b", CIDR: subnet28CIDR,
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
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-c", CIDR: subnet24CIDR, NoWait: true,
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
		VPCID: vpcID, ZoneID: zoneID, Name: name + "-a", CIDR: dupNameSubnetCIDR,
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
// RemoveRoute, and DeleteRouteTable. It creates its own /24 subnet
// (createLiveVPCAndSubnet) in its own VPC, or in the VPC named by
// VNGCLOUD_LIVE_NETWORK_VPC_ID when that is set, and never sends AddRoute,
// RemoveRoute, or a route table delete to any VPC or route table but the
// ones it creates here. It never leaves a subnet or route table behind, and
// leaves its VPC behind only when that VPC is a reused, permanent one. It
// must never run at the same time as TestLiveWriteNetworkVPC or
// TestLiveWriteNetworkACL, since the account's VPC quota leaves room for
// only one VPC created fresh.
func TestLiveWriteNetworkRouteTable(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network route table write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_ROUTE_TABLE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_ROUTE_TABLE=1 to run the live network route table write test")
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

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
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

	// Step 1: create this run's own VPC and /24 subnet. createLiveVPCAndSubnet
	// registers the VPC's own t.Cleanup as soon as its id is known. No other
	// resource here can hold this VPC's subnet, so blocked is nil.
	vpcID, subnetID := createLiveVPCAndSubnet(ctx, t, client, portalClient, nil)
	t.Log("step 1: created this run's own VPC and /24 subnet")

	// Step 2: delete every leftover vngcloud-live-* route table from a
	// previous run of this same VPC. Since vpcID is freshly created above,
	// this never finds one in practice; it stays as the same defensive
	// sweep TestLiveWriteNetworkVPC's own leftover step uses. listAllRouteTables
	// pages through the whole account, which can include another concurrent
	// run's tables in a different VPC; the NetworkID check keeps this sweep
	// from ever touching one of those, including one that is currently that
	// other VPC's main table. Within this VPC, a table that is still the
	// main table with a dependent subnet, or is still named by a subnet, is
	// also left alone.
	leftovers, err := listAllRouteTables(ctx, client)
	if err != nil {
		t.Fatalf("step 2 ListRouteTables: %s", safeErr(err))
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
	t.Logf("step 2: deleted %d leftover route table(s)", deletedLeftovers)

	// Step 3: create the route table.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	start := time.Now()
	created, err := client.CreateRouteTable(ctx, &network.CreateRouteTableInput{VPCID: vpcID, Name: name})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the table may
		// still have reached the server. Find and delete it by its exact
		// name.
		deleteRouteTableByName(t, client, vpcID, name)
		t.Fatalf("step 3 CreateRouteTable: %s", safeErr(err))
	}
	routeTableID := created.RouteTable.UUID
	if routeTableID == "" {
		deleteRouteTableByName(t, client, vpcID, name)
		t.Fatal("step 3: CreateRouteTable returned an empty id; the design requires one")
	}
	t.Logf("step 3: created route table, status %s, routes %d, wait %s",
		created.RouteTable.Status, len(created.RouteTable.Routes), time.Since(start))

	// A VPC created with no main route table gets one assigned
	// automatically (confirmed live): the first table ever created in it
	// becomes its main table. A freshly created VPC has no main table
	// before this create, so becameMain there answers the design's own
	// open question of whether that first table carries any routes right
	// after becoming main; a reused VPC (VNGCLOUD_LIVE_NETWORK_VPC_ID)
	// already has one from an earlier run, so becameMain there is simply
	// false and this step's own table stays an ordinary, non-main one.
	vpcAfterCreate, err := client.GetVPC(ctx, &network.GetVPCInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("step 3 GetVPC (check main table): %s", safeErr(err))
	}
	becameMain := vpcAfterCreate.VPC.RouteTableID == routeTableID
	routesAfterMain := created.RouteTable.Routes
	if becameMain {
		if refreshed, err := client.GetRouteTable(ctx, &network.GetRouteTableInput{RouteTableID: routeTableID}); err != nil {
			t.Logf("step 3: GetRouteTable after becoming main: %s", safeErr(err))
		} else {
			routesAfterMain = refreshed.RouteTable.Routes
		}
	}
	t.Logf("step 3: new route table became the VPC's main route table: %v, routes: %d", becameMain, len(routesAfterMain))

	// Step 4: register the fallback cleanup as soon as routeTableID is
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

	// Step 5: create the same name again. Whether route table names are
	// unique per VPC or per project is not yet confirmed live, so either a
	// refusal or a second table is possible; an unexpected success is
	// cleaned up too, since it would otherwise leak a second table. NoWait
	// means the delete's own guard read may hit the new table before it is
	// readable, so a NotFound there is retried briefly rather than treated
	// as already gone.
	dup, dupErr := client.CreateRouteTable(ctx, &network.CreateRouteTableInput{VPCID: vpcID, Name: name, NoWait: true})
	if dupErr == nil {
		t.Log("step 5: creating a duplicate name succeeded")
		if dup.RouteTable.UUID != "" {
			deleteRouteTableRetryNotFound(ctx, t, client, dup.RouteTable.UUID)
		}
	} else {
		t.Logf("step 5: duplicate name refused, %s", safeErr(dupErr))
	}

	// Step 6: add a route. The target address is not known to be a live
	// interface in the VPC; whether the server requires that is exactly
	// what this step observes.
	start = time.Now()
	added, err := client.AddRoute(ctx, &network.AddRouteInput{
		RouteTableID:    routeTableID,
		DestinationCIDR: "10.251.200.0/24",
		Target:          "10.251.200.10",
	})
	if err != nil {
		t.Fatalf("step 6 AddRoute: %s", safeErr(err))
	}
	t.Logf("step 6: added route, changed %v, status %s, routes %d, wait %s",
		added.Changed, added.RouteTable.Status, len(added.RouteTable.Routes), time.Since(start))

	// Step 7: add the same route again; the design expects a no-op.
	again, err := client.AddRoute(ctx, &network.AddRouteInput{
		RouteTableID:    routeTableID,
		DestinationCIDR: "10.251.200.0/24",
		Target:          "10.251.200.10",
		NoWait:          true,
	})
	if err != nil {
		t.Fatalf("step 7 AddRoute (repeat): %s", safeErr(err))
	}
	if again.Changed {
		t.Error("step 7: repeating the same add reported Changed true, want false")
	} else {
		t.Log("step 7: repeat add was a no-op as expected")
	}

	// Step 8: add a conflicting target for the same destination; the
	// design expects a refusal naming the current target, nothing sent.
	_, conflictErr := client.AddRoute(ctx, &network.AddRouteInput{
		RouteTableID:    routeTableID,
		DestinationCIDR: "10.251.200.0/24",
		Target:          "10.251.200.99",
		NoWait:          true,
	})
	if !errors.Is(conflictErr, vngcloud.ErrInvalidInput) {
		t.Errorf("step 8: conflicting target err = %s, want ErrInvalidInput", safeErr(conflictErr))
	} else {
		t.Log("step 8: conflicting target refused as expected")
	}

	// Step 9: remove the route.
	start = time.Now()
	removed, err := client.RemoveRoute(ctx, &network.RemoveRouteInput{RouteTableID: routeTableID, DestinationCIDR: "10.251.200.0/24"})
	if err != nil {
		t.Fatalf("step 9 RemoveRoute: %s", safeErr(err))
	}
	t.Logf("step 9: removed route, changed %v, status %s, routes %d, wait %s",
		removed.Changed, removed.RouteTable.Status, len(removed.RouteTable.Routes), time.Since(start))

	// Step 10: remove it again; the design expects NotFound.
	_, removedAgainErr := client.RemoveRoute(ctx, &network.RemoveRouteInput{RouteTableID: routeTableID, DestinationCIDR: "10.251.200.0/24", NoWait: true})
	if !vngcloud.IsNotFound(removedAgainErr) {
		t.Errorf("step 10: repeat remove err = %s, want NotFound", safeErr(removedAgainErr))
	} else {
		t.Log("step 10: repeat remove returned NotFound as expected")
	}

	// Step 11: delete this run's own subnet before the route table. Step 3
	// showed whether this table became the VPC's main table; when it did,
	// this subnet relies on it implicitly (an empty routeTableUuid), which
	// would refuse the table's own delete with ErrDefaultResource.
	// DeleteSubnet waits for the subnet to leave the VPC's own subnet list.
	start = time.Now()
	if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: subnetID}); err != nil {
		t.Fatalf("step 11 DeleteSubnet: %s", safeErr(err))
	}
	t.Logf("step 11: deleted this run's subnet, wait %s", time.Since(start))

	// Step 12: delete the table explicitly.
	start = time.Now()
	if _, err := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: routeTableID}); err != nil {
		t.Fatalf("step 12 DeleteRouteTable: %s", safeErr(err))
	}
	t.Logf("step 12: deleted route table, wait %s", time.Since(start))

	// Step 13: repeat delete; the design expects NotFound, since the
	// delete's own guard reads run first.
	_, repeatErr := client.DeleteRouteTable(ctx, &network.DeleteRouteTableInput{RouteTableID: routeTableID, NoWait: true})
	if !vngcloud.IsNotFound(repeatErr) {
		t.Errorf("step 13: repeat delete err = %s, want NotFound", safeErr(repeatErr))
	} else {
		t.Log("step 13: repeat delete returned NotFound as expected")
	}
}

// isLiveNetworkACLName reports whether name is one this test's own runs
// create.
func isLiveNetworkACLName(name string) bool {
	return strings.HasPrefix(name, "vngcloud-live-")
}

// isLiveDefaultACLRule mirrors the network package's own default-rule
// marker (a Priority of 2000 or above, one past
// network.checkACLRulePriority's 1999-priority bound, or the decoded
// System flag), which is unexported and so cannot be called directly from
// this package. Confirmed live, a new ACL's own pass-all rules at Priority
// 0 are not caught by this marker: they are ordinary, removable rules.
func isLiveDefaultACLRule(rule network.ACLRule) bool {
	return rule.Priority >= 2000 || rule.System
}

// findACLRule returns the rule in rules matching direction (case-sensitive,
// since every direction this test sends is already lowercase) and
// priority, for logging a just-added rule's stored fields.
func findACLRule(rules []network.ACLRule, direction string, priority int) (network.ACLRule, bool) {
	for _, rule := range rules {
		if rule.Direction == direction && rule.Priority == priority {
			return rule, true
		}
	}
	return network.ACLRule{}, false
}

// listAllNetworkACLs pages through every network ACL the account has.
func listAllNetworkACLs(ctx context.Context, client *network.Client) ([]network.ACL, error) {
	var all []network.ACL
	for page := 1; ; page++ {
		out, err := client.ListNetworkACLs(ctx, &network.ListNetworkACLsInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// aclBusyRetryInterval and aclBusyRetryBound bound retryACLBusy: the design
// confirms an ACL write can leave the ACL busy for up to about 20 seconds,
// during which the very next write to it returns network.ErrBusy, so every
// ACL write step in this file retries on that error rather than treating it
// as a failure.
const (
	aclBusyRetryInterval = 10 * time.Second
	aclBusyRetryBound    = 2 * time.Minute
)

// retryACLBusy calls fn, which performs one ACL write and returns its usual
// output and error, retrying every aclBusyRetryInterval for up to
// aclBusyRetryBound while the error wraps network.ErrBusy. network.ErrBusy
// always means fn's own write sent nothing, so retrying it is safe; any
// other error, nil included, returns at once. A live run once let a
// network.ErrBusy from one write pass through as a hard failure, which
// then cascaded into cleanup deleting a subnet still held by the ACL;
// every ACL write in this file goes through this helper instead.
func retryACLBusy[T any](ctx context.Context, t *testing.T, fn func() (T, error)) (T, error) {
	t.Helper()
	deadline := time.Now().Add(aclBusyRetryBound)
	for {
		out, err := fn()
		if !errors.Is(err, network.ErrBusy) {
			return out, err
		}
		if !time.Now().Before(deadline) {
			return out, err
		}
		select {
		case <-ctx.Done():
			return out, err
		case <-time.After(aclBusyRetryInterval):
		}
	}
}

// disassociateAllNetworkACLSubnets removes every subnet aclID's own read
// still lists, retrying each disassociate with retryACLBusy, since the ACL
// can still be settling an earlier write. It logs but does not fail the
// test on a disassociate error, and does nothing when the ACL is already
// gone. It returns the subnet ids it could not remove even after retrying
// (stuck: the caller decides whether leaving one behind is fatal, since
// deleting that subnet or its VPC out from under a still-associated ACL is
// what stranded an ACL on a past live run) and whether any disassociate
// actually changed the ACL (changed): a delete sent right after one gets a
// 500 and changes nothing (see aclCleanupWaitStep), so a caller about to
// delete aclID waits first only when this is true.
func disassociateAllNetworkACLSubnets(ctx context.Context, t *testing.T, client *network.Client, aclID string) (stuck []string, changed bool) {
	t.Helper()
	acl, err := client.GetNetworkACL(ctx, &network.GetNetworkACLInput{NetworkACLID: aclID})
	if err != nil {
		if !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: get network ACL before disassociate: %s", safeErr(err))
		}
		return nil, false
	}
	for _, subnetID := range acl.ACL.SubnetIDs {
		subnetID := subnetID
		out, err := retryACLBusy(ctx, t, func() (*network.DisassociateNetworkACLSubnetOutput, error) {
			return client.DisassociateNetworkACLSubnet(ctx, &network.DisassociateNetworkACLSubnetInput{NetworkACLID: aclID, SubnetID: subnetID})
		})
		if err != nil {
			t.Errorf("cleanup: disassociate subnet: %s", safeErr(err))
			stuck = append(stuck, subnetID)
			continue
		}
		if out.Changed {
			changed = true
		}
	}
	return stuck, changed
}

// aclCleanupWaitStep is how long this file's cleanup helpers wait, both
// before a network ACL delete that follows a disassociate which actually
// changed the ACL, and between each retry deleteNetworkACLRetrying makes
// after a plain HTTP 500: confirmed live, a subnets write (associate or
// disassociate) leaves the ACL busy for about this long, with no change in
// its own Status to mark the window, and a DELETE sent inside it answers
// 500 and changes nothing rather than the 400 any other write gets.
const aclCleanupWaitStep = 30 * time.Second

// aclDeleteRetries is how many extra attempts deleteNetworkACLRetrying makes
// after DeleteNetworkACL answers a plain HTTP 500, aclCleanupWaitStep apart.
// Waiting out the busy window above, more than once if needed, is what
// tells that transient 500 apart from the account's one network ACL whose
// own DELETE always 500s no matter how long a caller waits (see
// deleteNetworkACLLeftover); only exhausting every retry counts an ACL as
// that kind of permanently stuck one.
const aclDeleteRetries = 3

// waitACLCleanupStep waits aclCleanupWaitStep and reports true, or returns
// false at once if ctx ends first.
func waitACLCleanupStep(ctx context.Context, t *testing.T) bool {
	t.Helper()
	select {
	case <-ctx.Done():
		return false
	case <-time.After(aclCleanupWaitStep):
		return true
	}
}

// deleteNetworkACLRetrying calls DeleteNetworkACL through retryACLBusy and,
// when that still fails with a plain HTTP 500, retries the whole call up to
// aclDeleteRetries more times, aclCleanupWaitStep apart. DeleteNetworkACL
// always reads the ACL again before it sends anything, so retrying after
// the busy window passes, or after an earlier attempt's own list confirm
// already deleted it, is always safe. Any other error, nil included,
// returns at once; a ctx that ends during a wait also returns at once, with
// that last 500.
func deleteNetworkACLRetrying(ctx context.Context, t *testing.T, client *network.Client, aclID string) error {
	t.Helper()
	var err error
	for attempt := 0; ; attempt++ {
		_, err = retryACLBusy(ctx, t, func() (*network.DeleteNetworkACLOutput, error) {
			return client.DeleteNetworkACL(ctx, &network.DeleteNetworkACLInput{NetworkACLID: aclID})
		})
		var apiErr *vngcloud.APIError
		if err == nil || !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError {
			return err
		}
		if attempt >= aclDeleteRetries || !waitACLCleanupStep(ctx, t) {
			return err
		}
	}
}

// deleteNetworkACLLeftover disassociates every subnet a leftover
// vngcloud-live-* network ACL still holds, waits out the ACL's own busy
// window when that changed anything, then tries to delete it through
// deleteNetworkACLRetrying, and reports whether that delete succeeded
// (deleted) and, if not, whether the ACL is the account's own permanently
// stuck one (stuck): the account holds one network ACL whose subnets can be
// disassociated but whose own DELETE always answers with the server's own
// 500, even after every retry (see vserver-network-writes-checks.md). A
// leftover sweep that ran into it before this treated a single 500 like any
// other failure and kept failing the whole test on every future run once
// its VPC became the account's last one and had to be reused; this instead
// counts an ACL as stuck only once deleteNetworkACLRetrying's own retries
// are exhausted, and leaves it alone. It ignores ErrDefaultResource and
// NotFound: a default ACL, or one already gone, is left alone too, and
// neither counts as stuck.
func deleteNetworkACLLeftover(ctx context.Context, t *testing.T, client *network.Client, aclID string) (deleted, stuck bool) {
	t.Helper()
	_, changed := disassociateAllNetworkACLSubnets(ctx, t, client, aclID)
	if changed {
		waitACLCleanupStep(ctx, t)
	}
	err := deleteNetworkACLRetrying(ctx, t, client, aclID)
	switch {
	case err == nil:
		return true, false
	case errors.Is(err, network.ErrDefaultResource), vngcloud.IsNotFound(err):
		return false, false
	default:
		var apiErr *vngcloud.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusInternalServerError {
			return false, true
		}
		t.Errorf("cleanup: delete leftover network ACL: %s", safeErr(err))
		return false, false
	}
}

// deleteNetworkACLByName finds a network ACL by its exact name and deletes
// it, disassociating any subnet it still holds first: the recovery
// TestLiveWriteNetworkACL takes after a create whose own response never
// arrived, since the POST is not resent and the ACL may still exist under
// the name it was given.
func deleteNetworkACLByName(t *testing.T, client *network.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	list, err := listAllNetworkACLs(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list network ACLs by name: %s", safeErr(err))
		return
	}
	for _, acl := range list {
		if acl.Name != name {
			continue
		}
		aclID := acl.UUID
		disassociateAllNetworkACLSubnets(ctx, t, client, aclID)
		if _, err := retryACLBusy(ctx, t, func() (*network.DeleteNetworkACLOutput, error) {
			return client.DeleteNetworkACL(ctx, &network.DeleteNetworkACLInput{NetworkACLID: aclID})
		}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete network ACL by name: %s", safeErr(err))
		}
	}
}

// TestLiveWriteNetworkACL exercises network ACL, rule, and subnet
// association writes against the real account named in .env:
// CreateNetworkACL, GetNetworkACL, AddNetworkACLRule, RemoveNetworkACLRule,
// AssociateNetworkACLSubnet, DisassociateNetworkACLSubnet, and
// DeleteNetworkACL. It creates its own /24 subnet (createLiveVPCAndSubnet)
// in its own VPC, or in the VPC named by VNGCLOUD_LIVE_NETWORK_VPC_ID when
// that is set, and only ever associates that subnet with the ACL it
// creates here; every rule it adds sources traffic only from
// 203.0.113.0/24. It never leaves a subnet or network ACL behind, and
// leaves its VPC behind only when that VPC is a reused, permanent one. It
// must never run at the same time as TestLiveWriteNetworkVPC or
// TestLiveWriteNetworkRouteTable, since the account's VPC quota leaves
// room for only one VPC created fresh.
//
// Every write step goes through retryACLBusy, since the account leaves an
// ACL busy for up to about 20 seconds after a rules or subnets write
// settles, and that window does not always show in the ACL's own status
// (see network.AssociateNetworkACLSubnet). If step 4's own cleanup still
// finds this run's subnet associated after retrying the disassociate, it
// stops short of deleting the ACL, and aclHoldsSubnet then stops the VPC's
// own cleanup from deleting the subnet or the VPC out from under it,
// failing the test loudly instead so the ACL can be cleaned up by hand.
func TestLiveWriteNetworkACL(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network ACL write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_ACL") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_ACL=1 to run the live network ACL write test")
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

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
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

	// Step 1: create this run's own VPC and /24 subnet. createLiveVPCAndSubnet
	// registers the VPC's own t.Cleanup as soon as its id is known; this
	// test never associates or disassociates any subnet but the one
	// returned here.
	//
	// vpcID, subnetID, and aclID are declared here, before any has a value,
	// so aclHoldsSubnet below can close over them and be passed into
	// createLiveVPCAndSubnet in the same call that sets vpcID and subnetID:
	// aclID is set in step 3, and aclStuck only if step 4's own cleanup
	// finds the ACL still lists this subnet after disassociating with
	// retries. aclHoldsSubnet is never called until every t.Cleanup ahead of
	// the VPC's own runs first (t.Cleanup runs last-registered-first, and
	// step 4 registers after this call does), so all three are always final
	// by the time it matters.
	var vpcID, subnetID, aclID string
	var aclStuck atomic.Bool
	aclHoldsSubnet := func() string {
		if !aclStuck.Load() {
			return ""
		}
		return fmt.Sprintf("network ACL %s still lists this run's subnet %s", aclID, subnetID)
	}
	vpcID, subnetID = createLiveVPCAndSubnet(ctx, t, client, portalClient, aclHoldsSubnet)
	t.Log("step 1: created this run's own VPC and /24 subnet")

	// Step 2: delete every leftover vngcloud-live-* network ACL from a
	// previous run that belongs to this run's own VPC, disassociating any
	// subnet it still holds first. With a freshly created VPC this never
	// finds one in practice; with a reused VPC (VNGCLOUD_LIVE_NETWORK_VPC_ID)
	// it also finds the account's one permanently stuck ACL, whose own
	// DELETE always 500s even after deleteNetworkACLRetrying's own retries;
	// that one is counted and left alone rather than failing the test. A
	// leftover in a different VPC is left alone too: this test's disassociate
	// and delete calls must never touch an ACL outside the VPC it was told to
	// use. knownStuckLeftoverACLs records every id found stuck here, by id,
	// so step 4's own cleanup never repeats a hopeless retry sequence against
	// the same permanently stuck ACL a second time.
	leftovers, err := listAllNetworkACLs(ctx, client)
	if err != nil {
		t.Fatalf("step 2 ListNetworkACLs: %s", safeErr(err))
	}
	deletedLeftovers, stuckLeftovers := 0, 0
	knownStuckLeftoverACLs := make(map[string]bool)
	for _, leftover := range leftovers {
		if !isLiveNetworkACLName(leftover.Name) {
			continue
		}
		if leftover.NetworkID != vpcID {
			t.Logf("step 2: skipping leftover network ACL %s: NetworkID %s does not match this run's VPC %s",
				leftover.UUID, leftover.NetworkID, vpcID)
			continue
		}
		deleted, stuck := deleteNetworkACLLeftover(ctx, t, client, leftover.UUID)
		if deleted {
			deletedLeftovers++
		}
		if stuck {
			stuckLeftovers++
			knownStuckLeftoverACLs[leftover.UUID] = true
		}
	}
	t.Logf("step 2: deleted %d leftover network ACL(s), %d permanently stuck", deletedLeftovers, stuckLeftovers)

	// Step 3: create the ACL.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	start := time.Now()
	created, err := client.CreateNetworkACL(ctx, &network.CreateNetworkACLInput{VPCID: vpcID, Name: name})
	if err != nil {
		// A POST is not retried after an ambiguous failure, so the ACL may
		// still have reached the server. Find and delete it by its exact
		// name.
		deleteNetworkACLByName(t, client, name)
		t.Fatalf("step 3 CreateNetworkACL: %s", safeErr(err))
	}
	aclID = created.ACL.UUID
	if aclID == "" {
		deleteNetworkACLByName(t, client, name)
		t.Fatal("step 3: CreateNetworkACL returned an empty id; the design requires one")
	}
	t.Logf("step 3: created network ACL, status %s, default %v, wait %s",
		created.ACL.Status, created.ACL.DefaultACL, time.Since(start))

	// Step 4: register the fallback cleanup as soon as aclID is known,
	// before any later step can fail and skip the explicit delete below.
	// disassociateAllNetworkACLSubnets already retries each disassociate on
	// ErrBusy; if the ACL still lists this run's own subnet afterward,
	// aclStuck is set and this returns at once, deleting neither the ACL nor
	// the subnet, so the VPC's own cleanup (registered before this one, and
	// so run after it) sees aclHoldsSubnet report the reason and leaves the
	// subnet and VPC alone too, instead of deleting a subnet still held by a
	// stuck ACL as a past live run did.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		stuck, changed := disassociateAllNetworkACLSubnets(cleanupCtx, t, client, aclID)
		if slices.Contains(stuck, subnetID) {
			aclStuck.Store(true)
			t.Errorf("cleanup: network ACL %s still lists this run's subnet %s after busy retries; leaving the ACL, subnet, and VPC for manual cleanup",
				aclID, subnetID)
			return
		}
		if changed {
			waitACLCleanupStep(cleanupCtx, t)
		}
		if err := deleteNetworkACLRetrying(cleanupCtx, t, client, aclID); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete network ACL: %s", safeErr(err))
		}
		final, err := listAllNetworkACLs(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final ListNetworkACLs: %s", safeErr(err))
			return
		}
		remaining, stuckRemaining := 0, 0
		for _, acl := range final {
			if !isLiveNetworkACLName(acl.Name) {
				continue
			}
			if acl.UUID == aclID {
				// The delete above should have removed this run's own ACL;
				// still present means it failed for a reason other than
				// NotFound, so it counts as an unexplained leftover.
				remaining++
				continue
			}
			// A same-named ACL from another run, most often the account's
			// own permanently stuck network ACL when this test reused an
			// existing VPC (VNGCLOUD_LIVE_NETWORK_VPC_ID). The list's own
			// NetworkID is not reliable (confirmed live: it can come back
			// empty for an ACL that does belong to a VPC), so this reads
			// the ACL's own detail and only tries it, and counts it toward
			// this run's tally, when its VPCID matches vpcID; a read
			// failure fails closed, leaving it alone rather than guessing.
			// An ACL confirmed to belong elsewhere is left untouched, the
			// same principle step 2 already follows: this run's cleanup
			// must never disassociate or delete an ACL outside its own VPC.
			detail, err := client.GetNetworkACL(cleanupCtx, &network.GetNetworkACLInput{NetworkACLID: acl.UUID})
			if err != nil {
				if !vngcloud.IsNotFound(err) {
					t.Errorf("cleanup: get network ACL %s to check its VPC: %s", acl.UUID, safeErr(err))
				}
				continue
			}
			if detail.ACL.VPCID != vpcID {
				t.Logf("cleanup: skipping same-named network ACL %s: VPC %s does not match this run's VPC %s",
					acl.UUID, detail.ACL.VPCID, vpcID)
				continue
			}
			if knownStuckLeftoverACLs[acl.UUID] {
				// Step 2 already ran this id through deleteNetworkACLRetrying
				// and confirmed it permanently stuck; a second full retry
				// sequence against the same dead end only spends more time for
				// the same answer.
				t.Logf("cleanup: network ACL %s was already confirmed permanently stuck in step 2; leaving it alone", acl.UUID)
				remaining++
				stuckRemaining++
				continue
			}
			if deleted, stuck := deleteNetworkACLLeftover(cleanupCtx, t, client, acl.UUID); !deleted {
				remaining++
				if stuck {
					stuckRemaining++
				}
			}
		}
		t.Logf("cleanup: vngcloud-live network ACL(s) remaining: %d (%d permanently stuck)", remaining, stuckRemaining)
		if remaining != stuckRemaining {
			t.Errorf("cleanup: expected only permanently stuck vngcloud-live network ACLs to remain, found %d, %d stuck",
				remaining, stuckRemaining)
		}
	})

	// Step 5: read the ACL back. The full default rule list, and whether a
	// default rule outside seqNumber 0 or the system flag's live value
	// exist, are still a live check; this step is the observation for it.
	// None of the fields logged here are secrets.
	afterCreate, err := client.GetNetworkACL(ctx, &network.GetNetworkACLInput{NetworkACLID: aclID})
	if err != nil {
		t.Fatalf("step 5 GetNetworkACL: %s", safeErr(err))
	}
	defaultRuleCount := 0
	for _, rule := range afterCreate.ACL.Rules {
		if !isLiveDefaultACLRule(rule) {
			continue
		}
		defaultRuleCount++
		t.Logf("step 5: default rule: type %s, seqNumber %d, port %s, action %s, system %v",
			rule.Direction, rule.Priority, rule.Port, rule.Action, rule.System)
	}
	t.Logf("step 5: read network ACL, total rules %d, default rules %d, associated subnets %d",
		len(afterCreate.ACL.Rules), defaultRuleCount, len(afterCreate.ACL.SubnetIDs))

	// Step 6: add an inbound tcp rule for port 443 from 203.0.113.0/24.
	// Priority stays within 1 to 1999, the user range confirmed live;
	// Protocol is sent in the server's own accepted spelling.
	start = time.Now()
	tcpRule, err := retryACLBusy(ctx, t, func() (*network.AddNetworkACLRuleOutput, error) {
		return client.AddNetworkACLRule(ctx, &network.AddNetworkACLRuleInput{
			NetworkACLID: aclID, Direction: "inbound", Priority: 100, Protocol: "tcp",
			CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443,
		})
	})
	if err != nil {
		t.Fatalf("step 6 AddNetworkACLRule (tcp): %s", safeErr(err))
	}
	t.Logf("step 6: added tcp rule, changed %v, status %s, total rules %d, wait %s",
		tcpRule.Changed, tcpRule.ACL.Status, len(tcpRule.ACL.Rules), time.Since(start))
	if rule, ok := findACLRule(tcpRule.ACL.Rules, "inbound", 100); ok {
		t.Logf("step 6: stored port for the tcp rule: %q", rule.Port)
	}

	// Step 7: add an ANY rule and an icmp rule, to observe how each stores
	// port. ANY and icmp each require the full port range, PortRangeMin 0
	// and PortRangeMax 65535 (icmp also accepts 0 and 0 together); leaving
	// both at their zero value is refused for either protocol.
	anyRule, err := retryACLBusy(ctx, t, func() (*network.AddNetworkACLRuleOutput, error) {
		return client.AddNetworkACLRule(ctx, &network.AddNetworkACLRuleInput{
			NetworkACLID: aclID, Direction: "inbound", Priority: 101, Protocol: "ANY",
			CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 0, PortRangeMax: 65535,
		})
	})
	if err != nil {
		t.Fatalf("step 7 AddNetworkACLRule (ANY): %s", safeErr(err))
	}
	t.Logf("step 7: added ANY rule, changed %v, total rules %d", anyRule.Changed, len(anyRule.ACL.Rules))
	if rule, ok := findACLRule(anyRule.ACL.Rules, "inbound", 101); ok {
		t.Logf("step 7: stored port for the ANY rule: %q", rule.Port)
	}

	icmpRule, err := retryACLBusy(ctx, t, func() (*network.AddNetworkACLRuleOutput, error) {
		return client.AddNetworkACLRule(ctx, &network.AddNetworkACLRuleInput{
			NetworkACLID: aclID, Direction: "inbound", Priority: 102, Protocol: "icmp",
			CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 0, PortRangeMax: 65535,
		})
	})
	if err != nil {
		t.Fatalf("step 7 AddNetworkACLRule (icmp): %s", safeErr(err))
	}
	t.Logf("step 7: added icmp rule, changed %v, total rules %d", icmpRule.Changed, len(icmpRule.ACL.Rules))
	if rule, ok := findACLRule(icmpRule.ACL.Rules, "inbound", 102); ok {
		t.Logf("step 7: stored port for the icmp rule: %q", rule.Port)
	}

	// Step 8: add a rule at a priority already used, with a different
	// protocol; the design expects a refusal, nothing sent.
	_, conflictErr := client.AddNetworkACLRule(ctx, &network.AddNetworkACLRuleInput{
		NetworkACLID: aclID, Direction: "inbound", Priority: 100, Protocol: "udp",
		CIDR: "203.0.113.0/24", Action: "pass", PortRangeMin: 443, NoWait: true,
	})
	if !errors.Is(conflictErr, vngcloud.ErrInvalidInput) {
		t.Errorf("step 8: conflicting priority err = %s, want ErrInvalidInput", safeErr(conflictErr))
	} else {
		t.Log("step 8: conflicting priority refused as expected")
	}

	// Step 9: remove the three rules just added.
	for _, priority := range []int{100, 101, 102} {
		priority := priority
		start = time.Now()
		removed, err := retryACLBusy(ctx, t, func() (*network.RemoveNetworkACLRuleOutput, error) {
			return client.RemoveNetworkACLRule(ctx, &network.RemoveNetworkACLRuleInput{NetworkACLID: aclID, Direction: "inbound", Priority: priority})
		})
		if err != nil {
			t.Fatalf("step 9 RemoveNetworkACLRule (priority %d): %s", priority, safeErr(err))
		}
		t.Logf("step 9: removed rule at priority %d, changed %v, total rules %d, wait %s",
			priority, removed.Changed, len(removed.ACL.Rules), time.Since(start))
	}

	// Step 10: remove one again; the design expects NotFound.
	_, removedAgainErr := client.RemoveNetworkACLRule(ctx, &network.RemoveNetworkACLRuleInput{NetworkACLID: aclID, Direction: "inbound", Priority: 100, NoWait: true})
	if !vngcloud.IsNotFound(removedAgainErr) {
		t.Errorf("step 10: repeat remove err = %s, want NotFound", safeErr(removedAgainErr))
	} else {
		t.Log("step 10: repeat remove returned NotFound as expected")
	}

	// Step 11: remove the inbound priority-0 pass-all rule. Confirmed live,
	// it is an ordinary rule, not a default one, so this must succeed; a
	// fresh read (returned on RemoveNetworkACLRule itself) then confirms
	// it is gone.
	removedPassAll, err := retryACLBusy(ctx, t, func() (*network.RemoveNetworkACLRuleOutput, error) {
		return client.RemoveNetworkACLRule(ctx, &network.RemoveNetworkACLRuleInput{NetworkACLID: aclID, Direction: "inbound", Priority: 0})
	})
	if err != nil {
		t.Fatalf("step 11 RemoveNetworkACLRule (priority-0 pass-all): %s", safeErr(err))
	}
	if !removedPassAll.Changed {
		t.Error("step 11: Changed = false, want true: the priority-0 pass-all rule must be removable")
	}
	if _, ok := findACLRule(removedPassAll.ACL.Rules, "inbound", 0); ok {
		t.Error("step 11: the priority-0 pass-all rule is still present after removal")
	} else {
		t.Logf("step 11: removed the inbound priority-0 pass-all rule, total rules %d", len(removedPassAll.ACL.Rules))
	}

	// Step 12: confirm the priority-2000 deny-all rules cannot be removed:
	// the design treats a Priority of 2000 or above as a default rule the
	// server protects, so each direction's own remove must fail with
	// ErrDefaultResource and send nothing.
	for _, direction := range []string{"inbound", "outbound"} {
		_, denyErr := client.RemoveNetworkACLRule(ctx, &network.RemoveNetworkACLRuleInput{NetworkACLID: aclID, Direction: direction, Priority: 2000, NoWait: true})
		if !errors.Is(denyErr, network.ErrDefaultResource) {
			t.Errorf("step 12: remove %s priority-2000 rule err = %s, want ErrDefaultResource", direction, safeErr(denyErr))
		} else {
			t.Logf("step 12: %s priority-2000 rule remove refused as expected", direction)
		}
	}

	// Step 13: associate this run's own subnet with the ACL, wait out its
	// busy window, associate it again, and try to delete the ACL while it
	// remains associated. Associating a subnet can cut that subnet's traffic
	// at once, so this never associates any subnet but the one created in
	// step 1.
	start = time.Now()
	associated, err := retryACLBusy(ctx, t, func() (*network.AssociateNetworkACLSubnetOutput, error) {
		return client.AssociateNetworkACLSubnet(ctx, &network.AssociateNetworkACLSubnetInput{NetworkACLID: aclID, SubnetID: subnetID})
	})
	if err != nil {
		t.Fatalf("step 13a AssociateNetworkACLSubnet: %s", safeErr(err))
	}
	if !associated.Changed {
		t.Error("step 13a: Changed = false, want true: associating a subnet not yet in the ACL must change it")
	}
	if !slices.Contains(associated.ACL.SubnetIDs, subnetID) {
		t.Error("step 13a: the ACL's own subnetAssociationList does not name the subnet just associated")
	}
	t.Logf("step 13a: associated the subnet, status %s, associated subnets %d, wait %s",
		associated.ACL.Status, len(associated.ACL.SubnetIDs), time.Since(start))
	subnetAfterAssociate, err := client.GetSubnet(ctx, &network.GetSubnetInput{VPCID: vpcID, SubnetID: subnetID})
	if err != nil {
		t.Fatalf("step 13a GetSubnet: %s", safeErr(err))
	}
	// Which subnet field, if any, names the associated ACL is unconfirmed
	// live (InterfaceACLPolicyUUID has been seen empty here); log every
	// candidate rather than asserting one.
	t.Logf("step 13a: subnet ACL fields after associate: interfaceAclPolicyId %q, interfaceAclPolicyUuid %q, interfaceAclPolicyName %q",
		subnetAfterAssociate.Subnet.InterfaceACLPolicyID,
		subnetAfterAssociate.Subnet.InterfaceACLPolicyUUID,
		subnetAfterAssociate.Subnet.InterfaceACLPolicyName)

	// Step 13b: wait out the associate's own busy window before this ACL
	// gets any other write. Confirmed live, a subnets write leaves the ACL
	// busy for about 20 more seconds without ever changing Status, so
	// nothing in a read marks when it ends; retryACLBusy would retry a
	// write sent too soon, but waiting the full window first keeps the
	// remaining steps' own retry counts meaningful.
	select {
	case <-ctx.Done():
		t.Fatalf("step 13b: context ended while waiting out the ACL's busy window: %s", safeErr(ctx.Err()))
	case <-time.After(30 * time.Second):
	}

	againAssociated, err := retryACLBusy(ctx, t, func() (*network.AssociateNetworkACLSubnetOutput, error) {
		return client.AssociateNetworkACLSubnet(ctx, &network.AssociateNetworkACLSubnetInput{NetworkACLID: aclID, SubnetID: subnetID})
	})
	if err != nil {
		t.Fatalf("step 13c AssociateNetworkACLSubnet (repeat): %s", safeErr(err))
	}
	if againAssociated.Changed {
		t.Error("step 13c: repeat associate reported Changed true, want false: the subnet is already in this ACL")
	} else {
		t.Log("step 13c: repeat associate was a no-op as expected")
	}

	_, deleteWhileAssociatedErr := client.DeleteNetworkACL(ctx, &network.DeleteNetworkACLInput{NetworkACLID: aclID})
	if deleteWhileAssociatedErr == nil {
		t.Fatal("step 13d: DeleteNetworkACL while a subnet is associated succeeded; the design expects a refusal")
	}
	if errors.Is(deleteWhileAssociatedErr, network.ErrInUse) {
		t.Logf("step 13d: delete while associated refused with the SDK's own ErrInUse, as expected")
	} else {
		t.Logf("step 13d: delete while associated refused by the server instead of the SDK's own guard: %s",
			safeErr(deleteWhileAssociatedErr))
	}
	stillAssociated, err := client.GetNetworkACL(ctx, &network.GetNetworkACLInput{NetworkACLID: aclID})
	if err != nil {
		t.Fatalf("step 13d GetNetworkACL after refused delete: %s", safeErr(err))
	}
	if !slices.Contains(stillAssociated.ACL.SubnetIDs, subnetID) {
		t.Error("step 13d: the ACL no longer lists the associated subnet after a refused delete; it may have been deleted")
	}

	// Step 14: disassociate the subnet, then check its own ACL fields
	// afterward. What a subnet falls back to once disassociated is not yet
	// confirmed live, so this only logs the fields rather than asserting a
	// value for them.
	start = time.Now()
	disassociated, err := retryACLBusy(ctx, t, func() (*network.DisassociateNetworkACLSubnetOutput, error) {
		return client.DisassociateNetworkACLSubnet(ctx, &network.DisassociateNetworkACLSubnetInput{NetworkACLID: aclID, SubnetID: subnetID})
	})
	if err != nil {
		t.Fatalf("step 14a DisassociateNetworkACLSubnet: %s", safeErr(err))
	}
	if !disassociated.Changed {
		t.Error("step 14a: Changed = false, want true: disassociating an associated subnet must change it")
	}
	if slices.Contains(disassociated.ACL.SubnetIDs, subnetID) {
		t.Error("step 14a: the ACL still lists the subnet after disassociating it")
	}
	t.Logf("step 14a: disassociated the subnet, status %s, associated subnets %d, wait %s",
		disassociated.ACL.Status, len(disassociated.ACL.SubnetIDs), time.Since(start))

	subnetAfterDisassociate, err := client.GetSubnet(ctx, &network.GetSubnetInput{VPCID: vpcID, SubnetID: subnetID})
	if err != nil {
		t.Fatalf("step 14b GetSubnet: %s", safeErr(err))
	}
	t.Logf("step 14b: subnet ACL fields after disassociate: interfaceAclPolicyId %q, interfaceAclPolicyUuid %q, interfaceAclPolicyName %q",
		subnetAfterDisassociate.Subnet.InterfaceACLPolicyID,
		subnetAfterDisassociate.Subnet.InterfaceACLPolicyUUID,
		subnetAfterDisassociate.Subnet.InterfaceACLPolicyName)

	// Step 14c: wait out the disassociate's own busy window before deleting
	// the ACL, the same 30 seconds step 13b waits before the repeat
	// associate. Confirmed live, a DELETE sent inside that window answers
	// 500 and changes nothing, unlike the 400 "is being updated" any other
	// write gets in it; without this wait, step 15 could exercise that busy
	// case by accident instead of an ordinary delete.
	select {
	case <-ctx.Done():
		t.Fatalf("step 14c: context ended while waiting out the ACL's busy window: %s", safeErr(ctx.Err()))
	case <-time.After(30 * time.Second):
	}

	// Step 15: delete the ACL explicitly.
	start = time.Now()
	if _, err := retryACLBusy(ctx, t, func() (*network.DeleteNetworkACLOutput, error) {
		return client.DeleteNetworkACL(ctx, &network.DeleteNetworkACLInput{NetworkACLID: aclID})
	}); err != nil {
		t.Fatalf("step 15 DeleteNetworkACL: %s", safeErr(err))
	}
	t.Logf("step 15: deleted network ACL, wait %s", time.Since(start))

	// Step 16: repeat delete; the design expects NotFound, through the list
	// confirm after the server's 500, since the delete's own guard reads
	// run first.
	_, repeatErr := client.DeleteNetworkACL(ctx, &network.DeleteNetworkACLInput{NetworkACLID: aclID})
	if !vngcloud.IsNotFound(repeatErr) {
		t.Errorf("step 16: repeat delete err = %s, want NotFound", safeErr(repeatErr))
	} else {
		t.Log("step 16: repeat delete returned NotFound as expected")
	}
}

// liveDHCPOptionsResolversFor returns region's documented default DNS
// resolvers, named in the design and its product docs: HCM's pair for
// hcm-3, HAN's for han-1. CreateDHCPOptions never adds them on its own, so
// the live check supplies them explicitly, as the manager's own probe did.
func liveDHCPOptionsResolversFor(region string) []string {
	if strings.HasPrefix(region, "han") {
		return []string{"10.236.10.196", "10.236.10.197"}
	}
	return []string{"10.166.12.196", "10.166.12.197"}
}

// listAllDHCPOptions pages through every DHCP options set the account has,
// since a leftover cleanup or a remaining-set check must not miss one that
// landed past the first page.
func listAllDHCPOptions(ctx context.Context, client *network.Client) ([]network.DHCPOptions, error) {
	var all []network.DHCPOptions
	for page := 1; ; page++ {
		out, err := client.ListDHCPOptions(ctx, &network.ListDHCPOptionsInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// deleteLiveDHCPOptionsSet deletes id, tolerating NotFound (already gone),
// and reports deleted true only when the delete itself succeeded: a caller
// counting how many leftovers it actually removed must not count NotFound
// or a skip as a delete. A set DeleteDHCPOptions itself reports as still
// attached to a VPC (ErrInUse) is left alone rather than forced:
// TestLiveWriteNetworkDHCPOptions registers this cleanup for each set it
// creates before it registers the run's own VPC cleanup, so t.Cleanup's
// LIFO order runs the VPC's delete first and frees every set the VPC held;
// an attachment found here means an earlier step left the set attached some
// other way, and the set is left for a later run's own leftover sweep
// rather than risking a delete this design never allows.
func deleteLiveDHCPOptionsSet(ctx context.Context, t *testing.T, client *network.Client, id string) (deleted bool) {
	t.Helper()
	_, err := client.DeleteDHCPOptions(ctx, &network.DeleteDHCPOptionsInput{DHCPOptionsID: id})
	switch {
	case err == nil:
		return true
	case vngcloud.IsNotFound(err):
	case errors.Is(err, network.ErrInUse):
		t.Log("cleanup: a DHCP options set is still attached to a VPC; leaving it for a later run's leftover sweep")
	default:
		t.Errorf("cleanup: delete DHCP options set: %s", safeErr(err))
	}
	return false
}

// TestLiveWriteNetworkDHCPOptions exercises ListDHCPOptions, GetDHCPOptions,
// CreateDHCPOptions, DeleteDHCPOptions, SetVPCDHCPOptions, and
// ClearVPCDHCPOptions against the real account named in .env, in hcm-3.
// Neither a DHCP options set nor its write calls appear on any pricing page
// (see the design), so both are treated as free pending the next day's
// bill.
//
// It deletes every leftover vngcloud-live-* VPC first, then every leftover
// vngcloud-live-* DHCP options set (step 1); creates two sets with the
// region's default resolvers, registering each one's own cleanup as soon as
// its id is known, before creating the VPC below, so t.Cleanup's LIFO order
// deletes the VPC before either set (step 2); reads the first set back and
// checks ListDHCPOptions finds it by an exact and a substring Name filter
// (step 3); creates this run's own VPC and /24 subnet (createLiveVPCAndSubnet,
// step 4); sets the VPC to the first set, expecting Changed true within the
// design's 60-second bound, then repeats the same call expecting Changed
// false (step 5); tries to delete the first set while it is still attached,
// expecting the SDK's own ErrInUse (step 6); sets the VPC to the second set,
// which frees the first, and deletes the first set (step 7); clears the
// VPC's set with ClearVPCDHCPOptions, expecting Changed true and an empty
// DHCPOptionID within the same 60-second bound, then repeats the call
// expecting Changed false (step 8); reattaches the second set, deletes the
// subnet and VPC, then verifies that VPC deletion detached the set before
// deleting it (step 9); and logs portal.ListQuotaUsed's row count before
// step 2 and after step 9. It never sets Private DNS on its VPC, so
// DNSStatus stays DISABLED throughout and never blocks the
// SetVPCDHCPOptions and ClearVPCDHCPOptions calls above. It must never run
// at the same time as TestLiveWriteNetworkVPC, TestLiveWriteNetworkRouteTable,
// or TestLiveWriteNetworkACL, since the account's VPC quota leaves room for
// only one. Every step logs only statuses, counts, and timings, never a VPC
// or DHCP options set's own id or name.
func TestLiveWriteNetworkDHCPOptions(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network DHCP options write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_DHCP") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_DHCP=1 to run the live network DHCP options write test")
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
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

	// Step 1: delete every leftover vngcloud-live-* VPC, then every leftover
	// vngcloud-live-* DHCP options set, from a previous run. A leftover set
	// still attached to a leftover VPC becomes unattached once that VPC is
	// deleted, so the VPC sweep always runs first. A VPC a network ACL
	// still holds or with Private DNS on is skipped rather than deleted
	// (see leftoverVPCsToDelete): the account can hold a permanently stuck
	// VPC like that, and this test must not fail on its delete; its DHCP
	// options set then stays attached, and the set sweep below already
	// tolerates that.
	leftoverVPCs, err := listAllVPCs(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListVPCs: %s", safeErr(err))
	}
	var vpcCandidates []network.VPC
	for _, leftover := range leftoverVPCs {
		if isLiveSecurityGroupName(leftover.Name) {
			vpcCandidates = append(vpcCandidates, leftover)
		}
	}
	deletableVPCs, _ := leftoverVPCsToDelete(ctx, t, client, vpcCandidates)
	deletedVPCLeftovers := 0
	for _, leftover := range deletableVPCs {
		deleteVPCAndSubnets(ctx, t, client, leftover.UUID, nil)
		deletedVPCLeftovers++
	}
	leftoverSets, err := listAllDHCPOptions(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListDHCPOptions: %s", safeErr(err))
	}
	deletedSetLeftovers := 0
	for _, leftover := range leftoverSets {
		if !isLiveSecurityGroupName(leftover.Name) {
			continue
		}
		if deleteLiveDHCPOptionsSet(ctx, t, client, leftover.UUID) {
			deletedSetLeftovers++
		}
	}
	t.Logf("step 1: deleted %d leftover VPC(s) and %d leftover DHCP options set(s)", deletedVPCLeftovers, deletedSetLeftovers)

	quotaBefore, err := portalClient.ListQuotaUsed(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListQuotaUsed: %s", safeErr(err))
	}
	t.Logf("step 1: %d quota row(s) before this run's writes", len(quotaBefore.Items))

	// Step 2: create two DHCP options sets with the region's default
	// resolvers and no MTU (the server's own default applies). Each one's
	// cleanup is registered as soon as its id is known and before the VPC
	// below is created, so t.Cleanup's LIFO order deletes the VPC first.
	suffix1, err := randomHex(4)
	if err != nil {
		t.Fatalf("generate DHCP options set name suffix: %v", err)
	}
	name1 := "vngcloud-live-" + suffix1
	createdSet1, err := client.CreateDHCPOptions(ctx, &network.CreateDHCPOptionsInput{Name: name1, DNSServers: liveDHCPOptionsResolversFor(region)})
	if err != nil {
		t.Fatalf("step 2 CreateDHCPOptions (first set): %s", safeErr(err))
	}
	set1ID := createdSet1.DHCPOptions.UUID
	if set1ID == "" {
		t.Fatal("step 2: CreateDHCPOptions (first set) returned an empty id; the design requires one")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		deleteLiveDHCPOptionsSet(cleanupCtx, t, client, set1ID)
	})
	t.Logf("step 2: created the first DHCP options set, status %s, mtu %d", createdSet1.DHCPOptions.Status, createdSet1.DHCPOptions.MTU)

	suffix2, err := randomHex(4)
	if err != nil {
		t.Fatalf("generate DHCP options set name suffix: %v", err)
	}
	name2 := "vngcloud-live-" + suffix2
	createdSet2, err := client.CreateDHCPOptions(ctx, &network.CreateDHCPOptionsInput{Name: name2, DNSServers: liveDHCPOptionsResolversFor(region)})
	if err != nil {
		t.Fatalf("step 2 CreateDHCPOptions (second set): %s", safeErr(err))
	}
	set2ID := createdSet2.DHCPOptions.UUID
	if set2ID == "" {
		t.Fatal("step 2: CreateDHCPOptions (second set) returned an empty id; the design requires one")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		deleteLiveDHCPOptionsSet(cleanupCtx, t, client, set2ID)
	})
	t.Logf("step 2: created the second DHCP options set, status %s, mtu %d", createdSet2.DHCPOptions.Status, createdSet2.DHCPOptions.MTU)

	// Step 3: read the first set back, and check the Name filter finds it
	// by an exact match and by a substring of its name. Whether the server
	// matches by substring or only exactly is not yet confirmed live, so
	// this only logs what each search returns rather than asserting one
	// behavior.
	gotSet1, err := client.GetDHCPOptions(ctx, &network.GetDHCPOptionsInput{DHCPOptionsID: set1ID})
	if err != nil {
		t.Fatalf("step 3 GetDHCPOptions: %s", safeErr(err))
	}
	if gotSet1.DHCPOptions.UUID != set1ID || len(gotSet1.DHCPOptions.VPCIDs) != 0 {
		t.Errorf("step 3: unexpected set after create: %d associated VPC(s), want 0", len(gotSet1.DHCPOptions.VPCIDs))
	}
	exactMatch, err := client.ListDHCPOptions(ctx, &network.ListDHCPOptionsInput{Name: name1})
	if err != nil {
		t.Fatalf("step 3 ListDHCPOptions (exact name): %s", safeErr(err))
	}
	t.Logf("step 3: exact name filter returned %d row(s)", len(exactMatch.Items))
	substringMatch, err := client.ListDHCPOptions(ctx, &network.ListDHCPOptionsInput{Name: name1[:len(name1)-4]})
	if err != nil {
		t.Fatalf("step 3 ListDHCPOptions (substring name): %s", safeErr(err))
	}
	t.Logf("step 3: substring name filter returned %d row(s)", len(substringMatch.Items))

	// Step 4: create this run's own VPC and /24 subnet.
	// createLiveVPCAndSubnet registers the VPC's own t.Cleanup as soon as
	// its id is known, after both sets' cleanups above, so it runs first.
	// This VPC never has Private DNS enabled, so DNSStatus stays DISABLED
	// and never blocks a SetVPCDHCPOptions call below.
	vpcID, _ := createLiveVPCAndSubnet(ctx, t, client, portalClient, nil)
	t.Log("step 4: created this run's own VPC and /24 subnet")

	// Step 5: set the VPC to the first set, expecting Changed true within
	// the design's 60-second bound, then repeat the same call expecting
	// Changed false.
	start := time.Now()
	setFirst, err := client.SetVPCDHCPOptions(ctx, &network.SetVPCDHCPOptionsInput{VPCID: vpcID, DHCPOptionsID: set1ID})
	if err != nil {
		t.Fatalf("step 5 SetVPCDHCPOptions (first set): %s", safeErr(err))
	}
	if !setFirst.Changed {
		t.Error("step 5: Changed = false, want true: the VPC had no set before this call")
	}
	if setFirst.VPC.DHCPOptionID != set1ID {
		t.Error("step 5: the VPC's DHCPOptionID does not name the first set after the call settled")
	}
	t.Logf("step 5: set the VPC to the first set, wait %s", time.Since(start))

	start = time.Now()
	setFirstAgain, err := client.SetVPCDHCPOptions(ctx, &network.SetVPCDHCPOptionsInput{VPCID: vpcID, DHCPOptionsID: set1ID})
	if err != nil {
		t.Fatalf("step 5 SetVPCDHCPOptions (first set, repeat): %s", safeErr(err))
	}
	if setFirstAgain.Changed {
		t.Error("step 5: repeat set reported Changed true, want false: the VPC already names this set")
	}
	t.Logf("step 5: repeat set was a no-op as expected, wait %s", time.Since(start))

	// Step 6: try to delete the first set while it is still attached,
	// expecting the SDK's own ErrInUse.
	_, deleteWhileAttachedErr := client.DeleteDHCPOptions(ctx, &network.DeleteDHCPOptionsInput{DHCPOptionsID: set1ID})
	if !errors.Is(deleteWhileAttachedErr, network.ErrInUse) {
		t.Errorf("step 6: delete while attached err = %s, want ErrInUse", safeErr(deleteWhileAttachedErr))
	} else {
		t.Log("step 6: delete while attached refused with ErrInUse as expected")
	}

	// Step 7: set the VPC to the second set, which frees the first, then
	// delete the first set.
	start = time.Now()
	setSecond, err := client.SetVPCDHCPOptions(ctx, &network.SetVPCDHCPOptionsInput{VPCID: vpcID, DHCPOptionsID: set2ID})
	if err != nil {
		t.Fatalf("step 7 SetVPCDHCPOptions (second set): %s", safeErr(err))
	}
	if !setSecond.Changed {
		t.Error("step 7: Changed = false, want true: the VPC named the first set before this call")
	}
	t.Logf("step 7: moved the VPC to the second set, wait %s", time.Since(start))

	if _, err := client.DeleteDHCPOptions(ctx, &network.DeleteDHCPOptionsInput{DHCPOptionsID: set1ID}); err != nil {
		t.Errorf("step 7 DeleteDHCPOptions (first set, now unattached): %s", safeErr(err))
	} else {
		t.Log("step 7: deleted the first set now that it is unattached")
	}

	// Step 8: clear the VPC's DHCP options set, expecting Changed true and
	// an empty DHCPOptionID within the design's 60-second bound, then repeat
	// the same call expecting Changed false.
	start = time.Now()
	cleared, err := client.ClearVPCDHCPOptions(ctx, &network.ClearVPCDHCPOptionsInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("step 8 ClearVPCDHCPOptions: %s", safeErr(err))
	}
	if !cleared.Changed {
		t.Error("step 8: Changed = false, want true: the VPC named the second set before this call")
	}
	if cleared.VPC.DHCPOptionID != "" {
		t.Error("step 8: the VPC's DHCPOptionID is not empty after the call settled")
	}
	t.Logf("step 8: cleared the VPC's DHCP options set, wait %s", time.Since(start))

	start = time.Now()
	clearedAgain, err := client.ClearVPCDHCPOptions(ctx, &network.ClearVPCDHCPOptionsInput{VPCID: vpcID})
	if err != nil {
		t.Fatalf("step 8 ClearVPCDHCPOptions (repeat): %s", safeErr(err))
	}
	if clearedAgain.Changed {
		t.Error("step 8: repeat clear reported Changed true, want false: the VPC already has no set")
	}
	t.Logf("step 8: repeat clear was a no-op as expected, wait %s", time.Since(start))

	start = time.Now()
	setSecondAgain, err := client.SetVPCDHCPOptions(ctx, &network.SetVPCDHCPOptionsInput{VPCID: vpcID, DHCPOptionsID: set2ID})
	if err != nil {
		t.Fatalf("step 9 SetVPCDHCPOptions (second set, reattach): %s", safeErr(err))
	}
	if !setSecondAgain.Changed || setSecondAgain.VPC.DHCPOptionID != set2ID {
		t.Error("step 9: reattaching the second set did not settle on that set")
	}
	t.Logf("step 9: reattached the second set, wait %s", time.Since(start))

	// Step 9: delete the subnet and VPC explicitly rather than waiting for
	// their registered cleanups; those cleanups then find both already gone
	// and do nothing. VPC deletion must detach its DHCP options set.
	deleteVPCAndSubnets(ctx, t, client, vpcID, nil)
	t.Log("step 9: deleted the subnet and VPC")
	secondSetAfterVPCDelete, err := client.GetDHCPOptions(ctx, &network.GetDHCPOptionsInput{DHCPOptionsID: set2ID})
	if err != nil {
		t.Errorf("step 9 GetDHCPOptions (second set after VPC delete): %s", safeErr(err))
	} else if len(secondSetAfterVPCDelete.DHCPOptions.VPCIDs) != 0 {
		t.Errorf("step 9: second set has %d associated VPC(s) after VPC delete, want 0", len(secondSetAfterVPCDelete.DHCPOptions.VPCIDs))
	} else if _, err := client.DeleteDHCPOptions(ctx, &network.DeleteDHCPOptionsInput{DHCPOptionsID: set2ID}); err != nil {
		t.Errorf("step 9 DeleteDHCPOptions (second set after VPC delete): %s", safeErr(err))
	} else {
		t.Log("step 9: VPC deletion detached and the SDK deleted the second set")
	}

	quotaAfter, err := portalClient.ListQuotaUsed(ctx, nil)
	if err != nil {
		t.Fatalf("step 9 ListQuotaUsed: %s", safeErr(err))
	}
	t.Logf("step 9: %d quota row(s) after this run's writes", len(quotaAfter.Items))
}

// listAllVirtualIPAddresses pages through every virtual IP the account has.
func listAllVirtualIPAddresses(ctx context.Context, client *network.Client) ([]network.VirtualIPAddress, error) {
	var all []network.VirtualIPAddress
	for page := 1; ; page++ {
		out, err := client.ListVirtualIPAddresses(ctx, &network.ListVirtualIPAddressesInput{Page: page})
		if err != nil {
			return all, err
		}
		all = append(all, out.Items...)
		if page >= out.TotalPage {
			return all, nil
		}
	}
}

// liveVirtualIPNamePattern matches every virtual IP name this test creates.
var liveVirtualIPNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}(-b(-renamed)?|-c)?$`)

func isLiveVirtualIPName(name string) bool {
	return liveVirtualIPNamePattern.MatchString(name)
}

// deleteLiveVirtualIPs removes prior test virtual IPs before parent cleanup.
func deleteLiveVirtualIPs(ctx context.Context, t *testing.T, client *network.Client) int {
	t.Helper()
	all, err := listAllVirtualIPAddresses(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list virtual ip addresses: %s", safeVirtualIPErr(err))
		return 0
	}
	deleted := 0
	for _, vip := range all {
		if !isLiveVirtualIPName(vip.Name) {
			continue
		}
		if _, err := client.DeleteVirtualIPAddress(ctx, &network.DeleteVirtualIPAddressInput{VirtualIPAddressID: vip.UUID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete virtual ip: %s", safeVirtualIPErr(err))
			continue
		}
		deleted++
	}
	return deleted
}

// registerVirtualIPCleanupIfCreated registers cleanup before create errors
// are checked because a failed post-create wait can still return an ID.
func registerVirtualIPCleanupIfCreated(t *testing.T, client *network.Client, out *network.CreateVirtualIPAddressOutput, label string) {
	t.Helper()
	if out == nil || out.VirtualIPAddress.UUID == "" {
		return
	}
	id := out.VirtualIPAddress.UUID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := client.DeleteVirtualIPAddress(cleanupCtx, &network.DeleteVirtualIPAddressInput{VirtualIPAddressID: id}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete %s virtual ip: %s", label, safeVirtualIPErr(err))
		}
	})
}

// safeVirtualIPErr keeps virtual-IP live-test logs free of IDs, addresses,
// CIDRs, and SDK sentinel detail that may include them.
func safeVirtualIPErr(err error) string {
	if err == nil {
		return "none"
	}
	var apiErr *vngcloud.APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("status=%d code=%s", apiErr.StatusCode, apiErr.Code)
	}
	switch {
	case errors.Is(err, network.ErrNotSettled):
		return "not settled"
	case errors.Is(err, network.ErrFailed):
		return "write failed"
	case errors.Is(err, network.ErrInUse):
		return "resource in use"
	case errors.Is(err, vngcloud.ErrInvalidInput):
		return "invalid input"
	case vngcloud.IsNotFound(err):
		return "not found"
	default:
		return fmt.Sprintf("non-API error (%T)", err)
	}
}

// looksLikePaymentRefusal reports whether err is a *vngcloud.APIError whose
// message names payment, balance, or credit, case-insensitively: the
// design's own signal that a create is paid rather than merely malformed.
// The message itself is never logged or returned; it is read only to decide
// whether TestLiveWriteNetworkVirtualIP stops.
func looksLikePaymentRefusal(err error) bool {
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "payment") || strings.Contains(msg, "balance") || strings.Contains(msg, "credit")
}

// isVirtualIPAddressDuplicateRefusal accepts only documented duplicate
// address refusals. Other errors leave the create outcome unknown.
func isVirtualIPAddressDuplicateRefusal(err error) bool {
	var apiErr *vngcloud.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusBadRequest || apiErr.StatusCode == http.StatusConflict
}

// TestLiveWriteNetworkVirtualIP exercises CreateVirtualIPAddress,
// UpdateVirtualIPAddress, and DeleteVirtualIPAddress against the account
// named in .env, in hcm-3, on a VPC and /24 subnet this run creates and
// deletes itself (createLiveVPCAndSubnet).
//
// Whether a private virtual IP create is billed is not yet confirmed (see
// the design); this test creates only its first virtual IP before checking
// for that, and stops at once, deleting nothing but logging the outcome, if
// the create is refused for payment, balance, or credit. It never retries
// that create.
//
// It deletes every leftover vngcloud-live-* virtual IP first (step 1);
// creates the run's VPC and subnet (step 2); creates a virtual IP with mode
// Active/Passive and no address, stopping here if that is refused for
// payment (step 3); creates a second virtual IP with mode Active/Active and
// a given address in the subnet (step 4); creates a third with the same
// address, expecting the server to refuse a used address (step 5); renames
// the second virtual IP, resending its mode, and checks the mode did not
// change (step 6); deletes the second virtual IP and confirms a 404, then
// repeats the delete and logs its status (step 7); deletes the subnet while
// the first virtual IP remains, expecting ErrInUse (step 8); and deletes the
// first virtual IP (step 9). Every step logs through safeVirtualIPErr. Every
// direct t.Logf call names only a status, a mode, or a count.
func TestLiveWriteNetworkVirtualIP(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live network virtual IP write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_NETWORK_VIP") != "1" {
		t.Skip("set VNGCLOUD_LIVE_NETWORK_VIP=1 to run the live network virtual IP write test")
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
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

	// Step 1: delete every leftover vngcloud-live-* virtual IP from a
	// previous run, before its VPC and subnet are ever touched.
	leftovers := deleteLiveVirtualIPs(ctx, t, client)
	t.Logf("step 1: deleted %d leftover virtual IP(s)", leftovers)

	// Step 2: create the run's own VPC and /24 subnet. Nothing else can
	// still hold this run's subnet by the time cleanup runs (unlike the
	// ACL test's aclHoldsSubnet), since steps 7 and 9 delete every virtual
	// IP this run creates before the subnet and VPC cleanup registered
	// here runs.
	vpcID, subnetID := createLiveVPCAndSubnet(ctx, t, client, portalClient, nil)

	// Step 3: create the run's first virtual IP. A refusal naming payment,
	// balance, or credit means the create is paid; stop here rather than
	// retry or fall back to a different mode or address.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix
	created, err := client.CreateVirtualIPAddress(ctx, &network.CreateVirtualIPAddressInput{
		SubnetID: subnetID, Name: name, Mode: network.VirtualIPModeActivePassive,
	})
	registerVirtualIPCleanupIfCreated(t, client, created, "first")
	if err != nil {
		if looksLikePaymentRefusal(err) {
			t.Skipf("step 3: create refused for payment, balance, or credit, %s; add owner-approved credit before this live check", safeVirtualIPErr(err))
		}
		t.Fatalf("step 3 CreateVirtualIPAddress: %s", safeVirtualIPErr(err))
	}
	vipID := created.VirtualIPAddress.UUID
	if vipID == "" {
		t.Fatal("step 3: CreateVirtualIPAddress returned an empty id; the design requires one")
	}
	t.Logf("step 3: created virtual IP, status %s, mode %s", created.VirtualIPAddress.Status, created.VirtualIPAddress.Mode)

	// Step 4: create a second virtual IP with mode Active/Active and a
	// given address in the run's subnet. The subnet's own CIDR comes from
	// GetSubnet rather than a fixed constant, since createLiveVPCAndSubnet
	// now derives it from pickFreeVPCCIDR and vpcBlockCIDR and does not
	// return it.
	subnet, err := client.GetSubnet(ctx, &network.GetSubnetInput{VPCID: vpcID, SubnetID: subnetID})
	if err != nil {
		t.Fatalf("step 4 GetSubnet: %s", safeVirtualIPErr(err))
	}
	subnetPrefix, err := netip.ParsePrefix(subnet.Subnet.CIDR)
	if err != nil {
		t.Fatalf("step 4 parse subnet CIDR: %s", safeVirtualIPErr(err))
	}
	addr := subnetPrefix.Masked().Addr().As4()
	addr[3] = 10
	address := netip.AddrFrom4(addr).String()
	created2, err := client.CreateVirtualIPAddress(ctx, &network.CreateVirtualIPAddressInput{
		SubnetID: subnetID, Name: name + "-b", Mode: network.VirtualIPModeActiveActive, IPAddress: address,
	})
	registerVirtualIPCleanupIfCreated(t, client, created2, "second")
	if err != nil {
		t.Fatalf("step 4 CreateVirtualIPAddress with address: %s", safeVirtualIPErr(err))
	}
	vipID2 := created2.VirtualIPAddress.UUID
	if vipID2 == "" {
		t.Fatal("step 4: CreateVirtualIPAddress returned an empty id; the design requires one")
	}
	t.Logf("step 4: created a second virtual IP with a given address, status %s", created2.VirtualIPAddress.Status)
	// Step 5: create again with the same address; the server keeps an
	// address unique within a subnet, so the design expects a refusal.
	dup, dupErr := client.CreateVirtualIPAddress(ctx, &network.CreateVirtualIPAddressInput{
		SubnetID: subnetID, Name: name + "-c", Mode: network.VirtualIPModeActiveActive, IPAddress: address,
	})
	registerVirtualIPCleanupIfCreated(t, client, dup, "duplicate-address")
	switch {
	case dupErr == nil:
		t.Error("step 5: creating a virtual IP with a used address succeeded; the design expects a refusal")
	case dup != nil && dup.VirtualIPAddress.UUID != "":
		t.Fatalf("step 5 CreateVirtualIPAddress with a used address: %s", safeVirtualIPErr(dupErr))
	case isVirtualIPAddressDuplicateRefusal(dupErr):
		t.Logf("step 5: used address refused, %s", safeVirtualIPErr(dupErr))
	default:
		t.Fatalf("step 5 CreateVirtualIPAddress with a used address: %s", safeVirtualIPErr(dupErr))
	}

	// Step 6: rename the second virtual IP, resending its mode; the mode
	// must not change from a name-only update.
	updated, err := client.UpdateVirtualIPAddress(ctx, &network.UpdateVirtualIPAddressInput{
		VirtualIPAddressID: vipID2, Name: vngcloud.Ptr(name + "-b-renamed"),
	})
	if err != nil {
		t.Fatalf("step 6 UpdateVirtualIPAddress: %s", safeVirtualIPErr(err))
	}
	if updated.VirtualIPAddress.Mode != network.VirtualIPModeActiveActive {
		t.Fatalf("step 6: mode = %q after a name-only update, want %q unchanged",
			updated.VirtualIPAddress.Mode, network.VirtualIPModeActiveActive)
	}
	t.Logf("step 6: renamed the second virtual IP, mode unchanged")

	// Step 7: delete the second virtual IP and confirm a 404, then repeat
	// the delete and log its status.
	if _, err := client.DeleteVirtualIPAddress(ctx, &network.DeleteVirtualIPAddressInput{VirtualIPAddressID: vipID2}); err != nil {
		t.Fatalf("step 7 DeleteVirtualIPAddress: %s", safeVirtualIPErr(err))
	}
	if _, err := client.GetVirtualIPAddress(ctx, &network.GetVirtualIPAddressInput{VirtualIPAddressID: vipID2}); !vngcloud.IsNotFound(err) {
		t.Fatalf("step 7: get after delete = %s, want NotFound", safeVirtualIPErr(err))
	}
	_, repeatErr := client.DeleteVirtualIPAddress(ctx, &network.DeleteVirtualIPAddressInput{VirtualIPAddressID: vipID2})
	t.Logf("step 7: deleted the second virtual IP, confirmed 404, repeat delete status %s", safeVirtualIPErr(repeatErr))

	// Step 8: the subnet delete must be refused while the first virtual IP
	// remains.
	if _, err := client.DeleteSubnet(ctx, &network.DeleteSubnetInput{VPCID: vpcID, SubnetID: subnetID, NoWait: true}); !errors.Is(err, network.ErrInUse) {
		t.Fatalf("step 8: DeleteSubnet while a virtual IP remains = %s, want ErrInUse", safeVirtualIPErr(err))
	}
	t.Log("step 8: subnet delete refused while a virtual IP remains")

	// Step 9: delete the first virtual IP, so the run's own subnet and VPC
	// cleanup can proceed without ErrInUse.
	if _, err := client.DeleteVirtualIPAddress(ctx, &network.DeleteVirtualIPAddressInput{VirtualIPAddressID: vipID}); err != nil {
		t.Fatalf("step 9 DeleteVirtualIPAddress: %s", safeVirtualIPErr(err))
	}
	t.Log("step 9: deleted the first virtual IP")
}

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

// vcrLiveUserNameSuffixPattern is the live vCR write test's own repository
// user naming scheme. A live 400 confirms a user's own name rule is 6 to 14
// characters, too short for vcrlive- (let alone vngcloud-live-) plus 8 hex
// digits, so users get their own vcu- prefix instead. The pattern is
// anchored at both ends, unlike vcrLiveNameSuffixPattern: vcu- is short
// enough that an unrelated name could otherwise end with it by chance,
// wrongly pulling that name into the leftover sweep and cleanup checks.
var vcrLiveUserNameSuffixPattern = regexp.MustCompile(`^vcu-[0-9a-f]{8}$`)

// isLiveVCRUserName reports whether name matches
// vcrLiveUserNameSuffixPattern exactly.
func isLiveVCRUserName(name string) bool {
	return vcrLiveUserNameSuffixPattern.MatchString(name)
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

// deleteVCRUserByName lists users and deletes any whose name equals name
// exactly, matching CreateUser's own lookup: a live capture confirms the
// server applies no account prefix to a user's name. It is used after a
// CreateUser call returns an error or fails to resolve the created user's
// own id by list, since a POST that returned an error may still have
// reached the server, and the id-lookup itself, not only the create, can be
// the thing that failed. It runs on its own timeout, not the calling test
// step's context, so it can still clean up after that step's context is
// the reason the step failed.
func deleteVCRUserByName(t *testing.T, client *containerregistry.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	list, err := client.ListUsers(ctx, &containerregistry.ListUsersInput{Name: name})
	if err != nil {
		t.Errorf("cleanup: list users by name: %s", safeErr(err))
		return
	}
	for _, u := range list.Items {
		if u.Name != name {
			continue
		}
		if _, err := client.DeleteUser(ctx, &containerregistry.DeleteUserInput{UserID: u.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete user by name: %s", safeErr(err))
		}
	}
}

// TestLiveWriteContainerRegistryUser exercises ListPermissions, CreateUser,
// ListRepositoryUsers, and DeleteUser against the account named in .env, on
// a repository the test creates for itself.
//
// Whether a vCR repository costs money is unknown until the design's cost
// probe runs; like TestLiveWriteContainerRegistryRepository, this test
// stops at the repository create if the server refuses it for payment (a
// 4xx naming balance, credit, payment, or order), logs the status and code,
// and skips rather than fails. Repository users are assumed free once
// repositories are.
//
// It deletes every leftover vcu-* user, then every leftover vcrlive-*
// repository holding no images, from a previous run, users first even
// though an attached user does not itself block a repository delete (step
// 1); creates a vcrlive-<8 hex> repository (step 2); reads ListPermissions
// and picks the action named exactly "Pull Images" (step 3); registers a
// by-exact-name fallback cleanup for a vcu-<8 hex> user before creating it
// with that one pull-only permission and a 1-day duration, logging only a
// boolean for a non-empty secret and its length, never the secret itself
// (step 4); registers the fallback delete by id as soon as the created
// user's id is known (step 5); confirms the user appears in
// ListRepositoryUsers on the repository (step 6); deletes the user,
// confirming a repeat delete returns NotFound (step 7); and deletes the
// repository, confirming a repeat delete returns NotFound (step 8). docker
// login is never run: it would put the secret in a credential store, and
// the login name is an open question until the owner tries it by hand.
func TestLiveWriteContainerRegistryUser(t *testing.T) {
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

	// Step 1: delete every leftover vcu-* user, then every leftover
	// vcrlive-* repository holding no images, from a previous run.
	leftoverUsers, err := client.ListUsers(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListUsers: %s", safeErr(err))
	}
	deletedUsers := 0
	for _, leftover := range leftoverUsers.Items {
		if !isLiveVCRUserName(leftover.Name) {
			continue
		}
		if _, err := client.DeleteUser(ctx, &containerregistry.DeleteUserInput{UserID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover user: %s", safeErr(err))
		}
		deletedUsers++
	}
	leftoverRepos, err := client.ListRepositories(ctx, nil)
	if err != nil {
		t.Fatalf("step 1 ListRepositories: %s", safeErr(err))
	}
	deletedRepos := 0
	for _, leftover := range leftoverRepos.Items {
		if !isLiveVCRRepositoryName(leftover.Name) || leftover.ImageCount > 0 {
			continue
		}
		if _, err := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: leftover.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover repository: %s", safeErr(err))
		}
		deletedRepos++
	}
	t.Logf("step 1: deleted %d leftover user(s) and %d leftover repository(ies)", deletedUsers, deletedRepos)

	// Step 2: create the repository this user is attached to. Whether this
	// is free is the open question the design's cost probe answers; this
	// test stops here, skipping rather than failing, if the server refuses
	// for payment.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	repoName := "vcrlive-" + suffix

	createdRepo, err := client.CreateRepository(ctx, &containerregistry.CreateRepositoryInput{
		Name:         repoName,
		QuotaLimitGB: 1,
	})
	var repositoryID string
	if err == nil {
		repositoryID = createdRepo.Repository.ID
	}
	if err != nil || repositoryID == "" {
		t.Cleanup(func() { deleteVCRRepositoryByName(t, client, repoName) })
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
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteRepository(cleanupCtx, &containerregistry.DeleteRepositoryInput{RepositoryID: repositoryID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete repository: %s", safeErr(err))
		}
	})
	t.Log("step 2: created repository")

	// Step 3: read the known permissions and pick the action named exactly
	// "Pull Images".
	const pullAction = "Pull Images"
	perms, err := client.ListPermissions(ctx, nil)
	if err != nil {
		t.Fatalf("step 3 ListPermissions: %s", safeErr(err))
	}
	if len(perms.Items) == 0 {
		t.Fatal("step 3: ListPermissions returned no actions")
	}
	foundPullAction := false
	for _, p := range perms.Items {
		if p.Action == pullAction {
			foundPullAction = true
			break
		}
	}
	if !foundPullAction {
		t.Fatalf("step 3: no %q action found among %d permission(s)", pullAction, len(perms.Items))
	}
	t.Logf("step 3: read %d permission(s), found %q", len(perms.Items), pullAction)

	// Step 4: create a pull-only user on the repository, with a 1-day
	// duration. A POST is not retried after an ambiguous failure, and the
	// user's own id is resolved by a list lookup rather than the create
	// response, so either one failing still means the user may exist;
	// register the by-exact-name fallback before the create itself, not
	// only after it fails, so a create whose response never reaches this
	// process still gets cleaned up.
	userName := "vcu-" + suffix
	t.Cleanup(func() { deleteVCRUserByName(t, client, userName) })
	createdUser, err := client.CreateUser(ctx, &containerregistry.CreateUserInput{
		Name:         userName,
		DurationDays: vngcloud.Ptr(1),
		Permissions: []containerregistry.UserPermission{
			{RepositoryID: repositoryID, Actions: []string{pullAction}},
		},
	})
	var userID string
	var hasSecret bool
	var secretLen int
	if createdUser != nil {
		userID = createdUser.User.ID
		secret := createdUser.SecretKey.Reveal()
		hasSecret = secret != ""
		secretLen = len(secret)
		t.Logf("step 4: create response secret present=%v length=%d", hasSecret, secretLen)
	}
	if err != nil {
		t.Fatalf("step 4 CreateUser: %s", safeErr(err))
	}
	if userID == "" {
		t.Fatal("step 4: CreateUser did not resolve the created user's id by list; check list-users and delete it directly")
	}
	if !hasSecret {
		t.Fatal("step 4: CreateUser returned an empty secret")
	}

	// Step 5: register the fallback delete as soon as userID is known,
	// before any later step can fail and skip the explicit delete in step 7.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteUser(cleanupCtx, &containerregistry.DeleteUserInput{UserID: userID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete user: %s", safeErr(err))
		}
	})

	// Step 6: list the repository's users and confirm the created user
	// appears.
	repoUsers, err := client.ListRepositoryUsers(ctx, &containerregistry.ListRepositoryUsersInput{RepositoryID: repositoryID})
	if err != nil {
		t.Fatalf("step 6 ListRepositoryUsers: %s", safeErr(err))
	}
	found := false
	for _, u := range repoUsers.Items {
		if u.ID == userID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("step 6: ListRepositoryUsers did not include the created user")
	}
	t.Log("step 6: confirmed the user appears on the repository")

	// Step 7: delete the user and confirm a repeat delete returns NotFound.
	// docker login is never run: it would put the secret in a credential
	// store.
	if _, err := client.DeleteUser(ctx, &containerregistry.DeleteUserInput{UserID: userID}); err != nil {
		t.Fatalf("step 7 DeleteUser: %s", safeErr(err))
	}
	_, secondUserErr := client.DeleteUser(ctx, &containerregistry.DeleteUserInput{UserID: userID})
	if !vngcloud.IsNotFound(secondUserErr) {
		t.Fatalf("step 7: repeat delete = %v, want NotFound", secondUserErr)
	}

	// Step 8: delete the repository and confirm a repeat delete returns
	// NotFound.
	if _, err := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: repositoryID}); err != nil {
		t.Fatalf("step 8 DeleteRepository: %s", safeErr(err))
	}
	_, secondRepoErr := client.DeleteRepository(ctx, &containerregistry.DeleteRepositoryInput{RepositoryID: repositoryID})
	if !vngcloud.IsNotFound(secondRepoErr) {
		t.Fatalf("step 8: repeat delete = %v, want NotFound", secondRepoErr)
	}
}

// liveIAMServiceAccountNamePattern is this test's own naming scheme:
// vngcloud-live-<8 lowercase hex>, exactly, so a name that merely starts
// with vngcloud-live- but was not generated by this test is never swept up
// as a leftover.
var liveIAMServiceAccountNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveIAMServiceAccountName(name string) bool {
	return liveIAMServiceAccountNamePattern.MatchString(name)
}

// listAllLiveIAMServiceAccounts lists every service account whose exact name
// matches liveIAMServiceAccountNamePattern. ListServiceAccounts' default
// page size is far above the account's 20-service-account quota, so one
// call always returns every service account there is, live or not.
func listAllLiveIAMServiceAccounts(ctx context.Context, client *iam.Client) ([]iam.ServiceAccount, error) {
	out, err := client.ListServiceAccounts(ctx, nil)
	if err != nil {
		return nil, err
	}
	var live []iam.ServiceAccount
	for _, sa := range out.Items {
		if isLiveIAMServiceAccountName(sa.Name) {
			live = append(live, sa)
		}
	}
	return live, nil
}

// deleteLiveIAMServiceAccounts deletes every leftover service account whose
// exact name matches liveIAMServiceAccountNamePattern, and returns how many
// it deleted. It is used to sweep up a previous run's leftovers before this
// test creates its own service account.
func deleteLiveIAMServiceAccounts(ctx context.Context, t *testing.T, client *iam.Client) int {
	t.Helper()
	live, err := listAllLiveIAMServiceAccounts(ctx, client)
	if err != nil {
		t.Errorf("delete live iam service accounts: list: %s", safeErr(err))
		return 0
	}
	deleted := 0
	for _, sa := range live {
		if _, err := client.DeleteServiceAccount(ctx, &iam.DeleteServiceAccountInput{ServiceAccountID: sa.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live iam service accounts: delete: %s", safeErr(err))
			continue
		}
		deleted++
	}
	return deleted
}

// deleteLiveIAMServiceAccountByExactName deletes the one service account
// whose Name is exactly name, if the account has one. CreateServiceAccount
// returns a nil Output when the create request itself was rejected outright
// or an ambiguous 5xx or network error left the outcome unknown (see
// wrapAmbiguousServiceAccountCreateErr in service_accounts_write.go), so a
// cleanup that must run before any id is known has to search by the name
// this test generated instead.
func deleteLiveIAMServiceAccountByExactName(ctx context.Context, t *testing.T, client *iam.Client, name string) {
	t.Helper()
	out, err := client.ListServiceAccounts(ctx, nil)
	if err != nil {
		t.Errorf("cleanup: list service accounts by name: %s", safeErr(err))
		return
	}
	for _, sa := range out.Items {
		if sa.Name != name {
			continue
		}
		if _, err := client.DeleteServiceAccount(ctx, &iam.DeleteServiceAccountInput{ServiceAccountID: sa.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete service account: %s", safeErr(err))
		}
	}
}

// writeLiveSecretFile writes secret to a new, mode-0600 file in a fresh temp
// directory, mirroring the CLI's --secret-file contract at the SDK layer: a
// live write test must never log a client secret, only whether one is
// present. t.TempDir cleans the file up when the test ends.
func writeLiveSecretFile(t *testing.T, name, secret string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("write secret file %s: %v", name, err)
	}
}

// VNGCLOUD_LIVE_IAM_SERVICE_ACCOUNT must be set to "1" in addition to
// VNGCLOUD_LIVE_WRITE.
//
// It deletes every leftover vngcloud-live-* service account from a previous
// run first (step 1); creates one, writing its client secret to a temp file
// (step 2); registers its cleanup, and the final remaining-account check, as
// soon as its id is known (step 3), before any later step can fail and skip
// the explicit delete below; reads it back (step 4); updates its
// description (step 5); resets its secret to a second temp file, checking
// only whether the two secrets differ, never their values (step 6); and
// deletes it explicitly (step 7). Every step logs only statuses, counts, and
// booleans about secrets, never a service account's own id, name, or client
// secret.
func TestLiveWriteIAMServiceAccount(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live iam service account write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_IAM_SERVICE_ACCOUNT") != "1" {
		t.Skip("set VNGCLOUD_LIVE_IAM_SERVICE_ACCOUNT=1 to run the live iam service account write test")
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
	client := iam.New(cfg)

	// Step 1: sweep up leftovers from an earlier run.
	leftovers := deleteLiveIAMServiceAccounts(ctx, t, client)
	t.Logf("step 1: deleted %d leftover service account(s)", leftovers)

	// Step 2: create.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	name := "vngcloud-live-" + suffix

	// Register a by-exact-name cleanup before the create call: it is the
	// only way to find and delete an account that reached the server despite
	// an error that leaves Output nil below (a rejected create, or an
	// ambiguous 5xx or network error). It runs even if this test never gets
	// past that Fatalf.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteLiveIAMServiceAccountByExactName(cleanupCtx, t, client, name)
	})

	created, err := client.CreateServiceAccount(ctx, &iam.CreateServiceAccountInput{Name: name, Description: "vngcloud live write test"})
	// CreateServiceAccount returns a non-nil Output whenever the create
	// request itself succeeded, even when it also returns iam.ErrNoSecret or
	// iam.ErrCreateUnconfirmed: the account exists either way, so cleanup
	// (step 3) is registered from created.ServiceAccount.ID before this Fatals
	// on err, or the account would leak past this test.
	if created == nil {
		t.Fatalf("step 2 CreateServiceAccount: %s", safeErr(err))
	}
	serviceAccountID := created.ServiceAccount.ID
	if serviceAccountID == "" {
		t.Fatalf("step 2: CreateServiceAccount returned an empty id: %s", safeErr(err))
	}

	// Step 3: register cleanup and the final remaining-account check as soon
	// as serviceAccountID is known, before any later step (err from step 2
	// included) can fail and skip the explicit delete below.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteServiceAccount(cleanupCtx, &iam.DeleteServiceAccountInput{ServiceAccountID: serviceAccountID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete service account: %s", safeErr(err))
		}
		live, err := listAllLiveIAMServiceAccounts(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final list: %s", safeErr(err))
			return
		}
		t.Logf("cleanup: vngcloud-live service account(s) remaining: %d", len(live))
		if len(live) != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live service accounts, found %d", len(live))
		}
	})

	if err != nil {
		t.Fatalf("step 2 CreateServiceAccount: %s", safeErr(err))
	}
	firstSecret := created.ClientSecret.Reveal()
	writeLiveSecretFile(t, "secret-1", firstSecret)
	t.Logf("step 2: created service account, secret present=%v", firstSecret != "")

	// Step 4: read it back.
	got, err := client.GetServiceAccount(ctx, &iam.GetServiceAccountInput{ServiceAccountID: serviceAccountID})
	if err != nil {
		t.Fatalf("step 4 GetServiceAccount: %s", safeErr(err))
	}
	t.Logf("step 4: read service account, enabled=%v", got.ServiceAccount.Enabled)

	// Step 5: update its description.
	updated, err := client.UpdateServiceAccount(ctx, &iam.UpdateServiceAccountInput{
		ServiceAccountID: serviceAccountID,
		Description:      vngcloud.Ptr("vngcloud live write test, updated"),
	})
	if err != nil {
		t.Fatalf("step 5 UpdateServiceAccount: %s", safeErr(err))
	}
	t.Logf("step 5: updated service account, description changed=%v", updated.ServiceAccount.Description != got.ServiceAccount.Description)

	// Step 6: reset its secret to a second temp file, and check only whether
	// the two secrets differ, never their values.
	reset, err := client.ResetServiceAccountSecret(ctx, &iam.ResetServiceAccountSecretInput{ServiceAccountID: serviceAccountID})
	if err != nil {
		t.Fatalf("step 6 ResetServiceAccountSecret: %s", safeErr(err))
	}
	secondSecret := reset.ClientSecret.Reveal()
	writeLiveSecretFile(t, "secret-2", secondSecret)
	t.Logf("step 6: reset secret, present=%v, differs from step 2's=%v",
		secondSecret != "", secondSecret != "" && secondSecret != firstSecret)

	// Step 7: delete it explicitly.
	if _, err := client.DeleteServiceAccount(ctx, &iam.DeleteServiceAccountInput{ServiceAccountID: serviceAccountID}); err != nil {
		t.Fatalf("step 7 DeleteServiceAccount: %s", safeErr(err))
	}
	t.Log("step 7: deleted service account")
}

// liveIAMPolicyNamePattern is this test's own naming scheme, the same
// vngcloud-live-<8 hex> form liveIAMServiceAccountNamePattern uses.
var liveIAMPolicyNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveIAMPolicyName(name string) bool {
	return liveIAMPolicyNamePattern.MatchString(name)
}

// listAllLiveIAMPolicies lists every customer policy whose exact name
// matches liveIAMPolicyNamePattern. ListPolicies' default page size is far
// above the account's 20-customer-policy quota, so one call always returns
// every policy there is, live or not.
func listAllLiveIAMPolicies(ctx context.Context, client *iam.Client) ([]iam.PolicySummary, error) {
	out, err := client.ListPolicies(ctx, nil)
	if err != nil {
		return nil, err
	}
	var live []iam.PolicySummary
	for _, p := range out.Items {
		if isLiveIAMPolicyName(p.Name) {
			live = append(live, p)
		}
	}
	return live, nil
}

// deleteLiveIAMPolicies deletes every leftover policy whose exact name
// matches liveIAMPolicyNamePattern, detaching it from every service account
// and group it still holds first, and returns how many it deleted. It never
// touches a managed policy (DeletePolicy's own guard refuses one), the
// caller's IAM user, or a group other than one it detaches from here: this
// test only ever attaches a policy to a service account or a group it
// creates itself.
func deleteLiveIAMPolicies(ctx context.Context, t *testing.T, client *iam.Client) int {
	t.Helper()
	live, err := listAllLiveIAMPolicies(ctx, client)
	if err != nil {
		t.Errorf("delete live iam policies: list: %s", safeErr(err))
		return 0
	}
	deleted := 0
	for _, p := range live {
		attachments, err := client.ListPolicyAttachments(ctx, &iam.ListPolicyAttachmentsInput{PolicyID: p.ID})
		if err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live iam policies: list attachments: %s", safeErr(err))
			continue
		}
		if err != nil {
			continue
		}
		for _, saID := range attachments.ServiceAccountIDs {
			if _, err := client.DetachServiceAccountPolicy(ctx, &iam.DetachServiceAccountPolicyInput{PolicyID: p.ID, ServiceAccountID: saID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("delete live iam policies: detach: %s", safeErr(err))
			}
		}
		for _, group := range attachments.Groups {
			if _, err := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{PolicyID: p.ID, GroupID: group.ID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("delete live iam policies: detach from group: %s", safeErr(err))
			}
		}
		if _, err := client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyID: p.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live iam policies: delete: %s", safeErr(err))
			continue
		}
		deleted++
	}
	return deleted
}

// deleteLiveIAMPolicyByExactName deletes the one customer policy whose Name
// is exactly name, if the account has one, detaching it from any service
// account or group it still holds first. CreatePolicy never returns a
// non-nil Output on error (see CreatePolicy in policies_write.go), so a
// cleanup that must run before any id is known has to search by the name
// this test generated instead.
func deleteLiveIAMPolicyByExactName(ctx context.Context, t *testing.T, client *iam.Client, name string) {
	t.Helper()
	out, err := client.ListPolicies(ctx, nil)
	if err != nil {
		t.Errorf("cleanup: list policies by name: %s", safeErr(err))
		return
	}
	for _, p := range out.Items {
		if p.Name != name {
			continue
		}
		attachments, err := client.ListPolicyAttachments(ctx, &iam.ListPolicyAttachmentsInput{PolicyID: p.ID})
		if err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: list attachments for policy: %s", safeErr(err))
			continue
		}
		if err != nil {
			continue
		}
		for _, saID := range attachments.ServiceAccountIDs {
			if _, err := client.DetachServiceAccountPolicy(ctx, &iam.DetachServiceAccountPolicyInput{PolicyID: p.ID, ServiceAccountID: saID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: detach policy: %s", safeErr(err))
			}
		}
		for _, group := range attachments.Groups {
			if _, err := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{PolicyID: p.ID, GroupID: group.ID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: detach policy from group: %s", safeErr(err))
			}
		}
		if _, err := client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyID: p.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete policy: %s", safeErr(err))
		}
	}
}

// containsPolicyID reports whether policies holds one whose ID is id.
func containsPolicyID(policies []iam.PolicySummary, id string) bool {
	for _, p := range policies {
		if p.ID == id {
			return true
		}
	}
	return false
}

// VNGCLOUD_LIVE_IAM_POLICY must be set to "1" in addition to
// VNGCLOUD_LIVE_WRITE.
//
// It sweeps leftover vngcloud-live-* policies and service accounts from a
// previous run first, policies before accounts so a leftover attachment is
// detached before either delete (step 1); creates a read-only customer
// policy granting one vServer List action on "*" (step 2); updates its
// description (step 3); creates a service account to attach it to (step 4);
// attaches the policy (step 5); lists the attachment both ways to confirm
// it landed (step 6); detaches it (step 7); and deletes both the policy and
// the service account (step 8). It never attaches to, or otherwise
// touches, a managed policy, the caller's own IAM user, or any group.
// Cleanup is registered as soon as the policy's id is known, before any
// later step can fail and skip the explicit deletes. Every step logs only
// statuses, codes, and booleans.
func TestLiveWriteIAMPolicy(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live iam policy write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_IAM_POLICY") != "1" {
		t.Skip("set VNGCLOUD_LIVE_IAM_POLICY=1 to run the live iam policy write test")
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
	client := iam.New(cfg)

	// Step 1: sweep up leftovers from an earlier run.
	leftoverPolicies := deleteLiveIAMPolicies(ctx, t, client)
	leftoverAccounts := deleteLiveIAMServiceAccounts(ctx, t, client)
	t.Logf("step 1: deleted %d leftover polic(ies), %d leftover service account(s)", leftoverPolicies, leftoverAccounts)

	// Step 2: create a read-only customer policy.
	policySuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	policyName := "vngcloud-live-" + policySuffix

	// Register a by-exact-name cleanup before the create call: CreatePolicy
	// never returns a non-nil Output on error, whether the create was
	// rejected outright, left ambiguous by a 5xx or network error, or landed
	// but failed its confirm read, so this is the only way to find and
	// delete a policy that reached the server despite an error below. It
	// runs even if this test never gets past the Fatalf that follows.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteLiveIAMPolicyByExactName(cleanupCtx, t, client, policyName)
	})

	createdPolicy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		Name: policyName,
		Statements: []iam.Statement{
			{Effect: "allow", Actions: []string{"vserver:ListServers"}, Resources: []string{"*"}},
		},
	})
	if err != nil {
		t.Fatalf("step 2 CreatePolicy: %s", safeErr(err))
	}
	policyID := createdPolicy.Policy.ID
	if policyID == "" {
		t.Fatal("step 2: CreatePolicy returned an empty id")
	}

	// Register cleanup as soon as policyID is known, before any later step
	// can fail and skip the explicit deletes below. serviceAccountID is
	// filled in by step 4; the cleanup closure reads it when it runs, after
	// the rest of the test body has finished.
	var serviceAccountID string
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if serviceAccountID != "" {
			if _, err := client.DetachServiceAccountPolicy(cleanupCtx, &iam.DetachServiceAccountPolicyInput{PolicyID: policyID, ServiceAccountID: serviceAccountID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: detach policy: %s", safeErr(err))
			}
		}
		if _, err := client.DeletePolicy(cleanupCtx, &iam.DeletePolicyInput{PolicyID: policyID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete policy: %s", safeErr(err))
		}
		if serviceAccountID != "" {
			if _, err := client.DeleteServiceAccount(cleanupCtx, &iam.DeleteServiceAccountInput{ServiceAccountID: serviceAccountID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: delete service account: %s", safeErr(err))
			}
		}
		livePolicies, err := listAllLiveIAMPolicies(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final policy list: %s", safeErr(err))
		} else {
			t.Logf("cleanup: vngcloud-live polic(ies) remaining: %d", len(livePolicies))
			if len(livePolicies) != 0 {
				t.Errorf("cleanup: expected 0 vngcloud-live policies, found %d", len(livePolicies))
			}
		}
		liveAccounts, err := listAllLiveIAMServiceAccounts(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final service account list: %s", safeErr(err))
			return
		}
		t.Logf("cleanup: vngcloud-live service account(s) remaining: %d", len(liveAccounts))
		if len(liveAccounts) != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live service accounts, found %d", len(liveAccounts))
		}
	})
	t.Logf("step 2: created policy, managed=%v", createdPolicy.Policy.Managed())

	// Step 3: update its description.
	updatedPolicy, err := client.UpdatePolicy(ctx, &iam.UpdatePolicyInput{
		PolicyID:    policyID,
		Description: vngcloud.Ptr("vngcloud live write test, updated"),
	})
	if err != nil {
		t.Fatalf("step 3 UpdatePolicy: %s", safeErr(err))
	}
	t.Logf("step 3: updated policy, statements unchanged=%v", len(updatedPolicy.Policy.Statements) == len(createdPolicy.Policy.Statements))

	// Step 4: create a service account to attach the policy to, with its own
	// independent name suffix.
	saSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 4 generate name suffix: %v", err)
	}
	saName := "vngcloud-live-" + saSuffix

	// Register a by-exact-name cleanup before the create call, as step 2
	// does for the policy: it is the only way to find and delete this
	// service account if the create below leaves Output nil.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteLiveIAMServiceAccountByExactName(cleanupCtx, t, client, saName)
	})

	createdSA, err := client.CreateServiceAccount(ctx, &iam.CreateServiceAccountInput{
		Name:        saName,
		Description: "vngcloud live write test",
	})
	// As in TestLiveWriteIAMServiceAccount, CreateServiceAccount returns a
	// non-nil Output whenever the create request itself succeeded, so
	// serviceAccountID is set from it before this Fatals on err.
	if createdSA == nil {
		t.Fatalf("step 4 CreateServiceAccount: %s", safeErr(err))
	}
	serviceAccountID = createdSA.ServiceAccount.ID
	if serviceAccountID == "" {
		t.Fatalf("step 4: CreateServiceAccount returned an empty id: %s", safeErr(err))
	}
	if err != nil {
		t.Fatalf("step 4 CreateServiceAccount: %s", safeErr(err))
	}
	t.Logf("step 4: created service account, secret present=%v", createdSA.ClientSecret.Reveal() != "")

	// Step 5: attach the policy to the service account.
	if _, err := client.AttachServiceAccountPolicy(ctx, &iam.AttachServiceAccountPolicyInput{PolicyID: policyID, ServiceAccountID: serviceAccountID}); err != nil {
		t.Fatalf("step 5 AttachServiceAccountPolicy: %s", safeErr(err))
	}
	t.Log("step 5: attached policy to service account")

	// Step 6: list the attachment both ways to confirm it landed.
	policyAttachments, err := client.ListPolicyAttachments(ctx, &iam.ListPolicyAttachmentsInput{PolicyID: policyID})
	if err != nil {
		t.Fatalf("step 6 ListPolicyAttachments: %s", safeErr(err))
	}
	saAttachments, err := client.ListServiceAccountPolicies(ctx, &iam.ListServiceAccountPoliciesInput{ServiceAccountID: serviceAccountID})
	if err != nil {
		t.Fatalf("step 6 ListServiceAccountPolicies: %s", safeErr(err))
	}
	t.Logf("step 6: policy shows the attachment=%v, service account shows the attachment=%v",
		slices.Contains(policyAttachments.ServiceAccountIDs, serviceAccountID),
		containsPolicyID(saAttachments.Items, policyID))

	// Step 7: detach it.
	if _, err := client.DetachServiceAccountPolicy(ctx, &iam.DetachServiceAccountPolicyInput{PolicyID: policyID, ServiceAccountID: serviceAccountID}); err != nil {
		t.Fatalf("step 7 DetachServiceAccountPolicy: %s", safeErr(err))
	}
	t.Log("step 7: detached policy from service account")

	// Step 8: delete both explicitly.
	if _, err := client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyID: policyID}); err != nil {
		t.Fatalf("step 8 DeletePolicy: %s", safeErr(err))
	}
	if _, err := client.DeleteServiceAccount(ctx, &iam.DeleteServiceAccountInput{ServiceAccountID: serviceAccountID}); err != nil {
		t.Fatalf("step 8 DeleteServiceAccount: %s", safeErr(err))
	}
	t.Log("step 8: deleted policy and service account")
}

// liveIAMGroupNamePattern is this test's own naming scheme, the same
// vngcloud-live-<8 hex> form liveIAMPolicyNamePattern uses.
var liveIAMGroupNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveIAMGroupName(name string) bool {
	return liveIAMGroupNamePattern.MatchString(name)
}

// listAllLiveIAMGroups lists every group whose exact name matches
// liveIAMGroupNamePattern. ListGroups returns a bare array with no paging,
// well under the account's 20-group quota.
func listAllLiveIAMGroups(ctx context.Context, client *iam.Client) ([]iam.GroupSummary, error) {
	out, err := client.ListGroups(ctx, nil)
	if err != nil {
		return nil, err
	}
	var live []iam.GroupSummary
	for _, g := range out.Items {
		if isLiveIAMGroupName(g.Name) {
			live = append(live, g)
		}
	}
	return live, nil
}

// detachLiveIAMGroupMembers detaches every policy id holds and removes
// every member it has, so a DeleteGroup call on id is never refused with
// ErrInUse. This test only ever adds a random, nonexistent user id as a
// member (never the caller), so a leftover member here is never the caller
// or one of its real groups.
func detachLiveIAMGroupMembers(ctx context.Context, t *testing.T, client *iam.Client, id string) {
	t.Helper()
	got, err := client.GetGroup(ctx, &iam.GetGroupInput{GroupID: id})
	if err != nil && !vngcloud.IsNotFound(err) {
		t.Errorf("delete live iam group: get: %s", safeErr(err))
		return
	}
	if err != nil {
		return
	}
	for _, policyID := range got.Group.PolicyIDs {
		if _, err := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{PolicyID: policyID, GroupID: id}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live iam group: detach policy: %s", safeErr(err))
		}
	}
	for _, userID := range got.Group.UserIDs {
		if _, err := client.RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{GroupID: id, UserID: userID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live iam group: remove member: %s", safeErr(err))
		}
	}
}

// deleteLiveIAMGroups deletes every leftover group whose exact name matches
// liveIAMGroupNamePattern, detaching its policies and removing its members
// first, and returns how many it deleted.
func deleteLiveIAMGroups(ctx context.Context, t *testing.T, client *iam.Client) int {
	t.Helper()
	live, err := listAllLiveIAMGroups(ctx, client)
	if err != nil {
		t.Errorf("delete live iam groups: list: %s", safeErr(err))
		return 0
	}
	deleted := 0
	for _, g := range live {
		detachLiveIAMGroupMembers(ctx, t, client, g.ID)
		if _, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupID: g.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("delete live iam groups: delete: %s", safeErr(err))
			continue
		}
		deleted++
	}
	return deleted
}

// deleteLiveIAMGroupByExactName deletes the one group whose Name is exactly
// name, if the account has one, detaching its policies and removing its
// members first. CreateGroup never returns a non-nil Output on a rejected or
// ambiguous create (the same shape as CreatePolicy; see groups_write.go), so
// a cleanup that must run before any id is known has to search by the name
// this test generated instead.
func deleteLiveIAMGroupByExactName(ctx context.Context, t *testing.T, client *iam.Client, name string) {
	t.Helper()
	out, err := client.ListGroups(ctx, nil)
	if err != nil {
		t.Errorf("cleanup: list groups by name: %s", safeErr(err))
		return
	}
	for _, g := range out.Items {
		if g.Name != name {
			continue
		}
		detachLiveIAMGroupMembers(ctx, t, client, g.ID)
		if _, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupID: g.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete group: %s", safeErr(err))
		}
	}
}

// containsGroupID reports whether groups holds one whose ID is id.
func containsGroupID(groups []iam.GroupSummary, id string) bool {
	for _, g := range groups {
		if g.ID == id {
			return true
		}
	}
	return false
}

// randomLiveUserID returns a random, syntactically valid UUID that this
// account can never have assigned to a real IAM user: the design's decision
// on live membership checks probes AddUserToGroup and RemoveUserFromGroup
// with a random nonexistent id instead of a spare real user, since the only
// real IAM user on the test account is the caller, which the guard already
// protects.
func randomLiveUserID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	buf[6] = (buf[6] & 0x0f) | 0x40 // version 4
	buf[8] = (buf[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16]), nil
}

// VNGCLOUD_LIVE_IAM_GROUP must be set to "1" in addition to
// VNGCLOUD_LIVE_WRITE.
//
// It sweeps leftover vngcloud-live-* groups and policies from a previous
// run first, groups before policies so a leftover group attachment is
// detached before either delete (step 1); creates a read-only customer
// policy granting one vServer List action on "*" (step 2); creates a group
// (step 3); updates its description (step 4); attaches the policy (step 5);
// lists the attachment both ways to confirm it landed (step 6); checks that
// DeleteGroup refuses with iam.ErrInUse while the group still holds the
// policy (step 7); checks that AddUserToGroup with the caller's own id
// refuses with iam.ErrSelfChange (step 8); lists the account's IAM users and
// records, as booleans only, whether the caller's own id appears exactly and
// case-insensitively (step 9); probes AddUserToGroup and RemoveUserFromGroup
// with a random, nonexistent user id and records only their resulting
// status, since the design leaves that server behavior unconfirmed (step
// 10); detaches the policy (step 11); and deletes both the group and the
// policy (step 12). It never touches a managed policy, the caller's own IAM
// user, or any group the caller actually belongs to: the self-change probe
// in step 8 is refused before it ever reaches the server. Cleanup is
// registered as soon as each id is known, before any later step can fail and
// skip the explicit deletes. Every step logs only statuses, codes, and
// booleans, never a name or an id.
func TestLiveWriteIAMGroup(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live iam group write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_IAM_GROUP") != "1" {
		t.Skip("set VNGCLOUD_LIVE_IAM_GROUP=1 to run the live iam group write test")
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
	client := iam.New(cfg)

	// Step 1: sweep up leftovers from an earlier run, groups before policies
	// so a leftover group-to-policy attachment is detached before either
	// delete.
	leftoverGroups := deleteLiveIAMGroups(ctx, t, client)
	leftoverPolicies := deleteLiveIAMPolicies(ctx, t, client)
	t.Logf("step 1: deleted %d leftover group(s), %d leftover polic(ies)", leftoverGroups, leftoverPolicies)

	// Step 2: create a read-only customer policy.
	policySuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate name suffix: %v", err)
	}
	policyName := "vngcloud-live-" + policySuffix

	// Register a by-exact-name cleanup before the create call: CreatePolicy
	// never returns a non-nil Output on error, so this is the only way to
	// find and delete a policy that reached the server despite an error
	// below. It runs even if this test never gets past the Fatalf that
	// follows.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteLiveIAMPolicyByExactName(cleanupCtx, t, client, policyName)
	})

	createdPolicy, err := client.CreatePolicy(ctx, &iam.CreatePolicyInput{
		Name: policyName,
		Statements: []iam.Statement{
			{Effect: "allow", Actions: []string{"vserver:ListServers"}, Resources: []string{"*"}},
		},
	})
	if err != nil {
		t.Fatalf("step 2 CreatePolicy: %s", safeErr(err))
	}
	policyID := createdPolicy.Policy.ID
	if policyID == "" {
		t.Fatal("step 2: CreatePolicy returned an empty id")
	}

	// Register the policy's cleanup and final-state check as soon as
	// policyID is known, before any later step can fail and skip the
	// explicit deletes below. groupID is filled in by step 3; the cleanup
	// closure reads it when it runs, after the rest of the test body has
	// finished.
	var groupID string
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if groupID != "" {
			if _, err := client.DetachGroupPolicy(cleanupCtx, &iam.DetachGroupPolicyInput{PolicyID: policyID, GroupID: groupID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: detach policy from group: %s", safeErr(err))
			}
			detachLiveIAMGroupMembers(cleanupCtx, t, client, groupID)
			if _, err := client.DeleteGroup(cleanupCtx, &iam.DeleteGroupInput{GroupID: groupID}); err != nil && !vngcloud.IsNotFound(err) {
				t.Errorf("cleanup: delete group: %s", safeErr(err))
			}
		}
		if _, err := client.DeletePolicy(cleanupCtx, &iam.DeletePolicyInput{PolicyID: policyID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete policy: %s", safeErr(err))
		}
		liveGroups, err := listAllLiveIAMGroups(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final group list: %s", safeErr(err))
		} else {
			t.Logf("cleanup: vngcloud-live group(s) remaining: %d", len(liveGroups))
			if len(liveGroups) != 0 {
				t.Errorf("cleanup: expected 0 vngcloud-live groups, found %d", len(liveGroups))
			}
		}
		livePolicies, err := listAllLiveIAMPolicies(cleanupCtx, client)
		if err != nil {
			t.Errorf("cleanup: final policy list: %s", safeErr(err))
			return
		}
		t.Logf("cleanup: vngcloud-live polic(ies) remaining: %d", len(livePolicies))
		if len(livePolicies) != 0 {
			t.Errorf("cleanup: expected 0 vngcloud-live policies, found %d", len(livePolicies))
		}
	})
	t.Logf("step 2: created policy, managed=%v", createdPolicy.Policy.Managed())

	// Step 3: create a group, with its own independent name suffix.
	groupSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate name suffix: %v", err)
	}
	groupName := "vngcloud-live-" + groupSuffix

	// Register a by-exact-name cleanup before the create call, as step 2
	// does for the policy: it is the only way to find and delete this group
	// if the create below leaves Output nil.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteLiveIAMGroupByExactName(cleanupCtx, t, client, groupName)
	})

	createdGroup, err := client.CreateGroup(ctx, &iam.CreateGroupInput{Name: groupName, Description: "vngcloud live write test"})
	// As in TestLiveWriteIAMPolicy, CreateGroup returns a non-nil Output only
	// once its own create request succeeded, so groupID is set from it
	// before this Fatals on err.
	if createdGroup == nil {
		t.Fatalf("step 3 CreateGroup: %s", safeErr(err))
	}
	groupID = createdGroup.Group.ID
	if groupID == "" {
		t.Fatalf("step 3: CreateGroup returned an empty id: %s", safeErr(err))
	}
	if err != nil {
		t.Fatalf("step 3 CreateGroup: %s", safeErr(err))
	}
	t.Logf("step 3: created group, mode=%v", createdGroup.Group.Mode)

	// Step 4: update its description.
	updatedGroup, err := client.UpdateGroup(ctx, &iam.UpdateGroupInput{
		GroupID:     groupID,
		Description: vngcloud.Ptr("vngcloud live write test, updated"),
	})
	if err != nil {
		t.Fatalf("step 4 UpdateGroup: %s", safeErr(err))
	}
	t.Logf("step 4: updated group, name unchanged=%v", updatedGroup.Group.Name == createdGroup.Group.Name)

	// Step 5: attach the policy to the group.
	if _, err := client.AttachGroupPolicy(ctx, &iam.AttachGroupPolicyInput{PolicyID: policyID, GroupID: groupID}); err != nil {
		t.Fatalf("step 5 AttachGroupPolicy: %s", safeErr(err))
	}
	t.Log("step 5: attached policy to group")

	// Step 6: list the attachment both ways to confirm it landed.
	groupPolicies, err := client.ListGroupPolicies(ctx, &iam.ListGroupPoliciesInput{GroupID: groupID})
	if err != nil {
		t.Fatalf("step 6 ListGroupPolicies: %s", safeErr(err))
	}
	policyAttachments, err := client.ListPolicyAttachments(ctx, &iam.ListPolicyAttachmentsInput{PolicyID: policyID})
	if err != nil {
		t.Fatalf("step 6 ListPolicyAttachments: %s", safeErr(err))
	}
	t.Logf("step 6: group shows the attachment=%v, policy shows the attachment=%v",
		containsPolicyID(groupPolicies.Items, policyID),
		containsGroupID(policyAttachments.Groups, groupID))

	// Step 7: DeleteGroup must refuse with ErrInUse while the group still
	// holds the policy, sending no request.
	if _, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupID: groupID}); !errors.Is(err, iam.ErrInUse) {
		t.Fatalf("step 7 DeleteGroup: err = %s, want ErrInUse", safeErr(err))
	}
	t.Log("step 7: DeleteGroup refused a non-empty group")

	// Step 8: AddUserToGroup with the caller's own id must refuse as a
	// self-change, sending no request.
	caller, err := client.GetCallerIdentity(ctx, nil)
	if err != nil {
		t.Fatalf("step 8 GetCallerIdentity: %s", safeErr(err))
	}
	if _, err := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{GroupID: groupID, UserID: caller.UserID}); !errors.Is(err, iam.ErrSelfChange) {
		t.Fatalf("step 8 AddUserToGroup(caller): err = %s, want ErrSelfChange", safeErr(err))
	}
	t.Log("step 8: AddUserToGroup refused adding the caller")

	// Step 9: ListUsers and check whether the caller's own userinfo id
	// appears in it, exact and case-insensitive, without logging either id:
	// a mismatch here would mean a target user id could equal the caller's
	// own id in a different case, which the guard's case-insensitive self
	// match (see policy_guard.go) accounts for and an exact-only compare
	// would miss.
	users, err := client.ListUsers(ctx, nil)
	if err != nil {
		t.Fatalf("step 9 ListUsers: %s", safeErr(err))
	}
	var exactMatch, foldMatch bool
	for _, u := range users.Items {
		if u.ID == caller.UserID {
			exactMatch = true
		}
		if strings.EqualFold(u.ID, caller.UserID) {
			foldMatch = true
		}
	}
	t.Logf("step 9: caller id found in ListUsers exact=%v, case-insensitive=%v", exactMatch, foldMatch)

	// Step 10: probe AddUserToGroup and RemoveUserFromGroup with a random,
	// nonexistent user id. The design leaves the server's behavior here
	// unconfirmed, so this only records what happens rather than asserting a
	// specific outcome; RemoveUserFromGroup always runs afterward to leave
	// no membership behind, whatever the add did.
	randomUserID, err := randomLiveUserID()
	if err != nil {
		t.Fatalf("step 10 generate random user id: %v", err)
	}
	_, addErr := client.AddUserToGroup(ctx, &iam.AddUserToGroupInput{GroupID: groupID, UserID: randomUserID})
	t.Logf("step 10: AddUserToGroup(random nonexistent user) = %s", safeErr(addErr))
	_, removeErr := client.RemoveUserFromGroup(ctx, &iam.RemoveUserFromGroupInput{GroupID: groupID, UserID: randomUserID})
	t.Logf("step 10: RemoveUserFromGroup(random nonexistent user) = %s", safeErr(removeErr))

	// Step 11: detach the policy.
	if _, err := client.DetachGroupPolicy(ctx, &iam.DetachGroupPolicyInput{PolicyID: policyID, GroupID: groupID}); err != nil {
		t.Fatalf("step 11 DetachGroupPolicy: %s", safeErr(err))
	}
	t.Log("step 11: detached policy from group")

	// Step 12: delete both explicitly.
	if _, err := client.DeleteGroup(ctx, &iam.DeleteGroupInput{GroupID: groupID}); err != nil {
		t.Fatalf("step 12 DeleteGroup: %s", safeErr(err))
	}
	if _, err := client.DeletePolicy(ctx, &iam.DeletePolicyInput{PolicyID: policyID}); err != nil {
		t.Fatalf("step 12 DeletePolicy: %s", safeErr(err))
	}
	t.Log("step 12: deleted group and policy")
}

// createLiveVirtualIPAddress reconciles an ambiguous POST through the
// existing private virtual IP list and get calls.
func createLiveVirtualIPAddress(ctx context.Context, client *network.Client, c *core.Client, subnetID, name string) (network.VirtualIPAddress, error) {
	projectID, err := c.RequireProjectID(ctx)
	if err != nil {
		return network.VirtualIPAddress{}, err
	}
	var resp struct {
		Data struct {
			UUID string `json:"uuid"`
		} `json:"data"`
	}
	req := transport.Request{
		Operation: "tagging.live.CreateVirtualIPAddress",
		Method:    http.MethodPost,
		URL:       c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v2", Parts: []string{projectID, "virtualIpAddress"}}),
		Body:      map[string]any{"name": name, "subnetId": subnetID, "mode": "Active/Active"},
		OK:        []int{200, 201, 202},
	}
	if err := c.DoJSON(ctx, req, &resp); err != nil {
		vip, reconcileErr := findLiveVirtualIPAddress(ctx, client, name, subnetID)
		if reconcileErr != nil {
			return network.VirtualIPAddress{}, fmt.Errorf("create virtual IP: %w; reconcile: %w", err, reconcileErr)
		}
		return vip, nil
	}
	if resp.Data.UUID == "" {
		if _, err := findLiveVirtualIPAddress(ctx, client, name, subnetID); err != nil {
			return network.VirtualIPAddress{}, fmt.Errorf("create virtual IP returned an empty id: %w", err)
		}
		return network.VirtualIPAddress{}, errors.New("create virtual IP returned an empty id")
	}
	vip, err := findLiveVirtualIPAddress(ctx, client, name, subnetID)
	if err != nil {
		return network.VirtualIPAddress{}, err
	}
	if vip.UUID != resp.Data.UUID {
		return network.VirtualIPAddress{}, fmt.Errorf("create virtual IP id did not match the reconciled id")
	}
	return vip, nil
}

var errLiveVirtualIPAddressNotFound = errors.New("live virtual IP address not found")

var liveVirtualIPAddressNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func findLiveVirtualIPAddress(ctx context.Context, client *network.Client, name, subnetID string) (network.VirtualIPAddress, error) {
	var found []network.VirtualIPAddress
	complete := false
	for page := 1; page <= 1000; page++ {
		out, err := client.ListVirtualIPAddresses(ctx, &network.ListVirtualIPAddressesInput{Name: name, Page: page})
		if err != nil {
			return network.VirtualIPAddress{}, err
		}
		if page == 1 && len(out.Items) == 0 && out.TotalPage == 0 && out.TotalItem == 0 {
			return network.VirtualIPAddress{}, errLiveVirtualIPAddressNotFound
		}
		for _, vip := range out.Items {
			if vip.Name != name || vip.SubnetID != subnetID {
				continue
			}
			if vip.UUID == "" {
				return network.VirtualIPAddress{}, errors.New("listed virtual IP has an empty id")
			}
			detail, err := client.GetVirtualIPAddress(ctx, &network.GetVirtualIPAddressInput{VirtualIPAddressID: vip.UUID})
			if err != nil {
				return network.VirtualIPAddress{}, err
			}
			if detail.VirtualIPAddress.UUID != vip.UUID || detail.VirtualIPAddress.Name != name || detail.VirtualIPAddress.SubnetID != subnetID {
				return network.VirtualIPAddress{}, errors.New("virtual IP detail did not match the exact name and subnet")
			}
			if detail.VirtualIPAddress.Type != "private" {
				return network.VirtualIPAddress{}, errors.New("virtual IP is not private")
			}
			if len(detail.VirtualIPAddress.AddressPairIPs) != 0 {
				return network.VirtualIPAddress{}, errors.New("virtual IP has address pair attachments")
			}
			pairs, err := client.ListAddressPairsByVirtualIPAddress(ctx, &network.ListAddressPairsByVirtualIPAddressInput{VirtualIPAddressID: vip.UUID})
			if err != nil {
				return network.VirtualIPAddress{}, err
			}
			if len(pairs.Items) != 0 {
				return network.VirtualIPAddress{}, errors.New("virtual IP has address pair attachments")
			}
			found = append(found, detail.VirtualIPAddress)
		}
		if out.TotalPage < page {
			return network.VirtualIPAddress{}, errors.New("virtual IP list returned an incomplete page count")
		}
		if page >= out.TotalPage {
			complete = true
			break
		}
	}
	if !complete {
		return network.VirtualIPAddress{}, errors.New("virtual IP list did not finish")
	}
	if len(found) == 0 {
		return network.VirtualIPAddress{}, errLiveVirtualIPAddressNotFound
	}
	if len(found) != 1 {
		return network.VirtualIPAddress{}, fmt.Errorf("found %d virtual IPs with the exact name and subnet, want 1", len(found))
	}
	return found[0], nil
}

func deleteLiveVirtualIPAddress(ctx context.Context, c *core.Client, vip network.VirtualIPAddress) error {
	if err := validateLiveVirtualIPAddressDelete(vip); err != nil {
		return err
	}
	projectID, err := c.RequireProjectID(ctx)
	if err != nil {
		return err
	}
	req := transport.Request{
		Operation: "tagging.live.DeleteVirtualIPAddress",
		Method:    http.MethodDelete,
		URL:       c.RouteURL(routes.Route{Product: routes.ProductVServer, Version: "v2", Parts: []string{projectID, "virtualIpAddress", vip.UUID}}),
		OK:        []int{200, 202, 204},
	}
	return c.DoJSON(ctx, req, nil)
}

func validateLiveVirtualIPAddressDelete(vip network.VirtualIPAddress) error {
	if err := core.CheckPathID("tagging.live.DeleteVirtualIPAddress", "VirtualIPAddressID", vip.UUID); err != nil {
		return err
	}
	if !liveVirtualIPAddressNamePattern.MatchString(vip.Name) || vip.SubnetID == "" || vip.Type != "private" || len(vip.AddressPairIPs) != 0 {
		return errors.New("refuse to delete a virtual IP that is not this test's unattached private resource")
	}
	return nil
}

func deleteLiveVirtualIPAddressByExactNameAndSubnet(ctx context.Context, t *testing.T, client *network.Client, c *core.Client, name, subnetID string) {
	t.Helper()
	vip, err := findLiveVirtualIPAddress(ctx, client, name, subnetID)
	if errors.Is(err, errLiveVirtualIPAddressNotFound) || vngcloud.IsNotFound(err) {
		return
	}
	if err != nil {
		t.Errorf("cleanup: reconcile virtual IP: %s", safeErr(err))
		return
	}
	if err := deleteLiveVirtualIPAddress(ctx, c, vip); err != nil && !vngcloud.IsNotFound(err) {
		t.Errorf("cleanup: delete virtual IP: %s", safeErr(err))
	}
}

func systemTagSet(tags []tagging.Tag) map[tagging.Tag]int {
	out := make(map[tagging.Tag]int)
	for _, tag := range tags {
		if tag.SystemTag {
			out[tag]++
		}
	}
	return out
}

func systemTagsEqual(a, b []tagging.Tag) bool {
	return reflect.DeepEqual(systemTagSet(a), systemTagSet(b))
}

func TestSystemTagsEqual(t *testing.T) {
	before := []tagging.Tag{
		{Key: "vng.zone", Value: "hcm-3", SystemTag: true},
		{Key: "vng.region", Value: "hcm", SystemTag: true},
		{Key: "vng.createdBy", Value: "system", SystemTag: true},
	}
	after := []tagging.Tag{
		{Key: "vng.createdBy", Value: "system", SystemTag: true},
		{Key: "vng.region", Value: "hcm", SystemTag: true},
		{Key: "vng.zone", Value: "hcm-3", SystemTag: true},
	}
	if !systemTagsEqual(before, after) {
		t.Fatal("systemTagsEqual = false, want equal system tag sets")
	}
	after[0].Value = "changed"
	if systemTagsEqual(before, after) {
		t.Fatal("systemTagsEqual = true, want false for a changed system tag value")
	}
	after = append([]tagging.Tag(nil), before...)
	after[0].CreatedAt = "changed"
	if systemTagsEqual(before, after) {
		t.Fatal("systemTagsEqual = true, want false for a changed system tag timestamp")
	}
	after = append(after, before[0])
	if systemTagsEqual(before, after) {
		t.Fatal("systemTagsEqual = true, want false for a duplicate system tag")
	}
}

func TestFindLiveVirtualIPAddressEmptyPageIsNotFound(t *testing.T) {
	client := network.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"listData":[],"page":1,"pageSize":25,"totalPage":0,"totalItem":0}`))
	})))

	_, err := findLiveVirtualIPAddress(context.Background(), client, "vngcloud-live-0123abcd", "subnet-1")
	if !errors.Is(err, errLiveVirtualIPAddressNotFound) {
		t.Fatalf("findLiveVirtualIPAddress() error = %v, want errLiveVirtualIPAddressNotFound", err)
	}
}

func TestFindLiveVirtualIPAddressRefusesNonPrivateBeforeAddressPairs(t *testing.T) {
	client := network.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/project-1/virtualIpAddress":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"vip-1","name":"vngcloud-live-0123abcd","subnetId":"subnet-1"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":1}`))
		case "/v2/project-1/virtualIpAddress/vip-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"vip-1","name":"vngcloud-live-0123abcd","subnetId":"subnet-1","type":"public"}}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})))

	if _, err := findLiveVirtualIPAddress(context.Background(), client, "vngcloud-live-0123abcd", "subnet-1"); err == nil {
		t.Fatal("findLiveVirtualIPAddress() error = nil, want non-private refusal")
	}
}

func TestFindLiveVirtualIPAddressRefusesAttachedResource(t *testing.T) {
	client := network.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/project-1/virtualIpAddress":
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"vip-1","name":"vngcloud-live-0123abcd","subnetId":"subnet-1"}],"page":1,"pageSize":1,"totalPage":1,"totalItem":1}`))
		case "/v2/project-1/virtualIpAddress/vip-1":
			_, _ = w.Write([]byte(`{"data":{"uuid":"vip-1","name":"vngcloud-live-0123abcd","subnetId":"subnet-1","type":"private","addressPairIps":["pair"]}}`))
		default:
			t.Fatalf("unexpected request: %s", r.URL.String())
		}
	})))

	if _, err := findLiveVirtualIPAddress(context.Background(), client, "vngcloud-live-0123abcd", "subnet-1"); err == nil {
		t.Fatal("findLiveVirtualIPAddress() error = nil, want attachment refusal")
	}
}

func TestFindLiveVirtualIPAddressPagesAndValidates(t *testing.T) {
	client := network.New(testutil.NewConfig(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/project-1/virtualIpAddress" {
			if r.URL.Query().Get("page") == "1" {
				_, _ = w.Write([]byte(`{"listData":[{"uuid":"vip-other","name":"other","subnetId":"subnet-1"}],"page":1,"pageSize":1,"totalPage":2,"totalItem":2}`))
				return
			}
			_, _ = w.Write([]byte(`{"listData":[{"uuid":"vip-1","name":"vngcloud-live-0123abcd","subnetId":"subnet-1"}],"page":2,"pageSize":1,"totalPage":2,"totalItem":2}`))
			return
		}
		if r.URL.Path == "/v2/project-1/virtualIpAddress/vip-1" {
			_, _ = w.Write([]byte(`{"data":{"uuid":"vip-1","name":"vngcloud-live-0123abcd","subnetId":"subnet-1","type":"private"}}`))
			return
		}
		if r.URL.Path == "/v2/project-1/virtualIpAddress/vip-1/addressPairs" {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		t.Fatalf("unexpected request: %s", r.URL.String())
	})))

	vip, err := findLiveVirtualIPAddress(context.Background(), client, "vngcloud-live-0123abcd", "subnet-1")
	if err != nil {
		t.Fatalf("findLiveVirtualIPAddress() error = %v", err)
	}
	if vip.UUID != "vip-1" {
		t.Fatalf("UUID = %q, want vip-1", vip.UUID)
	}
}

func TestValidateLiveVirtualIPAddressDeleteRefusesUnsafeResource(t *testing.T) {
	for _, vip := range []network.VirtualIPAddress{
		{Name: "vngcloud-live-0123abcd", SubnetID: "subnet-1", Type: "private"},
		{UUID: "vip-1", Name: "vngcloud-live-0123abcd", SubnetID: "subnet-1", Type: "public"},
		{UUID: "vip-1", Name: "other", SubnetID: "subnet-1", Type: "private"},
	} {
		if err := validateLiveVirtualIPAddressDelete(vip); err == nil {
			t.Fatalf("validateLiveVirtualIPAddressDelete(%+v) error = nil, want refusal", vip)
		}
	}
}

// TestLiveWriteTagging exercises tagging.TagResource and
// tagging.UntagResource against the account named in .env: it creates a
// private virtual IP address in its own VPC and subnet, tags it, edits the
// tag, removes it, and checks throughout that the platform's own system
// tags (vng.zone, vng.region, vng.createdBy, confirmed live) are still on
// the resource, since the tag PUT never sends or touches them.
//
// This branch has no network.CreateVirtualIPAddress or
// network.DeleteVirtualIPAddress yet, so the virtual IP is created and
// deleted directly on the vServer gateway, the same way the type probe
// this test replaces did. It creates its own VPC and subnet with
// createLiveVPCAndSubnet, and never touches a resource without the
// vngcloud-live- prefix; it never associates its subnet with a network ACL
// or route table, so it passes createLiveVPCAndSubnet a nil blocked func.
func TestLiveWriteTagging(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live tagging write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_TAGGING") != "1" {
		t.Skip("set VNGCLOUD_LIVE_TAGGING=1 to run the live tagging write test")
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

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
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

	networkClient := network.New(cfg)
	portalClient := portal.New(cfg)
	taggingClient := tagging.New(cfg)
	coreClient := core.ClientOf(cfg)

	// Step 1: create this run's own VPC and /24 subnet. createLiveVPCAndSubnet
	// registers their cleanup as soon as each id is known. This test never
	// associates its subnet with an ACL or route table, so it passes a nil
	// blocked func.
	_, subnetID := createLiveVPCAndSubnet(ctx, t, networkClient, portalClient, nil)
	t.Log("step 1: created this run's own VPC and /24 subnet")

	// Step 2: create a private virtual IP in that subnet.
	suffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("generate virtual IP name suffix: %v", err)
	}
	vipName := "vngcloud-live-" + suffix
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		deleteLiveVirtualIPAddressByExactNameAndSubnet(cleanupCtx, t, networkClient, coreClient, vipName, subnetID)
	})
	vip, err := createLiveVirtualIPAddress(ctx, networkClient, coreClient, subnetID, vipName)
	if err != nil {
		t.Fatalf("step 2 create virtual IP: %s", safeErr(err))
	}
	vipID := vip.UUID
	if vipID == "" {
		t.Fatal("step 2: reconciled virtual IP has an empty id")
	}
	t.Log("step 2: created a private virtual IP")

	// Step 3: read the tags the create left: the system tags GreenNode's
	// console shows on every resource.
	before, err := taggingClient.ListResourceTags(ctx, &tagging.ListResourceTagsInput{ResourceID: vipID})
	if err != nil {
		t.Fatalf("step 3 ListResourceTags: %s", safeErr(err))
	}
	if len(systemTagSet(before.Items)) == 0 {
		t.Fatal("step 3: no system tag on a freshly created virtual IP, want at least one")
	}
	systemBefore := before.Items

	// Step 4: tag it.
	tagged, err := taggingClient.TagResource(ctx, &tagging.TagResourceInput{
		ResourceID: vipID, ResourceType: tagging.ResourceTypeVirtualIPAddress, Key: "vngcloud-live-tag", Value: "one",
	})
	if err != nil {
		t.Fatalf("step 4 TagResource: %s", safeErr(err))
	}
	if !tagged.Changed {
		t.Fatal("step 4: Changed = false, want true for a new key")
	}
	if !systemTagsEqual(systemBefore, tagged.Tags) {
		t.Fatal("step 4: system tags changed")
	}
	t.Log("step 4: tagged the virtual IP")

	// Step 5: edit the tag.
	edited, err := taggingClient.TagResource(ctx, &tagging.TagResourceInput{
		ResourceID: vipID, ResourceType: tagging.ResourceTypeVirtualIPAddress, Key: "vngcloud-live-tag", Value: "two",
	})
	if err != nil {
		t.Fatalf("step 5 TagResource (edit): %s", safeErr(err))
	}
	if edited.Previous == nil || *edited.Previous != "one" {
		t.Fatalf("step 5: Previous = %v, want \"1\"", edited.Previous)
	}
	if !systemTagsEqual(systemBefore, edited.Tags) {
		t.Fatal("step 5: system tags changed")
	}
	t.Log("step 5: edited the tag")

	// Step 6: untag it.
	untagged, err := taggingClient.UntagResource(ctx, &tagging.UntagResourceInput{
		ResourceID: vipID, ResourceType: tagging.ResourceTypeVirtualIPAddress, Key: "vngcloud-live-tag",
	})
	if err != nil {
		t.Fatalf("step 6 UntagResource: %s", safeErr(err))
	}
	if !untagged.Changed || untagged.Previous == nil || *untagged.Previous != "two" {
		t.Fatalf("step 6: Changed/Previous = %v/%v, want true/\"2\"", untagged.Changed, untagged.Previous)
	}
	if !systemTagsEqual(systemBefore, untagged.Tags) {
		t.Fatal("step 6: system tags changed")
	}
	t.Log("step 6: untagged the virtual IP")

	// Step 7: confirm the system tags read back exactly as step 3 found
	// them, with no user tag left.
	after, err := taggingClient.ListResourceTags(ctx, &tagging.ListResourceTagsInput{ResourceID: vipID})
	if err != nil {
		t.Fatalf("step 7 ListResourceTags: %s", safeErr(err))
	}
	if !systemTagsEqual(systemBefore, after.Items) {
		t.Fatal("step 7: system tags changed")
	}
	if len(after.Items) != len(systemTagSet(systemBefore)) {
		t.Fatal("step 7: user tags remain")
	}
	t.Log("step 7: system tags survived the tag write, edit, and removal; no user tag remains")

	// Step 8: delete the virtual IP; the cleanup above repeats this and
	// tolerates NotFound.
	freshVIP, err := findLiveVirtualIPAddress(ctx, networkClient, vipName, subnetID)
	if err != nil {
		t.Fatalf("step 8 reconcile virtual IP: %s", safeErr(err))
	}
	if err := deleteLiveVirtualIPAddress(ctx, coreClient, freshVIP); err != nil {
		t.Fatalf("step 8 delete virtual IP: %s", safeErr(err))
	}
	t.Log("step 8: deleted the virtual IP")
}

// listAllLogAlarmsPageCap bounds listAllLogAlarms' page walk, the same rule
// listAllChannels applies to its own paging: a server that never returns an
// empty page and never reports a TotalItem the walk can reach would
// otherwise turn a cleanup helper into an infinite loop.
const listAllLogAlarmsPageCap = 1000

// listAllLogAlarms pages through every Log alarm the account has.
func listAllLogAlarms(ctx context.Context, client *monitor.Client) ([]monitor.Alarm, error) {
	var all []monitor.Alarm
	for page := 1; page <= listAllLogAlarmsPageCap; page++ {
		out, err := client.ListAlarms(ctx, &monitor.ListAlarmsInput{Kind: monitor.AlarmKindLog, Page: page})
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

// liveLogAlarmPollInterval and liveLogAlarmPollBound are
// TestLiveWriteMonitorLogAlarm's own cadence and bound for
// pollLogAlarmSettled, matching CreateLogAlarm's own wait so the test's
// direct poll and the SDK's wait describe the same real-world timing.
const (
	liveLogAlarmPollInterval = 2 * time.Second
	liveLogAlarmPollBound    = 60 * time.Second
)

// pollLogAlarmSettled polls for a Log alarm's Status to settle (anything
// but CREATING or UPDATING), by id when id is not empty, else by exact
// name, up to liveLogAlarmPollBound. It exists because CreateLogAlarm's own
// NoWait skips that wait entirely, and the test needs to look at the create
// response's own id separately from whichever alarm the poll eventually
// finds. It returns the last alarm read, whether it settled within the
// bound, and any read error.
func pollLogAlarmSettled(ctx context.Context, client *monitor.Client, id, name string) (monitor.Alarm, bool, error) {
	deadline := time.Now().Add(liveLogAlarmPollBound)
	for {
		var found *monitor.Alarm
		if id != "" {
			out, err := client.GetAlarm(ctx, &monitor.GetAlarmInput{AlarmID: id})
			if err != nil {
				return monitor.Alarm{}, false, err
			}
			found = &out.Alarm
		} else {
			list, err := listAllLogAlarms(ctx, client)
			if err != nil {
				return monitor.Alarm{}, false, err
			}
			for i := range list {
				if list[i].Name == name {
					found = &list[i]
					break
				}
			}
		}
		if found != nil && found.Status != monitor.LogAlarmStatusCreating && found.Status != monitor.LogAlarmStatusUpdating {
			return *found, true, nil
		}
		if !time.Now().Before(deadline) {
			if found != nil {
				return *found, false, nil
			}
			return monitor.Alarm{}, false, nil
		}
		select {
		case <-ctx.Done():
			return monitor.Alarm{}, false, ctx.Err()
		case <-time.After(liveLogAlarmPollInterval):
		}
	}
}

// liveLogAlarmNamePattern is TestLiveWriteMonitorLogAlarm's own naming
// scheme: vngcloud-live-<8 lowercase hex>, exactly, matching the pattern
// every other live monitor write test uses for its own leftovers.
var liveLogAlarmNamePattern = regexp.MustCompile(`^vngcloud-live-[0-9a-f]{8}$`)

func isLiveLogAlarmName(name string) bool {
	return liveLogAlarmNamePattern.MatchString(name)
}

// deleteLogAlarmByName lists Log alarms and deletes any whose Name matches
// name. It is used after a CreateLogAlarm failure, since a POST that
// returned an error may still have reached the server (the design's own
// no-retry rule for that POST is exactly why). It runs on its own timeout,
// not the calling test step's context, so it can still clean up after that
// step's own context is the reason the step failed.
func deleteLogAlarmByName(t *testing.T, client *monitor.Client, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	list, err := listAllLogAlarms(ctx, client)
	if err != nil {
		t.Errorf("cleanup: list log alarms by name: %s", safeErr(err))
		return
	}
	for _, a := range list {
		if a.Name != name {
			continue
		}
		if _, err := client.DeleteLogAlarm(ctx, &monitor.DeleteLogAlarmInput{AlarmID: a.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete log alarm by name: %s", safeErr(err))
		}
	}
}

// TestLiveWriteMonitorLogAlarm exercises CreateLogAlarm, GetAlarm,
// ListAlarms, UpdateLogAlarm, and DeleteLogAlarm against the account named
// in .env.
//
// Unlike the design's own live check, this test never orders a log
// project: the account's Basic class allows only a few log project orders
// or recoveries a month (see monitor-log-alarms.md), so
// VNGCLOUD_LIVE_MONITOR_LOG_PROJECT_ID must name an existing ACTIVE log
// project instead. The test creates, reads, updates, and deletes a log
// alarm and a webhook channel on that project; it never creates, deletes,
// or purges the project itself.
//
// VNGCLOUD_LIVE_MONITOR_LOG_ALARM must be set to "1" in addition to
// VNGCLOUD_LIVE_WRITE, so this test never runs alongside the account's
// other live write tests by accident. VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL
// names the webhook URL the created channel notifies.
//
// It deletes every leftover vngcloud-live-* log alarm and channel first
// (step 1); creates a webhook channel (step 2); creates a frequency log
// alarm named vngcloud-live-<8 hex> with the match-all query, threshold
// 1000, and that channel in InAlarm, with NoWait so the test can see the
// create response's own AlarmID before any wait, then polls and settles it
// itself with pollLogAlarmSettled, recording both that presence and how
// long the poll took (step 3); reads it back and records the alarmLog
// shape: whether Log is set, whether Filter is present, and the threshold
// fields (step 4); lists it back to confirm ListAlarms decodes the same
// alarmLog shape a Get does (step 5); updates ThresholdValue and Name,
// confirming LogProjectID round-trips unchanged (step 6); deletes it
// (step 7); deletes it again to record the second delete's error code
// (step 8); and confirms no vngcloud-live-* alarm remains (step 9). Every
// step logs only counts, statuses, field presence, and timings, never the
// alarm's, channel's, or project's id.
func TestLiveWriteMonitorLogAlarm(t *testing.T) {
	if os.Getenv("VNGCLOUD_LIVE_WRITE") != "1" {
		t.Skip("set VNGCLOUD_LIVE_WRITE=1 to run the live monitor log alarm write test")
	}
	if os.Getenv("VNGCLOUD_LIVE_MONITOR_LOG_ALARM") != "1" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_LOG_ALARM=1 to run the live log alarm write test")
	}
	projectID := os.Getenv("VNGCLOUD_LIVE_MONITOR_LOG_PROJECT_ID")
	if projectID == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_LOG_PROJECT_ID to an existing ACTIVE log project's id to run the live log alarm write test; this test never orders one")
	}
	webhookURL := os.Getenv("VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL")
	if webhookURL == "" {
		t.Skip("set VNGCLOUD_LIVE_MONITOR_WEBHOOK_URL to the approved webhook URL to run the live monitor log alarm write test")
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
	// Step 1: delete every leftover vngcloud-live-* log alarm and channel
	// from a previous run.
	leftoverAlarms, err := listAllLogAlarms(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListAlarms: %s", safeErr(err))
	}
	deletedAlarms := 0
	for _, a := range leftoverAlarms {
		if !isLiveLogAlarmName(a.Name) {
			continue
		}
		if _, err := client.DeleteLogAlarm(ctx, &monitor.DeleteLogAlarmInput{AlarmID: a.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover log alarm: %s", safeErr(err))
		}
		deletedAlarms++
	}
	leftoverChannels, err := listAllChannels(ctx, client)
	if err != nil {
		t.Fatalf("step 1 ListChannels: %s", safeErr(err))
	}
	deletedChannels := 0
	for _, ch := range leftoverChannels {
		if !isLiveChannelName(ch.Name) {
			continue
		}
		if _, err := client.DeleteChannel(ctx, &monitor.DeleteChannelInput{ChannelID: ch.ID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Fatalf("step 1 delete leftover channel: %s", safeErr(err))
		}
		deletedChannels++
	}
	t.Logf("step 1: deleted %d leftover log alarm(s) and %d leftover channel(s)", deletedAlarms, deletedChannels)
	// Step 2: create the webhook channel the alarm's InAlarm will name.
	channelSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 2 generate channel name suffix: %v", err)
	}
	channelName := "vngcloud-live-" + channelSuffix
	createdChannel, err := client.CreateChannel(ctx, &monitor.CreateChannelInput{
		Name: channelName, Type: monitor.ChannelTypeWebhook, Address: webhookURL,
	})
	if err != nil {
		deleteChannelByName(t, client, channelName)
		t.Fatalf("step 2 CreateChannel: %s", safeErr(err))
	}
	channelID := createdChannel.Channel.ID
	if channelID == "" {
		deleteChannelByName(t, client, channelName)
		t.Fatal("step 2: CreateChannel returned an empty id")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteChannel(cleanupCtx, &monitor.DeleteChannelInput{ChannelID: channelID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete channel: %s", safeErr(err))
		}
	})
	t.Log("step 2: created the webhook channel")
	// Step 3: create the log alarm: frequency (the default), match-all
	// query (QueryString and Filter both left empty), threshold 1000, this
	// channel in InAlarm, with NoWait, then poll and settle it here, so
	// created.AlarmID reflects only what the create response itself
	// carried.
	alarmSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 3 generate alarm name suffix: %v", err)
	}
	alarmName := "vngcloud-live-" + alarmSuffix
	created, err := client.CreateLogAlarm(ctx, &monitor.CreateLogAlarmInput{
		Name: alarmName, LogProjectID: projectID, ThresholdValue: vngcloud.Ptr(1000.0),
		InAlarm: []string{channelID}, NoWait: true,
	})
	if err != nil {
		deleteLogAlarmByName(t, client, alarmName)
		t.Fatalf("step 3 CreateLogAlarm: %s", safeErr(err))
	}
	t.Logf("step 3: create response carried an id: %v", created.AlarmID != "")
	pollStart := time.Now()
	settled, ok, err := pollLogAlarmSettled(ctx, client, created.AlarmID, alarmName)
	pollElapsed := time.Since(pollStart)
	if err != nil {
		deleteLogAlarmByName(t, client, alarmName)
		t.Fatalf("step 3 poll for settle: %s", safeErr(err))
	}
	if !ok {
		deleteLogAlarmByName(t, client, alarmName)
		t.Fatalf("step 3: alarm did not settle within %s", liveLogAlarmPollBound)
	}
	if settled.ID == "" {
		deleteLogAlarmByName(t, client, alarmName)
		t.Fatal("step 3: settled alarm has no id")
	}
	alarmID := settled.ID
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if _, err := client.DeleteLogAlarm(cleanupCtx, &monitor.DeleteLogAlarmInput{AlarmID: alarmID}); err != nil && !vngcloud.IsNotFound(err) {
			t.Errorf("cleanup: delete log alarm: %s", safeErr(err))
		}
	})
	t.Logf("step 3: settled after %s, status %s", pollElapsed, settled.Status)
	// Step 4: read the alarm back and record its shape.
	read, err := client.GetAlarm(ctx, &monitor.GetAlarmInput{AlarmID: alarmID})
	if err != nil {
		t.Fatalf("step 4 GetAlarm: %s", safeErr(err))
	}
	t.Logf("step 4: Kind=%q, Log present: %v", read.Alarm.Kind, read.Alarm.Log != nil)
	if read.Alarm.Log != nil {
		t.Logf("step 4: LogProjectID matches input: %v, Filter present: %v, ThresholdType=%q, Condition=%q, ThresholdValue=%v, TimeFrame=%v",
			read.Alarm.Log.LogProjectID == projectID, len(read.Alarm.Log.Filter) > 0,
			read.Alarm.Log.ThresholdType, read.Alarm.Log.Condition, read.Alarm.Log.ThresholdValue, read.Alarm.Log.TimeFrame)
	}
	// Step 5: list it back to confirm ListAlarms decodes the same alarmLog
	// shape a Get does.
	listed, err := client.ListAlarms(ctx, &monitor.ListAlarmsInput{Kind: monitor.AlarmKindLog, Name: alarmName})
	if err != nil {
		t.Fatalf("step 5 ListAlarms: %s", safeErr(err))
	}
	foundInList := false
	for _, item := range listed.Items {
		if item.Name == alarmName {
			foundInList = true
			t.Logf("step 5: list item Log present: %v", item.Log != nil)
		}
	}
	if !foundInList {
		t.Fatal("step 5: created alarm not found in ListAlarms by name")
	}
	// Step 6: update the threshold and the name; confirm LogProjectID
	// round-trips unchanged.
	renameSuffix, err := randomHex(4)
	if err != nil {
		t.Fatalf("step 6 generate rename suffix: %v", err)
	}
	newName := "vngcloud-live-" + renameSuffix
	updated, err := client.UpdateLogAlarm(ctx, &monitor.UpdateLogAlarmInput{
		AlarmID: alarmID, Name: &newName, ThresholdValue: vngcloud.Ptr(2000.0),
	})
	if err != nil {
		t.Fatalf("step 6 UpdateLogAlarm: %s", safeErr(err))
	}
	if updated.Alarm.Name != newName {
		t.Fatal("step 6: UpdateLogAlarm did not change Name")
	}
	if updated.Alarm.Log == nil || updated.Alarm.Log.ThresholdValue != 2000 {
		t.Fatal("step 6: ThresholdValue did not change to 2000")
	}
	if updated.Alarm.Log.LogProjectID != projectID {
		t.Fatal("step 6: LogProjectID changed, want unchanged")
	}
	t.Log("step 6: updated threshold and name; LogProjectID unchanged")
	// Step 7: delete the alarm explicitly, so the cleanup above finds it
	// already gone.
	if _, err := client.DeleteLogAlarm(ctx, &monitor.DeleteLogAlarmInput{AlarmID: alarmID}); err != nil && !vngcloud.IsNotFound(err) {
		t.Fatalf("step 7 DeleteLogAlarm: %s", safeErr(err))
	}
	t.Log("step 7: deleted the alarm")
	// Step 8: delete it again, to record the second delete's own error
	// code; only the code is logged, since the message may name the
	// alarm's id.
	_, secondDeleteErr := client.DeleteLogAlarm(ctx, &monitor.DeleteLogAlarmInput{AlarmID: alarmID})
	switch {
	case secondDeleteErr == nil:
		t.Log("step 8: second delete succeeded without error")
	case vngcloud.IsNotFound(secondDeleteErr):
		t.Logf("step 8: second delete returned NotFound, code %s", vngcloud.ErrorCode(secondDeleteErr))
	default:
		t.Fatalf("step 8: second delete: unexpected code %s", vngcloud.ErrorCode(secondDeleteErr))
	}
	// Step 9: confirm no vngcloud-live-* log alarm remains.
	final, err := listAllLogAlarms(ctx, client)
	if err != nil {
		t.Fatalf("step 9 final ListAlarms: %s", safeErr(err))
	}
	remaining := 0
	for _, a := range final {
		if isLiveLogAlarmName(a.Name) {
			remaining++
		}
	}
	t.Logf("step 9: vngcloud-live log alarms remaining: %d", remaining)
	if remaining != 0 {
		t.Fatalf("step 9: expected 0 vngcloud-live log alarms, found %d", remaining)
	}
}
