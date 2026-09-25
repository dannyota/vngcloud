package core

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"danny.vn/vngcloud/internal/iamuser"
)

// timeNow is overridden in tests to control the 30-second freshness window
// that decides whether a 401 invalidates a cached IAM token, without adding
// a public clock option.
var timeNow = time.Now

// IAMUserAuth holds IAM User credentials for VNG Cloud console authentication.
type IAMUserAuth struct {
	RootEmail string
	Username  string
	Password  string
	TOTP      TOTPProvider

	// Advanced test hooks. Leave empty for normal SDK use.
	SigninBaseURL string
	TokenURL      string
	DashboardURI  string
	HTTPClient    *http.Client

	mu          sync.Mutex
	cachedToken string
	expiresAt   time.Time
	obtainedAt  time.Time
}

type loginEndpoints struct {
	signin    string
	token     string
	dashboard string
}

func (a *IAMUserAuth) validate() error {
	if a == nil {
		return fmt.Errorf("%w: IAMUser is required", ErrInvalidConfig)
	}
	if a.RootEmail == "" {
		return fmt.Errorf("%w: IAMUser.RootEmail is required", ErrInvalidConfig)
	}
	if a.Username == "" {
		return fmt.Errorf("%w: IAMUser.Username is required", ErrInvalidConfig)
	}
	if a.Password == "" {
		return fmt.Errorf("%w: IAMUser.Password is required", ErrInvalidConfig)
	}
	return nil
}

// token returns a token for ep, reusing the in-memory cache when it is
// fresh and otherwise performing one login while holding mu, so concurrent
// callers sharing this IAMUserAuth (whether from one Config or several)
// never log in more than once for the same request. This is the path used
// when no token cache directory is configured; with one configured,
// iamTokenSource instead calls cachedIfFresh, doLogin, and remember
// directly, so the cross-process disk lock (not this mutex) is what
// serializes the actual login.
func (a *IAMUserAuth) token(ctx context.Context, ep loginEndpoints) (string, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cachedToken != "" && time.Until(a.expiresAt) > 30*time.Second {
		return a.cachedToken, a.expiresAt, nil
	}

	token, expiresAt, err := a.doLogin(ctx, ep)
	if err != nil {
		return "", time.Time{}, err
	}
	a.cachedToken = token
	a.expiresAt = expiresAt
	a.obtainedAt = timeNow()
	return a.cachedToken, a.expiresAt, nil
}

// doLogin performs one physical login against ep. It touches no cache,
// in-memory or on disk: callers decide separately whether to record the
// result.
func (a *IAMUserAuth) doLogin(ctx context.Context, ep loginEndpoints) (string, time.Time, error) {
	req := iamuser.LoginRequest{
		RootEmail:     a.RootEmail,
		Username:      a.Username,
		Password:      a.Password,
		TOTP:          a.TOTP,
		SigninBaseURL: firstNonEmpty(a.SigninBaseURL, ep.signin),
		TokenURL:      firstNonEmpty(a.TokenURL, ep.token),
		DashboardURI:  firstNonEmpty(a.DashboardURI, ep.dashboard),
		HTTPClient:    a.HTTPClient,
	}
	result, err := iamuser.Login(ctx, req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %w", ErrAuth, err)
	}
	return result.AccessToken, result.ExpiresAt, nil
}

// cachedIfFresh returns the in-memory token when present and not within 30
// seconds of expiry, without touching the network or disk.
func (a *IAMUserAuth) cachedIfFresh() (string, time.Time, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cachedToken != "" && time.Until(a.expiresAt) > 30*time.Second {
		return a.cachedToken, a.expiresAt, true
	}
	return "", time.Time{}, false
}

// remember records token as the in-memory cache, for this process to reuse
// without a disk read, after it came from the token cache (either reused
// from disk or freshly logged in there).
func (a *IAMUserAuth) remember(token string, expiresAt time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedToken = token
	a.expiresAt = expiresAt
	a.obtainedAt = timeNow()
}

// Invalidate drops the cached token when sent is still the cached one and it
// has been held at least 30 seconds. A token younger than that is left in
// place: the retry after a 401 then reuses it, the server rejects it again,
// and the call ends in ErrAuth instead of a second login inside the same
// 30-second TOTP window.
func (a *IAMUserAuth) Invalidate(sent string) {
	if sent == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cachedToken == sent && timeNow().Sub(a.obtainedAt) >= 30*time.Second {
		a.cachedToken = ""
		a.expiresAt = time.Time{}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
