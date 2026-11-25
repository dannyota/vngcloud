//go:build livewrite

package livetest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNATHoldRejectsProbeSymlink(t *testing.T) {
	requireNATHoldPlatform(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	hold, err := newNATHold(dir, "2s")
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	if err := os.WriteFile(filepath.Join(dir, "target"), []byte("pass"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(dir, natDetailResult)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, natRulesResult), []byte("pass"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var result error
	cleaned := false
	runNATPostActive(context.Background(), func() error {
		return hold.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error { return nil })
	}, func(context.Context) { cleaned = true }, func(err error) { result = err })
	if result == nil || !cleaned {
		t.Fatal("symlink result must fail and still run cleanup")
	}
}

func TestNATHoldRequiresProbeResults(t *testing.T) {
	requireNATHoldPlatform(t)
	for _, tc := range []struct {
		name, detail, rules string
		valid               bool
	}{
		{"missing", "", "", false}, {"detail missing", "", "pass", false}, {"rules missing", "pass", "", false}, {"empty detail", "", "pass", false},
		{"failed detail", "fail", "pass", false}, {"failed rules", "pass", "fail", false}, {"malformed", "pass\n", "pass", false}, {"pass", "pass", "pass", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			hold, err := newNATHold(dir, "2s")
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Close()
			if tc.detail != "" || tc.name == "empty detail" {
				if err := os.WriteFile(filepath.Join(dir, "nat-detail-result"), []byte(tc.detail), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.rules != "" {
				if err := os.WriteFile(filepath.Join(dir, "nat-rules-result"), []byte(tc.rules), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			err = hold.wait(context.Background(), natHoldIDs{}, func() {}, func(context.Context, time.Duration) error { t.Fatal("unexpected wait"); return nil })
			if (err == nil) != tc.valid {
				t.Fatalf("hold returned %v", err)
			}
		})
	}
}
