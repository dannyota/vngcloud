package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigureSetGetMode(t *testing.T) {
	home := withCleanEnv(t)
	if _, err := runConfigure(t, "", []string{"configure", "set", "region", "hcm-3"}); err != nil {
		t.Fatal(err)
	}
	got, err := runConfigure(t, "", []string{"configure", "get", "region"})
	if err != nil || got != "hcm-3\n" {
		t.Fatalf("get = %q, %v", got, err)
	}
	info, err := os.Stat(configPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}

func TestConfigureInteractiveCanceledInputDoesNotWrite(t *testing.T) {
	home := withCleanEnv(t)
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	isTerminalOverride = func(any) bool { return true }
	defer func() { isTerminalOverride = nil }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := newRootCmd(r, io.Discard, io.Discard)
	root.SetArgs([]string{"configure"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	if _, err := w.Write([]byte("hcm-3\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("input did not cancel")
	}
	if _, err := os.Stat(configPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config exists or stat failed: %v", err)
	}
}

func TestConfigureCanceledPipedInputDoesNotWrite(t *testing.T) {
	home := withCleanEnv(t)
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	root := newRootCmd(r, io.Discard, io.Discard)
	root.SetArgs([]string{"configure", "set", "region", "-"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()
	if _, err := w.Write([]byte("h")); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled input did not return")
	}
	if _, err := os.Stat(configPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("config exists or stat failed: %v", err)
	}
}

func TestConfigureConcurrentLatchPreserved(t *testing.T) {
	home := withCleanEnv(t)
	r, w := io.Pipe()
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()
	root := newRootCmd(r, io.Discard, io.Discard)
	root.SetArgs([]string{"configure", "set", "region", "-"})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(context.Background()) }()
	if _, err := w.Write([]byte("h")); err != nil {
		t.Fatal(err)
	}
	if _, err := runConfigure(t, "", []string{"configure", "set", "read_only", "true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("cm-3\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("stale writer succeeded after read-only latch")
	}
	data, err := os.ReadFile(configPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "read_only = true") {
		t.Fatalf("latch lost: %q", data)
	}
}
