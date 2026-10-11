package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type serviceAccountRoundTripper func(*http.Request) (*http.Response, error)

func (f serviceAccountRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func serviceAccountTestOption(rt http.RoundTripper, now func() time.Time) Option {
	return clientOptionFunc(func(c *clientConfig) { c.serviceAccountTransport = rt; c.serviceAccountClock = now })
}

func serviceAccountResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestServiceAccountConfigSnapshotAndIsolation(t *testing.T) {
	fixture, err := os.ReadFile("../../testdata/auth/service_account_token.json")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		id, secret, ok := r.BasicAuth()
		if !ok || id != "synthetic-client" || secret != "synthetic-secret" {
			t.Error("wrong credentials")
		}
		return serviceAccountResponse(200, string(fixture)), nil
	})
	option := WithServiceAccountCredentials("synthetic-client", "synthetic-secret", true)
	opts := []Option{WithRegion("hcm-3"), option, serviceAccountTestOption(rt, time.Now)}
	first, err := NewConfig(opts...)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewConfig(opts...)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("NewConfig sent request")
	}
	if ClientOf(first).UsesIAMUserLogin() {
		t.Fatal("service account has IAM provenance")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := first.Authenticate(context.Background()); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("shared config exchanges = %d", calls.Load())
	}
	if err := second.Authenticate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("separate configs exchanges = %d", calls.Load())
	}
}

func TestServiceAccountSelectionAndValidation(t *testing.T) {
	var sends atomic.Int64
	rt := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) { sends.Add(1); return nil, errors.New("unexpected") })
	for _, fields := range [][2]string{{"", ""}, {"client", ""}, {"", "secret"}, {"a:b", "secret"}, {"client", "a b"}, {"a\n", "secret"}, {"client", "a\x00"}, {"client", "a\u00a0b"}, {strings.Repeat("a", 4097), "secret"}, {"client", strings.Repeat("a", 4097)}} {
		option := WithServiceAccountCredentials(fields[0], fields[1], true)
		if _, err := NewConfig(WithRegion("hcm-3"), option, serviceAccountTestOption(rt, time.Now)); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("validation: %v", err)
		}
		for _, opts := range [][]Option{{option, WithStaticToken("token")}, {WithStaticToken("token"), option}, {option, WithCredentialsProvider(&fakeCredentialsProvider{})}, {WithCredentialsProvider(&fakeCredentialsProvider{}), option}} {
			if _, err := NewConfig(append([]Option{WithRegion("hcm-3")}, opts...)...); err != nil {
				t.Fatal(err)
			}
		}
	}
	if sends.Load() != 0 {
		t.Fatal("invalid credentials sent")
	}
	for _, opts := range [][]Option{{WithIAMUser(&IAMUserAuth{}), WithServiceAccountCredentials("client", "secret", true)}, {WithServiceAccountCredentials("client", "secret", true), WithIAMUser(&IAMUserAuth{})}} {
		if _, err := NewConfig(append([]Option{WithRegion("hcm-3")}, opts...)...); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials("", "", false), WithIAMUser(&IAMUserAuth{RootEmail: "r", Username: "u", Password: "p"})); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfig(WithRegion("hcm-3"), WithServiceAccountCredentials(strings.Repeat("a", 4096), strings.Repeat("b", 4096), true)); err != nil {
		t.Fatal(err)
	}
}

func TestServiceAccountInvalidationBoundaries(t *testing.T) {
	start := time.Unix(10000, 0)
	for _, age := range []time.Duration{-time.Second, 29 * time.Second, 30 * time.Second} {
		t.Run(age.String(), func(t *testing.T) {
			now := start
			calls := 0
			rt := serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
				calls++
				return serviceAccountResponse(200, fmt.Sprintf(`{"access_token":"token-%d","expires_in":1800}`, calls)), nil
			})
			source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, 120*time.Second, nil, rt, func() time.Time { return now })
			if _, err := source.Token(context.Background()); err != nil {
				t.Fatal(err)
			}
			now = start.Add(age)
			source.Invalidate("other-token")
			if _, err := source.Token(context.Background()); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal("invalidated another token")
			}
			source.Invalidate("token-1")
			if _, err := source.Token(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 1
			if age < 0 || age >= 30*time.Second {
				want = 2
			}
			if calls != want {
				t.Fatalf("exchanges = %d, want %d", calls, want)
			}
		})
	}
}

func TestServiceAccountCanceledWaiter(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, serviceAccountRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-release
		return serviceAccountResponse(200, `{"access_token":"token","expires_in":1800}`), nil
	}), time.Now)
	done := make(chan error, 1)
	go func() { _, err := source.Token(context.Background()); done <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	wait := make(chan error, 1)
	go func() { _, err := source.Token(ctx); wait <- err }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-wait; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := source.InvalidateContext(ctx, "token"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("canceled waiter sent")
	}
}

func TestServiceAccountMemoryExpiryAndCookies(t *testing.T) {
	start := time.Unix(10000, 0)
	for _, remaining := range []time.Duration{30*time.Second + time.Nanosecond, 30 * time.Second} {
		t.Run(remaining.String(), func(t *testing.T) {
			now := start
			calls := 0
			rt := serviceAccountRoundTripper(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Cookie") != "" {
					t.Fatal("exchange sent cookie")
				}
				response := serviceAccountResponse(200, fmt.Sprintf(`{"access_token":"token-%d","expires_in":1800}`, calls))
				response.Header.Set("Set-Cookie", "synthetic-cookie=value; Path=/")
				return response, nil
			})
			source := newServiceAccountTokenSource(&serviceAccountCredentials{clientID: "client", secret: newServiceAccountSecret("secret")}, time.Minute, nil, rt, func() time.Time { return now })
			if _, err := source.Token(context.Background()); err != nil {
				t.Fatal(err)
			}
			now = start.Add(1800*time.Second - remaining)
			if _, err := source.Token(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := 1
			if remaining <= 30*time.Second {
				want = 2
			}
			if calls != want {
				t.Fatalf("exchanges = %d, want %d", calls, want)
			}
		})
	}
}

func TestServiceAccountWinnerDoesNotFallBack(t *testing.T) {
	failure := errors.New("synthetic-provider-failure")
	provider := &fakeCredentialsProvider{err: failure}
	serviceAccount := WithServiceAccountCredentials("client", "secret", true)
	for _, options := range [][]Option{
		{serviceAccount, WithStaticToken("token"), WithCredentialsProvider(provider)},
		{WithCredentialsProvider(provider), WithStaticToken("token"), serviceAccount},
	} {
		cfg, err := NewConfig(append([]Option{WithRegion("hcm-3")}, options...)...)
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Authenticate(context.Background()); !errors.Is(err, failure) {
			t.Fatalf("provider winner: %v", err)
		}
	}
	for _, options := range [][]Option{{serviceAccount, WithStaticToken("token")}, {WithStaticToken("token"), serviceAccount}} {
		cfg, err := NewConfig(append([]Option{WithRegion("hcm-3")}, options...)...)
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Authenticate(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}
