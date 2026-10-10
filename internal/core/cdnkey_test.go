package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const (
	keyOption = "<secret>-option-key"
	keyEnv    = "<secret>-env-key"
	keyFile   = "<secret>-file-key"
)

// cdnKeyOf loads a Config and returns the vCDN key it holds.
func cdnKeyOf(t *testing.T, opts ...Option) (string, error) {
	t.Helper()
	cfg, err := LoadConfig(context.Background(), append([]Option{WithRegion("hcm-3"), WithStaticToken("tok")}, opts...)...)
	if err != nil {
		return "", err
	}
	return ClientOf(cfg).CDNAPIKey()
}

func writeKeyFile(t *testing.T, home, body string) {
	t.Helper()
	writeFile(t, credentialsPath(home), body, 0o600)
}

func TestCDNAPIKeyPrecedence(t *testing.T) {
	home := setupHome(t)
	writeKeyFile(t, home, "[default]\nvcdn_api_key = "+keyFile+"\n")

	if got, err := cdnKeyOf(t); err != nil || got != keyFile {
		t.Fatalf("file only: got %q, %v", got, err)
	}
	t.Setenv("VNGCLOUD_VCDN_API_KEY", keyEnv)
	if got, err := cdnKeyOf(t); err != nil || got != keyEnv {
		t.Fatalf("env over file: got %q, %v", got, err)
	}
	if got, err := cdnKeyOf(t, WithCDNAPIKey(keyOption)); err != nil || got != keyOption {
		t.Fatalf("option over env: got %q, %v", got, err)
	}
}

func TestCDNAPIKeyExplicitProfileSkipsEnvironment(t *testing.T) {
	home := setupHome(t)
	writeKeyFile(t, home, "[prod]\nvcdn_api_key = "+keyFile+"\n")
	t.Setenv("VNGCLOUD_VCDN_API_KEY", keyEnv)

	if got, err := cdnKeyOf(t, WithProfile("prod")); err != nil || got != keyFile {
		t.Fatalf("explicit profile: got %q, %v, want the file key", got, err)
	}
	writeKeyFile(t, home, "[prod]\nusername = x\n[default]\nvcdn_api_key = "+keyFile+"\n")
	if got, err := cdnKeyOf(t, WithProfile("prod")); err != nil || got != "" {
		t.Fatalf("explicit profile without a file key: got %q, %v, want no key", got, err)
	}
	// VNGCLOUD_PROFILE is not explicit, so the environment key still applies.
	t.Setenv("VNGCLOUD_PROFILE", "prod")
	if got, err := cdnKeyOf(t); err != nil || got != keyEnv {
		t.Fatalf("env profile: got %q, %v, want the env key", got, err)
	}
}

func TestCDNAPIKeyAbsent(t *testing.T) {
	setupHome(t)
	got, err := cdnKeyOf(t)
	if got != "" || err != nil {
		t.Fatalf("got %q, %v, want no key and no error", got, err)
	}
}

// The key is not an IAM credential: LoadConfig still fails without one.
func TestCDNAPIKeyDoesNotCountAsIAMCredential(t *testing.T) {
	setupHome(t)
	_, err := LoadConfig(context.Background(), WithRegion("hcm-3"), WithCDNAPIKey(keyOption))
	if !errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrNoCredentials", err)
	}
	if strings.Contains(fmt.Sprint(err), keyOption) {
		t.Fatalf("error holds the key: %v", err)
	}
}

func TestCDNAPIKeyRefusesBadValueNamingSource(t *testing.T) {
	bad := map[string]string{
		"space":   "<secret> bad",
		"tab":     "<secret>\tbad",
		"newline": "<secret>\nbad",
		"nul":     "<secret>\x00bad",
		"long":    strings.Repeat("k", 4097),
	}
	for name, value := range bad {
		t.Run("option "+name, func(t *testing.T) {
			setupHome(t)
			_, err := cdnKeyOf(t, WithCDNAPIKey(value))
			assertBadKey(t, err, "WithCDNAPIKey", value)
		})
		t.Run("env "+name, func(t *testing.T) {
			if strings.Contains(value, "\x00") {
				t.Skip("an environment variable cannot hold NUL")
			}
			setupHome(t)
			t.Setenv("VNGCLOUD_VCDN_API_KEY", value)
			_, err := cdnKeyOf(t)
			assertBadKey(t, err, "VNGCLOUD_VCDN_API_KEY", value)
		})
	}
	t.Run("file", func(t *testing.T) {
		home := setupHome(t)
		writeKeyFile(t, home, "[default]\nvcdn_api_key = "+strings.Repeat("k", 4097)+"\n")
		_, err := cdnKeyOf(t)
		assertBadKey(t, err, "vcdn_api_key", strings.Repeat("k", 4097))
	})
	t.Run("NewConfig", func(t *testing.T) {
		_, err := NewConfig(WithRegion("hcm-3"), WithStaticToken("tok"), WithCDNAPIKey("<secret> bad"))
		assertBadKey(t, err, "WithCDNAPIKey", "<secret> bad")
	})
	t.Run("max length passes", func(t *testing.T) {
		setupHome(t)
		if _, err := cdnKeyOf(t, WithCDNAPIKey(strings.Repeat("k", 4096))); err != nil {
			t.Fatal(err)
		}
	})
}

func assertBadKey(t *testing.T, err error, source, value string) {
	t.Helper()
	if !errors.Is(err, ErrInvalidConfig) || errors.Is(err, ErrNoCredentials) {
		t.Fatalf("err = %v, want ErrInvalidConfig only", err)
	}
	if !strings.Contains(err.Error(), source) {
		t.Errorf("err = %q, want the source %q named", err, source)
	}
	if strings.Contains(err.Error(), strings.TrimSpace(value)) && len(value) > 8 {
		t.Errorf("err holds the key: %q", err)
	}
}

func TestCDNAPIKeyFileStillNeedsMode0600(t *testing.T) {
	home := setupHome(t)
	writeFile(t, credentialsPath(home), "[default]\nvcdn_api_key = "+keyFile+"\n", 0o644)
	_, err := cdnKeyOf(t)
	if !errors.Is(err, ErrCredentialsFile) || strings.Contains(err.Error(), keyFile) {
		t.Fatalf("err = %v, want ErrCredentialsFile without the key", err)
	}
}

func TestCDNAPIKeyNeverFormats(t *testing.T) {
	setupHome(t)
	cfg, err := LoadConfig(context.Background(), WithRegion("hcm-3"), WithStaticToken("tok"), WithCDNAPIKey(keyOption))
	if err != nil {
		t.Fatal(err)
	}
	c := ClientOf(cfg)
	var settings clientConfig
	WithCDNAPIKey(keyOption).apply(&settings)
	out := []string{fmt.Sprintf("%v %+v %#v", settings, settings, settings), fmt.Sprintf("%v %+v %#v", &settings, &settings, &settings), fmt.Sprintf("%v %+v %#v", cfg, cfg, cfg), fmt.Sprintf("%v %+v %#v", c, c, c), fmt.Sprintf("%+v %#v", c.cdnAPIKey, c.cdnAPIKey)}
	b, err := json.Marshal(struct{ K *cdnKey }{c.cdnAPIKey})
	if err != nil {
		t.Fatal(err)
	}
	out = append(out, string(b), slog.AnyValue(c.cdnAPIKey).String())
	for _, text := range out {
		if strings.Contains(text, keyOption) {
			t.Errorf("formatted output holds the key: %q", text)
		}
	}
}
