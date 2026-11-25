//go:build livewrite

package livetest_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

var errNATHoldTimeout = errors.New("NAT hold timed out")
var errNATHoldUnlink = errors.New("NAT hold IDs removal failed")
var errNATHoldPanic = errors.New("NAT hold failed")

func runNATPostActive(hold func() error, reconcile func(context.Context), report func(error)) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
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

func TestNATHold(t *testing.T) {
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
	runNATPostActive(func() error {
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
		runNATPostActive(func() error {
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

func TestNATMaxPrice(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  float64
		valid bool
	}{{"", 1000000, true}, {"123", 123, true}, {"1000000", 1000000, true}, {"0", 0, false}, {"1000001", 0, false}, {"1.5", 0, false}, {"-1", 0, false}} {
		got, err := natMaxPrice(tc.value)
		if (err == nil) != tc.valid || (tc.valid && got != tc.want) {
			t.Fatalf("%q: %v %v", tc.value, got, err)
		}
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
