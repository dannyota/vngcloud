package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"danny.vn/vngcloud/internal/tokencache"
	"danny.vn/vngcloud/internal/transport"
)

// Pointer fields prevent fmt from expanding private credential values in
// enclosing structs, where Go cannot call an unexported field's methods.
type serviceAccountCredentials struct {
	clientID string
	secret   *serviceAccountSecret
}

type serviceAccountSecret string

func newServiceAccountSecret(value string) *serviceAccountSecret {
	secret := serviceAccountSecret(value)
	return &secret
}

func (serviceAccountSecret) String() string               { return "[redacted]" }
func (serviceAccountSecret) GoString() string             { return "[redacted]" }
func (serviceAccountSecret) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte("[redacted]")) }
func (serviceAccountSecret) LogValue() slog.Value         { return slog.StringValue("[redacted]") }
func (serviceAccountSecret) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (serviceAccountSecret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

func (serviceAccountCredentials) String() string               { return "[redacted]" }
func (serviceAccountCredentials) GoString() string             { return "[redacted]" }
func (serviceAccountCredentials) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte("[redacted]")) }
func (serviceAccountCredentials) LogValue() slog.Value         { return slog.StringValue("[redacted]") }
func (serviceAccountCredentials) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (serviceAccountCredentials) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

// WithServiceAccountCredentials snapshots root-package or loader credentials.
// selected distinguishes a nil public auth option from a selected empty set.
func WithServiceAccountCredentials(clientID, clientSecret string, selected bool) Option {
	var snapshot *serviceAccountCredentials
	if selected {
		snapshot = &serviceAccountCredentials{clientID: clientID, secret: newServiceAccountSecret(clientSecret)}
	}
	return serviceAccountOption{snapshot: snapshot}
}

type serviceAccountOption struct{ snapshot *serviceAccountCredentials }

func (o serviceAccountOption) apply(cfg *clientConfig)    { cfg.serviceAccount = o.snapshot }
func (serviceAccountOption) String() string               { return "[redacted]" }
func (serviceAccountOption) GoString() string             { return "[redacted]" }
func (serviceAccountOption) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte("[redacted]")) }
func (serviceAccountOption) LogValue() slog.Value         { return slog.StringValue("[redacted]") }
func (serviceAccountOption) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (serviceAccountOption) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

func (a *serviceAccountCredentials) validate() error {
	for _, field := range []struct{ name, value string }{
		{"ClientID", a.clientID}, {"ClientSecret", string(*a.secret)},
	} {
		if field.value == "" {
			return fmt.Errorf("%w: ServiceAccount.%s is required", ErrInvalidConfig, field.name)
		}
		if len(field.value) > 4096 {
			return fmt.Errorf("%w: ServiceAccount.%s is longer than 4096 bytes", ErrInvalidConfig, field.name)
		}
		if strings.IndexFunc(field.value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return fmt.Errorf("%w: ServiceAccount.%s holds whitespace or a control character", ErrInvalidConfig, field.name)
		}
	}
	if strings.Contains(a.clientID, ":") {
		return fmt.Errorf("%w: ServiceAccount.ClientID holds a colon", ErrInvalidConfig)
	}
	return nil
}

type serviceAccountTokenSource struct {
	credentials *serviceAccountCredentials
	client      *http.Client
	logger      *slog.Logger
	now         func() time.Time
	mu          sync.Mutex
	cached      transport.Token
	obtainedAt  time.Time
	cache       *tokencache.Cache
	cacheKey    tokencache.ServiceAccountKey
	rejected    string
}

func newServiceAccountTokenSource(credentials *serviceAccountCredentials, timeout time.Duration, logger *slog.Logger, rt http.RoundTripper, now func() time.Time) *serviceAccountTokenSource {
	if now == nil {
		now = time.Now
	}
	return &serviceAccountTokenSource{credentials: credentials, client: newServiceAccountHTTPClient(timeout, rt), logger: logger, now: now}
}

func (s *serviceAccountTokenSource) Token(ctx context.Context) (transport.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return transport.Token{}, err
	}
	if s.cached.AccessToken != "" && s.cached.ExpiresAt.Sub(s.now()) > 30*time.Second {
		return s.cached, nil
	}
	if s.cache != nil {
		return s.diskToken(ctx)
	}
	token, err := s.exchange(ctx)
	if err != nil {
		return transport.Token{}, err
	}
	obtainedAt := s.now()
	if err := ctx.Err(); err != nil {
		return transport.Token{}, serviceAccountLoginError(ctx, 0, "token request failed")
	}
	s.cached, s.obtainedAt = token, obtainedAt
	return token, nil
}

func (s *serviceAccountTokenSource) Invalidate(sent string) {
	_ = s.InvalidateContext(context.Background(), sent)
}

func (s *serviceAccountTokenSource) InvalidateContext(ctx context.Context, sent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if sent == "" || s.cached.AccessToken != sent {
		return nil
	}
	age := s.now().Sub(s.obtainedAt)
	if err := ctx.Err(); err != nil {
		return err
	}
	if age < 0 || age >= 30*time.Second {
		s.cached = transport.Token{}
		if s.cache != nil {
			s.rejected = sent
		}
	}
	return nil
}

// diskToken runs under mu, retaining pending rejection until acquisition and
// the context-checked memory commit both succeed.
func (s *serviceAccountTokenSource) diskToken(ctx context.Context) (transport.Token, error) {
	cached, err := s.cache.GetServiceAccount(ctx, s.cacheKey, s.rejected, func(ctx context.Context) (tokencache.Token, error) {
		token, err := s.exchange(ctx)
		if err != nil {
			return tokencache.Token{}, err
		}
		return tokencache.Token{AccessToken: token.AccessToken, ExpiresAt: token.ExpiresAt}, nil
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return transport.Token{}, ctxErr
	}
	if errors.Is(err, tokencache.ErrUnavailable) {
		return transport.Token{}, ErrTokenCache
	}
	if err != nil {
		return transport.Token{}, err
	}
	s.cached = transport.Token{AccessToken: cached.AccessToken, ExpiresAt: cached.ExpiresAt}
	s.obtainedAt = cached.ObtainedAt
	s.rejected = ""
	return s.cached, nil
}

// WithServiceAccountTestTransport injects private exchange dependencies for
// tests within this module. It does not change the fixed endpoint and is
// never exposed by the public SDK.
func WithServiceAccountTestTransport(rt http.RoundTripper, now func() time.Time) Option {
	return clientOptionFunc(func(cfg *clientConfig) {
		cfg.serviceAccountTransport = rt
		cfg.serviceAccountClock = now
	})
}
