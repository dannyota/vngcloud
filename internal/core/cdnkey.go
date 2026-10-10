package core

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"
)

const (
	envCDNAPIKey = "VNGCLOUD_VCDN_API_KEY"

	// maxCDNAPIKeyLen bounds the key. A key is a JWT of a few hundred bytes.
	maxCDNAPIKeyLen = 4 << 10
)

// cdnKey is the vCDN API key. Every formatting and encoding path gives
// "[redacted]". A Client and a clientConfig hold it by pointer, so fmt
// prints an address, not the key, when it formats either whole: fmt cannot
// call a Format method on an unexported field.
// The root package's Secret type cannot be used here, since it imports this
// package.
type cdnKey string

func (cdnKey) String() string               { return "[redacted]" }
func (cdnKey) GoString() string             { return "[redacted]" }
func (cdnKey) Format(f fmt.State, _ rune)   { _, _ = f.Write([]byte("[redacted]")) }
func (cdnKey) LogValue() slog.Value         { return slog.StringValue("[redacted]") }
func (cdnKey) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (cdnKey) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }
func (k cdnKey) reveal() string             { return string(k) }

// WithCDNAPIKey sets the vCDN API key the cdn package sends as its bearer
// credential. It is separate from the IAM credentials: LoadConfig still
// needs those. Without this option LoadConfig reads VNGCLOUD_VCDN_API_KEY
// (skipped when the profile is explicit) and then vcdn_api_key in the
// profile's credentials file section.
func WithCDNAPIKey(key string) Option {
	return clientOptionFunc(func(cfg *clientConfig) {
		k := cdnKey(key)
		cfg.cdnAPIKey = &k
	})
}

// CDNAPIKey returns the vCDN API key, or "" when none is set. It returns
// the Client's configuration error for a zero Config.
func (c *Client) CDNAPIKey() (string, error) {
	if c.err != nil {
		return "", c.err
	}
	if c.cdnAPIKey == nil {
		return "", nil
	}
	return c.cdnAPIKey.reveal(), nil
}

// validateCDNAPIKey refuses a key that holds whitespace or a control
// character, or is over maxCDNAPIKeyLen. The error names source and never
// the value.
func validateCDNAPIKey(source string, key cdnKey) error {
	if len(key) > maxCDNAPIKeyLen {
		return fmt.Errorf("%w: vCDN API key from %s is longer than %d bytes", ErrInvalidConfig, source, maxCDNAPIKeyLen)
	}
	if strings.IndexFunc(string(key), func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("%w: vCDN API key from %s holds whitespace or a control character", ErrInvalidConfig, source)
	}
	return nil
}

// resolveCDNAPIKey applies the key precedence: the option, then (only when
// the profile is not explicit) VNGCLOUD_VCDN_API_KEY, then vcdn_api_key in
// the profile's credentials section. No key anywhere is not an error.
func resolveCDNAPIKey(settings *clientConfig, explicitProfile bool, profile string, credsSection map[string]string) error {
	switch {
	case settings.cdnAPIKey != nil && *settings.cdnAPIKey != "":
		return validateCDNAPIKey("WithCDNAPIKey", *settings.cdnAPIKey)
	case !explicitProfile && os.Getenv(envCDNAPIKey) != "":
		k := cdnKey(os.Getenv(envCDNAPIKey))
		settings.cdnAPIKey = &k
		return validateCDNAPIKey("the environment variable "+envCDNAPIKey, k)
	case credsSection["vcdn_api_key"] != "":
		k := cdnKey(credsSection["vcdn_api_key"])
		settings.cdnAPIKey = &k
		return validateCDNAPIKey(fmt.Sprintf("vcdn_api_key in profile %q of the credentials file", profile), k)
	}
	return nil
}
