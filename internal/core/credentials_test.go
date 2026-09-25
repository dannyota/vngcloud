package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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

// fakeIAMServers stands up signin and token httptest servers that complete
// the login flow (CSRF page, form-post redirect, token exchange) exactly as
// the real console does, issuing tokens named "tok-1", "tok-2", ... in call
// order. It returns the endpoints to plug into an IAMUserAuth and the total
// login count.
func fakeIAMServers(t *testing.T) (signinURL, tokenURL string, logins *atomic.Int64) {
	t.Helper()
	logins = &atomic.Int64{}

	var tokenURLRef string
	signin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte(`<html><input name="_csrf" value="csrf"></html>`))
		case http.MethodPost:
			http.Redirect(w, r, tokenURLRef+"/callback?code=auth-code", http.StatusFound)
		}
	}))
	t.Cleanup(signin.Close)

	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := logins.Add(1)
		_, _ = fmt.Fprintf(w, `{"accessToken":"tok-%d","expiresIn":3600}`, n)
	}))
	t.Cleanup(token.Close)
	tokenURLRef = token.URL

	return signin.URL, token.URL, logins
}

func fakeIAMUser(signinURL, tokenURL string) *IAMUserAuth {
	return &IAMUserAuth{
		RootEmail:     "root@example.test",
		Username:      "user",
		Password:      "pass",
		SigninBaseURL: signinURL,
		TokenURL:      tokenURL,
		DashboardURI:  tokenURL + "/",
	}
}

// withFakeClock overrides timeNow for the duration of the test and returns a
// setter for the fake time, restoring the real clock on cleanup.
func withFakeClock(t *testing.T) func(time.Time) {
	t.Helper()
	original := timeNow
	current := time.Now()
	timeNow = func() time.Time { return current }
	t.Cleanup(func() { timeNow = original })
	return func(next time.Time) { current = next }
}

func doRequest(t *testing.T, c *Client, url string) (int, error) {
	t.Helper()
	status, err := c.DoJSONStatus(context.Background(), transport.Request{
		Operation: "test",
		Method:    http.MethodGet,
		URL:       url,
		OK:        []int{http.StatusOK},
	}, nil)
	return status, err
}

func TestUnauthorizedLogsInAgain(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	setClock := withFakeClock(t)

	var seenMu sync.Mutex
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		seenMu.Lock()
		seen = append(seen, token)
		seenMu.Unlock()
		if token == "tok-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	c, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	setClock(time.Now().Add(60 * time.Second))

	if _, err := doRequest(t, c, api.URL); err != nil {
		t.Fatalf("DoJSONStatus() error = %v", err)
	}

	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seen) != 2 || seen[0] != "tok-1" || seen[1] != "tok-2" {
		t.Fatalf("seen tokens = %v, want [tok-1 tok-2]", seen)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2", logins.Load())
	}
}

func TestUnauthorizedFreshTokenNoRelogin(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	withFakeClock(t)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer api.Close()

	c, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}

	// The clock does not advance: the cached token is under 30 seconds old.
	if _, err := doRequest(t, c, api.URL); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

func TestParallelUnauthorizedLogsInOnce(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	setClock := withFakeClock(t)

	const n = 8
	var arrived sync.WaitGroup
	arrived.Add(n)
	release := make(chan struct{})

	var mu sync.Mutex
	seen := map[string]int{}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		seen[token]++
		mu.Unlock()
		if token == "tok-1" {
			arrived.Done()
			<-release
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	c, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if err := c.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	setClock(time.Now().Add(60 * time.Second))

	go func() {
		arrived.Wait()
		close(release)
	}()

	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := doRequest(t, c, api.URL)
			errs[i] = err
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d error = %v", i, err)
		}
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2", logins.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if seen["tok-1"] != n {
		t.Fatalf("tok-1 requests = %d, want %d", seen["tok-1"], n)
	}
	if seen["tok-2"] != n {
		t.Fatalf("tok-2 requests = %d, want %d", seen["tok-2"], n)
	}
}

// fakeCredentialsProvider is a minimal CredentialsProvider for tests that do
// not need a real login flow.
type fakeCredentialsProvider struct {
	mu          sync.Mutex
	token       Token
	err         error
	invalidated []string
}

func (p *fakeCredentialsProvider) Token(context.Context) (Token, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.token, p.err
}

func (p *fakeCredentialsProvider) Invalidate(sent string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.invalidated = append(p.invalidated, sent)
}

func TestNoRequestWithoutToken(t *testing.T) {
	called := false
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	provider := &fakeCredentialsProvider{}
	c, err := newClient(WithRegion("hcm-3"), WithCredentialsProvider(provider),
		WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	if _, err := doRequest(t, c, api.URL); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if called {
		t.Fatal("request reached the API despite an empty token")
	}
}

func TestCustomCredentialsProvider(t *testing.T) {
	provider := &fakeCredentialsProvider{token: Token{AccessToken: "tok-1", ExpiresAt: time.Now().Add(time.Hour)}}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer api.Close()

	c, err := newClient(WithRegion("hcm-3"), WithCredentialsProvider(provider),
		WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	if _, err := doRequest(t, c, api.URL); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.invalidated) != 1 || provider.invalidated[0] != "tok-1" {
		t.Fatalf("invalidated = %v, want [tok-1]", provider.invalidated)
	}
}

// TestTwoConfigsSharedTokenCacheLogInOnce builds two Configs from two
// independent IAMUserAuth values (not a shared pointer) with the same
// credentials, pointed at one token cache directory. Only the on-disk cache
// can make this share a login, since the two IAMUserAuth values have no
// in-memory state in common.
func TestTwoConfigsSharedTokenCacheLogInOnce(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	c1, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithTokenCache(cacheDir), WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() #1 error = %v", err)
	}
	c2, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithTokenCache(cacheDir), WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() #2 error = %v", err)
	}

	if err := c1.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() #1 error = %v", err)
	}
	if err := c2.Authenticate(context.Background()); err != nil {
		t.Fatalf("Authenticate() #2 error = %v", err)
	}

	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

// writeCacheFile writes a token cache entry directly to disk, the way an
// older process (or an attacker) might have left one, so a test can control
// its ObtainedAt independently of when the test itself runs.
func writeCacheFile(t *testing.T, dir string, key tokencache.Key, accessToken string, expiresAt, obtainedAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"accessToken": accessToken,
		"expiresAt":   expiresAt,
		"obtainedAt":  obtainedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, key.Hash()+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestCachedOldTokenRejectedLogsInOnce covers a disk token obtained by
// another process 10 minutes ago: old enough that the 30-second rule must
// let the server's 401 for it invalidate it, so the call ends in exactly one
// login instead of ErrAuth. A later process-style client, sharing only the
// disk cache, then reuses the freshly logged-in token without logging in
// again.
func TestCachedOldTokenRejectedLogsInOnce(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	auth := fakeIAMUser(signinURL, tokenURL)
	key := tokencache.Key{RootEmail: auth.RootEmail, Username: auth.Username, SigninURL: signinURL, TokenURL: tokenURL}

	var rejectedOldToken atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") == "tok-old" {
			rejectedOldToken.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	now := time.Now()
	writeCacheFile(t, cacheDir, key, "tok-old", now.Add(time.Hour), now.Add(-10*time.Minute))

	c1, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithTokenCache(cacheDir), WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() #1 error = %v", err)
	}
	if _, err := doRequest(t, c1, api.URL); err != nil {
		t.Fatalf("DoJSONStatus() #1 error = %v", err)
	}
	if rejectedOldToken.Load() != 1 {
		t.Fatalf("requests rejected for tok-old = %d, want 1", rejectedOldToken.Load())
	}
	if logins.Load() != 1 {
		t.Fatalf("logins after first process = %d, want 1", logins.Load())
	}

	c2, err := newClient(WithRegion("hcm-3"), WithIAMUser(fakeIAMUser(signinURL, tokenURL)),
		WithTokenCache(cacheDir), WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() #2 error = %v", err)
	}
	if _, err := doRequest(t, c2, api.URL); err != nil {
		t.Fatalf("DoJSONStatus() #2 error = %v", err)
	}
	if logins.Load() != 1 {
		t.Fatalf("logins after second process = %d, want 1 (it must reuse the cached token, not log in again)", logins.Load())
	}
}

// TestTokenSourceRestoresRejectedAfterCacheGetFails covers a Cache.Get call
// that fails (here, the login it attempts fails because ctx is already
// canceled) after iamTokenSource.Token had already taken the pending
// rejected token out of s.rejected to pass to it. Losing that rejection
// would let a later, successful call treat the same bad disk token as
// unrejected and reuse it forever.
func TestTokenSourceRestoresRejectedAfterCacheGetFails(t *testing.T) {
	signinURL, tokenURL, logins := fakeIAMServers(t)
	// A fresh subdirectory, not t.TempDir() itself: writeCacheFile's
	// os.MkdirAll only applies the 0700 mode when it creates the directory,
	// and t.TempDir() already exists.
	dir := filepath.Join(t.TempDir(), "cache")
	auth := fakeIAMUser(signinURL, tokenURL)
	key := tokencache.Key{RootEmail: auth.RootEmail, Username: auth.Username, SigninURL: signinURL, TokenURL: tokenURL}

	now := time.Now()
	writeCacheFile(t, dir, key, "tok-old", now.Add(time.Hour), now.Add(-time.Minute))

	source := &iamTokenSource{
		auth:      auth,
		endpoints: loginEndpoints{signin: signinURL, token: tokenURL, dashboard: tokenURL + "/"},
		cache:     tokencache.New(dir, time.Now),
		cacheKey:  key,
	}
	source.rejected = "tok-old"

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Token(canceledCtx); err == nil {
		t.Fatal("expected an error from a canceled login attempt")
	}
	if source.rejected != "tok-old" {
		t.Fatalf("rejected = %q, want tok-old restored after the failed Get", source.rejected)
	}

	got, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if got.AccessToken == "tok-old" {
		t.Fatal("expected the still-rejected disk token to be replaced, not reused")
	}
	if logins.Load() != 1 {
		t.Fatalf("logins = %d, want 1", logins.Load())
	}
}

// emptyAfterInvalidateProvider hands out one usable token, then an empty one
// (nil error) for every call once Invalidate has been called, simulating a
// broken CredentialsProvider.
type emptyAfterInvalidateProvider struct {
	invalidated atomic.Bool
}

func (p *emptyAfterInvalidateProvider) Token(context.Context) (Token, error) {
	if p.invalidated.Load() {
		return Token{}, nil
	}
	return Token{AccessToken: "tok-1", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (p *emptyAfterInvalidateProvider) Invalidate(string) { p.invalidated.Store(true) }

// TestRetryAfterInvalidateNeverSendsWithoutAToken covers the retry after a
// 401 when the provider's own invalidation leaves it with nothing to hand
// out: the retry must be refused before it is sent, never sent with a
// missing Authorization header.
func TestRetryAfterInvalidateNeverSendsWithoutAToken(t *testing.T) {
	var totalRequests atomic.Int64
	var unauthenticatedRequests atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		totalRequests.Add(1)
		if r.Header.Get("Authorization") == "" {
			unauthenticatedRequests.Add(1)
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer api.Close()

	c, err := newClient(WithRegion("hcm-3"), WithCredentialsProvider(&emptyAfterInvalidateProvider{}),
		WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}

	if _, err := doRequest(t, c, api.URL); !errors.Is(err, ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if totalRequests.Load() != 1 {
		t.Fatalf("requests sent = %d, want 1 (the retry must be refused before sending)", totalRequests.Load())
	}
	if unauthenticatedRequests.Load() != 0 {
		t.Fatalf("unauthenticated requests sent = %d, want 0", unauthenticatedRequests.Load())
	}
}

// TestAuthenticateFailsForEmptyTokenProvider covers Authenticate itself,
// separately from a request retry: a provider that never has a token to
// give must fail eagerly with ErrAuth rather than reporting success.
func TestAuthenticateFailsForEmptyTokenProvider(t *testing.T) {
	c, err := newClient(WithRegion("hcm-3"), WithCredentialsProvider(&fakeCredentialsProvider{}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if err := c.Authenticate(context.Background()); !errors.Is(err, ErrAuth) {
		t.Fatalf("Authenticate() error = %v, want ErrAuth", err)
	}
}

// TestStaticTokenConfigWithTokenCacheWritesNothing confirms that a static
// token never touches the token cache directory, even when one is
// configured: the cache is IAM-User-only.
func TestStaticTokenConfigWithTokenCacheWritesNothing(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer api.Close()

	c, err := newClient(WithRegion("hcm-3"), WithStaticToken("static-tok"),
		WithTokenCache(cacheDir), WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
	if err != nil {
		t.Fatalf("newClient() error = %v", err)
	}
	if _, err := doRequest(t, c, api.URL); err != nil {
		t.Fatalf("DoJSONStatus() error = %v", err)
	}

	if _, err := os.Stat(cacheDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cache dir stat error = %v, want os.ErrNotExist (nothing should be written)", err)
	}
}

func TestCredentialOptionPrecedence(t *testing.T) {
	t.Run("static token wins over IAM user", func(t *testing.T) {
		var gotAuth string
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer api.Close()

		c, err := newClient(WithRegion("hcm-3"),
			WithIAMUser(&IAMUserAuth{RootEmail: "r", Username: "u", Password: "p"}),
			WithStaticToken("static-tok"),
			WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
		if err != nil {
			t.Fatalf("newClient() error = %v", err)
		}
		if _, err := doRequest(t, c, api.URL); err != nil {
			t.Fatalf("DoJSONStatus() error = %v", err)
		}
		if gotAuth != "Bearer static-tok" {
			t.Fatalf("Authorization = %q, want the static token", gotAuth)
		}
	})

	t.Run("provider wins over static token", func(t *testing.T) {
		var gotAuth string
		api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		}))
		defer api.Close()

		provider := &fakeCredentialsProvider{token: Token{AccessToken: "provider-tok", ExpiresAt: time.Now().Add(time.Hour)}}
		c, err := newClient(WithRegion("hcm-3"),
			WithCredentialsProvider(provider),
			WithStaticToken("static-tok"),
			WithEndpointOverrides(EndpointOverrides{VServer: api.URL}))
		if err != nil {
			t.Fatalf("newClient() error = %v", err)
		}
		if _, err := doRequest(t, c, api.URL); err != nil {
			t.Fatalf("DoJSONStatus() error = %v", err)
		}
		if gotAuth != "Bearer provider-tok" {
			t.Fatalf("Authorization = %q, want the provider token", gotAuth)
		}
	})
}
