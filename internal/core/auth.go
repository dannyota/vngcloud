package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"danny.vn/vngcloud/internal/iamuser"
	"danny.vn/vngcloud/internal/transport"
)

// timeNow is overridden in tests to control the 30-second freshness window
// that decides whether a 401 invalidates a cached IAM token, without adding
// a public clock option.
var timeNow = time.Now

// IAMUserAuth holds IAM User credentials for GreenNode console authentication.
type IAMUserAuth struct {
	// RootEmail identifies the root account.
	RootEmail string
	// Username identifies the IAM user.
	Username string
	// Password supplies the IAM login password.
	Password Secret
	// TOTP supplies one-time codes when required.
	TOTP TOTPProvider

	// SigninBaseURL overrides the IAM sign-in endpoint.
	SigninBaseURL string
	// TokenURL overrides the IAM token endpoint.
	TokenURL string
	// DashboardURI overrides the login redirect URI.
	DashboardURI string
	// HTTPClient sends IAM login requests when set.
	HTTPClient *http.Client

	mu           transport.Gate
	cachedToken  *serviceAccountSecret
	expiresAt    time.Time
	obtainedAt   time.Time
	loginMu      transport.Gate
	failedAt     time.Time
	loginFailure error
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

func (a *IAMUserAuth) token(ctx context.Context, ep loginEndpoints, logger *slog.Logger) (string, time.Time, error) {
	if err := a.mu.LockContext(ctx); err != nil {
		return "", time.Time{}, err
	}
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}

	if a.cachedToken != nil && time.Until(a.expiresAt) > 30*time.Second {
		return string(*a.cachedToken), a.expiresAt, nil
	}

	token, expiresAt, err := a.doLogin(ctx, ep, logger)
	if err != nil {
		return "", time.Time{}, err
	}
	obtainedAt := timeNow()
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	a.cachedToken = newServiceAccountSecret(token)
	a.expiresAt = expiresAt
	a.obtainedAt = obtainedAt
	return string(*a.cachedToken), a.expiresAt, nil
}

func (a *IAMUserAuth) doLogin(ctx context.Context, ep loginEndpoints, logger *slog.Logger) (token string, expiresAt time.Time, err error) {
	if err := a.loginMu.LockContext(ctx); err != nil {
		return "", time.Time{}, err
	}
	defer a.loginMu.Unlock()
	age := timeNow().Sub(a.failedAt)
	if a.loginFailure != nil && age >= 0 && age < 30*time.Second {
		return "", time.Time{}, a.loginFailure
	}
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	defer func() {
		if err == nil {
			a.loginFailure = nil
			return
		}
		a.failedAt = timeNow()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			a.loginFailure = &LoginError{Err: ErrAuth, Reason: "login failed"}
		} else {
			a.loginFailure = err
		}
	}()
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
	}
	debugLog(logger, ctx, "login started")
	req := iamuser.LoginRequest{
		RootEmail:     a.RootEmail,
		Username:      a.Username,
		Password:      a.Password.Reveal(),
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
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, err
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

// Invalidate drops the cached token when sent is still the cached one and it
// has been held at least 30 seconds, or its obtain time is after now (the
// clock moved backward, which must not be read as "just obtained"). A token
// younger than that is left in place: the retry after a 401 then reuses it,
// the server rejects it again, and the call ends in ErrAuth instead of a
// second login inside the same 30-second TOTP window.
func (a *IAMUserAuth) Invalidate(sent string) {
	_ = a.invalidateContext(context.Background(), sent)
}

func (a *IAMUserAuth) invalidateContext(ctx context.Context, sent string) error {
	if err := a.mu.LockContext(ctx); err != nil {
		return err
	}
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if sent == "" || a.cachedToken == nil || string(*a.cachedToken) != sent {
		return nil
	}
	age := timeNow().Sub(a.obtainedAt)
	if err := ctx.Err(); err != nil {
		return err
	}
	if age < 0 || age >= 30*time.Second {
		a.cachedToken = nil
		a.expiresAt = time.Time{}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// cachedIfFresh checks context after acquiring the shared IAM lock.
func (a *IAMUserAuth) cachedIfFresh(ctx context.Context) (string, time.Time, bool, error) {
	if err := a.mu.LockContext(ctx); err != nil {
		return "", time.Time{}, false, err
	}
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", time.Time{}, false, err
	}
	if a.cachedToken != nil && time.Until(a.expiresAt) > 30*time.Second {
		return string(*a.cachedToken), a.expiresAt, true, nil
	}
	return "", time.Time{}, false, nil
}

// remember retains the disk token's original obtain time for 401 recovery.
func (a *IAMUserAuth) remember(ctx context.Context, token string, expiresAt, obtainedAt time.Time) error {
	if err := a.mu.LockContext(ctx); err != nil {
		return err
	}
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	a.cachedToken, a.expiresAt, a.obtainedAt = newServiceAccountSecret(token), expiresAt, obtainedAt
	return nil
}
