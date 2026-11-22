package tokencache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestIAMCacheUnsafeEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode and symlink fixtures")
	}
	for _, kind := range []string{"token-mode", "lock-mode", "token-symlink", "lock-symlink", "token-dir", "lock-dir", "oversize", "cache-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			chmodDirForTest(t, dir, 0o700)
			path := filepath.Join(dir, testKey().Hash()+".json")
			if strings.HasPrefix(kind, "lock") {
				path = filepath.Join(dir, testKey().Hash()+".lock")
			}
			switch kind {
			case "token-mode", "lock-mode":
				if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
			case "token-symlink", "lock-symlink":
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "token-dir", "lock-dir":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				if err := os.WriteFile(path, []byte(strings.Repeat(" ", (64<<10)+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			case "cache-symlink":
				link := filepath.Join(t.TempDir(), "cache")
				if err := os.Symlink(dir, link); err != nil {
					t.Fatal(err)
				}
				dir = link
			}
			calls := 0
			_, err := New(dir, time.Now).Get(context.Background(), testKey(), "", func(context.Context) (Token, error) { calls++; return Token{}, nil })
			if !errors.Is(err, ErrUnavailable) || calls != 0 {
				t.Fatalf("unsafe entry accepted: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestCacheRejectsCredentialKeyAliases(t *testing.T) {
	now := time.Now()
	valid := marshalFixture(t, fileContent{AccessToken: "first", ExpiresAt: now.Add(time.Hour), ObtainedAt: now})
	for _, key := range []string{"accessToken", "AccessToken"} {
		dir := t.TempDir()
		chmodDirForTest(t, dir, 0o700)
		body := strings.TrimSuffix(string(valid), "}") + `,"` + key + `":"second"}`
		if err := os.WriteFile(filepath.Join(dir, testKey().Hash()+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		calls := 0
		_, err := New(dir, func() time.Time { return now }).Get(context.Background(), testKey(), "", func(context.Context) (Token, error) {
			calls++
			return Token{AccessToken: "fresh", ExpiresAt: now.Add(time.Hour)}, nil
		})
		if err != nil || calls != 1 {
			t.Fatal("ambiguous cached credential was reused")
		}
	}
}

func TestCacheDoesNotPublishOversizedEntry(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	token, err := New(dir, func() time.Time { return now }).Get(context.Background(), testKey(), "", func(context.Context) (Token, error) {
		return Token{AccessToken: strings.Repeat("x", 64<<10), ExpiresAt: now.Add(time.Hour)}, nil
	})
	if err != nil || len(token.AccessToken) != 64<<10 {
		t.Fatal("cache write changed successful login")
	}
	if _, err := os.Stat(filepath.Join(dir, testKey().Hash()+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("oversized cache entry published")
	}
}
