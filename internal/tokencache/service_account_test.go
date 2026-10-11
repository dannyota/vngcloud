package tokencache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func serviceAccountTestKey() ServiceAccountKey {
	return NewServiceAccountKey("profile", "client", "https://token.example.test", "synthetic-secret")
}
func serviceAccountPaths(dir string, key ServiceAccountKey) (string, string) {
	base := filepath.Join(dir, "service-account-v1", key.Hash())
	return base + ".json", base + ".lock"
}

func TestServiceAccountKeyIdentity(t *testing.T) {
	base := serviceAccountTestKey()
	for _, fields := range [][5]string{{"other", "profile", "client", "https://token.example.test", "synthetic-secret"}, {"service-account", "other", "client", "https://token.example.test", "synthetic-secret"}, {"service-account", "profile", "other", "https://token.example.test", "synthetic-secret"}, {"service-account", "profile", "client", "https://other.example.test", "synthetic-secret"}, {"service-account", "profile", "client", "https://token.example.test", "other-secret"}} {
		if serviceAccountHash(fields[:]).Hash() == base.Hash() {
			t.Fatal("hash component ignored")
		}
	}
	if NewServiceAccountKey("ab", "c", "url", "secret").Hash() == NewServiceAccountKey("a", "bc", "url", "secret").Hash() {
		t.Fatal("ambiguous key")
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if strings.Contains(fmt.Sprintf(verb, base), "synthetic-secret") {
			t.Fatal("key leaked secret")
		}
	}
	raw, err := json.Marshal(base)
	if err != nil || strings.Contains(string(raw), "synthetic-secret") {
		t.Fatal("key encoding leaked")
	}
}

func TestServiceAccountCacheReuseAndNamespace(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	var calls atomic.Int64
	login := loginCounter(&calls, func() time.Time { return now })
	key := serviceAccountTestKey()
	first, err := c.GetServiceAccount(context.Background(), key, "", login)
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.GetServiceAccount(context.Background(), key, "", login)
	if err != nil || second.AccessToken != first.AccessToken || !second.ExpiresAt.Equal(first.ExpiresAt) || !second.ObtainedAt.Equal(first.ObtainedAt) || calls.Load() != 1 {
		t.Fatalf("reuse: %v", err)
	}
	if _, err := c.Get(context.Background(), testKey(), "", login); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("IAM namespace mixed")
	}
	tokenPath, lockPath := serviceAccountPaths(dir, key)
	for _, entry := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0o700}, {filepath.Dir(tokenPath), 0o700}, {tokenPath, 0o600}, {lockPath, 0o600}} {
		info, err := os.Lstat(entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != entry.mode {
			t.Fatal("unsafe mode")
		}
	}
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "synthetic-secret") || strings.Contains(string(raw), "client") {
		t.Fatal("credentials written")
	}
}

func TestServiceAccountCacheUnsafeEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode and symlink fixtures")
	}
	for _, kind := range []string{"token-mode", "lock-mode", "token-symlink", "lock-symlink", "token-dir", "lock-dir", "cache-mode", "namespace-mode", "namespace-symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cache")
			namespace := filepath.Join(dir, "service-account-v1")
			if err := os.MkdirAll(namespace, 0o700); err != nil {
				t.Fatal(err)
			}
			tokenPath, lockPath := serviceAccountPaths(dir, serviceAccountTestKey())
			path := tokenPath
			if strings.HasPrefix(kind, "lock") {
				path = lockPath
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
			case "cache-mode":
				chmodDirForTest(t, dir, 0o755)
			case "namespace-mode":
				chmodDirForTest(t, namespace, 0o755)
			case "namespace-symlink":
				if err := os.Remove(namespace); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), namespace); err != nil {
					t.Fatal(err)
				}
			}
			c := New(dir, time.Now)
			calls := 0
			_, err := c.GetServiceAccount(context.Background(), serviceAccountTestKey(), "", func(context.Context) (Token, error) { calls++; return Token{}, nil })
			if !errors.Is(err, ErrUnavailable) || calls != 0 {
				t.Fatalf("unsafe entry: %v, calls %d", err, calls)
			}
		})
	}
}

func TestServiceAccountCacheMalformedAndBounds(t *testing.T) {
	now := time.Now()
	valid := marshalFixture(t, fileContent{AccessToken: "cached", ExpiresAt: now.Add(time.Hour), ObtainedAt: now})
	for _, body := range []string{"", "null", "not json", "{}", string(valid) + "{}", string(valid) + strings.Repeat(" ", 65536-len(valid)), string(valid) + strings.Repeat(" ", 65537-len(valid))} {
		t.Run(fmt.Sprintf("bytes-%d", len(body)), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cache")
			tokenPath, _ := serviceAccountPaths(dir, serviceAccountTestKey())
			if err := os.MkdirAll(filepath.Dir(tokenPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tokenPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			c := New(dir, func() time.Time { return now })
			var calls atomic.Int64
			got, err := c.GetServiceAccount(context.Background(), serviceAccountTestKey(), "", loginCounter(&calls, func() time.Time { return now }))
			if err != nil {
				t.Fatal(err)
			}
			hit := len(body) == 65536
			if (got.AccessToken == "cached") != hit {
				t.Fatalf("bound/malformed hit = %t", got.AccessToken == "cached")
			}
		})
	}
}

func TestServiceAccountCacheRejectionAndExpiry(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name           string
		age, remaining time.Duration
		rejected       string
		miss           bool
	}{
		{"young", 29 * time.Second, time.Hour, "cached", false}, {"old", 30 * time.Second, time.Hour, "cached", true}, {"future", -time.Second, time.Hour, "cached", true}, {"different", time.Minute, time.Hour, "other", false}, {"expiry", 0, 30 * time.Second, "", true}, {"fresh", 0, 30*time.Second + time.Nanosecond, "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cache")
			path, _ := serviceAccountPaths(dir, serviceAccountTestKey())
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, marshalFixture(t, fileContent{AccessToken: "cached", ExpiresAt: now.Add(test.remaining), ObtainedAt: now.Add(-test.age)}), 0o600); err != nil {
				t.Fatal(err)
			}
			c := New(dir, func() time.Time { return now })
			var calls atomic.Int64
			got, err := c.GetServiceAccount(context.Background(), serviceAccountTestKey(), test.rejected, loginCounter(&calls, func() time.Time { return now }))
			if err != nil {
				t.Fatal(err)
			}
			if (got.AccessToken != "cached") != test.miss {
				t.Fatal("wrong rejection/expiry")
			}
		})
	}
}

func TestServiceAccountCacheContentionAndCancellation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	c := New(dir, time.Now)
	var calls atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := c.GetServiceAccount(context.Background(), serviceAccountTestKey(), "", loginCounter(&calls, time.Now)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal("multiple concurrent exchanges")
	}
	_, lockPath := serviceAccountPaths(dir, serviceAccountTestKey())
	unlock, err := acquireServiceAccountLock(context.Background(), lockPath, minBackoff, maxBackoff)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := c.GetServiceAccount(ctx, serviceAccountTestKey(), "", loginCounter(&calls, time.Now)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("exchange during lock cancellation")
	}
}

func TestServiceAccountCacheWriteFailureAndCancellation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	c := New(dir, time.Now)
	key := serviceAccountTestKey()
	path, _ := serviceAccountPaths(dir, key)
	token, err := c.GetServiceAccount(context.Background(), key, "", func(context.Context) (Token, error) {
		// A directory appearing after the read prevents atomic replacement.
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return Token{AccessToken: "fresh", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	if err != nil || token.AccessToken != "fresh" {
		t.Fatalf("write failure: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tok-") {
			t.Fatal("temporary file leaked")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetServiceAccount(ctx, key, "", func(context.Context) (Token, error) { t.Error("canceled login"); return Token{}, nil }); err != context.Canceled { //nolint:errorlint // Cache cancellation must return the bare caller context error.
		t.Fatal(err)
	}
	dir = filepath.Join(t.TempDir(), "cache")
	c = New(dir, func() time.Time { cancel(); return time.Now() })
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	key = serviceAccountTestKey()
	path, _ = serviceAccountPaths(dir, key)
	if _, err := c.GetServiceAccount(ctx, key, "", func(context.Context) (Token, error) {
		return Token{AccessToken: "canceled", ExpiresAt: time.Now().Add(time.Hour)}, nil
	}); err != context.Canceled { //nolint:errorlint // Cache cancellation must return the bare caller context error.
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("committed canceled token")
	}
}

func TestServiceAccountCacheOpenedInode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openServiceAccountFile(path, os.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateServiceAccountFile(path, file); err == nil {
		t.Fatal("replacement inode accepted")
	}
}

func TestServiceAccountCacheCrossProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("cross-process lock probe supports Linux, macOS, Windows")
	}
	if dir := os.Getenv("VNGCLOUD_TEST_SA_CACHE"); dir != "" {
		c := New(dir, time.Now)
		_, err := c.GetServiceAccount(context.Background(), serviceAccountTestKey(), "", func(context.Context) (Token, error) {
			f, err := os.OpenFile(filepath.Join(dir, "exchanges"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // The parent test supplies its temporary directory to this child test executable.
			if err != nil {
				return Token{}, err
			}
			defer func() { _ = f.Close() }()
			if _, err := f.WriteString("exchange\n"); err != nil {
				return Token{}, err
			}
			return Token{AccessToken: "shared", ExpiresAt: time.Now().Add(time.Hour)}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := filepath.Join(t.TempDir(), "cache")
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	commands := make([]*exec.Cmd, 4)
	for i := range commands {
		commands[i] = exec.CommandContext(ctx, binary, "-test.run=^TestServiceAccountCacheCrossProcess$") //nolint:gosec // Runs this test executable with fixed arguments, not a user-supplied program.
		commands[i].Env = append(os.Environ(), "VNGCLOUD_TEST_SA_CACHE="+dir)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "exchanges"))
	if err != nil || string(raw) != "exchange\n" {
		t.Fatalf("process exchanges: %q, %v", raw, err)
	}
}
