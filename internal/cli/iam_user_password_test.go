package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestIAMPasswordInput(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		bad         bool
	}{
		{" canary value \r\n", " canary value ", false},
		{"value\n", "value", false}, {"", "", true}, {"\n", "", true},
		{"value\n\n", "", true}, {"a\x00b", "", true}, {"\xff", "", true},
		{strings.Repeat("x", 4097), "", true},
	} {
		secret, err := readUserPassword(context.Background(), "-", strings.NewReader(tc.input), time.Second)
		if (err != nil) != tc.bad || secret.Reveal() != tc.want {
			t.Fatal("wrong password input result")
		}
		if err != nil && strings.Contains(err.Error(), "canary") {
			t.Fatal("input leaked")
		}
	}
}

func TestIAMPasswordStalledStdin(t *testing.T) {
	for _, cancelNow := range []bool{false, true} {
		r, w := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		if cancelNow {
			cancel()
		}
		done := make(chan error, 1)
		go func() { _, err := readUserPassword(ctx, "-", r, 20*time.Millisecond); done <- err }()
		select {
		case err := <-done:
			if err == nil || exitCode(err) != 2 {
				t.Fatal("missing input refusal")
			}
		case <-time.After(time.Second):
			t.Fatal("stalled password input")
		}
		cancel()
		_ = w.Close()
		_ = r.Close()
	}
}

func TestIAMPasswordPrivateFileAndSymlink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("synthetic value\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret, err := readUserPassword(context.Background(), path, nil, time.Second)
	if err != nil || secret.Reveal() != "synthetic value" {
		t.Fatal("private input failed")
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := readUserPassword(context.Background(), link, nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := readUserPassword(context.Background(), filepath.Dir(path), nil, time.Second); err == nil {
		t.Fatal("directory accepted")
	}
}

type failingIAMPasswordFile struct{ phase string }

func (f failingIAMPasswordFile) Write(p []byte) (int, error) {
	if f.phase == "write" {
		return 0, errors.New("synthetic canary")
	}
	if f.phase == "short" {
		return len(p) - 1, nil
	}
	return len(p), nil
}
func (f failingIAMPasswordFile) Sync() error {
	if f.phase == "sync" {
		return errors.New("synthetic canary")
	}
	return nil
}
func (f failingIAMPasswordFile) Close() error {
	if f.phase == "close" {
		return errors.New("synthetic canary")
	}
	return nil
}

func TestIAMPasswordPersistenceFailures(t *testing.T) {
	for _, phase := range []string{"open", "write", "short", "sync", "close"} {
		for _, removalFails := range []bool{false, true} {
			removed := false
			ops := iamPasswordFileOps{
				open: func(string) (iamPasswordFile, error) {
					if phase == "open" {
						return nil, errors.New("synthetic canary")
					}
					return failingIAMPasswordFile{phase}, nil
				},
				remove: func(string) error {
					removed = true
					if removalFails {
						return errors.New("synthetic canary")
					}
					return nil
				},
			}
			remains, err := persistUserPassword("unused", vngcloud.Secret("synthetic-input"), ops)
			if err == nil || strings.Contains(err.Error(), "canary") || removed != (phase != "open") || remains != (phase != "open" && removalFails) {
				t.Fatal("wrong persistence failure behavior")
			}
		}
	}
}

func TestIAMPasswordPersistenceCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if remains, err := persistUserPassword(path, "synthetic-input", userPasswordFileOps()); remains || err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("wrong output mode")
	}
	if _, err := persistUserPassword(path, "replacement", userPasswordFileOps()); err == nil {
		t.Fatal("collision accepted")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "synthetic-input\n" {
		t.Fatal("collision changed file")
	}
}

func TestIAMPasswordTerminalRefused(t *testing.T) {
	prev := isTerminalOverride
	isTerminalOverride = func(any) bool { return true }
	t.Cleanup(func() { isTerminalOverride = prev })
	if _, err := readUserPassword(context.Background(), "-", iamUnreadableStdin{t}, time.Second); err == nil {
		t.Fatal("terminal accepted")
	}
}

func TestIAMPasswordPipeCancellation(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close(); _ = w.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := readUserPassword(ctx, "-", r, time.Second); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled input accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stalled")
	}
}
