package core

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"danny.vn/vngcloud/internal/iamuser"
)

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

func (a *IAMUserAuth) token(ctx context.Context) (string, time.Time, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cachedToken != "" && time.Until(a.expiresAt) > 30*time.Second {
		return a.cachedToken, a.expiresAt, nil
	}

	req := iamuser.LoginRequest{
		RootEmail:     a.RootEmail,
		Username:      a.Username,
		Password:      a.Password,
		TOTP:          a.TOTP,
		SigninBaseURL: a.SigninBaseURL,
		TokenURL:      a.TokenURL,
		DashboardURI:  a.DashboardURI,
		HTTPClient:    a.HTTPClient,
	}
	token, expiresAt, err := iamuser.Login(ctx, req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("%w: %w", ErrAuth, err)
	}
	a.cachedToken = token
	a.expiresAt = expiresAt
	return token, expiresAt, nil
}
