package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"

	"danny.vn/vngcloud/internal/transport"
)

const serviceAccountTokenURL = "https://iamapis.vngcloud.vn/accounts-api/v2/auth/token"
const maxServiceAccountResponse = 64 << 10

func newServiceAccountHTTPClient(timeout time.Duration, rt http.RoundTripper) *http.Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	if rt == nil {
		// Match the standard transport defaults without consulting mutable globals.
		rt = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	return &http.Client{Transport: rt, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func serviceAccountLoginError(ctx context.Context, status int, reason string) error {
	cause := ErrAuth
	if err := ctx.Err(); err != nil {
		cause = err
	}
	return &ServiceAccountLoginError{Status: status, Reason: reason, Err: cause}
}

func (s *serviceAccountTokenSource) exchange(ctx context.Context) (token transport.Token, err error) {
	debugLog(s.logger, ctx, "exchange started")
	defer func() { debugLog(s.logger, ctx, "exchange finished", "ok", err == nil) }()
	if ctx.Err() != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, 0, "token request failed")
	}
	// A non-replayable body forbids transport retries after HTTP/2 refusal or
	// GOAWAY. Application retry limits alone cannot prevent those resends.
	const requestBody = `{"grant_type":"client_credentials"}`
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, serviceAccountTokenURL, io.NopCloser(strings.NewReader(requestBody)))
	if err != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, 0, "token request failed")
	}
	request.ContentLength = int64(len(requestBody))
	request.SetBasicAuth(s.credentials.clientID, string(*s.credentials.secret))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	start := s.now()
	if ctx.Err() != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, 0, "token request failed")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, 0, "token request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if ctx.Err() != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, response.StatusCode, "token request failed")
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return transport.Token{}, serviceAccountLoginError(ctx, response.StatusCode, "token endpoint redirected request")
	}
	if response.StatusCode != http.StatusOK {
		return transport.Token{}, serviceAccountLoginError(ctx, response.StatusCode, "token endpoint rejected request")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxServiceAccountResponse+1))
	if err != nil || len(body) > maxServiceAccountResponse {
		return transport.Token{}, serviceAccountLoginError(ctx, response.StatusCode, "invalid token response")
	}
	token, err = decodeServiceAccountToken(body, start, s.now())
	if err != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, response.StatusCode, "invalid token response")
	}
	if ctx.Err() != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, response.StatusCode, "token request failed")
	}
	return token, nil
}

func decodeServiceAccountToken(body []byte, start, now time.Time) (transport.Token, error) {
	invalid := errors.New("invalid token response")
	decoder := json.NewDecoder(bytes.NewReader(body))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return transport.Token{}, invalid
	}
	seen := make(map[string]bool)
	var accessToken, tokenType string
	var seconds int64
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return transport.Token{}, invalid
		}
		name, ok := key.(string)
		if !ok {
			return transport.Token{}, invalid
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return transport.Token{}, invalid
		}
		switch name {
		case "access_token", "token_type", "expires_in":
			if seen[name] || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return transport.Token{}, invalid
			}
			seen[name] = true
			switch name {
			case "access_token":
				err = json.Unmarshal(raw, &accessToken)
			case "token_type":
				err = json.Unmarshal(raw, &tokenType)
			case "expires_in":
				err = json.Unmarshal(raw, &seconds)
			}
			if err != nil {
				return transport.Token{}, invalid
			}
		}
	}
	closing, err := decoder.Token()
	if err != nil || closing != json.Delim('}') {
		return transport.Token{}, invalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return transport.Token{}, invalid
	}
	if accessToken == "" || strings.IndexFunc(accessToken, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return transport.Token{}, invalid
	}
	if seen["token_type"] && !strings.EqualFold(tokenType, "bearer") {
		return transport.Token{}, invalid
	}
	if seconds <= 30 || seconds > math.MaxInt64/int64(time.Second) {
		return transport.Token{}, invalid
	}
	expiry := start.Add(time.Duration(seconds) * time.Second)
	if expiry.Sub(now) <= 30*time.Second {
		return transport.Token{}, invalid
	}
	return transport.Token{AccessToken: accessToken, ExpiresAt: expiry}, nil
}
