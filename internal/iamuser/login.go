package iamuser

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"danny.vn/vngcloud/internal/endpoints"
)

const (
	dashboardClientID = "c9e78411-f2a2-41ba-a9e4-3c56263c181a"
	loginPath         = "/ap/auth/iam/login"
	twoFAPathMatch    = "/ap/auth/iam/google"
)

// FailureStep names the login step that failed. core turns a Step into a
// fixed, safe message; it never derives that message from the failure's own
// text (see LoginFailure).
type FailureStep string

const (
	StepSigninPage     FailureStep = "signin page"
	StepSigninSubmit   FailureStep = "signin submission"
	StepSigninRejected FailureStep = "signin rejection"
	StepTOTPRequired   FailureStep = "totp requirement"
	StepTOTPPage       FailureStep = "totp page"
	StepTOTPCode       FailureStep = "totp code"
	StepTOTPSubmit     FailureStep = "totp submission"
	StepTOTPRejected   FailureStep = "totp rejection"
	StepAuthCode       FailureStep = "authorization code"
	StepTokenExchange  FailureStep = "token exchange"
)

// LoginFailure reports which step of the login flow failed, the HTTP status
// observed there (0 when none applies), and whether the sign-in form was
// redisplayed after a submit (a captcha may be required). Error and Unwrap
// expose the underlying cause for this package's own diagnostics and tests;
// a caller outside this package must build its own message from Step,
// Status, and CaptchaSuspected instead, because the cause can hold a token
// response body or a full URL carrying an authorization code.
type LoginFailure struct {
	Step             FailureStep
	Status           int
	CaptchaSuspected bool
	cause            error
}

func (f *LoginFailure) Error() string { return f.cause.Error() }
func (f *LoginFailure) Unwrap() error { return f.cause }

type TOTPProvider interface {
	GetCode(ctx context.Context) (string, error)
}

type LoginRequest struct {
	RootEmail     string
	Username      string
	Password      string
	TOTP          TOTPProvider
	SigninBaseURL string
	TokenURL      string
	DashboardURI  string
	HTTPClient    *http.Client
}

// Result is a successful login. RefreshToken is only populated when the
// token endpoint returns one; the SDK does not yet use it.
type Result struct {
	AccessToken  string
	ExpiresAt    time.Time
	RefreshToken string
}

func Login(ctx context.Context, req LoginRequest) (Result, error) {
	signinBaseURL := req.SigninBaseURL
	if signinBaseURL == "" {
		signinBaseURL = endpoints.DefaultSignin
	}
	signinBaseURL = strings.TrimRight(signinBaseURL, "/")

	tokenURL := req.TokenURL
	if tokenURL == "" {
		tokenURL = endpoints.DefaultToken
	}
	dashboardURI := req.DashboardURI
	if dashboardURI == "" {
		dashboardURI = endpoints.DefaultDashboard
	}

	jar, _ := cookiejar.New(nil)
	httpClient := req.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 120 * time.Second}
	}
	client := *httpClient
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	verifier, challenge := generatePKCE()
	loginURL := fmt.Sprintf("%s%s?clientId=%s&responseType=code&codeChallenge=%s&codeChallengeMethod=S256&redirectUri=%s&rootEmail=%s",
		signinBaseURL,
		loginPath,
		dashboardClientID,
		challenge,
		url.QueryEscape(dashboardURI),
		url.QueryEscape(req.RootEmail),
	)

	pageBody, pageStatus, err := doGet(ctx, &client, loginURL)
	if err != nil {
		return Result{}, &LoginFailure{Step: StepSigninPage, Status: pageStatus, cause: fmt.Errorf("GET login page: %w", err)}
	}
	csrfToken := extractCSRFToken(string(pageBody))
	if csrfToken == "" {
		return Result{}, &LoginFailure{Step: StepSigninPage, Status: pageStatus, cause: errors.New("no CSRF token on login page")}
	}

	formData := url.Values{
		"_csrf":     {csrfToken},
		"rootEmail": {req.RootEmail},
		"username":  {req.Username},
		"password":  {req.Password},
	}
	location, status, err := doPostForm(ctx, &client, signinBaseURL, loginURL, formData)
	if err != nil {
		return Result{}, &LoginFailure{Step: StepSigninSubmit, cause: fmt.Errorf("POST login: %w", err)}
	}
	switch {
	case status == http.StatusMovedPermanently || status == http.StatusPermanentRedirect:
		return Result{}, &LoginFailure{Step: StepSigninSubmit, Status: status,
			cause: fmt.Errorf("login endpoint moved permanently (redirect to %q): update the Signin endpoint override", location)}
	case status == http.StatusOK:
		return Result{}, &LoginFailure{Step: StepSigninRejected, Status: status, CaptchaSuspected: true,
			cause: errors.New("login rejected: signin re-displayed the form (wrong credentials, or a captcha is required)")}
	case location == "":
		return Result{}, &LoginFailure{Step: StepSigninSubmit, Status: status, cause: errors.New("login failed: no redirect")}
	}

	if strings.Contains(location, twoFAPathMatch) {
		if req.TOTP == nil {
			return Result{}, &LoginFailure{Step: StepTOTPRequired, cause: errors.New("2FA required but no TOTP provider configured")}
		}
		location, err = handle2FA(ctx, &client, signinBaseURL, location, req.TOTP)
		if err != nil {
			return Result{}, err
		}
	}

	authCode := extractAuthCode(location)
	if authCode == "" {
		return Result{}, &LoginFailure{Step: StepAuthCode, cause: errors.New("no authorization code in redirect")}
	}

	token, err := exchangeToken(ctx, &client, tokenURL, dashboardURI, authCode, verifier)
	if err != nil {
		return Result{}, err
	}
	return Result{
		AccessToken:  token.AccessToken,
		ExpiresAt:    time.Now().Add(time.Duration(token.ExpiresIn) * time.Second),
		RefreshToken: token.RefreshToken,
	}, nil
}

func handle2FA(ctx context.Context, client *http.Client, signinBaseURL, redirectPath string, totp TOTPProvider) (string, error) {
	twoFAURL := redirectPath
	if !strings.HasPrefix(redirectPath, "http") {
		twoFAURL = signinBaseURL + redirectPath
	}

	pageBody, pageStatus, err := doGet(ctx, client, twoFAURL)
	if err != nil {
		return "", &LoginFailure{Step: StepTOTPPage, Status: pageStatus, cause: fmt.Errorf("GET 2FA page: %w", err)}
	}
	csrfToken := extractCSRFToken(string(pageBody))
	if csrfToken == "" {
		return "", &LoginFailure{Step: StepTOTPPage, Status: pageStatus, cause: errors.New("no CSRF token on 2FA page")}
	}

	code, err := totp.GetCode(ctx)
	if err != nil {
		return "", &LoginFailure{Step: StepTOTPCode, cause: fmt.Errorf("get TOTP code: %w", err)}
	}

	location, status, err := doPostForm(ctx, client, signinBaseURL, twoFAURL, url.Values{
		"_csrf": {csrfToken},
		"token": {code},
	})
	if err != nil {
		return "", &LoginFailure{Step: StepTOTPSubmit, cause: err}
	}
	if status == http.StatusOK {
		return "", &LoginFailure{Step: StepTOTPRejected, Status: status,
			cause: errors.New("2FA rejected: signin re-displayed the form (TOTP code wrong or already used)")}
	}
	return location, nil
}

type tokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    int64  `json:"expiresIn"`
}

func exchangeToken(ctx context.Context, client *http.Client, tokenURL, dashboardURI, authCode, verifier string) (*tokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"grantType":    "authorization_code",
		"code":         authCode,
		"redirectUri":  dashboardURI,
		"scope":        "openid",
		"codeVerifier": verifier,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, bytes.NewReader(body))
	if err != nil {
		return nil, &LoginFailure{Step: StepTokenExchange, cause: fmt.Errorf("create token request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(dashboardClientID+":")))

	resp, err := client.Do(req)
	if err != nil {
		return nil, &LoginFailure{Step: StepTokenExchange, cause: fmt.Errorf("token exchange request: %w", err)}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &LoginFailure{Step: StepTokenExchange, Status: resp.StatusCode,
			cause: fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, truncateBody(respBody))}
	}

	var token tokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return nil, &LoginFailure{Step: StepTokenExchange, Status: resp.StatusCode, cause: fmt.Errorf("parse token response: %w", err)}
	}
	if token.AccessToken == "" {
		return nil, &LoginFailure{Step: StepTokenExchange, Status: resp.StatusCode, cause: errors.New("empty access token in response")}
	}
	return &token, nil
}

// doGet fetches reqURL and returns its body and status. status is 0 only
// when the round trip itself failed (no response was received); otherwise it
// is the status actually returned, even alongside a non-nil error for a
// non-200 response, so a caller can build a LoginFailure that names the real
// status instead of always reporting none.
func doGet(ctx context.Context, client *http.Client, reqURL string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	tempClient := &http.Client{
		Jar:       client.Jar,
		Transport: client.Transport,
		Timeout:   client.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Host != via[0].URL.Host {
				return fmt.Errorf("redirected to %q: the signin endpoint has moved", req.URL.Host)
			}
			return nil
		},
	}
	resp, err := tempClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	respBody, err := io.ReadAll(resp.Body)
	return respBody, resp.StatusCode, err
}

func doPostForm(ctx context.Context, client *http.Client, signinBaseURL, postURL string, form url.Values) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", signinBaseURL)
	req.Header.Set("Referer", postURL)

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header.Get("Location"), resp.StatusCode, nil
}

func generatePKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(hash[:])
	return verifier, challenge
}

var (
	csrfRe1 = regexp.MustCompile(`content="([^"]+)"[^>]*name="csrf-token"`)
	csrfRe2 = regexp.MustCompile(`name="_csrf"[^>]*value="([^"]+)"`)
)

func extractCSRFToken(html string) string {
	if m := csrfRe1.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	if m := csrfRe2.FindStringSubmatch(html); len(m) > 1 {
		return m[1]
	}
	return ""
}

func truncateBody(body []byte) string {
	const max = 300
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

func extractAuthCode(location string) string {
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return u.Query().Get("code")
}
