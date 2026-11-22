package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/tokencache"
)

func TestCustomTOTPProviderRecordsLaterStep(t *testing.T) {
	now := time.Unix(3000, 0)
	oldNow := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = oldNow })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"accessToken":"synthetic","expiresIn":3600}`))
		case r.Method == "GET":
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
		case strings.Contains(r.URL.Path, "/google"):
			http.Redirect(w, r, "/callback?code=synthetic", http.StatusFound)
		default:
			http.Redirect(w, r, "/ap/auth/iam/google", http.StatusFound)
		}
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "cache")
	auth := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "password", SigninBaseURL: server.URL, TokenURL: server.URL + "/token", TOTP: TOTPFunc(func(context.Context) (string, error) { now = now.Add(30 * time.Second); return "synthetic", nil })}
	client, err := newClient(WithRegion("hcm-3"), WithIAMUser(auth), WithTokenCache(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.totp"))
	if err != nil || len(files) != 1 {
		t.Fatalf("marker files: %v, %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil || string(data) != "101" {
		t.Fatalf("marker %q, %v", data, err)
	}
}

func TestTOTPGuardWaitsAndUsesNextStep(t *testing.T) {
	now := time.Unix(3001, 0)
	original := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = original })
	waits := 0
	auth := &IAMUserAuth{waitTOTP: func(_ context.Context, delay time.Duration) error { waits++; now = now.Add(delay); return nil }}
	provider := &SecretTOTP{Secret: "JBSWY3DPEHPK3PXP"}
	guard := guardedTOTP{auth: auth, provider: provider}
	first, err := guard.GetCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := guard.GetCode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected, err := provider.codeForStep(101)
	if err != nil || second != expected || first == second || waits != 1 {
		t.Fatalf("wrong step or wait: %d, %v", waits, err)
	}
}

func TestTOTPGuardMalformedMarkerAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "malformed", true: "canceled"}[cancel], func(t *testing.T) {
			now := time.Unix(3001, 0)
			original := timeNow
			timeNow = func() time.Time { return now }
			t.Cleanup(func() { timeNow = original })
			dir := filepath.Join(t.TempDir(), "cache")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			key := tokencache.Key{Username: "user"}
			path := filepath.Join(dir, key.Hash()+".totp")
			if err := os.WriteFile(path, []byte("invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
			waits := 0
			auth := &IAMUserAuth{waitTOTP: func(_ context.Context, delay time.Duration) error {
				waits++
				if cancel {
					return context.Canceled
				}
				now = now.Add(delay)
				return nil
			}}
			calls := 0
			guard := guardedTOTP{auth: auth, cache: tokencache.New(dir, timeNow), key: key, provider: TOTPFunc(func(context.Context) (string, error) { calls++; return "synthetic", nil })}
			_, err := guard.GetCode(context.Background())
			if cancel {
				if !errors.Is(err, context.Canceled) || calls != 0 {
					t.Fatalf("cancellation: calls %d, %v", calls, err)
				}
				return
			}
			data, readErr := os.ReadFile(path)
			if err != nil || readErr != nil || string(data) != "101" || waits != 1 || calls != 1 {
				t.Fatalf("marker %q, waits %d, calls %d, errors %v %v", data, waits, calls, err, readErr)
			}
		})
	}
}

func TestTOTPMarkerWriteFailureIsNotMemoized(t *testing.T) {
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
		case strings.Contains(r.URL.Path, "/google"):
			submissions.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.Redirect(w, r, "/ap/auth/iam/google", http.StatusFound)
		}
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := tokencache.Key{Username: "user"}
	path := filepath.Join(dir, key.Hash()+".totp")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(3001, 0)
	original := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = original })
	auth := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "password", SigninBaseURL: server.URL, TOTP: TOTPFunc(func(context.Context) (string, error) { return "synthetic", nil })}
	auth.waitTOTP = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
	source := &iamTokenSource{auth: auth, cache: tokencache.New(dir, timeNow), cacheKey: key}
	_, err := source.Token(context.Background())
	if !errors.Is(err, ErrTokenCache) || submissions.Load() != 0 {
		t.Fatalf("marker failure: %v, submissions %d", err, submissions.Load())
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = source.Token(context.Background())
	if !errors.Is(err, ErrAuth) || submissions.Load() != 1 {
		t.Fatalf("retry: %v, submissions %d", err, submissions.Load())
	}
	_, err = source.Token(context.Background())
	if !errors.Is(err, ErrAuth) || submissions.Load() != 1 {
		t.Fatalf("verdict memo: %v, submissions %d", err, submissions.Load())
	}
}

func TestSeparateSessionsUseDifferentTOTPSteps(t *testing.T) {
	now := time.Unix(3001, 0)
	original := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = original })
	steps := make(chan int64, 2)
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"accessToken":"synthetic","expiresIn":0}`))
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
		case strings.Contains(r.URL.Path, "/google"):
			submissions.Add(1)
			http.Redirect(w, r, "/callback?code=synthetic", http.StatusFound)
		default:
			http.Redirect(w, r, "/ap/auth/iam/google", http.StatusFound)
		}
	}))
	defer server.Close()
	cache := tokencache.New(filepath.Join(t.TempDir(), "cache"), timeNow)
	key := tokencache.Key{Username: "user"}
	for range 2 {
		auth := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "password", SigninBaseURL: server.URL, TokenURL: server.URL + "/token", TOTP: TOTPFunc(func(context.Context) (string, error) { steps <- timeNow().Unix() / 30; return "synthetic", nil })}
		auth.waitTOTP = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
		source := &iamTokenSource{auth: auth, cache: cache, cacheKey: key}
		if _, err := source.Token(context.Background()); !errors.Is(err, ErrAuth) {
			t.Fatal(err)
		}
	}
	if len(steps) != 2 || submissions.Load() != 2 {
		t.Fatalf("step count %d, submissions %d", len(steps), submissions.Load())
	}
	first, second := <-steps, <-steps
	if first != 100 || second != 101 {
		t.Fatalf("submitted steps: %d, %d", first, second)
	}
}

func TestTOTPWaitCancellationSendsNothing(t *testing.T) {
	now := time.Unix(3001, 0)
	original := timeNow
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = original })
	var submissions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
		case strings.Contains(r.URL.Path, "/google"):
			submissions.Add(1)
		default:
			http.Redirect(w, r, "/ap/auth/iam/google", http.StatusFound)
		}
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	key := tokencache.Key{Username: "user"}
	if err := os.WriteFile(filepath.Join(dir, key.Hash()+".totp"), []byte("100"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	auth := &IAMUserAuth{RootEmail: "root", Username: "user", Password: "password", SigninBaseURL: server.URL, TOTP: TOTPFunc(func(context.Context) (string, error) { return "synthetic", nil })}
	auth.waitTOTP = func(ctx context.Context, _ time.Duration) error { cancel(); return ctx.Err() }
	source := &iamTokenSource{auth: auth, cache: tokencache.New(dir, timeNow), cacheKey: key}
	if _, err := source.Token(ctx); !errors.Is(err, context.Canceled) || submissions.Load() != 0 {
		t.Fatalf("error %v, submissions %d", err, submissions.Load())
	}
}

func TestCancelledIAMLoginDoesNotMemoize(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(entered)
			<-r.Context().Done()
			return
		}
		switch {
		case r.URL.Path == "/token":
			_, _ = w.Write([]byte(`{"accessToken":"synthetic","expiresIn":3600}`))
		case r.Method == "GET":
			_, _ = w.Write([]byte(`<input name="_csrf" value="csrf">`))
		case strings.Contains(r.URL.Path, "/google"):
			http.Redirect(w, r, "/callback?code=synthetic", http.StatusFound)
		default:
			http.Redirect(w, r, "/ap/auth/iam/google", http.StatusFound)
		}
	}))
	defer server.Close()
	client, err := newClient(WithRegion("hcm-3"), WithIAMUser(&IAMUserAuth{RootEmail: "root", Username: "user", Password: "password", SigninBaseURL: server.URL, TokenURL: server.URL + "/token", TOTP: TOTPFunc(func(context.Context) (string, error) { return "synthetic", nil })}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- client.Authenticate(ctx) }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("first login: %v", err)
	}
	if err := client.Authenticate(context.Background()); err != nil {
		t.Fatalf("fresh login: %v", err)
	}
}
