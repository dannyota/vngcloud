package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"danny.vn/vngcloud/internal/tokencache"
	"danny.vn/vngcloud/internal/transport"
)

func TestServiceAccountDiskCacheRotation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	var calls atomic.Int64
	rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
		n := calls.Add(1)
		return serviceAccountResponse(200, fmt.Sprintf(`{"access_token":"token-%d","expires_in":1800}`, n)), nil
	})
	option := WithServiceAccountCredentials("client", "old-secret", true)
	build := func(auth Option) Config {
		cfg, err := NewConfig(WithRegion("hcm-3"), WithTokenCache(dir), auth, serviceAccountTestOption(rt, time.Now))
		if err != nil {
			t.Fatal(err)
		}
		return cfg
	}
	first, second := build(option), build(option)
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := first.Authenticate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	wg.Go(func() {
		if err := second.Authenticate(context.Background()); err != nil {
			t.Error(err)
		}
	})
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("shared disk exchanges %d", calls.Load())
	}
	rotated := build(WithServiceAccountCredentials("client", "new-secret", true))
	if err := rotated.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("rotation reused old entry")
	}
	if err := build(option).Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("old entry purged")
	}
	entries, err := filepath.Glob(filepath.Join(dir, "service-account-v1", "*.json"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries %d: %v", len(entries), err)
	}
	for _, path := range entries {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "clientSecret") {
			t.Fatal("credential metadata written")
		}
	}
}

func TestServiceAccountCacheErrorBoundary(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "synthetic-private-path")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("client", "secret", true), WithTokenCache(dir), serviceAccountTestOption(serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("cache refusal sent exchange")
		return nil, errors.New("secret")
	}), time.Now))
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Authenticate(context.Background())
	if err != ErrTokenCache || errors.Unwrap(err) != nil || strings.Contains(fmt.Sprintf("%#v", err), dir) { //nolint:errorlint // The cache boundary must return the sentinel itself, without wrapping.
		t.Fatalf("unsafe cache error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := cfg.Authenticate(ctx); err != context.Canceled { //nolint:errorlint // The cache boundary must return the bare caller context error.
		t.Fatal(err)
	}
	for _, auth := range []Option{WithStaticToken("synthetic"), WithCredentialsProvider(&fakeCredentialsProvider{token: Token{AccessToken: "synthetic", ExpiresAt: time.Now().Add(time.Hour)}})} {
		cfg, err := NewConfig(WithRegion("hcm-3"), WithTokenCache(dir), auth)
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestServiceAccountProductRecoveryAndOnce(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		status int
		once   bool
	}{{"recover", 401, false}, {"forbidden", 403, false}, {"once-auth", 401, true}, {"once-throttle", 429, true}, {"once-server", 500, true}, {"once-network", 0, true}} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now()
			calls, products := 0, 0
			rt := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return serviceAccountResponse(200, fmt.Sprintf(`{"access_token":"token-%d","expires_in":1800}`, calls)), nil
			})
			product := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
				products++
				if scenario.status == 0 {
					return nil, errors.New("synthetic-network-failure")
				}
				if products == 1 {
					return serviceAccountResponse(scenario.status, `{}`), nil
				}
				return serviceAccountResponse(200, `{}`), nil
			})
			cfg, err := NewConfig(WithRegion("hcm-3"), WithTokenCache(filepath.Join(t.TempDir(), "cache")), WithServiceAccountCredentials("client", "secret", true), WithTransport(product), serviceAccountTestOption(rt, func() time.Time { return now }))
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Authenticate(context.Background()); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
			client := ClientOf(cfg)
			err = client.DoJSON(context.Background(), transport.Request{Operation: "synthetic.Write", Method: "PUT", URL: "https://product.example.test/item", OK: []int{200}, Once: scenario.once}, nil)
			if scenario.status == 401 && !scenario.once {
				if err != nil || calls != 2 || products != 2 {
					t.Fatalf("recovery %v exchanges %d sends %d", err, calls, products)
				}
				return
			}
			if err == nil || calls != 1 || products != 1 {
				t.Fatalf("once/denial %v exchanges %d sends %d", err, calls, products)
			}
			// A later acquisition distinguishes invalidation from a forbidden refresh.
			if err := cfg.Authenticate(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 1
			if scenario.status == 401 {
				want = 2
			}
			if calls != want {
				t.Fatalf("post-failure exchanges %d, want %d", calls, want)
			}
		})
	}
}

func TestServiceAccountCacheRejectedRestored(t *testing.T) {
	now := time.Now()
	dir := filepath.Join(t.TempDir(), "cache")
	key := tokencache.NewServiceAccountKey("", "client", serviceAccountTokenURL, "secret")
	cache := tokencache.New(dir, func() time.Time { return now })
	if _, err := cache.GetServiceAccount(context.Background(), key, "", func(context.Context) (tokencache.Token, error) {
		return tokencache.Token{AccessToken: "old", ExpiresAt: now.Add(time.Hour)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
		return serviceAccountResponse(200, `{"access_token":"new","expires_in":1800}`), nil
	}), func() time.Time { return now })
	source.cache, source.cacheKey = cache, key
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	source.Invalidate("old")
	held, release := make(chan struct{}), make(chan struct{})
	// Rejection forces this holder through login while retaining the disk lock.
	done := make(chan error, 1)
	go func() {
		_, err := cache.GetServiceAccount(context.Background(), key, "old", func(context.Context) (tokencache.Token, error) {
			close(held)
			<-release
			return tokencache.Token{}, errors.New("synthetic-stop")
		})
		done <- err
	}()
	<-held
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := source.Token(ctx); err != context.DeadlineExceeded { //nolint:errorlint // A canceled cache lock wait must return the bare caller context error.
		t.Fatal(err)
	}
	close(release)
	<-done
	if source.rejected != "old" {
		t.Fatal("pending rejection lost")
	}
	token, err := source.Token(context.Background())
	if err != nil || token.AccessToken != "new" {
		t.Fatalf("restored rejection %v", err)
	}
}

func TestServiceAccountCacheLoginFailureIdentity(t *testing.T) {
	for _, cancelExchange := range []bool{false, true} {
		dir := filepath.Join(t.TempDir(), "cache")
		ctx, cancel := context.WithCancel(context.Background())
		rt := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
			if cancelExchange {
				cancel()
				return nil, errors.New("synthetic-peer-secret")
			}
			return serviceAccountResponse(401, `{"errors":[{"message":"synthetic-peer-secret"}]}`), nil
		})
		cfg, err := NewConfig(WithRegion("hcm-3"), WithTokenCache(dir), WithServiceAccountCredentials("client", "secret", true), serviceAccountTestOption(rt, time.Now))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		err = cfg.Authenticate(ctx)
		if cancelExchange {
			if err != context.Canceled { //nolint:errorlint // Cache-boundary cancellation must return the bare caller context error.
				cancel()
				t.Fatal(err)
			}
		} else {
			var login *ServiceAccountLoginError
			if !errors.As(err, &login) || !errors.Is(err, ErrAuth) || errors.Is(err, ErrTokenCache) || login.Status != 401 {
				cancel()
				t.Fatal(err)
			}
		}
		cancel()
		if strings.Contains(fmt.Sprintf("%#v", err), "synthetic-peer-secret") {
			t.Fatal("cache login failure leaked")
		}
		entries, err := filepath.Glob(filepath.Join(dir, "service-account-v1", "*.json"))
		if err != nil || len(entries) != 0 {
			t.Fatal("failed login wrote token")
		}
	}
}

func TestServiceAccountCacheWriteFailureKeepsMemoryToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	key := tokencache.NewServiceAccountKey("", "client", serviceAccountTokenURL, "secret")
	path := filepath.Join(dir, "service-account-v1", key.Hash()+".json")
	calls := 0
	source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
		calls++
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		return serviceAccountResponse(200, `{"access_token":"fresh","expires_in":1800}`), nil
	}), time.Now)
	source.cache, source.cacheKey = tokencache.New(dir, time.Now), key
	for range 2 {
		token, err := source.Token(context.Background())
		if err != nil || token.AccessToken != "fresh" {
			t.Fatalf("successful exchange lost after write failure: %v", err)
		}
	}
	if calls != 1 {
		t.Fatal("write failure lost memory token")
	}
}
