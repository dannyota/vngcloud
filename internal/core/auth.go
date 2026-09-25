package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"danny.vn/vngcloud/internal/iamuser"
)

// timeNow is overridden in tests to control the 30-second freshness window
// that decides whether a 401 invalidates a cached IAM token, without adding
// a public clock option.
var timeNow = time.Now

// IAMUserAuth holds IAM User credentials for GreenNode console authentication.
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
func (a *IAMUserAuth) token(ctx context.Context, ep loginEndpoints, logger *slog.Logger) (string, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cachedToken != "" && time.Until(a.expiresAt) > 30*time.Second {
		return a.cachedToken, a.expiresAt, nil
	}

	token, expiresAt, err := a.doLogin(ctx, ep, logger)
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
// result. logger, when non-nil, gets exactly "login started" before the
// attempt and "login finished" with its outcome after; iamuser's own HTTP
// requests are never logged.
func (a *IAMUserAuth) doLogin(ctx context.Context, ep loginEndpoints, logger *slog.Logger) (string, time.Time, error) {
	debugLog(logger, ctx, "login started")
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
		debugLog(logger, ctx, "login finished", "ok", false)
		return "", time.Time{}, loginErrorFrom(ctx, err)
	}
	debugLog(logger, ctx, "login finished", "ok", true)
	return result.AccessToken, result.ExpiresAt, nil
}

// debugLog writes msg at Debug level when logger is set, so a Config built
// without WithLogger logs nothing.
func debugLog(logger *slog.Logger, ctx context.Context, msg string, args ...any) {
	if logger == nil {
		return
	}
	logger.DebugContext(ctx, msg, args...)
}

// loginErrorFrom builds the *LoginError this package returns for every IAM
// User login failure. Err is ErrAuth, unless ctx itself ended (canceled or
// past its deadline), in which case it is ctx.Err(); either way, err's own
// text is never surfaced or wrapped, only the Step, Status, and
// CaptchaSuspected an *iamuser.LoginFailure carries, because err's cause can
// hold a token response body or a full URL carrying an authorization code.
func loginErrorFrom(ctx context.Context, err error) error {
	le := &LoginError{Err: ErrAuth, Reason: "login failed"}
	if ctxErr := ctx.Err(); ctxErr != nil {
		le.Err = ctxErr
	}
	var failure *iamuser.LoginFailure
	if errors.As(err, &failure) {
		le.Status = failure.Status
		le.CaptchaSuspected = failure.CaptchaSuspected
		le.Reason = reasonForStep(failure.Step)
	}
	return le
}

// reasonForStep gives the fixed, safe LoginError.Reason text for step.
func reasonForStep(step iamuser.FailureStep) string {
	switch step {
	case iamuser.StepSigninPage:
		return "could not load the sign-in page"
	case iamuser.StepSigninSubmit:
		return "could not submit the sign-in form"
	case iamuser.StepSigninRejected:
		return "the sign-in form was rejected"
	case iamuser.StepTOTPRequired:
		return "the account requires a TOTP code but none was configured"
	case iamuser.StepTOTPPage:
		return "could not load the two-factor page"
	case iamuser.StepTOTPCode:
		return "could not get a TOTP code"
	case iamuser.StepTOTPSubmit:
		return "could not submit the two-factor code"
	case iamuser.StepTOTPRejected:
		return "the two-factor code was rejected"
	case iamuser.StepAuthCode:
		return "the sign-in response had no authorization code"
	case iamuser.StepTokenExchange:
		return "could not exchange the authorization code for a token"
	default:
		return "login failed"
	}
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
// from disk or freshly logged in there). obtainedAt is the token cache's own
// record of when the token was first obtained, not necessarily now: a fresh
// process that reuses another process's still-valid disk token must keep
// that token's true age, or Invalidate's 30-second rule could never fire for
// it.
func (a *IAMUserAuth) remember(token string, expiresAt, obtainedAt time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cachedToken = token
	a.expiresAt = expiresAt
	a.obtainedAt = obtainedAt
}

// Invalidate drops the cached token when sent is still the cached one and it
// has been held at least 30 seconds, or its obtain time is after now (the
// clock moved backward, which must not be read as "just obtained"). A token
// younger than that is left in place: the retry after a 401 then reuses it,
// the server rejects it again, and the call ends in ErrAuth instead of a
// second login inside the same 30-second TOTP window.
func (a *IAMUserAuth) Invalidate(sent string) {
	if sent == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cachedToken != sent {
		return
	}
	age := timeNow().Sub(a.obtainedAt)
	if age < 0 || age >= 30*time.Second {
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
