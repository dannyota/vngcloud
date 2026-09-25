package tokencache

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testKey() Key {
	return Key{
		Profile:   "default",
		RootEmail: "root@example.test",
		Username:  "user",
		SigninURL: "https://signin.example",
		TokenURL:  "https://token.example",
	}
}

// loginCounter builds a login callback for Cache.Get that counts calls and
// returns a token named by call order, far from expiry.
func loginCounter(count *atomic.Int64, now func() time.Time) func(context.Context) (Token, error) {
	return func(context.Context) (Token, error) {
		n := count.Add(1)
		return Token{AccessToken: "tok-" + itoa(n), ExpiresAt: now().Add(time.Hour)}, nil
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// chmodDirForTest sets a test directory's mode, for exercising the cache's
// directory permission checks. It is never used on a file holding a token.
func chmodDirForTest(t *testing.T, dir string, perm os.FileMode) {
	t.Helper()
	if err := os.Chmod(dir, perm); err != nil { //nolint:gosec // test directory permission fixture, not a secret file
		t.Fatal(err)
	}
}

// restoreDirForTest is chmodDirForTest(dir, 0700) for a t.Cleanup callback,
// which cannot call t.Fatal.
func restoreDirForTest(dir string) {
	_ = os.Chmod(dir, 0o700) //nolint:gosec // test directory permission fixture, not a secret file
}

// marshalFixture is json.Marshal for a fileContent test fixture. gosec's
// G117 flags AccessToken as a secret-shaped field name, but this writes a
// synthetic token to a per-test temp file, not a real credential.
func marshalFixture(t *testing.T, fc fileContent) []byte {
	t.Helper()
	data, err := json.Marshal(fc) //nolint:gosec // synthetic test fixture, not a real secret
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCacheReuseWithinExpiry(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	var logins atomic.Int64

	first, err := c.Get(context.Background(), testKey(), "", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	second, err := c.Get(context.Background(), testKey(), "", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if second.AccessToken != first.AccessToken {
		t.Fatalf("second token = %q, want reuse of %q", second.AccessToken, first.AccessToken)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

func TestCacheExpiryBoundary(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })

	// A token that expires in exactly 30 seconds is a miss (the SDK never
	// uses a token this close to expiry); one that expires in 31 seconds is
	// reused.
	write := func(expiresIn time.Duration) {
		fc := fileContent{AccessToken: "tok-boundary", ExpiresAt: now.Add(expiresIn), ObtainedAt: now}
		data := marshalFixture(t, fc)
		key := testKey()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, key.Hash()+".json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(30 * time.Second)
	var logins atomic.Int64
	got, err := c.Get(context.Background(), testKey(), "", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if logins.Load() != 1 || got.AccessToken == "tok-boundary" {
		t.Fatalf("expected a miss (fresh login) at exactly the 30s boundary; logins=%d token=%q", logins.Load(), got.AccessToken)
	}

	write(31 * time.Second)
	got, err = c.Get(context.Background(), testKey(), "", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if logins.Load() != 1 || got.AccessToken != "tok-boundary" {
		t.Fatalf("expected reuse at 31s from expiry; logins=%d token=%q", logins.Load(), got.AccessToken)
	}
}

func TestCacheKeyFieldChangeMisses(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	var logins atomic.Int64
	login := loginCounter(&logins, func() time.Time { return now })

	base := testKey()
	if _, err := c.Get(context.Background(), base, "", login); err != nil {
		t.Fatalf("Get(base) error = %v", err)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins after base = %d, want 1", logins.Load())
	}

	variants := []Key{
		{Profile: "other", RootEmail: base.RootEmail, Username: base.Username, SigninURL: base.SigninURL, TokenURL: base.TokenURL},
		{Profile: base.Profile, RootEmail: "other@example.test", Username: base.Username, SigninURL: base.SigninURL, TokenURL: base.TokenURL},
		{Profile: base.Profile, RootEmail: base.RootEmail, Username: "other", SigninURL: base.SigninURL, TokenURL: base.TokenURL},
		{Profile: base.Profile, RootEmail: base.RootEmail, Username: base.Username, SigninURL: "https://other.example", TokenURL: base.TokenURL},
		{Profile: base.Profile, RootEmail: base.RootEmail, Username: base.Username, SigninURL: base.SigninURL, TokenURL: "https://other.example"},
	}
	for i, variant := range variants {
		if _, err := c.Get(context.Background(), variant, "", login); err != nil {
			t.Fatalf("Get(variant %d) error = %v", i, err)
		}
	}
	if want := int64(1 + len(variants)); logins.Load() != want {
		t.Fatalf("logins = %d, want %d", logins.Load(), want)
	}

	// The base key again should still reuse, not add a login.
	if _, err := c.Get(context.Background(), base, "", login); err != nil {
		t.Fatalf("Get(base again) error = %v", err)
	}
	if logins.Load() != int64(1+len(variants)) {
		t.Fatalf("logins after repeat base = %d, want %d", logins.Load(), 1+len(variants))
	}
}

func TestKeyHashBoundary(t *testing.T) {
	a := Key{Profile: "ab", RootEmail: "c"}
	b := Key{Profile: "a", RootEmail: "bc"}
	if a.Hash() == b.Hash() {
		t.Fatalf("Key{ab,c}.Hash() == Key{a,bc}.Hash(): %s", a.Hash())
	}
}

func TestCacheMalformedEmptyAndUnreadableFilesAreMisses(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	key := testKey()
	path := filepath.Join(dir, key.Hash()+".json")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("malformed", func(t *testing.T) {
		if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		var logins atomic.Int64
		if _, err := c.Get(context.Background(), key, "", loginCounter(&logins, func() time.Time { return now })); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if logins.Load() != 1 {
			t.Fatalf("logins = %d, want 1", logins.Load())
		}
	})

	t.Run("empty", func(t *testing.T) {
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
		var logins atomic.Int64
		if _, err := c.Get(context.Background(), key, "", loginCounter(&logins, func() time.Time { return now })); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if logins.Load() != 1 {
			t.Fatalf("logins = %d, want 1", logins.Load())
		}
	})

	t.Run("mode 0000", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores file permissions")
		}
		if err := os.WriteFile(path, []byte(`{"accessToken":"x","expiresAt":"2999-01-01T00:00:00Z"}`), 0o000); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Chmod(path, 0o600) }()
		if _, err := os.ReadFile(path); err == nil {
			t.Skip("this environment does not enforce file mode bits")
		}
		var logins atomic.Int64
		if _, err := c.Get(context.Background(), key, "", loginCounter(&logins, func() time.Time { return now })); err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if logins.Load() != 1 {
			t.Fatalf("logins = %d, want 1", logins.Load())
		}
	})
}

func TestCacheDirAndFileModesOnUnix(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	var logins atomic.Int64

	if _, err := c.Get(context.Background(), testKey(), "", loginCounter(&logins, func() time.Time { return now })); err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir mode = %o, want 0700", perm)
	}

	tokenPath := filepath.Join(dir, testKey().Hash()+".json")
	fi, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("token file mode = %o, want 0600", perm)
	}
}

func TestCachePreexistingLooseDirIsError(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "cache")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	c := New(sub, func() time.Time { return now })
	var logins atomic.Int64

	_, err := c.Get(context.Background(), testKey(), "", loginCounter(&logins, func() time.Time { return now }))
	if err == nil {
		t.Fatal("expected an error for a pre-existing group/other-accessible dir")
	}
	if logins.Load() != 0 {
		t.Fatalf("logins = %d, want 0 (no login should be attempted)", logins.Load())
	}
	const marker = "secret-token-XYZ"
	if got := err.Error(); len(got) == 0 {
		t.Fatal("expected a non-empty error message")
	} else if containsMarker(got, marker) {
		t.Fatalf("error leaked a token-like value: %v", err)
	}
}

func containsMarker(s, marker string) bool {
	for i := 0; i+len(marker) <= len(s); i++ {
		if s[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}

func TestCacheRejectedTokenOldEnoughIsMissAndReplaced(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	key := testKey()

	fc := fileContent{AccessToken: "tok-old", ExpiresAt: now.Add(time.Hour), ObtainedAt: now.Add(-60 * time.Second)}
	data := marshalFixture(t, fc)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key.Hash()+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	var logins atomic.Int64
	got, err := c.Get(context.Background(), key, "tok-old", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.AccessToken == "tok-old" {
		t.Fatal("expected the rejected token to be replaced")
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}

	raw, err := os.ReadFile(filepath.Join(dir, key.Hash()+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk fileContent
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.AccessToken != got.AccessToken {
		t.Fatalf("on-disk token = %q, want %q", onDisk.AccessToken, got.AccessToken)
	}
}

func TestCacheRejectedTokenTooYoungIsNotAMiss(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	key := testKey()

	fc := fileContent{AccessToken: "tok-young", ExpiresAt: now.Add(time.Hour), ObtainedAt: now.Add(-5 * time.Second)}
	data := marshalFixture(t, fc)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key.Hash()+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	var logins atomic.Int64
	got, err := c.Get(context.Background(), key, "tok-young", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.AccessToken != "tok-young" {
		t.Fatalf("token = %q, want the still-young rejected token reused, not replaced", got.AccessToken)
	}
	if logins.Load() != 0 {
		t.Fatalf("logins = %d, want 0 (a token under 30 seconds old is not invalidated)", logins.Load())
	}
}

func TestCacheConcurrentAcrossTwoInstancesLogsInOnce(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	clock := func() time.Time { return now }
	c1 := New(dir, clock)
	c2 := New(dir, clock)
	var logins atomic.Int64
	login := loginCounter(&logins, clock)

	const n = 8
	errs := make([]error, n)
	done := make(chan struct{})
	for i := range n {
		go func(i int) {
			c := c1
			if i%2 == 0 {
				c = c2
			}
			_, err := c.Get(context.Background(), testKey(), "", login)
			errs[i] = err
			done <- struct{}{}
		}(i)
	}
	for range n {
		<-done
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d error = %v", i, err)
		}
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

func TestCacheHeldLockReturnsDeadlineExceeded(t *testing.T) {
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	now := time.Now()
	c := New(dir, func() time.Time { return now })
	key := testKey()

	lockPath := filepath.Join(dir, key.Hash()+".lock")
	unlock, err := acquireLock(context.Background(), lockPath, minBackoff, maxBackoff)
	if err != nil {
		t.Fatalf("acquireLock() error = %v", err)
	}
	defer unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	var logins atomic.Int64
	_, err = c.Get(ctx, key, "", loginCounter(&logins, func() time.Time { return now }))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if logins.Load() != 0 {
		t.Fatalf("logins = %d, want 0", logins.Load())
	}
}

func TestCacheReadOnlyDirAfterLoginStillReturnsToken(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	chmodDirForTest(t, dir, 0o700)
	key := testKey()
	// The lock file must already exist, since creating a new file in a
	// read-only directory would fail before the login is ever attempted.
	if err := os.WriteFile(filepath.Join(dir, key.Hash()+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	chmodDirForTest(t, dir, 0o500)
	t.Cleanup(func() { restoreDirForTest(dir) })

	now := time.Now()
	c := New(dir, func() time.Time { return now })
	var logins atomic.Int64

	got, err := c.Get(context.Background(), key, "", loginCounter(&logins, func() time.Time { return now }))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.AccessToken != "tok-1" {
		t.Fatalf("token = %q, want tok-1", got.AccessToken)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}

	chmodDirForTest(t, dir, 0o700)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".tok-") {
			t.Fatalf("leftover temp file: %s", entry.Name())
		}
	}
}
