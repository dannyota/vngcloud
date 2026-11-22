//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSharedCredentialsLockHelper(t *testing.T) {
	if os.Getenv("VNG_CONFIG_LOCK_HELPER") != "1" {
		return
	}
	root := newRootCmd(nil, io.Discard, io.Discard)
	root.SetArgs([]string{"configure", "set", os.Getenv("VNG_CONFIG_LOCK_KEY"), os.Getenv("VNG_CONFIG_LOCK_VALUE")})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureSharedCredentialsWriters(t *testing.T) {
	home := withCleanEnv(t)
	dir := filepath.Join(home, "shared")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := filepath.Join(dir, "credentials")
	f, err := os.OpenFile(credentials+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		}
	}()
	done := make(chan error, 2)
	for i, key := range []string{"username", "root_email"} {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSharedCredentialsLockHelper$") //nolint:gosec // Re-executes this test binary with fixed arguments.
		cmd.Env = append(os.Environ(), "VNG_CONFIG_LOCK_HELPER=1", "VNG_CONFIG_LOCK_KEY="+key, "VNG_CONFIG_LOCK_VALUE=value", envConfigFileVar+"="+filepath.Join(dir, string(rune('a'+i))), envCredsFileVar+"="+credentials)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		go func() { done <- cmd.Wait() }()
	}
	select {
	case err := <-done:
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		locked = false
		if err := <-done; err != nil {
			t.Logf("second writer: %v", err)
		}
		t.Fatalf("credential writer ignored held lock: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	locked = false
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(credentials)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "username = value") || !strings.Contains(string(data), "root_email = value") {
		t.Fatalf("credential edits lost: %q", data)
	}
}

func TestConfigureSymlinkTargetDoesNotChangeDuringLockWait(t *testing.T) {
	home := withCleanEnv(t)
	first := filepath.Join(home, "first")
	second := filepath.Join(home, "second")
	link := filepath.Join(home, "config")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("[default]\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(first, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envConfigFileVar, link)
	f, err := os.OpenFile(first+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	done := make(chan error, 1)
	go func() {
		root := newRootCmd(nil, io.Discard, io.Discard)
		root.SetArgs([]string{"configure", "set", "region", "hcm-3"})
		done <- root.ExecuteContext(context.Background())
	}()
	select {
	case err := <-done:
		t.Fatalf("writer did not wait: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, link); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(a), "region = hcm-3") || strings.Contains(string(b), "region = hcm-3") {
		t.Fatalf("edit applied to wrong target: %q / %q", a, b)
	}
}

func TestConfigureWaitsForLockBeforeReadingData(t *testing.T) {
	home := withCleanEnv(t)
	path := configPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, maxConfigureFileSize+1), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- applyConfigureValues(ctx, &globalFlags{}, map[string]string{"region": "hcm-3"}) }()
	select {
	case err := <-done:
		t.Fatalf("read before lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestConfigureCanceledLockWaitDoesNotWrite(t *testing.T) {
	home := withCleanEnv(t)
	path := configPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := newRootCmd(nil, io.Discard, io.Discard)
	root.SetArgs([]string{"configure", "set", "region", "hcm-3"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	select {
	case err := <-done:
		t.Fatalf("write did not wait for lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("lock wait did not cancel")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config exists or stat failed: %v", err)
	}
}
