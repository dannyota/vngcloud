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
)

const (
	defaultSigninBaseURL = "https://signin.vngcloud.vn"
	defaultTokenURL      = "https://dashboard.console.vngcloud.vn/accounts-api/v1/auth/token"
	defaultDashboardURI  = "https://dashboard.console.vngcloud.vn/"
	dashboardClientID    = "c9e78411-f2a2-41ba-a9e4-3c56263c181a"
	loginPath            = "/ap/auth/iam/login"
	twoFAPathMatch       = "/ap/auth/iam/google"
)

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

func Login(ctx context.Context, req LoginRequest) (accessToken string, expiresAt time.Time, err error) {
	signinBaseURL := req.SigninBaseURL
	if signinBaseURL == "" {
		signinBaseURL = defaultSigninBaseURL
	}
	signinBaseURL = strings.TrimRight(signinBaseURL, "/")

	tokenURL := req.TokenURL
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}
	dashboardURI := req.DashboardURI
	if dashboardURI == "" {
		dashboardURI = defaultDashboardURI
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

	pageBody, err := doGet(ctx, &client, loginURL)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("GET login page: %w", err)
	}
	csrfToken := extractCSRFToken(string(pageBody))
	if csrfToken == "" {
		return "", time.Time{}, errors.New("no CSRF token on login page")
	}

	formData := url.Values{
		"_csrf":     {csrfToken},
		"rootEmail": {req.RootEmail},
		"username":  {req.Username},
		"password":  {req.Password},
	}
	location, err := doPostForm(ctx, &client, signinBaseURL, loginURL, formData)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("POST login: %w", err)
	}
	if location == "" {
		return "", time.Time{}, errors.New("login failed: no redirect")
	}

	if strings.Contains(location, twoFAPathMatch) {
		if req.TOTP == nil {
			return "", time.Time{}, errors.New("2FA required but no TOTP provider configured")
		}
		location, err = handle2FA(ctx, &client, signinBaseURL, location, req.TOTP)
		if err != nil {
			return "", time.Time{}, err
		}
	}

	authCode := extractAuthCode(location)
	if authCode == "" {
		return "", time.Time{}, errors.New("no authorization code in redirect")
	}

	token, err := exchangeToken(ctx, &client, tokenURL, dashboardURI, authCode, verifier)
	if err != nil {
		return "", time.Time{}, err
	}
	return token.AccessToken, time.Now().Add(time.Duration(token.ExpiresIn) * time.Second), nil
}

func handle2FA(ctx context.Context, client *http.Client, signinBaseURL, redirectPath string, totp TOTPProvider) (string, error) {
	twoFAURL := redirectPath
	if !strings.HasPrefix(redirectPath, "http") {
		twoFAURL = signinBaseURL + redirectPath
	}

	pageBody, err := doGet(ctx, client, twoFAURL)
	if err != nil {
		return "", fmt.Errorf("GET 2FA page: %w", err)
	}
	csrfToken := extractCSRFToken(string(pageBody))
	if csrfToken == "" {
		return "", errors.New("no CSRF token on 2FA page")
	}

	code, err := totp.GetCode(ctx)
	if err != nil {
		return "", fmt.Errorf("get TOTP code: %w", err)
	}

	return doPostForm(ctx, client, signinBaseURL, twoFAURL, url.Values{
		"_csrf": {csrfToken},
		"token": {code},
	})
}

type tokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpiresIn   int64  `json:"expiresIn"`
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
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(dashboardClientID+":")))

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d", resp.StatusCode)
	}

	var token tokenResponse
	if err := json.Unmarshal(respBody, &token); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}
	if token.AccessToken == "" {
		return nil, errors.New("empty access token in response")
	}
	return &token, nil
}

func doGet(ctx context.Context, client *http.Client, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	tempClient := &http.Client{Jar: client.Jar, Transport: client.Transport, Timeout: client.Timeout}
	resp, err := tempClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

func doPostForm(ctx context.Context, client *http.Client, signinBaseURL, postURL string, form url.Values) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", signinBaseURL)
	req.Header.Set("Referer", postURL)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header.Get("Location"), nil
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

func extractAuthCode(location string) string {
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return u.Query().Get("code")
}
