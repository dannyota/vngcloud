//go:build unix

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"danny.vn/vngcloud"
)

func TestIAMPasswordUnsafeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, []byte("synthetic-input"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, "/dev/null"} {
		if _, err := readUserPassword(context.Background(), name, nil, time.Second); err == nil {
			t.Fatal("unsafe input accepted")
		}
	}
	fifo := path + "-fifo"
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readUserPassword(context.Background(), fifo, nil, time.Second); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO stalled")
	}
}

func TestIAMPasswordRealPipeBounds(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(strconv.FormatBool(canceled), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.Close(); _ = w.Close() }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 40 * time.Millisecond
			if canceled {
				timeout = time.Minute
			}
			type result struct {
				secret vngcloud.Secret
				err    error
			}
			done := make(chan result, 1)
			go func() { secret, err := readUserPassword(ctx, "-", r, timeout); done <- result{secret, err} }()
			// A completed pipe write proves the reader started consuming real input.
			if _, err := w.Write([]byte("password-pipe-canary")); err != nil {
				t.Fatal(err)
			}
			if canceled {
				timer := time.AfterFunc(40*time.Millisecond, cancel)
				defer timer.Stop()
			}
			select {
			case got := <-done:
				if got.secret != "" || got.err == nil || exitCode(got.err) != 2 || strings.Contains(fmt.Sprintf("%+v", got.err), "password-pipe-canary") {
					t.Fatal("unsafe input result")
				}
			case <-time.After(500 * time.Millisecond):
				_ = w.Close()
				<-done
				t.Fatal("real pipe input exceeded its bound")
			}
		})
	}
}
