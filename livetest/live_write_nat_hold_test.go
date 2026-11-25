//go:build livewrite

package livetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
	"danny.vn/vngcloud/billing"
	"danny.vn/vngcloud/network"
)

var errNATHoldTimeout = errors.New("NAT hold timed out")
var errNATHoldUnlink = errors.New("NAT hold IDs removal failed")
var errNATHoldPanic = errors.New("NAT hold failed")

func natHoldDeadlines(now, deadline time.Time, holdMax time.Duration) (time.Time, time.Time, error) {
	cleanup := deadline.Add(-time.Minute)
	holdEnd := cleanup.Add(-45 * time.Minute)
	if holdEnd.Before(now.Add(40*time.Minute + holdMax)) {
		return time.Time{}, time.Time{}, errors.New("NAT hold requires at least 86 minutes plus the hold maximum; use -timeout=120m")
	}
	return cleanup, holdEnd, nil
}

func recordNATPostOrderCash(report map[string]any, before, after float64, err error) {
	report["afterOrderCashAvailable"] = err == nil
	if err == nil {
		report["afterOrderCash"] = after
		report["debit"] = before - after
	}
}

func runNATPostActive(ctx context.Context, hold func() error, reconcile func(context.Context), report func(error)) {
	var holdErr error
	func() {
		defer func() {
			if recover() != nil {
				holdErr = errNATHoldPanic
			}
		}()
		holdErr = hold()
	}()
	defer func() { report(holdErr) }()
	reconcile(ctx)
}

type natHoldIDs struct {
	NAT            string `json:"nat"`
	VPC            string `json:"vpc"`
	VNetworkRegion string `json:"vnetregion"`
	Project        string `json:"project"`
}

type natHold struct {
	root *os.Root
	max  time.Duration
}

func natMaxPrice(value string) (float64, error) {
	if value == "" {
		return 1000000, nil
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil || n == 0 || n > 1000000 {
		return 0, errors.New("invalid NAT max price")
	}
	return float64(n), nil
}

func newNATHold(dir, value string) (*natHold, error) {
	max := 15 * time.Minute
	if value != "" {
		var err error
		max, err = time.ParseDuration(value)
		if err != nil || max <= 0 || max > 30*time.Minute {
			return nil, errors.New("invalid NAT hold maximum")
		}
	}
	if dir == "" {
		return &natHold{}, nil
	}
	if !filepath.IsAbs(dir) {
		return nil, errors.New("invalid NAT hold directory")
	}
	dir = filepath.Clean(dir)
	before, err := os.Lstat(dir)
	if err != nil || !before.IsDir() || before.Mode().Perm() != 0700 || before.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, errors.New("invalid NAT hold directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("invalid NAT hold directory")
	}
	after, err := root.Stat(".")
	if err != nil {
		_ = root.Close()
		return nil, errors.New("invalid NAT hold directory")
	}
	if !natHoldOwned(after) || !os.SameFile(before, after) || after.Mode().Perm() != 0700 {
		_ = root.Close()
		return nil, errors.New("invalid NAT hold directory")
	}
	for _, name := range []string{"nat-ids.json", "release"} {
		if _, statErr := root.Lstat(name); !os.IsNotExist(statErr) {
			_ = root.Close()
			return nil, errors.New("invalid NAT hold directory")
		}
	}
	probe, probeErr := root.OpenFile(".nat-hold-preflight", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if probeErr != nil {
		_ = root.Close()
		return nil, errors.New("invalid NAT hold directory")
	}
	closeErr := probe.Close()
	removeErr := root.Remove(".nat-hold-preflight")
	if closeErr != nil || removeErr != nil {
		_ = root.Close()
		return nil, errors.New("invalid NAT hold directory")
	}
	return &natHold{root: root, max: max}, nil
}

func (h *natHold) Close() {
	if h.root != nil {
		_ = h.root.Close()
	}
}

func (h *natHold) wait(ctx context.Context, ids natHoldIDs, log func(), sleep func(context.Context, time.Duration) error) (err error) {
	if h.root == nil {
		return nil
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	f, err := h.root.OpenFile("nat-ids.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = errors.Join(err, errNATHoldPanic)
		}
		if removeErr := h.root.Remove("nat-ids.json"); removeErr != nil {
			err = errors.Join(err, errNATHoldUnlink)
		}
	}()
	if err = f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err = f.Write(raw); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	log()
	bounded, cancel := context.WithTimeout(ctx, h.max)
	defer cancel()
	for elapsed := time.Duration(0); ; {
		if err = ctx.Err(); err != nil {
			return err
		}
		if bounded.Err() != nil {
			return errNATHoldTimeout
		}
		info, statErr := h.root.Lstat("release")
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return errors.New("invalid NAT hold release")
			}
			return nil
		}
		if !os.IsNotExist(statErr) {
			return errors.New("invalid NAT hold release")
		}
		if elapsed >= h.max {
			return errNATHoldTimeout
		}
		step := min(2*time.Second, h.max-elapsed)
		if err = sleep(bounded, step); err != nil {
			if ctx.Err() == nil && bounded.Err() != nil {
				return errNATHoldTimeout
			}
			return err
		}
		elapsed += step
	}
}

func natHoldSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func natHoldSupported(goos string) bool { return goos == "darwin" || goos == "linux" }

func requireNATHoldPlatform(t *testing.T) {
	t.Helper()
	if !natHoldSupported(runtime.GOOS) {
		t.Skip("hold unsupported")
	}
}

func TestNATHold(t *testing.T) {
	requireNATHoldPlatform(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, filepath.Join(dir, "missing"), "."} {
		if _, err := newNATHold(path, ""); err == nil {
			t.Fatalf("accepted directory %q", path)
		}
	}
	bad := t.TempDir()
	if err := os.Chmod(bad, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := newNATHold(bad, ""); err == nil {
		t.Fatal("accepted non-private directory")
	}
	for _, max := range []string{"bad", "0s", "31m"} {
		if _, err := newNATHold(dir, max); err == nil {
			t.Fatalf("accepted %q", max)
		}
	}
	h, err := newNATHold(dir, "4s")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ids := natHoldIDs{NAT: "n", VPC: "v", VNetworkRegion: "r", Project: "p"}
	calls := 0
	err = h.wait(context.Background(), ids, func() {}, func(context.Context, time.Duration) error {
		calls++
		raw, readErr := os.ReadFile(filepath.Join(dir, "nat-ids.json"))
		if readErr != nil || string(raw) != `{"nat":"n","vpc":"v","vnetregion":"r","project":"p"}` {
			t.Fatalf("content %q: %v", raw, readErr)
		}
		info, statErr := os.Stat(filepath.Join(dir, "nat-ids.json"))
		if statErr != nil || info.Mode().Perm() != 0600 {
			t.Fatal("bad file mode")
		}
		return nil
	})
	if !errors.Is(err, errNATHoldTimeout) || calls != 2 {
		t.Fatalf("timeout: %v, calls %d", err, calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "nat-ids.json")); !os.IsNotExist(err) {
		t.Fatal("ids not removed")
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := h.wait(context.Background(), ids, func() {}, func(context.Context, time.Duration) error { t.Fatal("waited after release"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "release")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := h.wait(ctx, ids, func() {}, func(ctx context.Context, _ time.Duration) error { return ctx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestNATHoldFreshDirectory(t *testing.T) {
	requireNATHoldPlatform(t)
	for _, name := range []string{"release", "nat-ids.json"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
				t.Fatal(err)
			}
			h, err := newNATHold(dir, "")
			if err == nil {
				h.Close()
				t.Fatal("accepted stale directory")
			}
		})
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	h, err := newNATHold(link+string(os.PathSeparator), "")
	if err == nil {
		h.Close()
		t.Fatal("accepted trailing slash symlink")
	}
}

func TestNATHoldRejectsNonRegularRelease(t *testing.T) {
	requireNATHoldPlatform(t)
	for _, symlink := range []bool{false, true} {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		h, err := newNATHold(dir, "2s")
		if err != nil {
			t.Fatal(err)
		}
		err = h.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error {
			path := filepath.Join(dir, "release")
			if symlink {
				return os.Symlink("nat-ids.json", path)
			}
			return os.Mkdir(path, 0700)
		})
		h.Close()
		if err == nil {
			t.Fatal("accepted nonregular release")
		}
	}
}

func TestNATHoldUnlinkFailure(t *testing.T) {
	requireNATHoldPlatform(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h, err := newNATHold(dir, "2s")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	err = h.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error {
		if err := os.Remove(filepath.Join(dir, "nat-ids.json")); err != nil {
			t.Fatal(err)
		}
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errNATHoldUnlink) {
		t.Fatalf("missing unlink error: %v", err)
	}
}

func TestNATHoldPanicAndUnlink(t *testing.T) {
	requireNATHoldPlatform(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h, err := newNATHold(dir, "2s")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	var result error
	runNATPostActive(context.Background(), func() error {
		return h.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error {
			if err := os.Remove(filepath.Join(dir, "nat-ids.json")); err != nil {
				t.Fatal(err)
			}
			panic("private")
		})
	}, func(context.Context) {}, func(err error) { result = err })
	if !errors.Is(result, errNATHoldPanic) || !errors.Is(result, errNATHoldUnlink) {
		t.Fatalf("missing panic or unlink error: %v", result)
	}
}

func TestNATPostActiveOrder(t *testing.T) {
	for _, panicHold := range []bool{false, true} {
		var steps []string
		var err error
		runNATPostActive(context.Background(), func() error {
			steps = append(steps, "hold")
			if panicHold {
				panic("private")
			}
			return errNATHoldTimeout
		}, func(ctx context.Context) {
			if ctx.Err() != nil {
				t.Fatal("delete context canceled")
			}
			steps = append(steps, "delete", "reconcile")
		}, func(e error) { err = e; steps = append(steps, "report") })
		if err == nil || len(steps) != 4 || steps[0] != "hold" || steps[1] != "delete" || steps[2] != "reconcile" || steps[3] != "report" {
			t.Fatalf("order %v error %v", steps, err)
		}
	}
}

func runNATTransportScenario(t *testing.T, writeFailure, createFailed bool) {
	t.Helper()
	var deletes, natReads, routeReads, billingReads int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := ""
		switch {
		case strings.HasSuffix(r.URL.Path, "/v3-whitelist"):
			body = `{"code":200,"success":true,"data":{"enabledForAll":true,"whitelistedPortalUserIds":[]}}`
		case strings.HasSuffix(r.URL.Path, "/vpcs"):
			body = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"vpc-1","projectId":"66b500000000000000000002","regionId":"66b500000000000000000001","zones":[{"uuid":"HAN01-1B"}],"projectUuid":"pro-00000000-0000-0000-0000-000000000001","regionUuid":"66b500000000000000000001"}]}`
		case strings.HasSuffix(r.URL.Path, "/nats/nat-1") && r.Method == http.MethodDelete:
			if r.Context().Err() != nil {
				t.Error("delete context canceled")
			}
			deletes++
			body = `{"code":0,"success":true}`
		case strings.HasSuffix(r.URL.Path, "/nats"):
			natReads++
			if deletes == 0 {
				body = `{"success":true,"page":1,"size":1000,"totalPage":1,"total":1,"data":[{"uuid":"nat-1","natName":"example","status":"ACTIVE","projectUuid":"pro-00000000-0000-0000-0000-000000000001","zoneUuid":"HAN01-1B","natPackage":{"uuid":"package-1"},"vpc":{"uuid":"vpc-1","projectId":"66b500000000000000000002","projectUuid":null,"regionUuid":null,"regionId":"66b500000000000000000001"}}]}`
			} else {
				body = `{"success":true,"page":1,"size":1000,"totalPage":0,"total":0,"data":[]}`
			}
		case strings.HasSuffix(r.URL.Path, "/route-table"):
			routeReads++
			body = `{"listData":[],"page":1,"pageSize":1000,"totalPage":0,"totalItem":0}`
		case r.URL.Path == "/gateway/api/v1/home/user-info":
			body = `{"code":200,"data":{"userId":123456}}`
		case r.URL.Path == "/gateway/api/v1/resources":
			billingReads++
			body = `{"code":200,"data":{"data":[]}}`
		case strings.Contains(r.URL.Path, "/navbar/balances/v1"):
			body = `{"code":200,"data":{"cash":100,"poc":null,"cashAvailable":null,"cashHolding":null,"pocHolding":null}}`
		case strings.HasSuffix(r.URL.Path, "/networks/vpc-1"):
			w.WriteHeader(http.StatusNotFound)
			return
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer api.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accessToken":"synthetic-token","expiresIn":3600}`))
	}))
	defer token.Close()
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			http.Redirect(w, r, token.URL+"/callback?code=synthetic-code", http.StatusFound)
		} else {
			_, _ = w.Write([]byte(`<input name="_csrf" value="synthetic-csrf">`))
		}
	}))
	defer signin.Close()
	natID := "nat-1"
	deleteTransport := &natDeleteTransport{base: http.DefaultTransport, natID: &natID}
	cfg, err := vngcloud.NewConfig(vngcloud.WithTransport(deleteTransport), vngcloud.WithRegion("han-1"), vngcloud.WithProjectID("pro-00000000-0000-0000-0000-000000000001"), vngcloud.WithRetry(0, 0), vngcloud.WithIAMUser(&vngcloud.IAMUserAuth{RootEmail: "root@example.test", Username: "user", Password: "synthetic", SigninBaseURL: signin.URL, TokenURL: token.URL, DashboardURI: token.URL + "/"}), vngcloud.WithEndpointOverrides(vngcloud.EndpointOverrides{VNetwork: api.URL + "/", Billing: api.URL + "/", VServer: api.URL + "/"}))
	if err != nil {
		t.Fatal(err)
	}
	dir := ""
	if !createFailed {
		dir = t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	h, err := newNATHold(dir, "2s")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if writeFailure {
		if err := os.WriteFile(filepath.Join(dir, "nat-ids.json"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	after, cashErr, cashRead := float64(80), error(nil), true
	natGone, vpcGone := false, false
	report := map[string]any{}
	createOut := &network.CreateNATInstanceOutput{NATInstance: &network.NATInstance{UUID: natID, Status: "ACTIVE"}}
	var createErr error
	if createFailed {
		createErr = context.DeadlineExceeded
	}
	state := natReconcileState{client: network.New(cfg), money: billing.New(cfg), report: report, zone: "66b500000000000000000001", vpc: "vpc-1", nat: createOut.NATInstance.UUID, name: "example", project: "pro-00000000-0000-0000-0000-000000000001", pkg: "package-1", before: 100, after: &after, cashErr: &cashErr, cashRead: &cashRead, natGone: &natGone, vpcGone: &vpcGone}
	var holdErr, reconcileErr error
	opCtx, cancelOp := context.WithCancel(context.Background())
	if createFailed {
		cancelOp()
		if err := state.run(opCtx); err == nil || deleteTransport.sent.Load() {
			t.Fatal("canceled pre-delete read marked a DELETE as sent")
		}
	}
	defer cancelOp()
	reconcileCtx, cancelReconcile := natReconcileContext(opCtx, time.Time{}, createErr == nil && createOut.NATInstance.Status == "ACTIVE")
	defer cancelReconcile()
	runNATPostActive(reconcileCtx, func() error {
		if createFailed {
			return nil
		}
		return h.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error { return nil })
	}, func(ctx context.Context) { reconcileErr = state.run(ctx) }, func(err error) {
		if deletes != 1 || !deleteTransport.sent.Load() || natReads < 3 || routeReads != 1 || billingReads != 1 || !natGone || !vpcGone {
			t.Error("hold failure reported before reconciliation completed")
		}
		holdErr = err
	})
	if !createFailed && (holdErr == nil || (!writeFailure && !errors.Is(holdErr, errNATHoldTimeout))) {
		t.Fatalf("hold error: %v", holdErr)
	}
	if reconcileErr != nil {
		t.Fatalf("reconcile: %v", reconcileErr)
	}
	if deletes != 1 || !deleteTransport.valid() || natReads < 3 || routeReads != 1 || billingReads != 1 || !natGone || !vpcGone {
		t.Fatalf("counts %d %d %d %d gone %v %v", deletes, natReads, routeReads, billingReads, natGone, vpcGone)
	}
}

func TestNATRealPostActiveTransport(t *testing.T) {
	requireNATHoldPlatform(t)
	for _, writeFailure := range []bool{false, true} {
		t.Run(strconv.FormatBool(writeFailure), func(t *testing.T) {
			runNATTransportScenario(t, writeFailure, false)
		})
	}
}

type natTestTransport func(*http.Request) (*http.Response, error)

func (f natTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNATDeleteTransportAttempt(t *testing.T) {
	id := "nat-1"
	transport := &natDeleteTransport{natID: &id, base: natTestTransport(func(*http.Request) (*http.Response, error) { return nil, errors.New("ambiguous") })}
	if transport.sent.Load() {
		t.Fatal("attempted before request")
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.test/nats/nat-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = transport.RoundTrip(req)
	if transport.sent.Load() {
		t.Fatal("GET marked as delete")
	}
	req.Method = http.MethodDelete
	_, _ = transport.RoundTrip(req)
	if !transport.sent.Load() {
		t.Fatal("handed-off DELETE was not recorded")
	}
}

func TestNATFailedCreateFreshCleanup(t *testing.T) {
	runNATTransportScenario(t, false, true)
}

func TestNATDefaultBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	runNATPostActive(ctx, func() error { return nil }, func(got context.Context) {
		wantDeadline, _ := ctx.Deadline()
		gotDeadline, _ := got.Deadline()
		if !gotDeadline.Equal(wantDeadline) {
			t.Fatal("default reconciliation deadline changed")
		}
	}, func(error) {})
}

func TestNATHoldPlatformSupport(t *testing.T) {
	for _, tc := range []struct {
		goos      string
		supported bool
	}{{"windows", false}, {"darwin", true}, {"linux", true}, {"freebsd", false}} {
		if natHoldSupported(tc.goos) != tc.supported {
			t.Fatalf("platform %s", tc.goos)
		}
	}
}

func TestNATHoldBudget(t *testing.T) {
	now := time.Unix(0, 0)
	if _, _, err := natHoldDeadlines(now, now.Add(100*time.Minute), 15*time.Minute); err == nil {
		t.Fatal("short timeout accepted")
	}
	cleanup, holdEnd, err := natHoldDeadlines(now, now.Add(120*time.Minute), 15*time.Minute)
	if err != nil || !cleanup.Equal(now.Add(119*time.Minute)) || !holdEnd.Equal(now.Add(74*time.Minute)) {
		t.Fatalf("deadlines %v %v: %v", cleanup, holdEnd, err)
	}
}

func TestNATRecordPostOrderCash(t *testing.T) {
	r := map[string]any{}
	recordNATPostOrderCash(r, 100, 80, nil)
	if r["afterOrderCashAvailable"] != true || r["afterOrderCash"] != float64(80) || r["debit"] != float64(20) {
		t.Fatal(r)
	}
	recordNATPostOrderCash(r, 100, 0, errors.New("unavailable"))
	if r["afterOrderCashAvailable"] != false {
		t.Fatal(r)
	}
}

func TestNATHoldDisabled(t *testing.T) {
	h, err := newNATHold("", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.wait(context.Background(), natHoldIDs{}, func() { t.Fatal("logged") }, func(context.Context, time.Duration) error { t.Fatal("waited"); return nil }); err != nil {
		t.Fatal(err)
	}
}
